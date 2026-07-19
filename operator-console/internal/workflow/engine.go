package workflow

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/state"
)

var ErrPreconditionChanged = errors.New("plan precondition changed")

type Effect struct {
	Code       string `json:"code"`
	MessageKey string `json:"messageKey"`
}

type Risk struct {
	Code       string `json:"code"`
	MessageKey string `json:"messageKey"`
}

type Preconditions struct {
	ProfileRevision int64 `json:"profileRevision"`
}

type Plan struct {
	ID            string        `json:"id"`
	ProfileID     string        `json:"profileId"`
	Intent        string        `json:"intent"`
	Digest        string        `json:"digest"`
	Status        string        `json:"status"`
	Preconditions Preconditions `json:"preconditions"`
	Effects       []Effect      `json:"effects"`
	Risks         []Risk        `json:"risks"`
	CreatedAt     time.Time     `json:"createdAt"`
}

type Verification struct {
	Code       string    `json:"code"`
	ObservedAt time.Time `json:"observedAt"`
}

type Run struct {
	ID                string       `json:"id"`
	PlanID            string       `json:"planId"`
	ProfileID         string       `json:"profileId"`
	State             string       `json:"state"`
	CurrentCheckpoint string       `json:"currentCheckpoint"`
	CancellationState string       `json:"cancellationState"`
	Verification      Verification `json:"verification"`
	CreatedAt         time.Time    `json:"createdAt"`
	UpdatedAt         time.Time    `json:"updatedAt"`
}

type JourneyTask struct {
	ID            string    `json:"id"`
	State         string    `json:"state"`
	Recommended   bool      `json:"recommended"`
	EvidenceState string    `json:"evidenceState"`
	EvidenceCode  string    `json:"evidenceCode,omitempty"`
	ObservedAt    time.Time `json:"observedAt,omitempty"`
}

type Journey struct {
	ProfileID string        `json:"profileId"`
	Tasks     []JourneyTask `json:"tasks"`
}

type Engine struct {
	store *state.Store
}

func New(store *state.Store) *Engine {
	return &Engine{store: store}
}

func (engine *Engine) ResumeActive(ctx context.Context) error {
	runs, err := engine.store.ListActiveRuns(ctx)
	if err != nil {
		return err
	}
	for _, run := range runs {
		go engine.executeVerification(run.ID)
	}
	return nil
}

func (engine *Engine) PlanVerification(ctx context.Context, profileID string) (Plan, error) {
	profile, err := engine.store.GetProfile(ctx, profileID)
	if err != nil {
		return Plan{}, err
	}
	id, err := newID()
	if err != nil {
		return Plan{}, err
	}
	createdAt := time.Now().UTC()
	digestInput := fmt.Sprintf("VerifyLauncher\n%s\n%d\nlauncher.verification.recorded", profile.ID, profile.Revision)
	digestBytes := sha256.Sum256([]byte(digestInput))
	plan := Plan{
		ID:        id,
		ProfileID: profile.ID,
		Intent:    "VerifyLauncher",
		Digest:    hex.EncodeToString(digestBytes[:]),
		Status:    "planned",
		Preconditions: Preconditions{
			ProfileRevision: profile.Revision,
		},
		Effects:   []Effect{{Code: "launcher.verification.recorded", MessageKey: "plan.effect.verification"}},
		Risks:     []Risk{},
		CreatedAt: createdAt,
	}
	if err := engine.store.CreatePlan(ctx, state.PlanRecord{
		ID:              plan.ID,
		ProfileID:       plan.ProfileID,
		Intent:          plan.Intent,
		Digest:          plan.Digest,
		Status:          plan.Status,
		ProfileRevision: plan.Preconditions.ProfileRevision,
		CreatedAt:       plan.CreatedAt,
	}); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func (engine *Engine) Journey(ctx context.Context, profileID string) (Journey, error) {
	profile, err := engine.store.GetProfile(ctx, profileID)
	if err != nil {
		return Journey{}, err
	}
	task := JourneyTask{
		ID:            "verify-launcher",
		State:         "ready",
		Recommended:   true,
		EvidenceState: "missing",
	}
	verification, err := engine.store.LatestVerification(ctx, profileID)
	if err == nil {
		task.EvidenceCode = verification.Code
		task.ObservedAt = verification.ObservedAt
		if verification.ProfileRevision == profile.Revision {
			task.State = "completed"
			task.Recommended = false
			task.EvidenceState = "current"
		} else {
			task.EvidenceState = "stale"
		}
	} else if !errors.Is(err, state.ErrNotFound) {
		return Journey{}, err
	}
	return Journey{ProfileID: profile.ID, Tasks: []JourneyTask{task}}, nil
}

func (engine *Engine) ValidateApproval(ctx context.Context, planID string) error {
	plan, err := engine.store.GetPlan(ctx, planID)
	if err != nil {
		return err
	}
	profile, err := engine.store.GetProfile(ctx, plan.ProfileID)
	if err != nil {
		return err
	}
	if profile.Revision != plan.ProfileRevision {
		return ErrPreconditionChanged
	}
	return nil
}

func (engine *Engine) Approve(ctx context.Context, planID string) (Run, error) {
	if err := engine.ValidateApproval(ctx, planID); err != nil {
		return Run{}, err
	}
	plan, err := engine.store.GetPlan(ctx, planID)
	if err != nil {
		return Run{}, err
	}
	id, err := newID()
	if err != nil {
		return Run{}, err
	}
	now := time.Now().UTC()
	run := Run{
		ID:                id,
		PlanID:            plan.ID,
		ProfileID:         plan.ProfileID,
		State:             "running",
		CurrentCheckpoint: "approved",
		CancellationState: "not-requested",
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := engine.store.CreateRun(ctx, state.RunRecord{
		ID:                run.ID,
		PlanID:            run.PlanID,
		ProfileID:         run.ProfileID,
		State:             run.State,
		CurrentCheckpoint: run.CurrentCheckpoint,
		CancellationState: run.CancellationState,
		CreatedAt:         run.CreatedAt,
		UpdatedAt:         run.UpdatedAt,
	}); err != nil {
		return Run{}, err
	}
	if err := engine.store.UpdatePlanStatus(ctx, plan.ID, "approved"); err != nil {
		return Run{}, err
	}
	if _, err := engine.store.AppendEvent(ctx, state.EventRecord{
		ProfileID:  run.ProfileID,
		RunID:      run.ID,
		Type:       "run.started",
		MessageKey: "activity.run.started",
		Parameters: `{}`,
		OccurredAt: now,
	}); err != nil {
		return Run{}, err
	}
	go engine.executeVerification(run.ID)
	return run, nil
}

func (engine *Engine) GetRun(ctx context.Context, id string) (Run, error) {
	record, err := engine.store.GetRun(ctx, id)
	if err != nil {
		return Run{}, err
	}
	return runFromRecord(record), nil
}

func (engine *Engine) Cancel(ctx context.Context, id string) (Run, error) {
	record, err := engine.store.RequestRunCancellation(ctx, id)
	if err != nil {
		return Run{}, err
	}
	return runFromRecord(record), nil
}

func (engine *Engine) executeVerification(runID string) {
	ctx := context.Background()
	run, err := engine.store.GetRun(ctx, runID)
	if err != nil || run.State != "running" {
		return
	}
	if run.CurrentCheckpoint == "approved" {
		time.Sleep(25 * time.Millisecond)
		if err := engine.store.UpdateRun(ctx, runID, "running", "execution-complete", "", nil); err != nil {
			return
		}
		run, err = engine.store.GetRun(ctx, runID)
		if err != nil {
			return
		}
		if _, err := engine.store.AppendEvent(ctx, state.EventRecord{
			ProfileID:  run.ProfileID,
			RunID:      run.ID,
			Type:       "run.checkpoint",
			MessageKey: "activity.run.checkpoint",
			Parameters: `{"checkpoint":"execution-complete"}`,
			OccurredAt: time.Now().UTC(),
		}); err != nil {
			return
		}
	}
	if run.CancellationState == "requested" {
		if err := engine.store.CompleteRunCancellation(ctx, runID, "execution-complete"); err != nil {
			return
		}
		return
	}
	time.Sleep(25 * time.Millisecond)
	observedAt := time.Now().UTC()
	_ = engine.store.CompleteRunVerification(ctx, runID, "verification-complete", "launcher.responding", observedAt)
}

func runFromRecord(record state.RunRecord) Run {
	run := Run{
		ID:                record.ID,
		PlanID:            record.PlanID,
		ProfileID:         record.ProfileID,
		State:             record.State,
		CurrentCheckpoint: record.CurrentCheckpoint,
		CancellationState: record.CancellationState,
		CreatedAt:         record.CreatedAt,
		UpdatedAt:         record.UpdatedAt,
	}
	if record.VerificationObservedAt != nil {
		run.Verification = Verification{Code: record.VerificationCode, ObservedAt: *record.VerificationObservedAt}
	}
	return run
}

func newID() (string, error) {
	buffer := make([]byte, 18)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
