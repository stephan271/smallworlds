package consoleworkflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/consoleauth"
)

var now = time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)

func samplePlan() ChangePlan {
	intent := IntentAddCapability
	actor := "alice"
	summary := "Add Immich to the collaboration preset."
	risks := []RiskLabel{RiskReversible, RiskCostBearing}
	return ChangePlan{
		ID:        "plan-1",
		Digest:    ComputeDigest(intent, actor, summary, risks),
		Intent:    intent,
		Actor:     actor,
		Summary:   summary,
		Risks:     risks,
		CreatedAt: now,
		ExpiresAt: now.Add(30 * time.Minute),
	}
}

func TestPlanValidate(t *testing.T) {
	if err := samplePlan().Validate(); err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}

	t.Run("digest must match content", func(t *testing.T) {
		plan := samplePlan()
		plan.Summary = "a tampered summary" // digest no longer matches
		if err := plan.Validate(); !errors.Is(err, ErrInvalidPlan) {
			t.Fatalf("err = %v, want ErrInvalidPlan", err)
		}
	})

	t.Run("summary size budget", func(t *testing.T) {
		plan := samplePlan()
		plan.Summary = strings.Repeat("x", MaxSummaryLength+1)
		plan.Digest = ComputeDigest(plan.Intent, plan.Actor, plan.Summary, plan.Risks)
		if err := plan.Validate(); !errors.Is(err, ErrInvalidPlan) {
			t.Fatalf("err = %v, want ErrInvalidPlan for oversize summary", err)
		}
	})

	t.Run("unknown intent", func(t *testing.T) {
		plan := samplePlan()
		plan.Intent = "DeleteEverything"
		plan.Digest = ComputeDigest(plan.Intent, plan.Actor, plan.Summary, plan.Risks)
		if err := plan.Validate(); !errors.Is(err, ErrInvalidPlan) {
			t.Fatalf("err = %v, want ErrInvalidPlan for unknown intent", err)
		}
	})
}

func TestIntentRequiredPermission(t *testing.T) {
	if IntentAddCapability.RequiredPermission() != consoleauth.PermissionPropose {
		t.Error("AddCapability should require Propose")
	}
	if IntentRevokeDevice.RequiredPermission() != consoleauth.PermissionAdminister {
		t.Error("RevokeDevice should require Administer")
	}
}

func TestApprovalBindsDigest(t *testing.T) {
	plan := samplePlan()
	approved, err := plan.Approve("owner", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := approved.ValidateApproval(now.Add(2 * time.Minute)); err != nil {
		t.Fatalf("ValidateApproval: %v", err)
	}

	// Editing the plan after approval changes the digest and invalidates it.
	drifted := approved
	drifted.Summary = "different summary"
	drifted.Digest = ComputeDigest(drifted.Intent, drifted.Actor, drifted.Summary, drifted.Risks)
	if err := drifted.ValidateApproval(now.Add(2 * time.Minute)); !errors.Is(err, ErrApprovalMismatch) {
		t.Fatalf("err = %v, want ErrApprovalMismatch after content change", err)
	}
}

func TestApprovalExpires(t *testing.T) {
	plan := samplePlan()
	approved, err := plan.Approve("owner", now)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := approved.ValidateApproval(plan.ExpiresAt.Add(time.Second)); !errors.Is(err, ErrApprovalMismatch) {
		t.Fatalf("err = %v, want ErrApprovalMismatch after expiry", err)
	}
	if _, err := plan.Approve("owner", plan.ExpiresAt.Add(time.Second)); !errors.Is(err, ErrApprovalMismatch) {
		t.Fatalf("approving an expired plan err = %v, want ErrApprovalMismatch", err)
	}
}

func TestRunLifecycleAndLokiReference(t *testing.T) {
	approved, err := samplePlan().Approve("owner", now)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	run, err := approved.Start("run-1", now)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if run.Phase != PhasePending {
		t.Fatalf("phase = %q, want pending", run.Phase)
	}
	if !strings.Contains(run.Loki.Query, "run-1") {
		t.Fatalf("loki query %q does not reference the run", run.Loki.Query)
	}

	run = run.Checkpoint("resources-created", now.Add(time.Second))
	run = run.Checkpoint("converged", now.Add(2*time.Second))
	if run.Phase != PhaseRunning || run.CurrentCheckpoint != "converged" {
		t.Fatalf("run = %+v, want running at converged", run)
	}
	if len(run.Checkpoints) != 2 || run.Loki.EventCount != 2 {
		t.Fatalf("checkpoints=%d events=%d, want 2/2", len(run.Checkpoints), run.Loki.EventCount)
	}

	run = run.Complete("added immich; 3 workloads healthy", now.Add(3*time.Second))
	if run.Phase != PhaseSucceeded {
		t.Fatalf("phase = %q, want succeeded", run.Phase)
	}
	if err := run.Validate(); err != nil {
		t.Fatalf("completed run invalid: %v", err)
	}
}

// TestEvidenceSummaryRedactsAndBounds proves completing a run scrubs secrets and
// keeps the record compact — detailed content belongs in Loki, not the record.
func TestEvidenceSummaryRedactsAndBounds(t *testing.T) {
	approved, _ := samplePlan().Approve("owner", now)
	run, _ := approved.Start("run-1", now)

	detail := "token=ghp_secretvalue123456 password: hunter2 rotated; " + strings.Repeat("noise ", 400)
	run = run.Complete(detail, now.Add(time.Second))

	if strings.Contains(run.EvidenceSummary, "hunter2") || strings.Contains(run.EvidenceSummary, "ghp_secretvalue123456") {
		t.Fatalf("evidence summary leaked a secret: %q", run.EvidenceSummary)
	}
	if len(run.EvidenceSummary) > MaxEvidenceSummaryLength {
		t.Fatalf("evidence summary length %d exceeds budget", len(run.EvidenceSummary))
	}
}

func TestRedactDetail(t *testing.T) {
	cases := []struct{ in, mustNotContain string }{
		{"Authorization: Bearer abcdef0123456789", "abcdef0123456789"},
		{"client_secret=supersecretvalue", "supersecretvalue"},
		{"-----BEGIN PRIVATE KEY-----\nMIIB...\n-----END PRIVATE KEY-----", "MIIB"},
	}
	for _, test := range cases {
		got := RedactDetail(test.in, 200)
		if strings.Contains(got, test.mustNotContain) {
			t.Errorf("RedactDetail(%q) = %q, still contains secret", test.in, got)
		}
	}
}

// TestPersistThroughRestart proves records written before a simulated restart
// are readable by a fresh store over the same durable backing (criterion 9).
func TestPersistThroughRestart(t *testing.T) {
	ctx := context.Background()
	backing := NewBacking()

	before := NewMemoryStore(backing)
	approved, _ := samplePlan().Approve("owner", now)
	if err := before.PutPlan(ctx, approved); err != nil {
		t.Fatalf("PutPlan: %v", err)
	}
	run, _ := approved.Start("run-1", now)
	run = run.Checkpoint("resources-created", now.Add(time.Second))
	if err := before.PutRun(ctx, run); err != nil {
		t.Fatalf("PutRun: %v", err)
	}

	// A pod restart: a brand-new store over the same durable backing.
	after := NewMemoryStore(backing)

	gotPlan, err := after.GetPlan(ctx, "plan-1")
	if err != nil {
		t.Fatalf("GetPlan after restart: %v", err)
	}
	if gotPlan.Approval == nil || gotPlan.Approval.Digest != approved.Digest {
		t.Fatalf("plan approval lost across restart: %+v", gotPlan.Approval)
	}
	gotRun, err := after.GetRun(ctx, "run-1")
	if err != nil {
		t.Fatalf("GetRun after restart: %v", err)
	}
	if gotRun.CurrentCheckpoint != "resources-created" || gotRun.Loki.Query == "" {
		t.Fatalf("run state lost across restart: %+v", gotRun)
	}
}

func TestStoreRejectsInvalidRecords(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore(nil)
	if err := store.PutPlan(ctx, ChangePlan{ID: "x"}); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("PutPlan err = %v, want ErrInvalidPlan", err)
	}
	if _, err := store.GetPlan(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetPlan err = %v, want ErrNotFound", err)
	}
}
