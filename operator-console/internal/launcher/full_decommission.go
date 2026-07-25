package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/decommission"
	"github.com/stephan271/smallworlds/operator-console/internal/state"
	"github.com/stephan271/smallworlds/operator-console/internal/workflow"
)

const fullDecommissionIntent = "FullDecommission"

// FullDecommissionInspector must obtain new ownership, protection and Recovery
// Bundle evidence. It is intentionally not the preserve-data inspector: a
// preserve-data plan is never a source of authority for destructive deletion.
type FullDecommissionInspector interface {
	InspectFull(context.Context, state.Profile) (decommission.FullInspection, error)
}

// FullDecommissionExecutor receives a single, bound deletion at a time. There
// is no bulk destroy API for an adapter to accidentally call.
type FullDecommissionExecutor interface {
	RemoveFull(context.Context, string, decommission.FullItem) error
}

// FullDecommissionStageHook is optional. It supplies a safe fault boundary
// immediately after a durable stage checkpoint, so an interruption can resume
// without replaying any earlier destructive call.
type FullDecommissionStageHook interface {
	AfterFullDecommissionStage(context.Context, string, decommission.Stage) error
}

type unavailableFullDecommissionInspector struct{}

func (unavailableFullDecommissionInspector) InspectFull(context.Context, state.Profile) (decommission.FullInspection, error) {
	return decommission.FullInspection{}, errors.New("launcher: full decommission inspection unavailable")
}

type unavailableFullDecommissionExecutor struct{}

func (unavailableFullDecommissionExecutor) RemoveFull(context.Context, string, decommission.FullItem) error {
	return errors.New("launcher: full decommission executor unavailable")
}

type fullDecommissionBinding struct {
	Plan       decommission.FullPlan       `json:"plan"`
	Inspection decommission.FullInspection `json:"inspection"`
}

func (server *Server) inspectFullDecommission(ctx context.Context, profileID string) (decommission.FullInspection, error) {
	profile, err := server.store.GetProfile(ctx, profileID)
	if err != nil {
		return decommission.FullInspection{}, err
	}
	inspection, err := server.fullDecommissionInspector.InspectFull(ctx, profile)
	if err != nil {
		return decommission.FullInspection{}, err
	}
	if inspection.ProfileID != profile.ID || inspection.ProfileRevision != profile.Revision || inspection.DeploymentMode != profile.DeploymentMode {
		return decommission.FullInspection{}, decommission.ErrInvalidInspection
	}
	if err := inspection.Finalize(); err != nil {
		return decommission.FullInspection{}, err
	}
	return inspection, nil
}

func (server *Server) fullDecommissionStatus(response http.ResponseWriter, request *http.Request) {
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
	inspection, err := server.inspectFullDecommission(request.Context(), profileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	}
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "full_decommission_inspection_unavailable")
		return
	}
	plan, err := decommission.BuildFullPlan(inspection)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "full_decommission_plan_failed")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"inspection": inspection, "preview": plan})
}

func (server *Server) planFullDecommission(response http.ResponseWriter, request *http.Request) {
	if !server.mutatingRequest(response, request) {
		return
	}
	var input struct {
		ProfileID string `json:"profileId"`
	}
	if !decodeJSON(response, request, 8*1024, &input) || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_full_decommission_request")
		return
	}
	inspection, err := server.inspectFullDecommission(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	}
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "full_decommission_inspection_unavailable")
		return
	}
	plan, err := decommission.BuildFullPlan(inspection)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "full_decommission_plan_failed")
		return
	}
	risks := []workflow.Risk{{Code: "decommission.irreversible", MessageKey: "plan.risk.full_decommission_irreversible"}}
	if plan.RequiresOwnerOverride {
		risks = append(risks, workflow.Risk{Code: "decommission.protection_insufficient", MessageKey: "plan.risk.full_decommission_protection_override"})
	}
	workflowPlan, err := server.workflow.PlanChangeWithRisks(request.Context(), input.ProfileID, fullDecommissionIntent, plan.Digest, []workflow.Effect{{Code: "decommission.profile_owned_resources_deleted", MessageKey: "plan.effect.full_decommission"}}, risks)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "full_decommission_plan_failed")
		return
	}
	binding, err := json.Marshal(fullDecommissionBinding{Plan: plan, Inspection: inspection})
	if err != nil || server.store.RecordFullDecommissionPlan(request.Context(), state.FullDecommissionPlanRecord{PlanID: workflowPlan.ID, ProfileID: input.ProfileID, Binding: string(binding), CreatedAt: workflowPlan.CreatedAt}) != nil {
		writeError(response, http.StatusInternalServerError, "full_decommission_plan_failed")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"plan": workflowPlan, "decommission": plan, "requiresTypedConfirmation": true})
}

// approveFullDecommission deliberately does not use the ordinary plan approval
// route. The typed phrase binds the profile and immutable plan digest; an
// explicit Lifecycle Authority override is additionally required when the
// freshly-inspected protection evidence is insufficient.
func (server *Server) approveFullDecommission(response http.ResponseWriter, request *http.Request) {
	if !server.mutatingRequest(response, request) {
		return
	}
	var input struct {
		PlanID         string `json:"planId"`
		ProfileID      string `json:"profileId"`
		PlanDigest     string `json:"planDigest"`
		Confirmation   string `json:"confirmation"`
		OwnerOverride  bool   `json:"ownerOverride"`
		OverrideReason string `json:"overrideReason"`
	}
	if !decodeJSON(response, request, 16*1024, &input) || input.PlanID == "" || input.ProfileID == "" || input.PlanDigest == "" || input.Confirmation == "" {
		writeError(response, http.StatusBadRequest, "full_decommission_typed_confirmation_required")
		return
	}
	record, err := server.store.GetFullDecommissionPlan(request.Context(), input.PlanID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "full_decommission_plan_not_found")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "full_decommission_approval_failed")
		return
	}
	var binding fullDecommissionBinding
	if json.Unmarshal([]byte(record.Binding), &binding) != nil || record.ProfileID != input.ProfileID || binding.Plan.ProfileID != input.ProfileID || binding.Plan.Digest != input.PlanDigest || input.Confirmation != binding.Plan.TypedConfirmation {
		writeError(response, http.StatusConflict, "full_decommission_confirmation_mismatch")
		return
	}
	if binding.Plan.RequiresOwnerOverride && (!input.OwnerOverride || strings.TrimSpace(input.OverrideReason) == "") {
		writeError(response, http.StatusConflict, "full_decommission_owner_override_required")
		return
	}
	workflowPlan, err := server.store.GetPlan(request.Context(), input.PlanID)
	if err != nil || workflowPlan.Intent != fullDecommissionIntent {
		writeError(response, http.StatusConflict, "full_decommission_confirmation_mismatch")
		return
	}
	parameters, _ := json.Marshal(map[string]any{"planDigest": binding.Plan.Digest, "protectionOverride": binding.Plan.RequiresOwnerOverride})
	_, _ = server.store.AppendEvent(request.Context(), state.EventRecord{ProfileID: input.ProfileID, Type: "full-decommission.approved", MessageKey: "activity.full_decommission.approved", Parameters: string(parameters), OccurredAt: time.Now().UTC()})
	run, err := server.workflow.Approve(request.Context(), input.PlanID)
	if errors.Is(err, workflow.ErrPreconditionChanged) {
		writeError(response, http.StatusConflict, "plan_precondition_changed")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "full_decommission_approval_failed")
		return
	}
	writeJSON(response, http.StatusAccepted, run)
}

func (server *Server) resumeFullDecommission(response http.ResponseWriter, request *http.Request) {
	if !server.mutatingRequest(response, request) {
		return
	}
	parts := strings.Split(strings.TrimPrefix(request.URL.Path, "/api/v1/full-decommission/runs/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "resume" {
		http.NotFound(response, request)
		return
	}
	run, err := server.store.GetRun(request.Context(), parts[0])
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "full_decommission_run_not_found")
		return
	}
	plan, err := server.store.GetPlan(request.Context(), run.PlanID)
	if err != nil || plan.Intent != fullDecommissionIntent || run.State != "running" {
		writeError(response, http.StatusConflict, "full_decommission_not_resumable")
		return
	}
	go server.executeFullDecommission(run.ID)
	writeJSON(response, http.StatusAccepted, workflow.Run{ID: run.ID, PlanID: run.PlanID, ProfileID: run.ProfileID, State: run.State, CurrentCheckpoint: run.CurrentCheckpoint, CancellationState: run.CancellationState, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt})
}

func (server *Server) executeFullDecommission(runID string) {
	if _, loaded := server.fullDecommissionActive.LoadOrStore(runID, true); loaded {
		return
	}
	defer server.fullDecommissionActive.Delete(runID)
	ctx := context.Background()
	run, err := server.store.GetRun(ctx, runID)
	if err != nil || run.State != "running" {
		return
	}
	record, err := server.store.GetFullDecommissionPlan(ctx, run.PlanID)
	if err != nil {
		server.failFullDecommission(ctx, run, "binding-missing")
		return
	}
	var binding fullDecommissionBinding
	if json.Unmarshal([]byte(record.Binding), &binding) != nil || binding.Plan.ProfileID != run.ProfileID {
		server.failFullDecommission(ctx, run, "binding-invalid")
		return
	}
	// Resume is itself a fresh inspection boundary, even when the interruption
	// happened after the final deletion stage and no item remains to trigger the
	// per-item inspection below.
	inspection, err := server.inspectFullDecommission(ctx, run.ProfileID)
	if err != nil || decommission.ValidateFullResume(binding.Plan, inspection, completedFullDecommissionCount(run.CurrentCheckpoint)) != nil {
		server.failFullDecommission(ctx, run, "reinspection-required")
		return
	}
	completed := completedFullDecommissionCount(run.CurrentCheckpoint)
	for index, item := range binding.Plan.Items {
		if item.Action != decommission.Remove {
			continue
		}
		if completed > 0 {
			completed--
			continue
		}
		inspection, err := server.inspectFullDecommission(ctx, run.ProfileID)
		if err != nil || decommission.ValidateFullResume(binding.Plan, inspection, completedFullDecommissionCount(run.CurrentCheckpoint)) != nil {
			server.failFullDecommission(ctx, run, "reinspection-required")
			return
		}
		if run.CancellationState == "requested" {
			_ = server.store.CompleteRunCancellation(ctx, run.ID, run.CurrentCheckpoint)
			return
		}
		if err := server.fullDecommissionExecutor.RemoveFull(ctx, run.ProfileID, item); err != nil {
			// Keep the completed count in the durable interruption checkpoint. A
			// bare "interrupted" marker would make a resume replay already
			// completed destructive calls after a later-stage failure.
			_ = server.fullDecommissionCheckpoint(ctx, run, "full-decommission-interrupted-"+strconv.Itoa(completedFullDecommissionCount(run.CurrentCheckpoint)))
			return
		}
		next := completedFullDecommissionCount(run.CurrentCheckpoint) + 1
		run.CurrentCheckpoint = "full-decommission-removals-" + strconv.Itoa(next)
		if err := server.fullDecommissionCheckpoint(ctx, run, run.CurrentCheckpoint); err != nil {
			return
		}
		if noLaterRemovalInStage(binding.Plan.Items, index, item.Stage) {
			run.CurrentCheckpoint = "full-decommission-" + string(item.Stage) + "-complete-" + strconv.Itoa(next)
			if err := server.fullDecommissionCheckpoint(ctx, run, run.CurrentCheckpoint); err != nil {
				return
			}
			if hook, ok := server.fullDecommissionExecutor.(FullDecommissionStageHook); ok {
				if err := hook.AfterFullDecommissionStage(ctx, run.ProfileID, item.Stage); err != nil {
					_ = server.fullDecommissionCheckpoint(ctx, run, "full-decommission-interrupted-"+strconv.Itoa(next))
					return
				}
			}
		}
	}
	if err := server.store.CompleteRunVerification(ctx, run.ID, "full-decommission-complete", "decommission.full_complete", time.Now().UTC()); err != nil {
		return
	}
	parameters, _ := json.Marshal(map[string]any{"planDigest": binding.Plan.Digest, "deletedResources": removalCount(binding.Plan.Items), "protectionOverride": binding.Plan.RequiresOwnerOverride})
	_, _ = server.store.AppendEvent(ctx, state.EventRecord{ProfileID: run.ProfileID, RunID: run.ID, Type: "full-decommission.completed", MessageKey: "activity.full_decommission.completed", Parameters: string(parameters), OccurredAt: time.Now().UTC()})
}

func noLaterRemovalInStage(items []decommission.FullItem, index int, stage decommission.Stage) bool {
	for _, item := range items[index+1:] {
		if item.Action == decommission.Remove && item.Stage == stage {
			return false
		}
	}
	return true
}
func removalCount(items []decommission.FullItem) int {
	count := 0
	for _, item := range items {
		if item.Action == decommission.Remove {
			count++
		}
	}
	return count
}
func completedFullDecommissionCount(checkpoint string) int {
	pieces := strings.Split(checkpoint, "-")
	if len(pieces) == 0 {
		return 0
	}
	count, _ := strconv.Atoi(pieces[len(pieces)-1])
	if strings.HasPrefix(checkpoint, "full-decommission-") {
		return count
	}
	return 0
}
func (server *Server) fullDecommissionCheckpoint(ctx context.Context, run state.RunRecord, checkpoint string) error {
	if err := server.store.UpdateRun(ctx, run.ID, "running", checkpoint, "", nil); err != nil {
		return err
	}
	_, err := server.store.AppendEvent(ctx, state.EventRecord{ProfileID: run.ProfileID, RunID: run.ID, Type: "run.checkpoint", MessageKey: "activity.run.checkpoint", Parameters: `{"checkpoint":"` + checkpoint + `"}`, OccurredAt: time.Now().UTC()})
	return err
}
func (server *Server) failFullDecommission(ctx context.Context, run state.RunRecord, checkpoint string) {
	_ = server.store.UpdateRun(ctx, run.ID, "failed", checkpoint, "", nil)
	_, _ = server.store.AppendEvent(ctx, state.EventRecord{ProfileID: run.ProfileID, RunID: run.ID, Type: "run.failed", MessageKey: "decommission.full_failed", Parameters: `{}`, OccurredAt: time.Now().UTC()})
}

// exportFullDecommissionActivity exposes only redacted activity fields, after
// the final completion record exists. It intentionally precedes profile-forget.
func (server *Server) exportFullDecommissionActivity(response http.ResponseWriter, request *http.Request) {
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
	events, err := server.store.ListEvents(request.Context(), profileID, 0)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "full_decommission_activity_unavailable")
		return
	}
	completed := false
	type activity struct {
		Type       string `json:"type"`
		MessageKey string `json:"messageKey"`
		OccurredAt string `json:"occurredAt"`
	}
	result := make([]activity, 0)
	for _, event := range events {
		if event.Type == "full-decommission.completed" {
			completed = true
		}
		if strings.HasPrefix(event.Type, "full-decommission") || event.RunID != "" && strings.Contains(event.MessageKey, "decommission") {
			result = append(result, activity{Type: event.Type, MessageKey: event.MessageKey, OccurredAt: event.OccurredAt.UTC().Format(time.RFC3339Nano)})
		}
	}
	if !completed {
		writeError(response, http.StatusConflict, "full_decommission_not_complete")
		return
	}
	response.Header().Set("Content-Disposition", "attachment; filename=smallworlds-full-decommission-activity.json")
	writeJSON(response, http.StatusOK, map[string]any{"profileId": profileID, "redacted": true, "activity": result})
}
