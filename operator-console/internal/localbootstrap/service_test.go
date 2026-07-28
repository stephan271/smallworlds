package localbootstrap_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/bootstrapassets"
	"github.com/stephan271/smallworlds/operator-console/internal/localbootstrap"
	"github.com/stephan271/smallworlds/operator-console/internal/nodeinspect"
	"github.com/stephan271/smallworlds/operator-console/internal/state"
	"github.com/stephan271/smallworlds/operator-console/internal/vault"
)

type resumableRunner struct {
	calls        int
	observations int
}

type cancellationRunner struct {
	store *state.Store
}

func (runner cancellationRunner) Run(ctx context.Context, request localbootstrap.RunRequest) (localbootstrap.Observation, error) {
	if err := request.Checkpoint("bootstrap-atomic-operation"); err != nil {
		return localbootstrap.Observation{}, err
	}
	if _, err := runner.store.RequestRunCancellation(ctx, request.RunID); err != nil {
		return localbootstrap.Observation{}, err
	}
	return localbootstrap.Observation{}, localbootstrap.ErrInterrupted
}

func (runner cancellationRunner) Observe(context.Context, localbootstrap.RunRequest) (localbootstrap.Observation, error) {
	return localbootstrap.Observation{}, errors.New("unexpected observation")
}

func (runner cancellationRunner) Detail(context.Context, localbootstrap.RunRequest) (localbootstrap.Detail, error) {
	return localbootstrap.Detail{}, errors.New("unexpected detail")
}

func (runner *resumableRunner) Run(_ context.Context, request localbootstrap.RunRequest) (localbootstrap.Observation, error) {
	runner.calls++
	if strings.Contains(request.Secrets, "cluster-secret-value") == false || request.Credentials.Password != "node-password-value" {
		return localbootstrap.Observation{}, errors.New("executor did not receive internal credentials")
	}
	if runner.calls == 1 {
		if err := request.Checkpoint("payload-staged"); err != nil {
			return localbootstrap.Observation{}, err
		}
		return localbootstrap.Observation{}, localbootstrap.ErrInterrupted
	}
	return localbootstrap.Observation{CommandCompleted: true, K3SReady: true, ArgoCDReady: true, OverlaySynced: true, ObservedAt: time.Now().UTC()}, nil
}

func (runner *resumableRunner) Observe(_ context.Context, _ localbootstrap.RunRequest) (localbootstrap.Observation, error) {
	runner.observations++
	if runner.observations == 1 {
		return localbootstrap.Observation{CommandCompleted: true, K3SReady: true, ArgoCDReady: true, OverlaySynced: false, ObservedAt: time.Now().UTC()}, nil
	}
	return localbootstrap.Observation{CommandCompleted: true, K3SReady: true, ArgoCDReady: true, OverlaySynced: true, ObservedAt: time.Now().UTC()}, nil
}

func (runner *resumableRunner) Detail(_ context.Context, _ localbootstrap.RunRequest) (localbootstrap.Detail, error) {
	return localbootstrap.Detail{Nodes: []localbootstrap.NodeCondition{{Name: "node", Ready: true}}}, nil
}

func TestServiceResumesAnInterruptedRunWithoutLeakingSecretsToActivity(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	profile := state.Profile{ID: "profile-1", Name: "Home", Language: "en", DeploymentMode: "local-lan", Revision: 1, CreatedAt: now}
	if _, err := store.CreateProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	overlay := state.OverlayIdentity{ProfileID: profile.ID, Provider: "github", Repository: "example/config", RepositoryURL: "https://github.com/example/config", Release: "v1.2.27", Commit: strings.Repeat("c", 40), RecordedAt: now}
	if err := store.RecordOverlayIdentity(ctx, overlay); err != nil {
		t.Fatal(err)
	}
	trust := state.NodeTrust{ProfileID: profile.ID, Host: "node.internal", Port: 22, Username: "operator", Fingerprint: "SHA256:pinned", ConfirmedAt: now}
	if err := store.RecordNodeTrust(ctx, trust); err != nil {
		t.Fatal(err)
	}
	plan := state.PlanRecord{ID: "plan-1", ProfileID: profile.ID, Intent: "BootstrapLocalNode", Digest: "digest", Status: "approved", ProfileRevision: 1, CreatedAt: now}
	binding := localbootstrap.Binding{PlanID: plan.ID, ProfileID: profile.ID, ProfileRevision: 1, Target: nodeinspect.Target{Kind: nodeinspect.RemoteTarget, Host: trust.Host, Port: trust.Port, Username: trust.Username}, HostFingerprint: trust.Fingerprint, NodeIdentity: trust.Fingerprint, InspectionDigest: strings.Repeat("a", 64), InspectedAt: now, Release: "v1.2.27", AssetID: "bootstrap-linux-amd64", AssetSHA256: strings.Repeat("b", 64), OverlayRepositoryURL: overlay.RepositoryURL, OverlayCommit: overlay.Commit, OverlayRelease: overlay.Release, AuthenticationKind: "password", SecretsVaultKey: profile.ID + "/cluster-secrets-manifest", Configuration: localbootstrap.Configuration{Domain: "example.internal", DataDirectory: "/var/lib/smallworlds-data", NodeName: "smallworlds-node"}}
	plan.Digest = binding.PlanDigest(plan.Intent)
	if err := store.CreatePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	encoded, err := binding.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordBootstrapPlan(ctx, state.BootstrapPlanRecord{PlanID: plan.ID, ProfileID: profile.ID, Binding: encoded, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	run := state.RunRecord{ID: "run-1", PlanID: plan.ID, ProfileID: profile.ID, State: "running", CurrentCheckpoint: "approved", CancellationState: "not-requested", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	secrets := map[string]string{profile.ID + "/node-password": "node-password-value", profile.ID + "/cluster-secrets-manifest": "apiVersion: v1\nkind: Secret\ndata:\n  token: cluster-secret-value"}
	vaultLocked := false
	loader := func(key string) (string, error) {
		if vaultLocked {
			return "", vault.ErrLocked
		}
		value, ok := secrets[key]
		if !ok {
			return "", vault.ErrSecretNotFound
		}
		return value, nil
	}
	runner := &resumableRunner{}
	service := localbootstrap.NewService(store, func(context.Context, string, string) (io.ReadCloser, bootstrapassets.Descriptor, error) {
		return io.NopCloser(strings.NewReader("archive")), bootstrapassets.Descriptor{SHA256: binding.AssetSHA256}, nil
	}, loader, runner)
	service.Execute(run.ID)
	interrupted, err := store.GetRun(ctx, run.ID)
	if err != nil || interrupted.State != "running" || interrupted.CurrentCheckpoint != "interrupted" {
		t.Fatalf("interrupted run = %#v, err = %v", interrupted, err)
	}
	service.Execute(run.ID)
	completed, err := store.GetRun(ctx, run.ID)
	if err != nil || completed.State != "verified" || completed.VerificationCode != "cluster.gitops.converged" || runner.calls != 2 {
		t.Fatalf("completed run = %#v, calls = %d, err = %v", completed, runner.calls, err)
	}
	events, err := store.ListEvents(ctx, profile.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if strings.Contains(event.Parameters, "cluster-secret-value") || strings.Contains(event.Parameters, "node-password-value") {
			t.Fatalf("activity leaked secret: %#v", event)
		}
	}

	// A Launcher restart while GitOps is still converging must only observe the
	// completed installation. Re-entering Runner.Run can restart k3s and prevent
	// the external evidence from ever becoming healthy.
	if err := store.UpdateRun(ctx, run.ID, "running", "awaiting-convergence", "", nil); err != nil {
		t.Fatal(err)
	}
	vaultLocked = true
	service.Execute(run.ID)
	locked, err := store.GetRun(ctx, run.ID)
	if err != nil || locked.State != "running" || locked.CurrentCheckpoint != "awaiting-convergence" || runner.calls != 2 || runner.observations != 0 {
		t.Fatalf("locked converging run = %#v, mutating calls = %d, observations = %d, err = %v", locked, runner.calls, runner.observations, err)
	}
	vaultLocked = false
	service.Execute(run.ID)
	converging, err := store.GetRun(ctx, run.ID)
	if err != nil || converging.State != "running" || converging.CurrentCheckpoint != "awaiting-convergence" || runner.calls != 2 || runner.observations != 1 {
		t.Fatalf("converging run = %#v, mutating calls = %d, observations = %d, err = %v", converging, runner.calls, runner.observations, err)
	}
	service.Execute(run.ID)
	reverified, err := store.GetRun(ctx, run.ID)
	if err != nil || reverified.State != "verified" || runner.calls != 2 || runner.observations != 2 {
		t.Fatalf("reverified run = %#v, mutating calls = %d, observations = %d, err = %v", reverified, runner.calls, runner.observations, err)
	}
}

func TestServiceDefersCancellationUntilTheAtomicCheckpointFinishes(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	profile := state.Profile{ID: "profile-1", Name: "Home", Language: "en", DeploymentMode: "local-lan", Revision: 1, CreatedAt: now}
	if _, err := store.CreateProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	overlay := state.OverlayIdentity{ProfileID: profile.ID, Provider: "github", Repository: "example/config", RepositoryURL: "https://github.com/example/config", Release: "v1.2.27", Commit: strings.Repeat("c", 40), RecordedAt: now}
	if err := store.RecordOverlayIdentity(ctx, overlay); err != nil {
		t.Fatal(err)
	}
	plan := state.PlanRecord{ID: "plan-1", ProfileID: profile.ID, Intent: "BootstrapLocalNode", Status: "approved", ProfileRevision: 1, CreatedAt: now}
	binding := localbootstrap.Binding{PlanID: plan.ID, ProfileID: profile.ID, ProfileRevision: 1, Target: nodeinspect.Target{Kind: nodeinspect.SameHostTarget}, NodeIdentity: "sha256:" + strings.Repeat("d", 64), InspectionDigest: strings.Repeat("a", 64), InspectedAt: now, Release: "v1.2.27", AssetID: "bootstrap-linux-amd64", AssetSHA256: strings.Repeat("b", 64), OverlayRepositoryURL: overlay.RepositoryURL, OverlayCommit: overlay.Commit, OverlayRelease: overlay.Release, AuthenticationKind: "same-host", Configuration: localbootstrap.Configuration{Domain: "example.internal", DataDirectory: "/var/lib/smallworlds-data", NodeName: "smallworlds-node"}}
	plan.Digest = binding.PlanDigest(plan.Intent)
	if err := store.CreatePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	encoded, err := binding.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordBootstrapPlan(ctx, state.BootstrapPlanRecord{PlanID: plan.ID, ProfileID: profile.ID, Binding: encoded, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	run := state.RunRecord{ID: "run-1", PlanID: plan.ID, ProfileID: profile.ID, State: "running", CurrentCheckpoint: "approved", CancellationState: "not-requested", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	loader := func(string) (string, error) { return "", vault.ErrSecretNotFound }
	service := localbootstrap.NewService(store, func(context.Context, string, string) (io.ReadCloser, bootstrapassets.Descriptor, error) {
		return io.NopCloser(strings.NewReader("archive")), bootstrapassets.Descriptor{SHA256: binding.AssetSHA256}, nil
	}, loader, cancellationRunner{store: store})
	service.Execute(run.ID)
	cancelled, err := store.GetRun(ctx, run.ID)
	if err != nil || cancelled.State != "cancelled" || cancelled.CancellationState != "completed" || cancelled.CurrentCheckpoint != "bootstrap-atomic-operation" {
		t.Fatalf("cancelled run = %#v, err = %v", cancelled, err)
	}
}

// --- Bounded retries, node exclusivity, and one run at a time -------------
//
// A bootstrap attempt installs a cluster on a real machine. Repeating that
// unattended every ten seconds is not resilience: a single interrupted run once
// became two bootstraps uninstalling each other's k3s on the same node while
// nobody was watching. These pin the three rules that stop it.

type bootstrapFixture struct {
	store   *state.Store
	plan    state.PlanRecord
	profile state.Profile
	binding localbootstrap.Binding
	now     time.Time
}

func newBootstrapFixture(t *testing.T) bootstrapFixture {
	t.Helper()
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	now := time.Now().UTC()
	profile := state.Profile{ID: "profile-1", Name: "Home", Language: "en", DeploymentMode: "local-lan", Revision: 1, CreatedAt: now}
	if _, err := store.CreateProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	overlay := state.OverlayIdentity{ProfileID: profile.ID, Provider: "github", Repository: "example/config", RepositoryURL: "https://github.com/example/config", Release: "v1.2.27", Commit: strings.Repeat("c", 40), RecordedAt: now}
	if err := store.RecordOverlayIdentity(ctx, overlay); err != nil {
		t.Fatal(err)
	}
	trust := state.NodeTrust{ProfileID: profile.ID, Host: "node.internal", Port: 22, Username: "operator", Fingerprint: "SHA256:pinned", ConfirmedAt: now}
	if err := store.RecordNodeTrust(ctx, trust); err != nil {
		t.Fatal(err)
	}
	plan := state.PlanRecord{ID: "plan-1", ProfileID: profile.ID, Intent: "BootstrapLocalNode", Digest: "digest", Status: "approved", ProfileRevision: 1, CreatedAt: now}
	binding := localbootstrap.Binding{PlanID: plan.ID, ProfileID: profile.ID, ProfileRevision: 1, Target: nodeinspect.Target{Kind: nodeinspect.RemoteTarget, Host: trust.Host, Port: trust.Port, Username: trust.Username}, HostFingerprint: trust.Fingerprint, NodeIdentity: trust.Fingerprint, InspectionDigest: strings.Repeat("a", 64), InspectedAt: now, Release: "v1.2.27", AssetID: "bootstrap-linux-amd64", AssetSHA256: strings.Repeat("b", 64), OverlayRepositoryURL: overlay.RepositoryURL, OverlayCommit: overlay.Commit, OverlayRelease: overlay.Release, AuthenticationKind: "agent", Configuration: localbootstrap.Configuration{Domain: "example.internal", DataDirectory: "/var/lib/smallworlds-data", NodeName: "smallworlds-node"}}
	plan.Digest = binding.PlanDigest(plan.Intent)
	if err := store.CreatePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	encoded, err := binding.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordBootstrapPlan(ctx, state.BootstrapPlanRecord{PlanID: plan.ID, ProfileID: profile.ID, Binding: encoded, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	return bootstrapFixture{store: store, plan: plan, profile: profile, binding: binding, now: now}
}

func (fixture bootstrapFixture) createRun(t *testing.T, id string, createdAt time.Time) {
	t.Helper()
	run := state.RunRecord{ID: id, PlanID: fixture.plan.ID, ProfileID: fixture.profile.ID, State: "running", CurrentCheckpoint: "approved", CancellationState: "not-requested", CreatedAt: createdAt, UpdatedAt: createdAt}
	if err := fixture.store.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
}

func (fixture bootstrapFixture) service(runner localbootstrap.Runner) *localbootstrap.Service {
	return localbootstrap.NewService(fixture.store, func(context.Context, string, string) (io.ReadCloser, bootstrapassets.Descriptor, error) {
		return io.NopCloser(strings.NewReader("archive")), bootstrapassets.Descriptor{SHA256: fixture.binding.AssetSHA256}, nil
	}, func(string) (string, error) { return "", vault.ErrSecretNotFound }, runner)
}

type stubRunner struct {
	calls int
	err   error
}

func (runner *stubRunner) Run(context.Context, localbootstrap.RunRequest) (localbootstrap.Observation, error) {
	runner.calls++
	return localbootstrap.Observation{}, runner.err
}

func (runner *stubRunner) Observe(context.Context, localbootstrap.RunRequest) (localbootstrap.Observation, error) {
	return localbootstrap.Observation{}, errors.New("unexpected observation")
}

func (runner *stubRunner) Detail(context.Context, localbootstrap.RunRequest) (localbootstrap.Detail, error) {
	return localbootstrap.Detail{}, errors.New("unexpected detail")
}

func TestRepeatedInterruptionEndsTheRunInsteadOfReinstallingForever(t *testing.T) {
	fixture := newBootstrapFixture(t)
	fixture.createRun(t, "run-1", fixture.now)
	runner := &stubRunner{err: localbootstrap.ErrInterrupted}
	service := fixture.service(runner)
	ctx := context.Background()
	for attempt := 1; attempt <= 5; attempt++ {
		service.Execute("run-1")
		run, err := fixture.store.GetRun(ctx, "run-1")
		if err != nil {
			t.Fatal(err)
		}
		if attempt < 5 && (run.State != "running" || run.CurrentCheckpoint != "interrupted") {
			t.Fatalf("attempt %d: run = %#v", attempt, run)
		}
		if attempt == 5 && (run.State != "failed" || run.CurrentCheckpoint != "execution-abandoned") {
			t.Fatalf("the run never gave up: %#v", run)
		}
	}
	if runner.calls != 5 {
		t.Fatalf("installer attempts = %d, want 5", runner.calls)
	}
	// The budget is counted from the Activity Record, so a launcher restart
	// cannot hand a crash-looping run a fresh set of attempts.
	interrupted, err := fixture.store.CountRunCheckpoints(ctx, "run-1", "interrupted")
	if err != nil || interrupted != 5 {
		t.Fatalf("recorded interruptions = %d, err = %v", interrupted, err)
	}
	service.Execute("run-1")
	if runner.calls != 5 {
		t.Fatalf("a failed run was executed again: calls = %d", runner.calls)
	}
}

func TestAnInstallerAlreadyRunningOnTheNodeIsWaitedForRatherThanDoubled(t *testing.T) {
	fixture := newBootstrapFixture(t)
	fixture.createRun(t, "run-1", fixture.now)
	runner := &stubRunner{err: localbootstrap.ErrAttemptInFlight}
	fixture.service(runner).Execute("run-1")
	ctx := context.Background()
	run, err := fixture.store.GetRun(ctx, "run-1")
	if err != nil || run.State != "running" || run.CurrentCheckpoint != "awaiting-previous-attempt" {
		t.Fatalf("run = %#v, err = %v", run, err)
	}
	// Waiting for someone else's installer is not a failed attempt of our own;
	// spending the budget on it would abandon a run that never got to try.
	interrupted, err := fixture.store.CountRunCheckpoints(ctx, "run-1", "interrupted")
	if err != nil || interrupted != 0 {
		t.Fatalf("waiting consumed the retry budget: %d, err = %v", interrupted, err)
	}
}

func TestASecondRunForTheSameCommunityWaitsForTheOlderOne(t *testing.T) {
	fixture := newBootstrapFixture(t)
	fixture.createRun(t, "run-older", fixture.now)
	fixture.createRun(t, "run-newer", fixture.now.Add(time.Minute))
	runner := &stubRunner{err: localbootstrap.ErrInterrupted}
	fixture.service(runner).Execute("run-newer")
	run, err := fixture.store.GetRun(context.Background(), "run-newer")
	if err != nil || run.CurrentCheckpoint != "awaiting-other-run" {
		t.Fatalf("run = %#v, err = %v", run, err)
	}
	if runner.calls != 0 {
		t.Fatal("a second installation started while another run was still going")
	}
}

func (runner cancellationRunner) Adopt(_ context.Context, _ localbootstrap.RunRequest, revision string) (string, error) {
	return revision, nil
}

func (runner *resumableRunner) Adopt(_ context.Context, _ localbootstrap.RunRequest, revision string) (string, error) {
	return revision, nil
}

func (runner *stubRunner) Adopt(_ context.Context, _ localbootstrap.RunRequest, revision string) (string, error) {
	return revision, nil
}
