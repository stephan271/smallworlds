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
	service := localbootstrap.NewService(store, func(_, _ string) (io.ReadCloser, bootstrapassets.Descriptor, error) {
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
	service := localbootstrap.NewService(store, func(_, _ string) (io.ReadCloser, bootstrapassets.Descriptor, error) {
		return io.NopCloser(strings.NewReader("archive")), bootstrapassets.Descriptor{SHA256: binding.AssetSHA256}, nil
	}, loader, cancellationRunner{store: store})
	service.Execute(run.ID)
	cancelled, err := store.GetRun(ctx, run.ID)
	if err != nil || cancelled.State != "cancelled" || cancelled.CancellationState != "completed" || cancelled.CurrentCheckpoint != "bootstrap-atomic-operation" {
		t.Fatalf("cancelled run = %#v, err = %v", cancelled, err)
	}
}
