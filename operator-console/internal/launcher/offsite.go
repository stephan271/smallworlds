package launcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/githttps"
	"github.com/stephan271/smallworlds/operator-console/internal/github"
	"github.com/stephan271/smallworlds/operator-console/internal/offsite"
	"github.com/stephan271/smallworlds/operator-console/internal/state"
	"github.com/stephan271/smallworlds/operator-console/internal/vault"
	"github.com/stephan271/smallworlds/operator-console/internal/workflow"
)

// ClusterSecretApplier writes a Cluster Secret through the operator's authorized
// path (a live Kubernetes API call in production). It is the *only* channel the
// offsite credentials take into the cluster — they never travel through Git. The
// launcher injects a deterministic implementation in tests; production wires a
// live adapter, which remains deferred like the other live cluster adapters.
type ClusterSecretApplier interface {
	ApplyClusterSecret(ctx context.Context, namespace, name string, data map[string]string) error
}

// ErrClusterSecretUnavailable is returned by the launcher default when no live
// cluster secret path is wired, so the propose handler can refuse honestly
// rather than claim the Cluster Secret was written.
var ErrClusterSecretUnavailable = errors.New("launcher: cluster secret path unavailable")

// unavailableClusterSecretApplier is the launcher default: with no live cluster
// adapter it cannot write a Cluster Secret, so it refuses rather than pretending
// the credentials reached the cluster.
type unavailableClusterSecretApplier struct{}

func (unavailableClusterSecretApplier) ApplyClusterSecret(context.Context, string, string, map[string]string) error {
	return ErrClusterSecretUnavailable
}

// OffsiteValidationRunner starts ONLY the declared bounded backup and
// replication work for a validation run, then returns the offsite evidence it
// observed (local-backup completion, replication completion, the offsite
// Recovery Point timestamp, and versioning). It deliberately returns *evidence*,
// never a pass/fail verdict — the verdict is derived by classifying that
// evidence, so a green Job exit can never be mistaken for real protection. The
// launcher injects a deterministic runner in tests; the live cluster adapter is
// deferred like the others.
type OffsiteValidationRunner interface {
	RunValidation(ctx context.Context, profileID string, destination offsite.Destination) (offsite.ValidationEvidence, error)
}

// ErrOffsiteValidationUnavailable is returned by the launcher default when no
// live backup/replication path is wired, so the validation run fails honestly
// rather than fabricating evidence.
var ErrOffsiteValidationUnavailable = errors.New("launcher: offsite validation path unavailable")

// unavailableOffsiteValidationRunner is the launcher default: with no live
// cluster adapter it cannot start backup/replication or read a Recovery Point,
// so it refuses rather than inventing evidence.
type unavailableOffsiteValidationRunner struct{}

func (unavailableOffsiteValidationRunner) RunValidation(context.Context, string, offsite.Destination) (offsite.ValidationEvidence, error) {
	return offsite.ValidationEvidence{}, ErrOffsiteValidationUnavailable
}

// unavailableOffsiteInspector is the launcher default: with no real S3 client it
// cannot inspect a destination bucket, so it reports reachability false and
// versioning unknown — honestly forcing an explicit acknowledgement rather than
// claiming versioning is enabled.
type unavailableOffsiteInspector struct{}

func (unavailableOffsiteInspector) Inspect(context.Context, offsite.Destination, offsite.Credentials) (offsite.Inspection, error) {
	return offsite.Inspection{Reachable: false, Versioning: offsite.VersioningUnknown}, nil
}

// offsiteRecord is the secret-free JSON persisted for a profile's offsite
// destination: the destination reference plus its last bucket inspection. No
// credential value is ever stored here — the access key and secret live only in
// the Launcher Vault.
type offsiteRecord struct {
	Reference  offsite.Reference  `json:"reference"`
	Inspection offsite.Inspection `json:"inspection"`
	Proposal   *offsiteProposal   `json:"proposal,omitempty"`
	Validation *offsiteValidation `json:"validation,omitempty"`
}

// offsiteValidation is the secret-free verdict of the last bounded validation
// run: the evidence-derived result, its remediation route, the offsite Recovery
// Point observed, and the run it came from. It is the authoritative offsite
// protection verdict — drawn from observed evidence, not a Job exit status.
type offsiteValidation struct {
	Result          string    `json:"result"`
	RemediationKey  string    `json:"remediationKey"`
	Verified        bool      `json:"verified"`
	RecoveryPointAt time.Time `json:"recoveryPointAt,omitempty"`
	RunID           string    `json:"runId"`
	ObservedAt      time.Time `json:"observedAt"`
}

// offsiteProposal is the secret-free record of a submitted offsite Change Plan:
// the remote commit identity of the Git proposal and whether the Cluster Secret
// was applied. It carries no credential value — the merge is observed, not
// performed here.
type offsiteProposal struct {
	Provider      string    `json:"provider"`
	Branch        string    `json:"branch,omitempty"`
	Commit        string    `json:"commit"`
	URL           string    `json:"url,omitempty"`
	SecretApplied bool      `json:"secretApplied"`
	OpenedAt      time.Time `json:"openedAt"`
}

func offsiteVaultKeys(profileID string) (accessKey, secretKey string) {
	return profileID + "/offsite-access-key-id", profileID + "/offsite-secret-access-key"
}

func (server *Server) inspectOffsite(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		ProfileID       string `json:"profileId"`
		Endpoint        string `json:"endpoint"`
		Region          string `json:"region"`
		Bucket          string `json:"bucket"`
		AccessKeyID     string `json:"accessKeyId"`
		SecretAccessKey string `json:"secretAccessKey"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 32*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_offsite_destination")
		return
	}
	if _, err := server.store.GetProfile(request.Context(), input.ProfileID); errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "offsite_inspection_failed")
		return
	}
	destination := offsite.Destination{Endpoint: input.Endpoint, Region: input.Region, Bucket: input.Bucket}
	if err := destination.Validate(); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_offsite_destination")
		return
	}
	accessKeyVaultKey, secretVaultKey := offsiteVaultKeys(input.ProfileID)
	credentials := offsite.Credentials{
		AccessKeyID:     server.rememberedSecret(input.AccessKeyID, accessKeyVaultKey),
		SecretAccessKey: server.rememberedSecret(input.SecretAccessKey, secretVaultKey),
	}
	if !credentials.Valid() {
		writeError(response, http.StatusBadRequest, "invalid_offsite_credentials")
		return
	}

	// Custody the credential values in the Launcher Vault, never in Git or the
	// persisted reference, and never returned to the browser.
	if err := server.vault.Store(accessKeyVaultKey, credentials.AccessKeyID); errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "offsite_storage_failed")
		return
	}
	if err := server.vault.Store(secretVaultKey, credentials.SecretAccessKey); err != nil {
		writeError(response, http.StatusInternalServerError, "offsite_storage_failed")
		return
	}
	expiresAt := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, reference := range []state.CredentialReference{
		{ProfileID: input.ProfileID, Kind: "offsite-s3-access-key", VaultKey: accessKeyVaultKey, Source: "operator", ExpiresAt: expiresAt, RotationStatus: "current"},
		{ProfileID: input.ProfileID, Kind: "offsite-s3-secret", VaultKey: secretVaultKey, Source: "operator", ExpiresAt: expiresAt, RotationStatus: "current"},
	} {
		if err := server.store.UpsertCredentialReference(request.Context(), reference); err != nil {
			writeError(response, http.StatusInternalServerError, "offsite_storage_failed")
			return
		}
	}

	inspection, err := server.offsite.Inspect(request.Context(), destination, credentials)
	if err != nil {
		writeError(response, http.StatusBadGateway, "offsite_inspection_failed")
		return
	}
	record := offsiteRecord{
		Reference:  offsite.NewReference(destination, "", "", credentials.AccessKeyID, false),
		Inspection: inspection,
	}
	if !server.persistOffsiteRecord(request.Context(), input.ProfileID, record) {
		writeError(response, http.StatusInternalServerError, "offsite_storage_failed")
		return
	}
	writeJSON(response, http.StatusOK, offsiteView(record))
}

func (server *Server) offsiteStatus(response http.ResponseWriter, request *http.Request) {
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
	record, ok := server.loadOffsiteRecord(request.Context(), profileID)
	if !ok {
		writeError(response, http.StatusNotFound, "offsite_not_configured")
		return
	}
	writeJSON(response, http.StatusOK, offsiteView(record))
}

func (server *Server) planOffsite(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		ProfileID    string `json:"profileId"`
		Acknowledged bool   `json:"acknowledged"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_offsite_plan_request")
		return
	}
	record, ok := server.loadOffsiteRecord(request.Context(), input.ProfileID)
	if !ok {
		writeError(response, http.StatusConflict, "offsite_not_configured")
		return
	}
	// Load the access key id from the Vault so the plan's fingerprint stays bound
	// to the custodied credential; the value never leaves this handler.
	accessKeyVaultKey, _ := offsiteVaultKeys(input.ProfileID)
	accessKeyID, err := server.vault.Load(accessKeyVaultKey)
	if errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	}
	if errors.Is(err, vault.ErrSecretNotFound) {
		writeError(response, http.StatusConflict, "offsite_credentials_missing")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "offsite_plan_failed")
		return
	}

	plan, err := offsite.Plan(record.Reference.Destination, record.Reference.Schedule, record.Reference.SecretName, accessKeyID, record.Inspection, input.Acknowledged)
	if errors.Is(err, offsite.ErrVersioningUnacknowledged) {
		writeError(response, http.StatusConflict, "offsite_versioning_acknowledgement_required")
		return
	}
	if errors.Is(err, offsite.ErrInvalidDestination) {
		writeError(response, http.StatusBadRequest, "invalid_offsite_destination")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "offsite_plan_failed")
		return
	}

	record.Reference = plan.Reference
	if !server.persistOffsiteRecord(request.Context(), input.ProfileID, record) {
		writeError(response, http.StatusInternalServerError, "offsite_plan_failed")
		return
	}

	workflowPlan, err := server.workflow.PlanChange(request.Context(), input.ProfileID, "ConfigureOffsiteProtection", plan.GitDiff, []workflow.Effect{
		{Code: "offsite.secret.injected", MessageKey: "plan.effect.offsite_secret"},
		{Code: "offsite.overlay.proposed", MessageKey: "plan.effect.offsite_overlay"},
	})
	if err != nil {
		writeError(response, http.StatusInternalServerError, "offsite_plan_failed")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{
		"plan":         workflowPlan,
		"secret":       plan.Secret,
		"gitDiff":      plan.GitDiff,
		"implications": plan.Implications,
	})
}

// proposeOffsite turns an approved offsite Change Plan into two authorized
// effects: it writes the credential values to the Cluster Secret through the
// authorized secret path, then opens a Git proposal carrying only the non-secret
// destination ConfigMap. It never mutates the destination config in the cluster
// directly, never writes a credential to Git, and never logs or returns a
// credential value. The merge stays a human step observed later.
func (server *Server) proposeOffsite(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		ProfileID string `json:"profileId"`
		PlanID    string `json:"planId"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" || input.PlanID == "" {
		writeError(response, http.StatusBadRequest, "invalid_offsite_proposal_request")
		return
	}
	plan, err := server.store.GetPlan(request.Context(), input.PlanID)
	if errors.Is(err, state.ErrNotFound) || plan.ProfileID != input.ProfileID || plan.Intent != "ConfigureOffsiteProtection" || plan.Status != "approved" {
		writeError(response, http.StatusConflict, "offsite_plan_not_approved")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "offsite_proposal_failed")
		return
	}
	record, ok := server.loadOffsiteRecord(request.Context(), input.ProfileID)
	if !ok {
		writeError(response, http.StatusConflict, "offsite_not_configured")
		return
	}
	identity, err := server.store.GetOverlayIdentity(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusConflict, "offsite_overlay_missing")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "offsite_proposal_failed")
		return
	}

	// Load both credential values from the Vault — the access key id also keeps
	// the rebuilt plan's fingerprint bound to the custodied key. Values never
	// leave this handler except into the Cluster Secret.
	accessKeyVaultKey, secretVaultKey := offsiteVaultKeys(input.ProfileID)
	accessKeyID, secretAccessKey, ok := server.loadOffsiteCredentials(response, accessKeyVaultKey, secretVaultKey)
	if !ok {
		return
	}

	changePlan, err := offsite.Plan(record.Reference.Destination, record.Reference.Schedule, record.Reference.SecretName, accessKeyID, record.Inspection, record.Reference.VersioningAcknowledged)
	if errors.Is(err, offsite.ErrVersioningUnacknowledged) {
		writeError(response, http.StatusConflict, "offsite_versioning_acknowledgement_required")
		return
	}
	if errors.Is(err, offsite.ErrInvalidDestination) {
		writeError(response, http.StatusBadRequest, "invalid_offsite_destination")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "offsite_proposal_failed")
		return
	}
	// The reviewed diff must be exactly what the operator approved.
	if !matchesOffsitePlan(plan, changePlan.GitDiff) {
		writeError(response, http.StatusConflict, "offsite_plan_mismatch")
		return
	}

	// 1) The credentials reach the cluster only through the authorized secret path.
	if err := server.secrets.ApplyClusterSecret(request.Context(), offsite.Namespace, changePlan.Secret.SecretName, offsite.SecretMaterial(offsite.Credentials{AccessKeyID: accessKeyID, SecretAccessKey: secretAccessKey})); err != nil {
		if errors.Is(err, ErrClusterSecretUnavailable) {
			writeError(response, http.StatusServiceUnavailable, "offsite_cluster_secret_unavailable")
			return
		}
		writeError(response, http.StatusBadGateway, "offsite_cluster_secret_failed")
		return
	}

	// 2) Only the non-secret destination config travels to Git, as a proposal.
	branch := githttps.ProposalBranchForDiff(changePlan.GitDiff)
	files := changePlan.ProposalFiles()
	proposal := offsiteProposal{Provider: identity.Provider, SecretApplied: true, OpenedAt: time.Now().UTC()}
	var mergeInstructionKey string
	switch identity.Provider {
	case "generic-https":
		username, uOK := server.loadOffsiteGitSecret(response, input.ProfileID+"/generic-git-username")
		if !uOK {
			return
		}
		token, tOK := server.loadOffsiteGitSecret(response, input.ProfileID+"/generic-git-token")
		if !tOK {
			return
		}
		submitted, err := server.genericGit.CreateProposalBranch(request.Context(), identity.RepositoryURL, username, token, branch, files)
		if errors.Is(err, githttps.ErrAuthentication) {
			writeError(response, http.StatusForbidden, "generic_git_authentication_failed")
			return
		}
		if errors.Is(err, githttps.ErrConcurrentChange) {
			writeError(response, http.StatusConflict, "offsite_proposal_conflict")
			return
		}
		if err != nil {
			writeError(response, http.StatusBadGateway, "offsite_proposal_failed")
			return
		}
		proposal.Branch, proposal.Commit = submitted.Branch, submitted.Commit
		mergeInstructionKey = "generic_git_manual_merge"
	case "github":
		token, tOK := server.loadOffsiteGitSecret(response, input.ProfileID+"/github-creation-token")
		if !tOK {
			return
		}
		submitted, err := server.github.CreateProposalWithFiles(request.Context(), token, github.Repository{FullName: identity.Repository, DefaultBranch: "main"}, branch, offsite.ProposalTitle, offsiteProposalBody(changePlan.Reference), files)
		if err != nil {
			// One refusal code covers every way this can fail, so GitHub's own
			// explanation only survives in the launcher's output.
			log.Printf("github overlay: propose offsite change: %v", err)
			writeError(response, http.StatusBadGateway, "offsite_proposal_failed")
			return
		}
		proposal.Branch, proposal.Commit, proposal.URL = branch, submitted.Commit, submitted.URL
		mergeInstructionKey = "github_manual_merge"
	default:
		writeError(response, http.StatusConflict, "offsite_overlay_unsupported")
		return
	}

	// 3) Record the proposal and its remote commit identity in the Activity Record.
	record.Proposal = &proposal
	if !server.persistOffsiteRecord(request.Context(), input.ProfileID, record) {
		writeError(response, http.StatusInternalServerError, "offsite_proposal_failed")
		return
	}
	server.recordOffsiteProposalEvent(request.Context(), input.ProfileID, proposal)

	writeJSON(response, http.StatusCreated, map[string]any{
		"provider":            proposal.Provider,
		"branch":              proposal.Branch,
		"commit":              proposal.Commit,
		"url":                 proposal.URL,
		"secret":              changePlan.Secret,
		"mergeInstructionKey": mergeInstructionKey,
	})
}

// loadOffsiteCredentials returns the custodied access key id and secret access
// key, writing the appropriate error (423/409/500) and returning ok=false on
// failure. The values are only ever passed to the Cluster Secret path.
func (server *Server) loadOffsiteCredentials(response http.ResponseWriter, accessKeyVaultKey, secretVaultKey string) (string, string, bool) {
	accessKeyID, err := server.vault.Load(accessKeyVaultKey)
	if errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return "", "", false
	}
	if errors.Is(err, vault.ErrSecretNotFound) {
		writeError(response, http.StatusConflict, "offsite_credentials_missing")
		return "", "", false
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "offsite_proposal_failed")
		return "", "", false
	}
	secretAccessKey, err := server.vault.Load(secretVaultKey)
	if errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return "", "", false
	}
	if errors.Is(err, vault.ErrSecretNotFound) {
		writeError(response, http.StatusConflict, "offsite_credentials_missing")
		return "", "", false
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "offsite_proposal_failed")
		return "", "", false
	}
	return accessKeyID, secretAccessKey, true
}

// loadOffsiteGitSecret loads a stored Git provider credential, writing the
// error (423/409/500) and returning ok=false on failure.
func (server *Server) loadOffsiteGitSecret(response http.ResponseWriter, vaultKey string) (string, bool) {
	value, err := server.vault.Load(vaultKey)
	if errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return "", false
	}
	if errors.Is(err, vault.ErrSecretNotFound) {
		writeError(response, http.StatusConflict, "offsite_git_credentials_missing")
		return "", false
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "offsite_proposal_failed")
		return "", false
	}
	return value, true
}

// matchesOffsitePlan confirms the reviewed Git diff still hashes to the approved
// plan's digest, so a proposal cannot drift from what was approved.
func matchesOffsitePlan(plan state.PlanRecord, gitDiff string) bool {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%d\n%s", "ConfigureOffsiteProtection", plan.ProfileID, plan.ProfileRevision, gitDiff)))
	return plan.Digest == hex.EncodeToString(digest[:])
}

// offsiteProposalBody renders a secret-free pull-request body describing the
// destination shape. It references the Cluster Secret by name only.
func offsiteProposalBody(reference offsite.Reference) string {
	return fmt.Sprintf("Add the offsite backup destination for bucket %q at %q (region %q). Credentials are held in the %q Cluster Secret and are not part of this change.",
		reference.Destination.Bucket, reference.Destination.Endpoint, reference.Destination.Region, reference.SecretName)
}

// recordOffsiteProposalEvent appends a secret-free Activity Record entry noting
// the proposal's provider and remote commit identity. Failures are non-fatal:
// the proposal is already submitted.
func (server *Server) recordOffsiteProposalEvent(ctx context.Context, profileID string, proposal offsiteProposal) {
	parameters, err := json.Marshal(map[string]string{"provider": proposal.Provider, "commit": proposal.Commit, "branch": proposal.Branch})
	if err != nil {
		return
	}
	_, _ = server.store.AppendEvent(ctx, state.EventRecord{
		ProfileID:  profileID,
		Type:       "offsite.proposal.opened",
		MessageKey: "activity.offsite.proposed",
		Parameters: string(parameters),
		OccurredAt: proposal.OpenedAt,
	})
}

// offsiteValidationIntent is the fixed plan intent the launcher owns for the
// bounded backup/replication validation run. Browser input never selects it.
const offsiteValidationIntent = "ValidateOffsiteProtection"

// validateOffsite plans a bounded validation run for a configured, proposed
// offsite destination. Approving the returned plan (via /plans/{id}/approve)
// starts the run, whose executor triggers only the declared backup + replication
// work and records a verdict drawn from observed offsite evidence.
func (server *Server) validateOffsite(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		ProfileID string `json:"profileId"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_offsite_validation_request")
		return
	}
	record, ok := server.loadOffsiteRecord(request.Context(), input.ProfileID)
	if !ok {
		writeError(response, http.StatusConflict, "offsite_not_configured")
		return
	}
	// Validation is only meaningful once the destination config has been proposed
	// (and, after merge, delivered); the launcher gates on the furthest state it
	// can observe.
	if record.Proposal == nil {
		writeError(response, http.StatusConflict, "offsite_proposal_required")
		return
	}
	plan, err := server.workflow.PlanChange(request.Context(), input.ProfileID, offsiteValidationIntent, offsiteValidationDetail(record.Reference), []workflow.Effect{
		{Code: "offsite.validation.backup_started", MessageKey: "plan.effect.offsite_validation_backup"},
		{Code: "offsite.validation.evidence_verified", MessageKey: "plan.effect.offsite_validation_evidence"},
	})
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "offsite_validation_failed")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"plan": plan})
}

// offsiteValidationDetail is the stable, secret-free description of the declared
// work bound into the validation plan's digest.
func offsiteValidationDetail(reference offsite.Reference) string {
	return fmt.Sprintf("offsite.validation:backup+replication->%s/%s", reference.Destination.Bucket, reference.SecretName)
}

// executeOffsiteValidation is the registered executor for the validation intent.
// It starts only the declared backup/replication work through the runner seam,
// persists a checkpoint per bounded stage, then classifies the outcome from the
// observed offsite evidence — never from a Job exit status — and records the
// verdict on the offsite record and as the run's evidence.
func (server *Server) executeOffsiteValidation(runID string) {
	ctx := context.Background()
	run, err := server.store.GetRun(ctx, runID)
	if err != nil || run.State != "running" {
		return
	}
	record, ok := server.loadOffsiteRecord(ctx, run.ProfileID)
	if !ok {
		server.failOffsiteRun(ctx, run, "offsite-not-configured")
		return
	}
	if !server.checkpointOffsiteRun(ctx, run, "declared-work-started") {
		return
	}
	if server.offsiteRunCancelled(ctx, runID) {
		_ = server.store.CompleteRunCancellation(ctx, runID, "declared-work-started")
		return
	}
	evidence, err := server.offsiteValidator.RunValidation(ctx, run.ProfileID, record.Reference.Destination)
	if err != nil {
		server.failOffsiteRun(ctx, run, "validation-unavailable")
		return
	}
	if !server.checkpointOffsiteRun(ctx, run, "offsite-evidence-observed") {
		return
	}
	now := time.Now().UTC()
	result := offsite.ClassifyValidation(evidence, now, offsite.DefaultOffsiteMaxAge)
	record.Validation = &offsiteValidation{
		Result:          string(result),
		RemediationKey:  result.RemediationKey(),
		Verified:        result.Verified(),
		RecoveryPointAt: evidence.OffsiteRecoveryPointAt,
		RunID:           runID,
		ObservedAt:      now,
	}
	server.persistOffsiteRecord(ctx, run.ProfileID, record)
	// The verdict is recorded as the run's observed evidence, not a pass/fail Job
	// status; the offsite record above holds the authoritative protection verdict.
	_ = server.store.CompleteRunVerification(ctx, runID, "validation-complete", string(result), now)
}

// checkpointOffsiteRun advances a running validation to a named safe point and
// appends a redacted Activity Record checkpoint event.
func (server *Server) checkpointOffsiteRun(ctx context.Context, run state.RunRecord, checkpoint string) bool {
	if err := server.store.UpdateRun(ctx, run.ID, "running", checkpoint, "", nil); err != nil {
		return false
	}
	_, _ = server.store.AppendEvent(ctx, state.EventRecord{
		ProfileID:  run.ProfileID,
		RunID:      run.ID,
		Type:       "run.checkpoint",
		MessageKey: "activity.run.checkpoint",
		Parameters: `{"checkpoint":"` + checkpoint + `"}`,
		OccurredAt: time.Now().UTC(),
	})
	return true
}

// offsiteRunCancelled reports whether a cooperative cancellation was requested.
func (server *Server) offsiteRunCancelled(ctx context.Context, runID string) bool {
	run, err := server.store.GetRun(ctx, runID)
	return err == nil && run.CancellationState == "requested"
}

// failOffsiteRun moves a validation run to a terminal failed state at a named
// checkpoint and records the failure in the Activity Record.
func (server *Server) failOffsiteRun(ctx context.Context, run state.RunRecord, checkpoint string) {
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

func (server *Server) persistOffsiteRecord(ctx context.Context, profileID string, record offsiteRecord) bool {
	encoded, err := json.Marshal(record)
	if err != nil {
		return false
	}
	return server.store.RecordOffsiteProtection(ctx, state.OffsiteProtectionReference{ProfileID: profileID, Reference: string(encoded), RecordedAt: time.Now().UTC()}) == nil
}

func (server *Server) loadOffsiteRecord(ctx context.Context, profileID string) (offsiteRecord, bool) {
	stored, err := server.store.GetOffsiteProtection(ctx, profileID)
	if err != nil {
		return offsiteRecord{}, false
	}
	var record offsiteRecord
	if err := json.Unmarshal([]byte(stored.Reference), &record); err != nil {
		return offsiteRecord{}, false
	}
	return record, true
}

// offsiteView is the secret-free browser projection of a stored offsite record:
// destination shape, the key fingerprint, and the inspection verdict — never the
// access key or secret.
func offsiteView(record offsiteRecord) map[string]any {
	view := map[string]any{
		"destination":             record.Reference.Destination,
		"schedule":                record.Reference.Schedule,
		"secretName":              record.Reference.SecretName,
		"accessKeyFingerprint":    record.Reference.AccessKeyFingerprint,
		"versioningAcknowledged":  record.Reference.VersioningAcknowledged,
		"reachable":               record.Inspection.Reachable,
		"versioning":              record.Inspection.Versioning,
		"requiresAcknowledgement": record.Inspection.RequiresAcknowledgement(),
	}
	if record.Proposal != nil {
		view["proposal"] = map[string]any{
			"provider":      record.Proposal.Provider,
			"branch":        record.Proposal.Branch,
			"commit":        record.Proposal.Commit,
			"url":           record.Proposal.URL,
			"secretApplied": record.Proposal.SecretApplied,
			"openedAt":      record.Proposal.OpenedAt,
		}
	}
	if record.Validation != nil {
		view["validation"] = map[string]any{
			"result":          record.Validation.Result,
			"remediationKey":  record.Validation.RemediationKey,
			"verified":        record.Validation.Verified,
			"recoveryPointAt": record.Validation.RecoveryPointAt,
			"runId":           record.Validation.RunID,
			"observedAt":      record.Validation.ObservedAt,
		}
	}
	return view
}
