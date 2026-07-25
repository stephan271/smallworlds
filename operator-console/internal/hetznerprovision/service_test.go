package hetznerprovision_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/hetzner"
	"github.com/stephan271/smallworlds/operator-console/internal/hetznerprovision"
	"github.com/stephan271/smallworlds/operator-console/internal/state"
	"github.com/stephan271/smallworlds/operator-console/internal/vault"
)

// harness assembles a store with an approved plan, its binding, and a running
// run, plus injectable stand-ins for the provider, the reconciler, and the
// convergence observation.
type harness struct {
	t          *testing.T
	store      *state.Store
	service    *hetznerprovision.Service
	binding    hetznerprovision.Binding
	run        state.RunRecord
	inspector  *recordingInspector
	reconciler *recordingReconciler
	observer   *recordingObserver
	vaultLock  bool
}

type recordingInspector struct {
	calls    int
	observed hetznerprovision.Observed
	err      error
}

func (inspector *recordingInspector) Observe(context.Context, hetznerprovision.Binding) (hetznerprovision.Observed, error) {
	inspector.calls++
	if inspector.err != nil {
		return hetznerprovision.Observed{}, inspector.err
	}
	return inspector.observed, nil
}

type recordingReconciler struct {
	calls      int
	failFirst  bool
	err        error
	checkpoint string
}

func (reconciler *recordingReconciler) Apply(_ context.Context, request hetznerprovision.ReconcileRequest) (hetznerprovision.Outcome, error) {
	reconciler.calls++
	if reconciler.checkpoint != "" {
		if err := request.Checkpoint(reconciler.checkpoint); err != nil {
			return hetznerprovision.Outcome{}, err
		}
	}
	if reconciler.err != nil {
		return hetznerprovision.Outcome{}, reconciler.err
	}
	if reconciler.failFirst && reconciler.calls == 1 {
		return hetznerprovision.Outcome{}, hetznerprovision.ErrReconcileInterrupted
	}
	if request.ProjectToken != projectToken {
		return hetznerprovision.Outcome{}, errors.New("reconciler did not receive the custodied token")
	}
	return hetznerprovision.Outcome{Applied: true, ServerAddress: "203.0.113.9", ServerID: "srv-1", ObservedAt: time.Now().UTC()}, nil
}

type recordingObserver struct {
	calls    int
	complete bool
}

func (observer *recordingObserver) Observe(context.Context, hetznerprovision.Binding, string) (hetznerprovision.Convergence, error) {
	observer.calls++
	if !observer.complete {
		return hetznerprovision.Convergence{K3SReady: true}, nil
	}
	return hetznerprovision.Convergence{K3SReady: true, ArgoCDReady: true, OverlaySynced: true, ObservedAt: time.Now().UTC()}, nil
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ctx, now := context.Background(), time.Now().UTC()
	profile := state.Profile{ID: "profile-1", Name: "Community", Language: "en", DeploymentMode: "hetzner", Revision: 4, CreatedAt: now}
	if _, err := store.CreateProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	// CreateProfile starts revisions at 1; the binding records the revision the
	// plan was approved against, so both must agree.
	stored, err := store.GetProfile(ctx, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	environment := environment()
	environment.ProfileRevision = stored.Revision
	binding, err := hetznerprovision.BindPlan(completePlan(), environment)
	if err != nil {
		t.Fatal(err)
	}
	plan := state.PlanRecord{ID: binding.PlanID, ProfileID: profile.ID, Intent: hetznerprovision.Intent, Digest: binding.PlanDigestFor(hetznerprovision.Intent), Status: "approved", ProfileRevision: stored.Revision, CreatedAt: now}
	if err := store.CreatePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	encoded, err := binding.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordHetznerProvisioningPlan(ctx, state.BootstrapPlanRecord{PlanID: plan.ID, ProfileID: profile.ID, Binding: encoded, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	run := state.RunRecord{ID: "run-1", PlanID: plan.ID, ProfileID: profile.ID, State: "running", CurrentCheckpoint: "approved", CancellationState: "not-requested", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}

	observation := healthyObservation()
	observation.ProfileRevision = stored.Revision
	test := &harness{
		t: t, store: store, binding: binding, run: run,
		inspector:  &recordingInspector{observed: observation},
		reconciler: &recordingReconciler{},
		observer:   &recordingObserver{complete: true},
	}
	test.service = hetznerprovision.NewService(store, test.inspector, test.reconciler, test.observer,
		func(key string) (string, error) {
			if test.vaultLock {
				return "", vault.ErrLocked
			}
			if key != "profile-1/hetzner-project-token" {
				return "", vault.ErrSecretNotFound
			}
			return projectToken, nil
		},
		func(context.Context, hetznerprovision.Binding) (hetzner.ChangePlan, error) {
			return completePlan(), nil
		},
		func(context.Context, hetznerprovision.Binding) (hetznerprovision.ModuleInput, error) {
			return moduleInput(), nil
		},
	)
	test.service.SetRetryDelay(time.Hour) // retries are driven explicitly in tests
	return test
}

func (test *harness) currentRun() state.RunRecord {
	test.t.Helper()
	run, err := test.store.GetRun(context.Background(), test.run.ID)
	if err != nil {
		test.t.Fatal(err)
	}
	return run
}

func TestServiceProvisionsAndVerifiesConvergence(t *testing.T) {
	test := newHarness(t)
	test.service.Execute(test.run.ID)

	run := test.currentRun()
	if run.State != "verified" || run.VerificationCode != "cluster.gitops.converged" {
		t.Fatalf("run = %#v, want a verified run", run)
	}
	if test.reconciler.calls != 1 || test.observer.calls != 1 {
		t.Fatalf("applies = %d, observations = %d, want one each", test.reconciler.calls, test.observer.calls)
	}
	// Every attempt re-inspects before doing anything, including the first.
	if test.inspector.calls != 1 {
		t.Fatalf("inspections = %d, want the project re-inspected before applying", test.inspector.calls)
	}
}

// This is the property the whole checkpoint scheme exists for: a launcher that
// died after applying must never apply again. A second apply of a plan whose
// state was not recorded is how a paid server gets created twice.
func TestServiceNeverAppliesTwiceAfterTheProviderChanged(t *testing.T) {
	test := newHarness(t)
	test.observer.complete = false
	test.service.Execute(test.run.ID)
	if run := test.currentRun(); run.CurrentCheckpoint != hetznerprovision.CheckpointAwaitingConvergence {
		t.Fatalf("checkpoint = %q, want the run awaiting convergence", run.CurrentCheckpoint)
	}
	if test.reconciler.calls != 1 {
		t.Fatalf("applies = %d, want one", test.reconciler.calls)
	}

	// Resume, as a restarted launcher would.
	test.observer.complete = true
	test.service.Execute(test.run.ID)
	if run := test.currentRun(); run.State != "verified" {
		t.Fatalf("run = %#v, want the resumed run verified", run)
	}
	if test.reconciler.calls != 1 {
		t.Fatalf("applies = %d, want the provider changed exactly once across the interruption", test.reconciler.calls)
	}
	if test.inspector.calls != 2 {
		t.Fatalf("inspections = %d, want the resumed attempt to re-inspect too", test.inspector.calls)
	}
}

// An apply interrupted before the provider was known to have changed leaves a
// retryable checkpoint, and the retry re-inspects first — so whatever the
// interrupted attempt managed to create is seen before anything else happens.
func TestServiceReinspectsBeforeRetryingAnInterruptedApply(t *testing.T) {
	test := newHarness(t)
	test.reconciler.failFirst = true
	test.service.Execute(test.run.ID)

	run := test.currentRun()
	if run.State != "running" || run.CurrentCheckpoint != hetznerprovision.CheckpointInterrupted {
		t.Fatalf("run = %#v, want a retryable interrupted run", run)
	}
	test.service.Execute(test.run.ID)
	if run := test.currentRun(); run.State != "verified" {
		t.Fatalf("run = %#v, want the retry to complete", run)
	}
	if test.inspector.calls != 2 {
		t.Fatalf("inspections = %d, want each attempt to re-inspect", test.inspector.calls)
	}
}

// A locked vault is the Operator's own doing. The run pauses and resumes; it
// does not fail, and it certainly does not proceed without the token.
func TestServiceWaitsForTheVaultRatherThanFailing(t *testing.T) {
	test := newHarness(t)
	test.vaultLock = true
	test.service.Execute(test.run.ID)

	run := test.currentRun()
	if run.State != "running" || run.CurrentCheckpoint != hetznerprovision.CheckpointWaitingForVault {
		t.Fatalf("run = %#v, want the run waiting for the vault", run)
	}
	if test.reconciler.calls != 0 || test.inspector.calls != 0 {
		t.Fatal("nothing may reach the provider while the vault is locked")
	}

	test.vaultLock = false
	test.service.Execute(test.run.ID)
	if run := test.currentRun(); run.State != "verified" {
		t.Fatalf("run = %#v, want the unlocked run to complete", run)
	}
}

// A fact that moved since approval is not a retry: the Operator has to review
// and approve a plan built against the project as it actually is.
func TestServiceFailsRatherThanApplyingAStalePlan(t *testing.T) {
	test := newHarness(t)
	test.inspector.observed.OverlayCommit = strings.Repeat("9", 40)
	test.service.Execute(test.run.ID)

	run := test.currentRun()
	if run.State != "failed" {
		t.Fatalf("run = %#v, want a failed run", run)
	}
	if !strings.Contains(run.CurrentCheckpoint, hetznerprovision.OverlayCheck) {
		t.Fatalf("checkpoint = %q, want it to name the check that failed", run.CurrentCheckpoint)
	}
	if test.reconciler.calls != 0 {
		t.Fatal("a stale plan must never reach the provider")
	}
}

// The launcher default has no verified toolchain. Refusing honestly is the
// point: applying with whatever OpenTofu happens to be installed would break
// the reproducibility the pinning exists for.
func TestServiceRefusesWithoutAVerifiedToolchain(t *testing.T) {
	test := newHarness(t)
	test.reconciler.err = hetznerprovision.ErrReconcilerUnavailable
	test.service.Execute(test.run.ID)

	run := test.currentRun()
	if run.State != "failed" || run.CurrentCheckpoint != "toolchain-unavailable" {
		t.Fatalf("run = %#v, want an honest toolchain refusal", run)
	}
}

// The approved workflow plan's digest is computed over every fact in the
// binding. Storing a different binding under an approved plan must be caught
// before anything is applied, or approval would authorise something the
// Operator never reviewed.
func TestServiceRefusesABindingThatDoesNotMatchTheApprovedPlan(t *testing.T) {
	test := newHarness(t)
	ctx, now := context.Background(), time.Now().UTC()

	approved := test.binding
	swapped := approved
	swapped.PlanID = "plan-2"
	swapped.OverlayCommit = strings.Repeat("7", 40)
	encoded, err := swapped.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	// The plan is approved under the digest of what the Operator reviewed; the
	// stored binding is a different one.
	approved.PlanID = "plan-2"
	plan := state.PlanRecord{ID: "plan-2", ProfileID: "profile-1", Intent: hetznerprovision.Intent, Digest: approved.PlanDigestFor(hetznerprovision.Intent), Status: "approved", ProfileRevision: test.binding.ProfileRevision, CreatedAt: now}
	if err := test.store.CreatePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if err := test.store.RecordHetznerProvisioningPlan(ctx, state.BootstrapPlanRecord{PlanID: plan.ID, ProfileID: "profile-1", Binding: encoded, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	run := state.RunRecord{ID: "run-2", PlanID: plan.ID, ProfileID: "profile-1", State: "running", CurrentCheckpoint: "approved", CancellationState: "not-requested", CreatedAt: now, UpdatedAt: now}
	if err := test.store.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	test.service.Execute(run.ID)

	executed, err := test.store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if executed.State != "failed" || executed.CurrentCheckpoint != "binding-digest-mismatch" {
		t.Fatalf("run = %#v, want the swapped binding refused", executed)
	}
	if test.reconciler.calls != 0 {
		t.Fatal("a swapped binding must never reach the provider")
	}
}

// Checkpoints are durable activity, and none of them may carry the token.
func TestServiceLeaksNoCredentialIntoActivity(t *testing.T) {
	test := newHarness(t)
	test.reconciler.checkpoint = "workspace-locked"
	test.service.Execute(test.run.ID)

	events, err := test.store.ListEvents(context.Background(), "profile-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("the run recorded no activity")
	}
	for _, event := range events {
		if strings.Contains(event.Parameters, projectToken) {
			t.Fatalf("activity leaked the project token: %#v", event)
		}
	}
}
