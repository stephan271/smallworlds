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

const preserveDecommissionIntent = "PreserveDataDecommission"

// PreserveDecommissionInspector is a read-only adapter. It must collect a new
// provider/local-node view for every plan and every resume.
type PreserveDecommissionInspector interface {
	Inspect(context.Context, state.Profile) (decommission.Inspection, error)
}

// PreserveDecommissionExecutor has no broad "destroy" operation. It receives
// one plan item at a time, after that item's stable identity was inspected and
// bound to the approved plan.
type PreserveDecommissionExecutor interface {
	Remove(context.Context, string, decommission.Item) error
}

type unavailablePreserveDecommissionInspector struct{}

func (unavailablePreserveDecommissionInspector) Inspect(context.Context, state.Profile) (decommission.Inspection, error) {
	return decommission.Inspection{}, errors.New("launcher: preserve-data inspection unavailable")
}

type unavailablePreserveDecommissionExecutor struct{}

func (unavailablePreserveDecommissionExecutor) Remove(context.Context, string, decommission.Item) error {
	return errors.New("launcher: preserve-data executor unavailable")
}

type preserveDecommissionBinding struct {
	Plan       decommission.Plan       `json:"plan"`
	Inspection decommission.Inspection `json:"inspection"`
}

func (server *Server) inspectPreserveDecommission(ctx context.Context, profileID string) (decommission.Inspection, error) {
	profile, err := server.store.GetProfile(ctx, profileID)
	if err != nil {
		return decommission.Inspection{}, err
	}
	inspection, err := server.decommissionInspector.Inspect(ctx, profile)
	if err != nil {
		return decommission.Inspection{}, err
	}
	if inspection.ProfileID != profile.ID || inspection.ProfileRevision != profile.Revision || inspection.DeploymentMode != profile.DeploymentMode {
		return decommission.Inspection{}, decommission.ErrInvalidInspection
	}
	if err := inspection.Finalize(); err != nil {
		return decommission.Inspection{}, err
	}
	return inspection, nil
}

func (server *Server) decommissionStatus(response http.ResponseWriter, request *http.Request) {
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
	inspection, err := server.inspectPreserveDecommission(request.Context(), profileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	}
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "decommission_inspection_unavailable")
		return
	}
	plan, err := decommission.BuildPlan(inspection)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "decommission_inspection_invalid")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"inspection": inspection, "preview": plan})
}

func (server *Server) inspectDecommission(response http.ResponseWriter, request *http.Request) {
	if !server.mutatingRequest(response, request) {
		return
	}
	var input struct {
		ProfileID string `json:"profileId"`
	}
	if !decodeJSON(response, request, 8*1024, &input) || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_decommission_request")
		return
	}
	inspection, err := server.inspectPreserveDecommission(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	}
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "decommission_inspection_unavailable")
		return
	}
	writeJSON(response, http.StatusOK, inspection)
}

// planPreserveDecommission always re-inspects. Browser input cannot submit a
// resource list, so a plan can never be widened by a stale UI payload.
func (server *Server) planPreserveDecommission(response http.ResponseWriter, request *http.Request) {
	if !server.mutatingRequest(response, request) {
		return
	}
	var input struct {
		ProfileID string `json:"profileId"`
	}
	if !decodeJSON(response, request, 8*1024, &input) || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_decommission_request")
		return
	}
	inspection, err := server.inspectPreserveDecommission(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	}
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "decommission_inspection_unavailable")
		return
	}
	plan, err := decommission.BuildPlan(inspection)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "decommission_plan_failed")
		return
	}
	risk := []workflow.Risk{{Code: "decommission.downtime", MessageKey: "plan.risk.decommission_downtime"}}
	if !plan.Approvable() {
		risk = append(risk, workflow.Risk{Code: "decommission.unknown_ownership", MessageKey: "plan.risk.decommission_unknown_ownership"})
	}
	workflowPlan, err := server.workflow.PlanChangeWithRisks(request.Context(), input.ProfileID, preserveDecommissionIntent, plan.Digest, []workflow.Effect{{Code: "decommission.profile_owned_compute_removed", MessageKey: "plan.effect.decommission_preserve_data"}}, risk)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "decommission_plan_failed")
		return
	}
	binding, err := json.Marshal(preserveDecommissionBinding{Plan: plan, Inspection: inspection})
	if err != nil || server.store.RecordDecommissionPlan(request.Context(), state.DecommissionPlanRecord{PlanID: workflowPlan.ID, ProfileID: input.ProfileID, Binding: string(binding), CreatedAt: workflowPlan.CreatedAt}) != nil {
		writeError(response, http.StatusInternalServerError, "decommission_plan_failed")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"plan": workflowPlan, "decommission": plan, "approvable": plan.Approvable()})
}

func (server *Server) resumePreserveDecommission(response http.ResponseWriter, request *http.Request) {
	if !server.mutatingRequest(response, request) {
		return
	}
	parts := strings.Split(strings.TrimPrefix(request.URL.Path, "/api/v1/decommission/runs/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "resume" {
		http.NotFound(response, request)
		return
	}
	run, err := server.store.GetRun(request.Context(), parts[0])
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "decommission_run_not_found")
		return
	}
	plan, err := server.store.GetPlan(request.Context(), run.PlanID)
	if err != nil || plan.Intent != preserveDecommissionIntent || run.State != "running" {
		writeError(response, http.StatusConflict, "decommission_not_resumable")
		return
	}
	go server.executePreserveDecommission(run.ID)
	writeJSON(response, http.StatusAccepted, workflow.Run{ID: run.ID, PlanID: run.PlanID, ProfileID: run.ProfileID, State: run.State, CurrentCheckpoint: run.CurrentCheckpoint, CancellationState: run.CancellationState, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt})
}

func (server *Server) executePreserveDecommission(runID string) {
	if _, loaded := server.decommissionActive.LoadOrStore(runID, true); loaded {
		return
	}
	defer server.decommissionActive.Delete(runID)
	ctx := context.Background()
	run, err := server.store.GetRun(ctx, runID)
	if err != nil || run.State != "running" {
		return
	}
	record, err := server.store.GetDecommissionPlan(ctx, run.PlanID)
	if err != nil {
		server.failDecommission(ctx, run, "binding-missing")
		return
	}
	var binding preserveDecommissionBinding
	if json.Unmarshal([]byte(record.Binding), &binding) != nil || binding.Plan.ProfileID != run.ProfileID {
		server.failDecommission(ctx, run, "binding-invalid")
		return
	}
	workflowPlan, err := server.store.GetPlan(ctx, run.PlanID)
	if err != nil || workflowPlan.Intent != preserveDecommissionIntent || workflowPlan.Digest == "" {
		server.failDecommission(ctx, run, "binding-invalid")
		return
	}
	if !binding.Plan.Approvable() {
		server.failDecommission(ctx, run, "ownership-unresolved")
		return
	}
	completed := completedDecommissionCount(run.CurrentCheckpoint)
	for _, item := range binding.Plan.Items {
		if item.Action != decommission.Remove {
			continue
		}
		if completed > 0 {
			completed--
			continue
		}
		inspection, err := server.inspectPreserveDecommission(ctx, run.ProfileID)
		if err != nil || decommission.ValidateResume(binding.Plan, inspection, completedDecommissionCount(run.CurrentCheckpoint)) != nil {
			server.failDecommission(ctx, run, "reinspection-required")
			return
		}
		if run.CancellationState == "requested" {
			_ = server.store.CompleteRunCancellation(ctx, run.ID, run.CurrentCheckpoint)
			return
		}
		if err := server.decommissionExecutor.Remove(ctx, run.ProfileID, item); err != nil {
			_ = server.decommissionCheckpoint(ctx, run, "interrupted")
			return
		}
		next := completedDecommissionCount(run.CurrentCheckpoint) + 1
		if err := server.decommissionCheckpoint(ctx, run, "decommission-completed-"+strconv.Itoa(next)); err != nil {
			return
		}
		run.CurrentCheckpoint = "decommission-completed-" + strconv.Itoa(next)
	}
	_ = server.store.CompleteRunVerification(ctx, run.ID, "preserve-data-complete", "decommission.preserve_data_complete", time.Now().UTC())
}

func completedDecommissionCount(checkpoint string) int {
	value := strings.TrimPrefix(checkpoint, "decommission-completed-")
	count, _ := strconv.Atoi(value)
	return count
}
func (server *Server) decommissionCheckpoint(ctx context.Context, run state.RunRecord, checkpoint string) error {
	if err := server.store.UpdateRun(ctx, run.ID, "running", checkpoint, "", nil); err != nil {
		return err
	}
	_, err := server.store.AppendEvent(ctx, state.EventRecord{ProfileID: run.ProfileID, RunID: run.ID, Type: "run.checkpoint", MessageKey: "activity.run.checkpoint", Parameters: `{"checkpoint":"` + checkpoint + `"}`, OccurredAt: time.Now().UTC()})
	return err
}
func (server *Server) failDecommission(ctx context.Context, run state.RunRecord, checkpoint string) {
	_ = server.store.UpdateRun(ctx, run.ID, "failed", checkpoint, "", nil)
	_, _ = server.store.AppendEvent(ctx, state.EventRecord{ProfileID: run.ProfileID, RunID: run.ID, Type: "run.failed", MessageKey: "decommission.failed", Parameters: `{}`, OccurredAt: time.Now().UTC()})
}
