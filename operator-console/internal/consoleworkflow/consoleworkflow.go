// Package consoleworkflow models the in-cluster Operator Console's durable
// plan/run records (plan section 6). Unlike the launcher — which persists in
// SQLite — the in-cluster console records a Change Plan and its Workflow Run as
// compact Kubernetes custom resources, so a console or executor pod restart
// loses no state (the records live in etcd, not process memory).
//
// The records are deliberately compact: they hold a redacted human summary, a
// digest that binds approval, risk labels, run phase and checkpoints, and a
// short evidence summary — never raw command output or large diffs. Detailed
// events stay redacted and are referenced from Loki through a stored query, not
// duplicated inline. Validate enforces those size budgets so the CRDs and Loki
// cannot accumulate unbounded history.
package consoleworkflow

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/consoleauth"
)

// Size budgets keep records compact enough for CRD status fields and Loki.
const (
	MaxSummaryLength         = 500
	MaxEvidenceSummaryLength = 1000
	MaxCheckpoints           = 64
	MaxRisks                 = 16
)

// IntentType is a typed, discriminated operation a Change Plan proposes. There
// is never a free-form command; the executor accepts only these typed intents.
type IntentType string

const (
	IntentAddCapability    IntentType = "AddCapability"
	IntentConfigureBackup  IntentType = "ConfigureBackup"
	IntentUpdateRelease    IntentType = "UpdateRelease"
	IntentEnrollDevice     IntentType = "EnrollDevice"
	IntentRevokeDevice     IntentType = "RevokeDevice"
	IntentRotateCredential IntentType = "RotateCredential"
)

// RequiredPermission is the Console Role permission an intent demands: routine
// proposals need Propose; sensitive access/credential changes need Administer.
func (intent IntentType) RequiredPermission() consoleauth.Permission {
	switch intent {
	case IntentEnrollDevice, IntentRevokeDevice, IntentRotateCredential:
		return consoleauth.PermissionAdminister
	default:
		return consoleauth.PermissionPropose
	}
}

// Valid reports whether the intent is a recognized, bounded first-release intent.
func (intent IntentType) Valid() bool {
	switch intent {
	case IntentAddCapability, IntentConfigureBackup, IntentUpdateRelease, IntentEnrollDevice, IntentRevokeDevice, IntentRotateCredential:
		return true
	default:
		return false
	}
}

// RiskLabel categorizes the consequence of executing a plan.
type RiskLabel string

const (
	RiskReversible     RiskLabel = "reversible"
	RiskDestructive    RiskLabel = "destructive"
	RiskCostBearing    RiskLabel = "cost-bearing"
	RiskLockout        RiskLabel = "lockout-risk"
	RiskSecretRotation RiskLabel = "secret-rotation"
	RiskDowntime       RiskLabel = "downtime"
)

// RunPhase is the lifecycle phase of a Workflow Run.
type RunPhase string

const (
	PhasePending   RunPhase = "pending"
	PhaseRunning   RunPhase = "running"
	PhaseVerifying RunPhase = "verifying"
	PhaseSucceeded RunPhase = "succeeded"
	PhaseFailed    RunPhase = "failed"
	PhaseCancelled RunPhase = "cancelled"
)

// Approval binds an actor's approval to one immutable plan digest at a time.
type Approval struct {
	Actor      string    `json:"actor"`
	ApprovedAt time.Time `json:"approvedAt"`
	Digest     string    `json:"digest"`
}

// ChangePlan is the compact, secret-free proposal record.
type ChangePlan struct {
	ID        string      `json:"id"`
	Digest    string      `json:"digest"`
	Intent    IntentType  `json:"intent"`
	Actor     string      `json:"actor"`
	Summary   string      `json:"summary"`
	Risks     []RiskLabel `json:"risks,omitempty"`
	CreatedAt time.Time   `json:"createdAt"`
	ExpiresAt time.Time   `json:"expiresAt"`
	Approval  *Approval   `json:"approval,omitempty"`
}

// Checkpoint is a durable, named safe point a run has reached.
type Checkpoint struct {
	Name      string    `json:"name"`
	ReachedAt time.Time `json:"reachedAt"`
}

// LokiReference points at the detailed, redacted events for a run without
// duplicating them in the record.
type LokiReference struct {
	Query      string `json:"query"`
	EventCount int    `json:"eventCount"`
}

// WorkflowRun is the compact record of executing an approved plan.
type WorkflowRun struct {
	ID                string        `json:"id"`
	PlanID            string        `json:"planId"`
	PlanDigest        string        `json:"planDigest"`
	Intent            IntentType    `json:"intent"`
	Actor             string        `json:"actor"`
	Phase             RunPhase      `json:"phase"`
	Checkpoints       []Checkpoint  `json:"checkpoints,omitempty"`
	CurrentCheckpoint string        `json:"currentCheckpoint,omitempty"`
	EvidenceSummary   string        `json:"evidenceSummary,omitempty"`
	Loki              LokiReference `json:"loki"`
	StartedAt         time.Time     `json:"startedAt"`
	UpdatedAt         time.Time     `json:"updatedAt"`
}

var (
	// ErrInvalidPlan is returned when a Change Plan fails validation.
	ErrInvalidPlan = errors.New("consoleworkflow: invalid change plan")
	// ErrInvalidRun is returned when a Workflow Run fails validation.
	ErrInvalidRun = errors.New("consoleworkflow: invalid workflow run")
	// ErrApprovalMismatch is returned when an approval does not bind the plan's
	// current digest, or the plan has expired.
	ErrApprovalMismatch = errors.New("consoleworkflow: approval does not bind the current plan")
)

// ComputeDigest derives a plan's content digest from the meaningful fields that
// approval binds. Any change to intent, actor, summary, or risks yields a new
// digest, invalidating a prior approval.
func ComputeDigest(intent IntentType, actor, summary string, risks []RiskLabel) string {
	sorted := append([]RiskLabel(nil), risks...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	riskStrings := make([]string, len(sorted))
	for i, risk := range sorted {
		riskStrings[i] = string(risk)
	}
	canonical := strings.Join([]string{string(intent), actor, summary, strings.Join(riskStrings, ",")}, "\n")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// Validate enforces the plan's compactness and structural invariants.
func (plan ChangePlan) Validate() error {
	if plan.ID == "" {
		return fmt.Errorf("%w: missing id", ErrInvalidPlan)
	}
	if !plan.Intent.Valid() {
		return fmt.Errorf("%w: unknown intent %q", ErrInvalidPlan, plan.Intent)
	}
	if plan.Actor == "" {
		return fmt.Errorf("%w: missing actor", ErrInvalidPlan)
	}
	if len(plan.Summary) > MaxSummaryLength {
		return fmt.Errorf("%w: summary exceeds %d characters", ErrInvalidPlan, MaxSummaryLength)
	}
	if len(plan.Risks) > MaxRisks {
		return fmt.Errorf("%w: too many risk labels", ErrInvalidPlan)
	}
	if plan.ExpiresAt.Before(plan.CreatedAt) {
		return fmt.Errorf("%w: expiry precedes creation", ErrInvalidPlan)
	}
	if plan.Digest != ComputeDigest(plan.Intent, plan.Actor, plan.Summary, plan.Risks) {
		return fmt.Errorf("%w: digest does not match content", ErrInvalidPlan)
	}
	if plan.Approval != nil && plan.Approval.Digest != plan.Digest {
		return fmt.Errorf("%w: approval binds a different digest", ErrInvalidPlan)
	}
	return nil
}

// Approve binds an actor's approval to the plan's current digest. It fails if
// the plan is invalid or has expired at the approval time.
func (plan ChangePlan) Approve(actor string, at time.Time) (ChangePlan, error) {
	if err := plan.Validate(); err != nil {
		return ChangePlan{}, err
	}
	if at.After(plan.ExpiresAt) {
		return ChangePlan{}, fmt.Errorf("%w: plan expired", ErrApprovalMismatch)
	}
	if actor == "" {
		return ChangePlan{}, fmt.Errorf("%w: missing approver", ErrInvalidPlan)
	}
	approved := plan
	approved.Approval = &Approval{Actor: actor, ApprovedAt: at, Digest: plan.Digest}
	return approved, nil
}

// ValidateApproval confirms the plan carries an approval that binds its current
// digest and has not expired — the check the executor makes before acting.
func (plan ChangePlan) ValidateApproval(now time.Time) error {
	if plan.Approval == nil || plan.Approval.Digest != plan.Digest {
		return ErrApprovalMismatch
	}
	if now.After(plan.ExpiresAt) {
		return fmt.Errorf("%w: plan expired", ErrApprovalMismatch)
	}
	return nil
}

// Start creates a pending Workflow Run for an approved plan, wiring a Loki
// reference for its detailed events.
func (plan ChangePlan) Start(runID string, at time.Time) (WorkflowRun, error) {
	if err := plan.ValidateApproval(at); err != nil {
		return WorkflowRun{}, err
	}
	if runID == "" {
		return WorkflowRun{}, fmt.Errorf("%w: missing run id", ErrInvalidRun)
	}
	return WorkflowRun{
		ID:         runID,
		PlanID:     plan.ID,
		PlanDigest: plan.Digest,
		Intent:     plan.Intent,
		Actor:      plan.Actor,
		Phase:      PhasePending,
		Loki:       NewLokiReference(runID),
		StartedAt:  at,
		UpdatedAt:  at,
	}, nil
}

// NewLokiReference builds the Loki query that locates a run's detailed events.
func NewLokiReference(runID string) LokiReference {
	return LokiReference{Query: fmt.Sprintf(`{app="operator-console",run=%q}`, runID)}
}

// Checkpoint records reaching a named safe point and advances the run's phase to
// running. The detailed event is not stored here; it is redacted and shipped to
// Loki, and the run's Loki event count is incremented.
func (run WorkflowRun) Checkpoint(name string, at time.Time) WorkflowRun {
	next := run
	next.Checkpoints = append(append([]Checkpoint(nil), run.Checkpoints...), Checkpoint{Name: name, ReachedAt: at})
	next.CurrentCheckpoint = name
	if next.Phase == PhasePending {
		next.Phase = PhaseRunning
	}
	next.Loki.EventCount++
	next.UpdatedAt = at
	return next
}

// Complete moves the run to succeeded with a redacted, size-bounded evidence
// summary.
func (run WorkflowRun) Complete(evidenceSummary string, at time.Time) WorkflowRun {
	next := run
	next.Phase = PhaseSucceeded
	next.EvidenceSummary = RedactDetail(evidenceSummary, MaxEvidenceSummaryLength)
	next.UpdatedAt = at
	return next
}

// Fail moves the run to failed with a redacted, size-bounded evidence summary.
func (run WorkflowRun) Fail(evidenceSummary string, at time.Time) WorkflowRun {
	next := run
	next.Phase = PhaseFailed
	next.EvidenceSummary = RedactDetail(evidenceSummary, MaxEvidenceSummaryLength)
	next.UpdatedAt = at
	return next
}

// Cancel records a cooperative cancellation at the current checkpoint.
func (run WorkflowRun) Cancel(at time.Time) WorkflowRun {
	next := run
	next.Phase = PhaseCancelled
	next.UpdatedAt = at
	return next
}

// Validate enforces the run's compactness and structural invariants.
func (run WorkflowRun) Validate() error {
	if run.ID == "" || run.PlanID == "" {
		return fmt.Errorf("%w: missing identifiers", ErrInvalidRun)
	}
	if !run.Intent.Valid() {
		return fmt.Errorf("%w: unknown intent %q", ErrInvalidRun, run.Intent)
	}
	if len(run.Checkpoints) > MaxCheckpoints {
		return fmt.Errorf("%w: too many checkpoints", ErrInvalidRun)
	}
	if len(run.EvidenceSummary) > MaxEvidenceSummaryLength {
		return fmt.Errorf("%w: evidence summary exceeds %d characters", ErrInvalidRun, MaxEvidenceSummaryLength)
	}
	if run.Loki.Query == "" {
		return fmt.Errorf("%w: missing Loki reference for detailed events", ErrInvalidRun)
	}
	return nil
}
