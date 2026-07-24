package console

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/consoleauth"
	"github.com/stephan271/smallworlds/operator-console/internal/consoleworkflow"
	"github.com/stephan271/smallworlds/operator-console/internal/operatordevice"
)

// revocationPlanTTL bounds how long a device-revocation plan stays approvable
// before it must be re-planned against the current device inventory.
const revocationPlanTTL = time.Hour

// DeviceDirectory lists the current Operator Devices from the Private Network
// coordination server (Headscale). Production wires a live adapter holding the
// coordination credentials; tests inject a deterministic directory. The live
// adapter is deferred like the console's other cluster seams.
type DeviceDirectory interface {
	Devices(ctx context.Context) ([]operatordevice.Device, error)
}

// ErrDirectoryUnavailable is returned by the default directory when no live
// coordination source is wired, so the administration surface refuses honestly
// rather than reporting a fabricated (and possibly empty) device inventory.
var ErrDirectoryUnavailable = errors.New("console: device directory unavailable")

type unavailableDirectory struct{}

func (unavailableDirectory) Devices(context.Context) ([]operatordevice.Device, error) {
	return nil, ErrDirectoryUnavailable
}

// MintedJoinKey is a freshly minted single-use join credential: the key string
// shown to the Owner exactly once and its expiry. It is never a reusable cluster
// or Headscale administrator credential — only a single-use, expiring pre-auth
// key for one device to join.
type MintedJoinKey struct {
	Key       string
	ExpiresAt time.Time
}

// InvitationIssuer mints a single-use, expiring join key (a Headscale pre-auth
// key) for one additional Operator Device. Production wires a live adapter;
// tests inject a deterministic issuer. The live adapter is deferred.
type InvitationIssuer interface {
	MintJoinKey(ctx context.Context, label string, ttl time.Duration) (MintedJoinKey, error)
}

// ErrInvitationUnavailable is returned by the default issuer when no live
// coordination source is wired, so issuing refuses honestly rather than claiming
// an invitation was created.
var ErrInvitationUnavailable = errors.New("console: invitation issuer unavailable")

type unavailableIssuer struct{}

func (unavailableIssuer) MintJoinKey(context.Context, string, time.Duration) (MintedJoinKey, error) {
	return MintedJoinKey{}, ErrInvitationUnavailable
}

// RevocationEvidence is what a revoker observed: whether it removed the selected
// device and whether it verified the device can no longer reach operator
// interfaces. It carries evidence, never a shortcut verdict, and its detail is
// redacted before storage.
type RevocationEvidence struct {
	Removed        bool
	AccessVerified bool
	Detail         string
}

// DeviceRevoker removes exactly one Operator Device by its stable identity and
// verifies the device has lost access. Production wires a live adapter that
// speaks to the coordination server; tests inject a deterministic revoker. The
// live adapter is deferred like the console's other cluster seams.
type DeviceRevoker interface {
	Revoke(ctx context.Context, stableID string) (RevocationEvidence, error)
}

// ErrRevocationUnavailable is returned by the default revoker when no live
// coordination source is wired, so execution refuses honestly rather than
// claiming a device was removed.
var ErrRevocationUnavailable = errors.New("console: device revoker unavailable")

type unavailableRevoker struct{}

func (unavailableRevoker) Revoke(context.Context, string) (RevocationEvidence, error) {
	return RevocationEvidence{}, ErrRevocationUnavailable
}

// --- enrollment ---

type enrollmentGuidanceView struct {
	Mode                   string                          `json:"mode"`
	ClusterCaTrustRequired bool                            `json:"clusterCaTrustRequired"`
	GatewayHostname        string                          `json:"gatewayHostname"`
	OperatorHostnames      []string                        `json:"operatorHostnames"`
	Steps                  []operatordevice.EnrollmentStep `json:"steps"`
}

func (server *Server) enrollmentGuidance() (operatordevice.Guidance, bool) {
	if server.config.BaseDomain == "" {
		return operatordevice.Guidance{}, false
	}
	gateway := "gateway." + server.config.BaseDomain
	hosts := []string{"console." + server.config.BaseDomain, "grafana." + server.config.BaseDomain, "argocd." + server.config.BaseDomain}
	guidance, err := operatordevice.EnrollmentGuidance(operatordevice.DeploymentMode(server.deploymentMode), gateway, hosts)
	if err != nil {
		return operatordevice.Guidance{}, false
	}
	return guidance, true
}

// handleEnrollmentGuidance serves the deployment-mode-aware enrollment path a
// joining device follows, so an Owner can preview it before issuing an
// invitation.
func (server *Server) handleEnrollmentGuidance(response http.ResponseWriter, request *http.Request) {
	guidance, ok := server.enrollmentGuidance()
	if !ok {
		writeError(response, http.StatusServiceUnavailable, "guidance_unavailable")
		return
	}
	writeJSON(response, http.StatusOK, guidanceView(guidance))
}

func guidanceView(guidance operatordevice.Guidance) enrollmentGuidanceView {
	return enrollmentGuidanceView{
		Mode:                   string(guidance.Mode),
		ClusterCaTrustRequired: guidance.ClusterCATrustRequired,
		GatewayHostname:        guidance.GatewayHostname,
		OperatorHostnames:      guidance.OperatorHostnames,
		Steps:                  guidance.Steps,
	}
}

// handleCreateInvitation mints a short-lived, single-use Enrollment Invitation
// for one additional Operator Device. It records the invitation accountably in
// the Activity Record (fingerprint only, never the key), and returns the
// one-time join key plus the enrollment guidance. The join key is shown here
// once and never persisted.
func (server *Server) handleCreateInvitation(response http.ResponseWriter, request *http.Request) {
	current, ok := server.readSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	var input struct {
		Label string `json:"label"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.Label == "" {
		writeError(response, http.StatusBadRequest, "invalid_invitation_request")
		return
	}

	minted, err := server.invitations.MintJoinKey(request.Context(), input.Label, operatordevice.DefaultInvitationTTL)
	if errors.Is(err, ErrInvitationUnavailable) {
		writeError(response, http.StatusServiceUnavailable, "invitation_unavailable")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadGateway, "invitation_failed")
		return
	}

	ttl := operatordevice.DefaultInvitationTTL
	if !minted.ExpiresAt.IsZero() {
		ttl = minted.ExpiresAt.Sub(server.now())
	}
	invitationID := server.newID()
	invitation, err := operatordevice.IssueInvitation(invitationID, input.Label, current.Username, operatordevice.Fingerprint(minted.Key), server.now(), ttl)
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_invitation_request")
		return
	}

	// Record the issuance in the Activity Record — attributable to the Owner,
	// carrying only the key fingerprint, never the join key itself.
	run := consoleworkflow.WorkflowRun{
		ID:        invitationID,
		PlanID:    invitationID,
		Intent:    consoleworkflow.IntentEnrollDevice,
		Actor:     current.Username,
		Phase:     consoleworkflow.PhasePending,
		Loki:      consoleworkflow.NewLokiReference(invitationID),
		StartedAt: server.now(),
		UpdatedAt: server.now(),
	}
	run = run.Checkpoint("invitation-issued", server.now())
	run = run.Complete(fmt.Sprintf("enrollment invitation for %q issued by %s (key fingerprint %s)", invitation.Label, invitation.IssuedBy, invitation.KeyFingerprint), server.now())
	if err := server.workflow.PutRun(request.Context(), run); err != nil {
		writeError(response, http.StatusInternalServerError, "invitation_record_failed")
		return
	}

	body := map[string]any{
		"invitationId":   invitation.ID,
		"label":          invitation.Label,
		"issuedBy":       invitation.IssuedBy,
		"keyFingerprint": invitation.KeyFingerprint,
		"expiresAt":      invitation.ExpiresAt,
		"singleUse":      true,
		// joinKey is shown exactly once; it is a single-use, expiring pre-auth key,
		// not a reusable cluster or Headscale administrator credential.
		"joinKey": minted.Key,
	}
	if guidance, ok := server.enrollmentGuidance(); ok {
		body["guidance"] = guidanceView(guidance)
	}
	writeJSON(response, http.StatusCreated, body)
}

// --- device inventory + owner-access summary ---

type deviceView struct {
	StableID    string     `json:"stableId"`
	Hostname    string     `json:"hostname"`
	Label       string     `json:"label,omitempty"`
	OwnerAccess bool       `json:"ownerAccess"`
	Self        bool       `json:"self"`
	Online      bool       `json:"online"`
	LastSeen    *time.Time `json:"lastSeen,omitempty"`
}

// handleAdministrationAccess serves the operator-access administration view,
// reachable only at Owner authority: the current Operator Devices, an
// owner-access summary (so alternative Owner access is visible before any
// revocation), and the enrollment/revocation Activity Record.
func (server *Server) handleAdministrationAccess(response http.ResponseWriter, request *http.Request) {
	devices, err := server.directory.Devices(request.Context())
	if errors.Is(err, ErrDirectoryUnavailable) {
		writeError(response, http.StatusServiceUnavailable, "directory_unavailable")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadGateway, "directory_unavailable")
		return
	}

	views := make([]deviceView, 0, len(devices))
	ownerDevices := 0
	for _, device := range devices {
		if device.OwnerAccess {
			ownerDevices++
		}
		views = append(views, deviceView{
			StableID:    device.StableID,
			Hostname:    device.Hostname,
			Label:       device.Label,
			OwnerAccess: device.OwnerAccess,
			Self:        device.Self,
			Online:      device.Online,
			LastSeen:    device.LastSeen,
		})
	}

	activity := server.adminActivity(request.Context())
	body := map[string]any{
		"devices": views,
		"summary": map[string]any{
			"totalDevices": len(views),
			"ownerDevices": ownerDevices,
		},
		"activity": activity,
	}
	if guidance, ok := server.enrollmentGuidance(); ok {
		body["guidance"] = guidanceView(guidance)
	}
	writeJSON(response, http.StatusOK, body)
}

// adminActivity projects the Owner-level (device enroll/revoke) Workflow Runs
// from the shared Activity Record. Propose-level runs (add-capability) are
// excluded so this Owner surface shows only sensitive administration history.
func (server *Server) adminActivity(ctx context.Context) []additionRunView {
	runs, err := server.workflow.ListRuns(ctx)
	if err != nil {
		return []additionRunView{}
	}
	views := make([]additionRunView, 0, len(runs))
	for _, run := range runs {
		if run.Intent.RequiredPermission() != consoleauth.PermissionAdminister {
			continue
		}
		views = append(views, additionRunView{
			ID:              run.ID,
			PlanID:          run.PlanID,
			Phase:           run.Phase,
			Checkpoint:      run.CurrentCheckpoint,
			EvidenceSummary: run.EvidenceSummary,
			StartedAt:       run.StartedAt,
		})
	}
	return views
}

// --- revocation: plan / approve / execute ---

var revocationSummaryTrailer = regexp.MustCompile(`\[revoke-device:([A-Za-z0-9][A-Za-z0-9:._-]{0,127})\]`)

func revocationSummary(assessment operatordevice.RevocationAssessment) string {
	risk := "none"
	if assessment.LockoutRisk {
		risk = assessment.LockoutReason
	}
	return fmt.Sprintf("Revoke Operator Device %q (%s). Lockout risk: %s. [revoke-device:%s]",
		assessment.Target.Hostname, assessment.AffectedStableID, risk, assessment.AffectedStableID)
}

func parseRevocationSummary(summary string) (stableID string, ok bool) {
	match := revocationSummaryTrailer.FindStringSubmatch(summary)
	if match == nil {
		return "", false
	}
	return match[1], true
}

// handleRevocationPlan inspects the current device inventory, assesses the
// effect of revoking the requested stable identity (alternative Owner access,
// lockout risk), and persists a compact approvable plan binding the affected
// stable device identity.
func (server *Server) handleRevocationPlan(response http.ResponseWriter, request *http.Request) {
	current, ok := server.readSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	var input struct {
		StableID string `json:"stableId"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.StableID == "" {
		writeError(response, http.StatusBadRequest, "invalid_device_request")
		return
	}

	devices, err := server.directory.Devices(request.Context())
	if errors.Is(err, ErrDirectoryUnavailable) {
		writeError(response, http.StatusServiceUnavailable, "directory_unavailable")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadGateway, "directory_unavailable")
		return
	}

	assessment, err := operatordevice.AssessRevocation(devices, input.StableID)
	if errors.Is(err, operatordevice.ErrDeviceNotFound) {
		writeError(response, http.StatusNotFound, "device_not_found")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_device_request")
		return
	}

	risks := []consoleworkflow.RiskLabel{consoleworkflow.RiskDestructive}
	if assessment.LockoutRisk {
		risks = append(risks, consoleworkflow.RiskLockout)
	}
	summary := revocationSummary(assessment)
	planID := server.newID()
	record := consoleworkflow.ChangePlan{
		ID:        planID,
		Digest:    consoleworkflow.ComputeDigest(consoleworkflow.IntentRevokeDevice, current.Username, summary, risks),
		Intent:    consoleworkflow.IntentRevokeDevice,
		Actor:     current.Username,
		Summary:   summary,
		Risks:     risks,
		CreatedAt: server.now(),
		ExpiresAt: server.now().Add(revocationPlanTTL),
	}
	if err := server.workflow.PutPlan(request.Context(), record); err != nil {
		writeError(response, http.StatusInternalServerError, "revocation_plan_failed")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{
		"planId":     planID,
		"digest":     record.Digest,
		"assessment": assessment,
	})
}

// handleRevocationApprove binds the Owner's approval to the plan's current
// digest, which — together with the lockout labeling — is the explicit consent a
// bounded revocation requires.
func (server *Server) handleRevocationApprove(response http.ResponseWriter, request *http.Request) {
	current, ok := server.readSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	plan, ok := server.loadRevocationPlan(response, request, request.PathValue("id"))
	if !ok {
		return
	}
	approved, err := plan.Approve(current.Username, server.now())
	if errors.Is(err, consoleworkflow.ErrApprovalMismatch) {
		writeError(response, http.StatusConflict, "revocation_plan_expired")
		return
	}
	if err != nil {
		writeError(response, http.StatusConflict, "revocation_plan_invalid")
		return
	}
	if err := server.workflow.PutPlan(request.Context(), approved); err != nil {
		writeError(response, http.StatusInternalServerError, "revocation_approve_failed")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"planId": approved.ID, "approvedBy": current.Username})
}

// handleRevocationExecute performs the bounded Runtime Action: it re-inspects the
// inventory to confirm the approved device is still present (refusing on drift),
// removes only that device, and records whether loss of access was verified in a
// redacted Activity Record. It removes exactly the affected stable identity and
// nothing else.
func (server *Server) handleRevocationExecute(response http.ResponseWriter, request *http.Request) {
	if _, ok := server.readSession(request); !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	plan, ok := server.loadRevocationPlan(response, request, request.PathValue("id"))
	if !ok {
		return
	}
	if err := plan.ValidateApproval(server.now()); err != nil {
		writeError(response, http.StatusConflict, "revocation_not_approved")
		return
	}
	stableID, ok := parseRevocationSummary(plan.Summary)
	if !ok {
		writeError(response, http.StatusInternalServerError, "revocation_plan_invalid")
		return
	}

	// Re-inspect: the approved device must still be present, or the plan no longer
	// describes reality and must not be executed.
	devices, err := server.directory.Devices(request.Context())
	if errors.Is(err, ErrDirectoryUnavailable) {
		writeError(response, http.StatusServiceUnavailable, "directory_unavailable")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadGateway, "directory_unavailable")
		return
	}
	reassessed, err := operatordevice.AssessRevocation(devices, stableID)
	if err != nil || reassessed.AffectedStableID != stableID {
		writeError(response, http.StatusConflict, "revocation_plan_mismatch")
		return
	}

	run, err := plan.Start(server.newID(), server.now())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "revocation_record_failed")
		return
	}

	evidence, err := server.revoker.Revoke(request.Context(), stableID)
	if errors.Is(err, ErrRevocationUnavailable) {
		writeError(response, http.StatusServiceUnavailable, "revocation_unavailable")
		return
	}
	if err != nil || !evidence.Removed {
		run = run.Fail(fmt.Sprintf("device %s removal failed: %s", stableID, evidence.Detail), server.now())
		_ = server.workflow.PutRun(request.Context(), run)
		writeError(response, http.StatusBadGateway, "revocation_failed")
		return
	}

	run = run.Checkpoint("device-removed", server.now())
	if evidence.AccessVerified {
		run = run.Checkpoint("access-loss-verified", server.now())
		run = run.Complete(fmt.Sprintf("device %s removed; loss of access verified: %s", stableID, evidence.Detail), server.now())
	} else {
		run = run.Fail(fmt.Sprintf("device %s removed but loss of access NOT verified: %s", stableID, evidence.Detail), server.now())
	}
	if err := server.workflow.PutRun(request.Context(), run); err != nil {
		writeError(response, http.StatusInternalServerError, "revocation_record_failed")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{
		"runId":            run.ID,
		"phase":            run.Phase,
		"affectedStableId": stableID,
		"accessVerified":   evidence.AccessVerified,
	})
}

func (server *Server) loadRevocationPlan(response http.ResponseWriter, request *http.Request, id string) (consoleworkflow.ChangePlan, bool) {
	plan, err := server.workflow.GetPlan(request.Context(), id)
	if errors.Is(err, consoleworkflow.ErrNotFound) {
		writeError(response, http.StatusNotFound, "revocation_plan_not_found")
		return consoleworkflow.ChangePlan{}, false
	}
	if err != nil || plan.Intent != consoleworkflow.IntentRevokeDevice {
		writeError(response, http.StatusConflict, "revocation_plan_invalid")
		return consoleworkflow.ChangePlan{}, false
	}
	return plan, true
}
