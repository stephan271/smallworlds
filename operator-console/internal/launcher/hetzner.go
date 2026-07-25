package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/capability"
	"github.com/stephan271/smallworlds/operator-console/internal/hetzner"
	"github.com/stephan271/smallworlds/operator-console/internal/state"
	"github.com/stephan271/smallworlds/operator-console/internal/tofu"
	"github.com/stephan271/smallworlds/operator-console/internal/vault"
	"github.com/stephan271/smallworlds/operator-console/internal/workflow"
)

// mutatingRequest enforces the launcher's contract for a state-changing POST:
// an authenticated session and a matching CSRF token.
func (server *Server) mutatingRequest(response http.ResponseWriter, request *http.Request) bool {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return false
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return false
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return false
	}
	return true
}

// decodeJSON reads a bounded JSON body, rejecting unknown fields.
func decodeJSON(response http.ResponseWriter, request *http.Request, limit int64, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, limit))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target) == nil
}

// HetznerProvider is the read-only provider boundary the launcher inspects
// through. *hetzner.Client is the production implementation; tests inject a
// deterministic one. Nothing on this interface can change a project.
type HetznerProvider interface {
	Probe(ctx context.Context, token string) (hetzner.TokenProbe, error)
	Inventory(ctx context.Context, token, domain string) ([]hetzner.Resource, error)
	Catalog(ctx context.Context, token string) (hetzner.PriceCatalog, error)
	Nameservers(ctx context.Context, token, domain string) ([]string, error)
}

// HetznerProvisioner applies an approved, still-current plan to the project. It
// is deliberately *not* implemented here: this issue's journey ends at an
// approved plan, and the launcher default refuses so that approving a plan can
// never quietly become provisioning it.
type HetznerProvisioner interface {
	Provision(ctx context.Context, profileID string, plan hetzner.ChangePlan) error
}

// ErrHetznerProvisioningUnavailable is returned by the launcher default: no
// provider resource change path is wired yet.
var ErrHetznerProvisioningUnavailable = errors.New("launcher: hetzner provisioning path unavailable")

type unavailableHetznerProvisioner struct{}

func (unavailableHetznerProvisioner) Provision(context.Context, string, hetzner.ChangePlan) error {
	return ErrHetznerProvisioningUnavailable
}

// hetznerProvisionIntent is the fixed plan intent the launcher owns for
// applying an infrastructure plan. Browser input never selects it.
const hetznerProvisionIntent = "ProvisionHetznerInfrastructure"

// hetznerVaultKey is where the project token is custodied. The value never
// leaves the Vault except into a provider call.
func hetznerVaultKey(profileID string) string { return profileID + "/hetzner-project-token" }

// hetznerRecord is the secret-free JSON persisted per profile: the token's
// fingerprint and project identity, the last inspection, and the last plan. No
// token value is ever stored here.
type hetznerRecord struct {
	Token       *hetzner.TokenAssessment `json:"token,omitempty"`
	ValidatedAt time.Time                `json:"validatedAt,omitempty"`
	Naming      hetzner.Naming           `json:"naming"`
	Inventory   *hetzner.Inventory       `json:"inventory,omitempty"`
	Delegation  *hetzner.Delegation      `json:"delegation,omitempty"`
	InspectedAt time.Time                `json:"inspectedAt,omitempty"`
	Plan        *hetzner.ChangePlan      `json:"plan,omitempty"`
	PlanID      string                   `json:"planId,omitempty"`
}

// validateHetznerToken custodies a Hetzner project token in the Launcher Vault
// and validates it for project identity and read/write authority before any
// planning. The token value is never returned, persisted outside the Vault, or
// logged — the record keeps only its fingerprint.
func (server *Server) validateHetznerToken(response http.ResponseWriter, request *http.Request) {
	if !server.mutatingRequest(response, request) {
		return
	}
	var input struct {
		ProfileID string `json:"profileId"`
		Token     string `json:"token"`
	}
	if !decodeJSON(response, request, 8*1024, &input) || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_hetzner_token_request")
		return
	}
	if _, err := server.store.GetProfile(request.Context(), input.ProfileID); errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "hetzner_token_validation_failed")
		return
	}
	if !hetzner.ValidToken(input.Token) {
		// Refused locally: a malformed token is never sent to the provider.
		writeJSON(response, http.StatusOK, hetzner.AssessToken(input.Token, hetzner.TokenProbe{}, ""))
		return
	}

	record, _ := server.loadHetznerRecord(request.Context(), input.ProfileID)
	boundProjectID := ""
	if record.Token != nil {
		boundProjectID = record.Token.ProjectID
	}
	probe, err := server.hetzner.Probe(request.Context(), input.Token)
	if errors.Is(err, hetzner.ErrRateLimited) {
		probe = hetzner.TokenProbe{RateLimited: true}
	} else if err != nil {
		writeError(response, http.StatusBadGateway, "hetzner_provider_unavailable")
		return
	}
	assessment := hetzner.AssessToken(input.Token, probe, boundProjectID)
	if !assessment.Usable() {
		writeJSON(response, http.StatusOK, assessment)
		return
	}

	// Only a usable token is custodied, so an operator cannot end up with a
	// rejected credential silently stored under the profile.
	if err := server.vault.Store(hetznerVaultKey(input.ProfileID), input.Token); errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "hetzner_token_storage_failed")
		return
	}
	if err := server.store.UpsertCredentialReference(request.Context(), state.CredentialReference{
		ProfileID:      input.ProfileID,
		Kind:           "hetzner-project-token",
		VaultKey:       hetznerVaultKey(input.ProfileID),
		Source:         "operator",
		ExpiresAt:      time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC),
		RotationStatus: "current",
	}); err != nil {
		writeError(response, http.StatusInternalServerError, "hetzner_token_storage_failed")
		return
	}
	record.Token, record.ValidatedAt = &assessment, time.Now().UTC()
	if !server.persistHetznerRecord(request.Context(), input.ProfileID, record) {
		writeError(response, http.StatusInternalServerError, "hetzner_token_storage_failed")
		return
	}
	writeJSON(response, http.StatusOK, assessment)
}

// hetznerStatus returns the secret-free view of what the launcher knows about
// the profile's project: token verdict, last inspection, toolchain, workspace.
func (server *Server) hetznerStatus(response http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedSession(request); !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", "GET")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	profileID := request.URL.Query().Get("profileId")
	if profileID == "" {
		writeError(response, http.StatusBadRequest, "profile_required")
		return
	}
	record, found := server.loadHetznerRecord(request.Context(), profileID)
	if !found {
		writeError(response, http.StatusNotFound, "hetzner_not_configured")
		return
	}
	view := hetznerView(record)
	toolchain, err := tofu.Inspect(server.assets)
	if err == nil {
		view["toolchain"] = toolchain
	} else {
		view["toolchain"] = toolchain
		view["toolchainReasonKey"] = "toolchain-artifacts-unavailable"
	}
	if workspace, err := tofu.OpenWorkspace(server.dataDir, profileID); err == nil {
		if status, err := workspace.Status(); err == nil {
			view["workspace"] = status
		}
	}
	writeJSON(response, http.StatusOK, view)
}

// inspectHetzner performs the read-only inventory: it lists every inspected
// resource kind, classifies ownership against this profile, and checks
// nameserver delegation. It changes nothing in the project.
func (server *Server) inspectHetzner(response http.ResponseWriter, request *http.Request) {
	if !server.mutatingRequest(response, request) {
		return
	}
	var input struct {
		ProfileID string `json:"profileId"`
		Domain    string `json:"domain"`
		EnvExt    string `json:"envExt"`
	}
	if !decodeJSON(response, request, 8*1024, &input) || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_hetzner_inspection_request")
		return
	}
	profile, err := server.store.GetProfile(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "hetzner_inspection_failed")
		return
	}
	naming := hetzner.Naming{Domain: input.Domain, EnvExt: input.EnvExt, ProfileID: input.ProfileID}
	if err := naming.Validate(); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_hetzner_naming")
		return
	}
	record, _ := server.loadHetznerRecord(request.Context(), input.ProfileID)
	if record.Token == nil || !record.Token.Usable() {
		writeError(response, http.StatusConflict, "hetzner_token_required")
		return
	}
	token, ok := server.loadHetznerToken(response, input.ProfileID)
	if !ok {
		return
	}

	resources, err := server.hetzner.Inventory(request.Context(), token, naming.Domain)
	if ok := server.writeProviderError(response, err); !ok {
		return
	}
	inventory, err := hetzner.Classify(naming, resources)
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_hetzner_naming")
		return
	}
	inventory.ProjectID = record.Token.ProjectID

	observed, err := server.hetzner.Nameservers(request.Context(), token, naming.Domain)
	if err != nil && !errors.Is(err, hetzner.ErrRateLimited) {
		if ok := server.writeProviderError(response, err); !ok {
			return
		}
	}
	delegation := hetzner.CheckDelegation(naming.Domain, observed, capability.DeploymentMode(profile.DeploymentMode))

	record.Naming, record.Inventory, record.Delegation, record.InspectedAt = naming, &inventory, &delegation, time.Now().UTC()
	// A new inspection invalidates any plan derived from the previous one.
	if record.Plan != nil && !record.Plan.StillCurrent(inventory) {
		record.Plan, record.PlanID = nil, ""
	}
	if !server.persistHetznerRecord(request.Context(), input.ProfileID, record) {
		writeError(response, http.StatusInternalServerError, "hetzner_inspection_failed")
		return
	}
	writeJSON(response, http.StatusOK, hetznerView(record))
}

// acquireHetznerToolchain obtains the pinned, verified OpenTofu and provider
// artifacts and prepares the profile's isolated state workspace. It never falls
// back to an ambient tofu binary and never shares a workspace between profiles.
func (server *Server) acquireHetznerToolchain(response http.ResponseWriter, request *http.Request) {
	if !server.mutatingRequest(response, request) {
		return
	}
	var input struct {
		ProfileID string `json:"profileId"`
	}
	if !decodeJSON(response, request, 4*1024, &input) || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_hetzner_toolchain_request")
		return
	}
	if _, err := server.store.GetProfile(request.Context(), input.ProfileID); errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "hetzner_toolchain_failed")
		return
	}
	workspace, err := tofu.OpenWorkspace(server.dataDir, input.ProfileID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "hetzner_workspace_failed")
		return
	}
	status, err := workspace.Status()
	if err != nil {
		writeError(response, http.StatusInternalServerError, "hetzner_workspace_failed")
		return
	}
	toolchain, err := tofu.Acquire(request.Context(), server.assets)
	if errors.Is(err, tofu.ErrToolchainUnavailable) {
		writeJSON(response, http.StatusServiceUnavailable, map[string]any{"error": "hetzner_toolchain_unavailable", "toolchain": toolchain, "workspace": status})
		return
	}
	if err != nil {
		writeJSON(response, http.StatusBadGateway, map[string]any{"error": "hetzner_toolchain_acquisition_failed", "toolchain": toolchain, "workspace": status})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"toolchain": toolchain, "workspace": status})
}

// planHetzner builds the immutable, cost-bearing Change Plan from the last
// inspection, the selected Cluster Capabilities, and the operator's
// infrastructure choice. It mutates nothing: approving the returned plan is a
// separate, explicit step, and the plan carries the inventory digest so a stale
// plan can be refused before any resource is touched.
func (server *Server) planHetzner(response http.ResponseWriter, request *http.Request) {
	if !server.mutatingRequest(response, request) {
		return
	}
	var input struct {
		ProfileID    string   `json:"profileId"`
		Mode         string   `json:"mode"`
		CommunityIDs []string `json:"communityIds"`
		Tier         string   `json:"tier"`
		Location     string   `json:"location"`
		ServerType   string   `json:"serverType"`
		VolumeGB     int      `json:"volumeGb"`
		Adoptions    []string `json:"adoptions"`
	}
	if !decodeJSON(response, request, 32*1024, &input) || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_hetzner_plan_request")
		return
	}
	profile, err := server.store.GetProfile(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "hetzner_plan_failed")
		return
	}
	record, _ := server.loadHetznerRecord(request.Context(), input.ProfileID)
	if record.Token == nil || !record.Token.Usable() {
		writeError(response, http.StatusConflict, "hetzner_token_required")
		return
	}
	if record.Inventory == nil {
		writeError(response, http.StatusConflict, "hetzner_inspection_required")
		return
	}
	assessed, err := capability.DefaultCatalog().Assess(capability.Selection{
		Mode:           capability.SelectionMode(input.Mode),
		DeploymentMode: capability.DeploymentMode(profile.DeploymentMode),
		CommunityIDs:   input.CommunityIDs,
	})
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_capability_selection")
		return
	}
	token, ok := server.loadHetznerToken(response, input.ProfileID)
	if !ok {
		return
	}
	catalog, err := server.hetzner.Catalog(request.Context(), token)
	if ok := server.writeProviderError(response, err); !ok {
		return
	}
	resolution, err := hetzner.ResolveChoice(assessed, catalog, hetzner.Choice{
		Tier:       hetzner.PresetTier(input.Tier),
		Location:   input.Location,
		ServerType: input.ServerType,
		VolumeGB:   input.VolumeGB,
	})
	if errors.Is(err, hetzner.ErrInvalidChoice) {
		writeError(response, http.StatusBadRequest, "invalid_hetzner_choice")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "hetzner_plan_failed")
		return
	}
	delegation := hetzner.Delegation{Status: hetzner.DelegationUnknown, ReasonKey: "delegation-lookup-inconclusive"}
	if record.Delegation != nil {
		delegation = *record.Delegation
	}
	changePlan, err := hetzner.BuildPlan(hetzner.PlanInput{
		Naming:     record.Naming,
		ProjectID:  record.Token.ProjectID,
		Inventory:  *record.Inventory,
		Resolution: resolution,
		Delegation: delegation,
		Adoptions:  input.Adoptions,
	})
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_hetzner_plan_request")
		return
	}

	// The Change Plan is recorded whether or not it is approvable: an operator
	// must be able to see the blocked plan and what it would cost.
	record.Plan = &changePlan
	risks := []workflow.Risk{{Code: "hetzner.cost.recurring", MessageKey: "plan.risk.hetzner_recurring_cost"}}
	for _, blocker := range changePlan.Blockers {
		risks = append(risks, workflow.Risk{Code: "hetzner.blocked." + blocker.Code, MessageKey: "plan.risk.hetzner_blocked"})
	}
	workflowPlan, err := server.workflow.PlanChangeWithRisks(request.Context(), input.ProfileID, hetznerProvisionIntent, hetznerPlanDetail(changePlan), []workflow.Effect{
		{Code: "hetzner.resources.reconciled", MessageKey: "plan.effect.hetzner_resources"},
		{Code: "hetzner.cost.recurring", MessageKey: "plan.effect.hetzner_cost"},
	}, risks)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "hetzner_plan_failed")
		return
	}
	record.PlanID = workflowPlan.ID
	if !server.persistHetznerRecord(request.Context(), input.ProfileID, record) {
		writeError(response, http.StatusInternalServerError, "hetzner_plan_failed")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{
		"plan":        workflowPlan,
		"changePlan":  changePlan,
		"resolution":  resolution,
		"approvable":  changePlan.Approvable(),
		"requirement": resolution.Requirement,
	})
}

// hetznerPresets returns the Small, Recommended, and High capacity presets for
// a selection, each with live availability and estimated recurring cost, plus
// the locations and server types an advanced choice can pick from.
func (server *Server) hetznerPresets(response http.ResponseWriter, request *http.Request) {
	if !server.mutatingRequest(response, request) {
		return
	}
	var input struct {
		ProfileID    string   `json:"profileId"`
		Mode         string   `json:"mode"`
		CommunityIDs []string `json:"communityIds"`
		Location     string   `json:"location"`
	}
	if !decodeJSON(response, request, 32*1024, &input) || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_hetzner_preset_request")
		return
	}
	profile, err := server.store.GetProfile(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "hetzner_presets_failed")
		return
	}
	assessed, err := capability.DefaultCatalog().Assess(capability.Selection{
		Mode:           capability.SelectionMode(input.Mode),
		DeploymentMode: capability.DeploymentMode(profile.DeploymentMode),
		CommunityIDs:   input.CommunityIDs,
	})
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_capability_selection")
		return
	}
	token, ok := server.loadHetznerToken(response, input.ProfileID)
	if !ok {
		return
	}
	catalog, err := server.hetzner.Catalog(request.Context(), token)
	if ok := server.writeProviderError(response, err); !ok {
		return
	}
	location := input.Location
	if location == "" && len(catalog.Locations) > 0 {
		location = catalog.Locations[0]
	}
	presets, requirement, err := hetzner.Presets(assessed, catalog, location)
	if err != nil {
		writeError(response, http.StatusBadGateway, "hetzner_provider_catalog_incomplete")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"presets":     presets,
		"requirement": requirement,
		"location":    location,
		"locations":   catalog.Locations,
		"offerings":   catalog.Offerings,
		"observedAt":  catalog.ObservedAt,
	})
}

// executeHetznerProvision is the registered executor for an approved
// infrastructure plan. It re-inspects the project first and refuses a plan that
// no longer matches it, so a resource that appeared or changed since approval
// cannot be created twice or adopted unseen. Only then does it hand the plan to
// the provisioner, which is not wired in this release and refuses honestly.
func (server *Server) executeHetznerProvision(runID string) {
	ctx := context.Background()
	run, err := server.store.GetRun(ctx, runID)
	if err != nil || run.State != "running" {
		return
	}
	record, found := server.loadHetznerRecord(ctx, run.ProfileID)
	if !found || record.Plan == nil || record.Token == nil {
		server.failHetznerRun(ctx, run, "plan-missing")
		return
	}
	token, err := server.vault.Load(hetznerVaultKey(run.ProfileID))
	if err != nil {
		server.failHetznerRun(ctx, run, "token-unavailable")
		return
	}
	resources, err := server.hetzner.Inventory(ctx, token, record.Naming.Domain)
	if err != nil {
		server.failHetznerRun(ctx, run, "re-inspection-failed")
		return
	}
	current, err := hetzner.Classify(record.Naming, resources)
	if err != nil {
		server.failHetznerRun(ctx, run, "re-inspection-failed")
		return
	}
	if !record.Plan.StillCurrent(current) {
		server.failHetznerRun(ctx, run, "plan-stale")
		return
	}
	if !record.Plan.Approvable() {
		server.failHetznerRun(ctx, run, "plan-blocked")
		return
	}
	if err := server.hetznerProvisioner.Provision(ctx, run.ProfileID, *record.Plan); err != nil {
		server.failHetznerRun(ctx, run, "provisioning-unavailable")
		return
	}
	_ = server.store.CompleteRunVerification(ctx, runID, "infrastructure-reconciled", "hetzner.infrastructure.reconciled", time.Now().UTC())
}

func (server *Server) failHetznerRun(ctx context.Context, run state.RunRecord, checkpoint string) {
	if err := server.store.UpdateRun(ctx, run.ID, "failed", checkpoint, "", nil); err != nil {
		return
	}
	_, _ = server.store.AppendEvent(ctx, state.EventRecord{
		ProfileID:  run.ProfileID,
		RunID:      run.ID,
		Type:       "run.failed",
		MessageKey: "activity.run.failed",
		Parameters: `{"checkpoint":"` + checkpoint + `"}`,
		OccurredAt: time.Now().UTC(),
	})
}

// hetznerPlanDetail is the stable, secret-free description bound into the
// workflow plan's digest. It carries the infrastructure plan's own digest, so
// approving a plan approves exactly the reviewed inventory, choice, and cost.
func hetznerPlanDetail(plan hetzner.ChangePlan) string {
	return fmt.Sprintf("hetzner.plan:%s/%s/%dGB@%s cost=%.2f %s", plan.Choice.Tier, plan.Choice.ServerType, plan.Choice.VolumeGB, plan.Choice.Location, plan.Cost.TotalMonthlyEUR, plan.Digest)
}

// loadHetznerToken loads the custodied project token, writing the appropriate
// error and returning ok=false on failure. The value only ever travels into a
// provider call.
func (server *Server) loadHetznerToken(response http.ResponseWriter, profileID string) (string, bool) {
	token, err := server.vault.Load(hetznerVaultKey(profileID))
	if errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return "", false
	}
	if errors.Is(err, vault.ErrSecretNotFound) {
		writeError(response, http.StatusConflict, "hetzner_token_required")
		return "", false
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "hetzner_token_storage_failed")
		return "", false
	}
	return token, true
}

// writeProviderError maps a provider failure onto a distinct response so a
// throttled or rejected call is never mistaken for an empty project.
func (server *Server) writeProviderError(response http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, hetzner.ErrRateLimited):
		writeError(response, http.StatusTooManyRequests, "hetzner_rate_limited")
	case errors.Is(err, hetzner.ErrUnauthorized):
		writeError(response, http.StatusForbidden, "hetzner_token_rejected")
	default:
		writeError(response, http.StatusBadGateway, "hetzner_provider_unavailable")
	}
	return false
}

func (server *Server) persistHetznerRecord(ctx context.Context, profileID string, record hetznerRecord) bool {
	encoded, err := json.Marshal(record)
	if err != nil {
		return false
	}
	return server.store.RecordHetznerProject(ctx, state.HetznerProjectReference{ProfileID: profileID, Reference: string(encoded), RecordedAt: time.Now().UTC()}) == nil
}

func (server *Server) loadHetznerRecord(ctx context.Context, profileID string) (hetznerRecord, bool) {
	stored, err := server.store.GetHetznerProject(ctx, profileID)
	if err != nil {
		return hetznerRecord{}, false
	}
	var record hetznerRecord
	if err := json.Unmarshal([]byte(stored.Reference), &record); err != nil {
		return hetznerRecord{}, false
	}
	return record, true
}

// hetznerView is the secret-free browser projection: the token's verdict and
// fingerprint, the classified inventory, the delegation check, and the last
// plan. The token value appears nowhere.
func hetznerView(record hetznerRecord) map[string]any {
	view := map[string]any{"naming": record.Naming}
	if record.Token != nil {
		view["token"] = record.Token
		view["validatedAt"] = record.ValidatedAt
	}
	if record.Inventory != nil {
		view["inventory"] = record.Inventory
		view["inspectedAt"] = record.InspectedAt
	}
	if record.Delegation != nil {
		view["delegation"] = record.Delegation
	}
	if record.Plan != nil {
		view["changePlan"] = record.Plan
		view["planId"] = record.PlanID
		view["approvable"] = record.Plan.Approvable()
	}
	return view
}
