package console

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/addcapability"
	"github.com/stephan271/smallworlds/operator-console/internal/assessment"
	"github.com/stephan271/smallworlds/operator-console/internal/capability"
	"github.com/stephan271/smallworlds/operator-console/internal/consoleworkflow"
)

// additionPlanTTL bounds how long an add-capability Change Plan stays approvable
// before it must be re-planned against fresh capacity and state.
const additionPlanTTL = time.Hour

// CapacityReporter reports the cluster's live capacity so an add-capability plan
// can compare estimated need against real headroom. Production observes node
// allocatable/used; tests inject a deterministic reporter. The live adapter is
// deferred like the console's other cluster seams.
type CapacityReporter interface {
	Capacity(ctx context.Context) (addcapability.Capacity, error)
}

// ErrCapacityUnavailable is returned by the default reporter when no live
// capacity source is wired, so planning refuses honestly rather than comparing
// against fabricated headroom.
var ErrCapacityUnavailable = errors.New("console: cluster capacity unavailable")

type unavailableCapacityReporter struct{}

func (unavailableCapacityReporter) Capacity(context.Context) (addcapability.Capacity, error) {
	return addcapability.Capacity{}, ErrCapacityUnavailable
}

// OpenedProposal is the secret-free remote identity of a Git proposal the
// console opened against the operator's overlay: the provider, the proposal
// branch, the remote commit, and (when the provider exposes one) the pull
// request URL. It never carries a credential.
type OpenedProposal struct {
	Provider string `json:"provider"`
	Branch   string `json:"branch,omitempty"`
	Commit   string `json:"commit"`
	URL      string `json:"url,omitempty"`
}

// ProposalOpener opens a Git proposal (branch + pull request) carrying the exact
// catalog-derived files against the operator's overlay, and returns its remote
// commit identity. It NEVER merges — the merge stays a human step the console
// only observes. Production wires a live GitHub/generic-git adapter holding the
// overlay credentials; tests inject a deterministic opener. The live adapter is
// deferred like the console's other cluster seams.
type ProposalOpener interface {
	OpenProposal(ctx context.Context, title, body string, files map[string]string) (OpenedProposal, error)
}

// ErrProposalUnavailable is returned by the default opener when no live overlay
// credential path is wired, so proposing refuses honestly rather than claiming a
// proposal was opened.
var ErrProposalUnavailable = errors.New("console: git proposal path unavailable")

type unavailableProposalOpener struct{}

func (unavailableProposalOpener) OpenProposal(context.Context, string, string, map[string]string) (OpenedProposal, error) {
	return OpenedProposal{}, ErrProposalUnavailable
}

// observedStates assesses every catalog capability and returns its headline
// state, the input the planner uses to decide what is disabled and addable.
func (server *Server) observedStates(ctx context.Context) map[string]assessment.CapabilityState {
	states := make(map[string]assessment.CapabilityState, len(server.catalog))
	for _, ref := range server.catalog {
		states[ref.ID] = server.config.Assessor.Assess(ctx, ref).State
	}
	return states
}

// handleAdditionOffers lists the Community Applications the operator may add now.
func (server *Server) handleAdditionOffers(response http.ResponseWriter, request *http.Request) {
	if len(server.richCatalog.Capabilities) == 0 {
		writeJSON(response, http.StatusOK, map[string]any{"offers": []addcapability.Offer{}})
		return
	}
	offers, err := addcapability.Offers(server.richCatalog, server.observedStates(request.Context()), server.deploymentMode)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "offers_unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"offers": offers})
}

// handleAdditionPlan builds a Change Plan for adding one capability: it compares
// the estimated resource need against live capacity, discloses the exposure,
// persistent-data, and protection implications, renders the exact catalog-derived
// Git diff, and persists a compact approvable plan in the Activity Record.
func (server *Server) handleAdditionPlan(response http.ResponseWriter, request *http.Request) {
	current, ok := server.readSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if len(server.richCatalog.Capabilities) == 0 {
		writeError(response, http.StatusServiceUnavailable, "addition_unavailable")
		return
	}
	var input struct {
		CapabilityID string `json:"capabilityId"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.CapabilityID == "" {
		writeError(response, http.StatusBadRequest, "invalid_addition_request")
		return
	}
	capacity, err := server.capacity.Capacity(request.Context())
	if errors.Is(err, ErrCapacityUnavailable) {
		writeError(response, http.StatusServiceUnavailable, "capacity_unavailable")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadGateway, "capacity_unavailable")
		return
	}
	plan, err := addcapability.BuildPlan(server.richCatalog, input.CapabilityID, server.observedStates(request.Context()), capacity, server.overlayTarget, server.deploymentMode)
	if errors.Is(err, addcapability.ErrNotOffered) {
		writeError(response, http.StatusConflict, "capability_not_offered")
		return
	}
	if errors.Is(err, addcapability.ErrInvalidOverlayTarget) {
		writeError(response, http.StatusInternalServerError, "overlay_target_invalid")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "addition_plan_failed")
		return
	}

	planID := server.newID()
	summary := additionSummary(plan, diffDigest(plan.GitDiff))
	risks := []consoleworkflow.RiskLabel{consoleworkflow.RiskCostBearing}
	record := consoleworkflow.ChangePlan{
		ID:        planID,
		Digest:    consoleworkflow.ComputeDigest(consoleworkflow.IntentAddCapability, current.Username, summary, risks),
		Intent:    consoleworkflow.IntentAddCapability,
		Actor:     current.Username,
		Summary:   summary,
		Risks:     risks,
		CreatedAt: server.now(),
		ExpiresAt: server.now().Add(additionPlanTTL),
	}
	if err := server.workflow.PutPlan(request.Context(), record); err != nil {
		writeError(response, http.StatusInternalServerError, "addition_plan_failed")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{
		"planId":  planID,
		"digest":  record.Digest,
		"summary": summary,
		"plan":    plan,
	})
}

// handleAdditionApprove binds the operator's approval to the plan's current
// digest. Approval is what later authorizes opening the proposal.
func (server *Server) handleAdditionApprove(response http.ResponseWriter, request *http.Request) {
	current, ok := server.readSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	plan, ok := server.loadAdditionPlan(response, request, request.PathValue("id"))
	if !ok {
		return
	}
	approved, err := plan.Approve(current.Username, server.now())
	if errors.Is(err, consoleworkflow.ErrApprovalMismatch) {
		writeError(response, http.StatusConflict, "addition_plan_expired")
		return
	}
	if err != nil {
		writeError(response, http.StatusConflict, "addition_plan_invalid")
		return
	}
	if err := server.workflow.PutPlan(request.Context(), approved); err != nil {
		writeError(response, http.StatusInternalServerError, "addition_approve_failed")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"planId": approved.ID, "approvedBy": current.Username})
}

// handleAdditionPropose turns an approved plan into a Git proposal against the
// operator's overlay. It re-derives the exact catalog-derived diff and refuses
// if it no longer matches what was approved, opens the proposal (branch + pull
// request) through the authorized overlay path, and records the proposal and its
// remote commit identity in the Activity Record as a Workflow Run — never merging
// and never mutating live Kubernetes resources directly.
func (server *Server) handleAdditionPropose(response http.ResponseWriter, request *http.Request) {
	if _, ok := server.readSession(request); !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	plan, ok := server.loadAdditionPlan(response, request, request.PathValue("id"))
	if !ok {
		return
	}
	if err := plan.ValidateApproval(server.now()); err != nil {
		writeError(response, http.StatusConflict, "addition_not_approved")
		return
	}
	target, approvedDigest, ok := parseAdditionSummary(plan.Summary)
	if !ok {
		writeError(response, http.StatusInternalServerError, "addition_plan_invalid")
		return
	}
	// Re-derive the exact catalog-derived diff. Capacity does not affect the diff,
	// so a zero capacity is fine here; a state change (e.g. a dependency now
	// present) changes the diff and is caught as a mismatch below.
	rebuilt, err := addcapability.BuildPlan(server.richCatalog, target, server.observedStates(request.Context()), addcapability.Capacity{}, server.overlayTarget, server.deploymentMode)
	if err != nil || diffDigest(rebuilt.GitDiff) != approvedDigest {
		writeError(response, http.StatusConflict, "addition_plan_mismatch")
		return
	}

	title := fmt.Sprintf("Add %s community application", target)
	body := additionProposalBody(rebuilt)
	opened, err := server.proposals.OpenProposal(request.Context(), title, body, rebuilt.Files)
	if errors.Is(err, ErrProposalUnavailable) {
		writeError(response, http.StatusServiceUnavailable, "proposal_unavailable")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadGateway, "proposal_failed")
		return
	}

	// Record the proposal in the Activity Record. The run stays open (running) —
	// the merge is a human step the console observes later, not one it performs.
	run, err := plan.Start(server.newID(), server.now())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "addition_record_failed")
		return
	}
	run = run.Checkpoint("proposal-opened", server.now())
	run.EvidenceSummary = consoleworkflow.RedactDetail(commitEvidence(opened), consoleworkflow.MaxEvidenceSummaryLength)
	if err := server.workflow.PutRun(request.Context(), run); err != nil {
		writeError(response, http.StatusInternalServerError, "addition_record_failed")
		return
	}

	writeJSON(response, http.StatusCreated, map[string]any{
		"runId":               run.ID,
		"provider":            opened.Provider,
		"branch":              opened.Branch,
		"commit":              opened.Commit,
		"url":                 opened.URL,
		"mergeObserved":       true,
		"mergeInstructionKey": "overlay_manual_merge",
	})
}

// loadAdditionPlan fetches an add-capability plan by id, writing the error and
// returning ok=false when it is missing or not an add-capability plan.
func (server *Server) loadAdditionPlan(response http.ResponseWriter, request *http.Request, id string) (consoleworkflow.ChangePlan, bool) {
	plan, err := server.workflow.GetPlan(request.Context(), id)
	if errors.Is(err, consoleworkflow.ErrNotFound) {
		writeError(response, http.StatusNotFound, "addition_plan_not_found")
		return consoleworkflow.ChangePlan{}, false
	}
	if err != nil || plan.Intent != consoleworkflow.IntentAddCapability {
		writeError(response, http.StatusConflict, "addition_plan_invalid")
		return consoleworkflow.ChangePlan{}, false
	}
	return plan, true
}

var additionSummaryTrailer = regexp.MustCompile(`\[add-capability:(\S+) sha256:([0-9a-f]{64})\]`)

// additionSummary renders a human, secret-free plan summary carrying a
// machine-parseable trailer that binds the target and the exact diff digest.
// Because the whole summary is folded into the plan digest, approving the plan
// binds precisely this target and this diff.
func additionSummary(plan addcapability.Plan, digest string) string {
	return fmt.Sprintf("Add community application %q (adds %s). [add-capability:%s sha256:%s]",
		plan.Target, strings.Join(plan.AddedCapabilities, ", "), plan.Target, digest)
}

func parseAdditionSummary(summary string) (target, digest string, ok bool) {
	match := additionSummaryTrailer.FindStringSubmatch(summary)
	if match == nil {
		return "", "", false
	}
	return match[1], match[2], true
}

func diffDigest(gitDiff string) string {
	sum := sha256.Sum256([]byte(gitDiff))
	return hex.EncodeToString(sum[:])
}

func commitEvidence(opened OpenedProposal) string {
	encoded, err := json.Marshal(opened)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// additionProposalBody renders a secret-free pull-request body describing what
// the proposal adds and its disclosed implications.
func additionProposalBody(plan addcapability.Plan) string {
	return fmt.Sprintf("Enable the %q community application by adding it to the GitOps overlay.\n\nAdds: %s\nExposure: %s\nProtection: %s\nPersistent data: %s\n\nThis change adds Desired Configuration only; it does not carry any credential.",
		plan.Target,
		strings.Join(plan.AddedCapabilities, ", "),
		strings.Join(plan.Exposure, ", "),
		strings.Join(plan.Protection, ", "),
		strings.Join(plan.PersistentData, ", "),
	)
}

// newID returns a short random hex identifier for plans and runs.
func (server *Server) newID() string {
	buffer := make([]byte, 16)
	if _, err := io.ReadFull(server.random, buffer); err != nil {
		return hex.EncodeToString([]byte(server.now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(buffer)
}

// additionPlanView is the compact projection of a persisted add-capability plan
// for the proposal workspace.
type additionPlanView struct {
	ID        string    `json:"id"`
	Summary   string    `json:"summary"`
	Actor     string    `json:"actor"`
	Approved  bool      `json:"approved"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// additionRunView is the compact projection of a persisted Workflow Run,
// carrying the proposal's remote commit identity from the Activity Record.
type additionRunView struct {
	ID              string                   `json:"id"`
	PlanID          string                   `json:"planId"`
	Phase           consoleworkflow.RunPhase `json:"phase"`
	Checkpoint      string                   `json:"checkpoint,omitempty"`
	EvidenceSummary string                   `json:"evidenceSummary,omitempty"`
	StartedAt       time.Time                `json:"startedAt"`
}

func capabilityDeploymentModeOrDefault(mode capability.DeploymentMode) capability.DeploymentMode {
	if mode == "" {
		return capability.Hetzner
	}
	return mode
}
