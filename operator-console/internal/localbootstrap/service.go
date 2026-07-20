package localbootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/bootstrapassets"
	"github.com/stephan271/smallworlds/operator-console/internal/nodeinspect"
	"github.com/stephan271/smallworlds/operator-console/internal/state"
	"github.com/stephan271/smallworlds/operator-console/internal/vault"
)

var ErrInterrupted = errors.New("local bootstrap execution interrupted")
var ErrExecutionPrecondition = errors.New("local bootstrap execution precondition changed")

type Observation struct {
	CommandCompleted bool
	K3SReady         bool
	ArgoCDReady      bool
	OverlaySynced    bool
	ObservedAt       time.Time
}

type RunRequest struct {
	Binding     Binding
	RunID       string
	Archive     io.Reader
	Credentials nodeinspect.Credentials
	Secrets     string
	Checkpoint  func(string) error
	Cancelled   func() bool
}

type Runner interface {
	Run(context.Context, RunRequest) (Observation, error)
}

type AssetOpener func(release, id string) (io.ReadCloser, bootstrapassets.Descriptor, error)
type SecretLoader func(key string) (string, error)

type Service struct {
	store      *state.Store
	openAsset  AssetOpener
	loadSecret SecretLoader
	runner     Runner
	active     sync.Map
	retryDelay time.Duration
}

func NewService(store *state.Store, openAsset AssetOpener, loadSecret SecretLoader, runner Runner) *Service {
	return &Service{store: store, openAsset: openAsset, loadSecret: loadSecret, runner: runner, retryDelay: 10 * time.Second}
}

func (service *Service) Execute(runID string) {
	if _, loaded := service.active.LoadOrStore(runID, true); loaded {
		return
	}
	defer service.active.Delete(runID)
	ctx := context.Background()
	run, err := service.store.GetRun(ctx, runID)
	if err != nil || run.State != "running" {
		return
	}
	planRecord, err := service.store.GetBootstrapPlan(ctx, run.PlanID)
	if err != nil {
		service.fail(ctx, run, "binding-missing", "local_bootstrap.binding_missing")
		return
	}
	binding, err := ParseBinding(planRecord.Binding)
	if err != nil || binding.PlanID != run.PlanID || binding.ProfileID != run.ProfileID {
		service.fail(ctx, run, "binding-invalid", "local_bootstrap.binding_invalid")
		return
	}
	plan, err := service.store.GetPlan(ctx, run.PlanID)
	if err != nil || plan.Intent != "BootstrapLocalNode" || plan.Digest != binding.PlanDigest(plan.Intent) {
		service.fail(ctx, run, "binding-digest-mismatch", "local_bootstrap.binding_invalid")
		return
	}
	if err := service.validateExternalPreconditions(ctx, binding); err != nil {
		service.fail(ctx, run, "precondition-changed", "local_bootstrap.precondition_changed")
		return
	}
	archive, descriptor, err := service.openAsset(binding.Release, binding.AssetID)
	if err != nil {
		_ = service.checkpoint(ctx, run, "waiting-for-assets")
		return
	}
	if descriptor.SHA256 != binding.AssetSHA256 {
		service.fail(ctx, run, "asset-unavailable", "local_bootstrap.asset_unavailable")
		return
	}
	defer archive.Close()
	credentials, err := service.loadCredentials(binding)
	if errors.Is(err, vault.ErrLocked) {
		_ = service.checkpoint(ctx, run, "waiting-for-vault")
		return
	}
	if err != nil {
		service.fail(ctx, run, "credentials-unavailable", "local_bootstrap.credentials_unavailable")
		return
	}
	secrets := ""
	if binding.SecretsVaultKey != "" {
		secrets, err = service.loadSecret(binding.SecretsVaultKey)
		if errors.Is(err, vault.ErrLocked) {
			_ = service.checkpoint(ctx, run, "waiting-for-vault")
			return
		}
		if err != nil {
			service.fail(ctx, run, "secrets-unavailable", "local_bootstrap.secrets_unavailable")
			return
		}
	}
	if service.cancelled(ctx, run.ID) {
		_ = service.store.CompleteRunCancellation(ctx, run.ID, "approved")
		return
	}
	if err := service.checkpoint(ctx, run, "preconditions-confirmed"); err != nil {
		return
	}
	observation, err := service.runner.Run(ctx, RunRequest{
		Binding: binding, RunID: run.ID, Archive: archive, Credentials: credentials, Secrets: secrets,
		Checkpoint: func(checkpoint string) error { return service.checkpoint(ctx, run, checkpoint) },
		Cancelled:  func() bool { return service.cancelled(ctx, run.ID) },
	})
	if err != nil {
		if service.cancelled(ctx, run.ID) {
			latest, loadErr := service.store.GetRun(ctx, run.ID)
			if loadErr == nil {
				_ = service.store.CompleteRunCancellation(ctx, run.ID, latest.CurrentCheckpoint)
			}
			return
		}
		if errors.Is(err, ErrExecutionPrecondition) {
			service.fail(ctx, run, "execution-precondition-changed", "local_bootstrap.precondition_changed")
			return
		}
		_ = service.checkpoint(ctx, run, "interrupted")
		time.AfterFunc(service.retryDelay, func() { service.Execute(run.ID) })
		return
	}
	if !observation.CommandCompleted {
		_ = service.checkpoint(ctx, run, "execution-incomplete")
		return
	}
	if err := service.checkpoint(ctx, run, "execution-complete"); err != nil {
		return
	}
	if service.cancelled(ctx, run.ID) {
		_ = service.store.CompleteRunCancellation(ctx, run.ID, "execution-complete")
		return
	}
	if !observation.K3SReady || !observation.ArgoCDReady || !observation.OverlaySynced {
		_ = service.checkpoint(ctx, run, "awaiting-convergence")
		time.AfterFunc(service.retryDelay, func() { service.Execute(run.ID) })
		return
	}
	observedAt := observation.ObservedAt.UTC()
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	_ = service.store.CompleteRunVerification(ctx, run.ID, "verification-complete", "cluster.gitops.converged", observedAt)
}

func (service *Service) validateExternalPreconditions(ctx context.Context, binding Binding) error {
	profile, err := service.store.GetProfile(ctx, binding.ProfileID)
	if err != nil || profile.Revision != binding.ProfileRevision {
		return ErrExecutionPrecondition
	}
	overlay, err := service.store.GetOverlayIdentity(ctx, binding.ProfileID)
	if err != nil || overlay.RepositoryURL != binding.OverlayRepositoryURL || overlay.Commit != binding.OverlayCommit || overlay.Release != binding.OverlayRelease || overlay.Domain != "" && overlay.Domain != binding.Configuration.Domain {
		return ErrExecutionPrecondition
	}
	if binding.Target.Kind == nodeinspect.RemoteTarget {
		trust, err := service.store.GetNodeTrust(ctx, binding.ProfileID)
		if err != nil || trust.Host != binding.Target.Host || trust.Port != binding.Target.Port || trust.Username != binding.Target.Username || trust.Fingerprint != binding.HostFingerprint {
			return ErrExecutionPrecondition
		}
	}
	return nil
}

func (service *Service) loadCredentials(binding Binding) (nodeinspect.Credentials, error) {
	kind := nodeinspect.AuthenticationKind(binding.AuthenticationKind)
	if binding.Target.Kind == nodeinspect.SameHostTarget {
		kind = nodeinspect.AgentAuthentication
	}
	credentials := nodeinspect.Credentials{Kind: kind}
	loadOptional := func(suffix string) (string, error) {
		value, err := service.loadSecret(binding.ProfileID + "/node-" + suffix)
		if errors.Is(err, vault.ErrSecretNotFound) {
			return "", nil
		}
		return value, err
	}
	var err error
	if kind == nodeinspect.PasswordAuthentication {
		credentials.Password, err = service.loadSecret(binding.ProfileID + "/node-password")
		if err != nil {
			return nodeinspect.Credentials{}, err
		}
	}
	if kind == nodeinspect.PrivateKeyAuthentication {
		privateKey, loadErr := service.loadSecret(binding.ProfileID + "/node-private-key")
		if loadErr != nil {
			return nodeinspect.Credentials{}, loadErr
		}
		credentials.PrivateKey = []byte(privateKey)
		credentials.KeyPassphrase, err = loadOptional("key-passphrase")
		if err != nil {
			return nodeinspect.Credentials{}, err
		}
	}
	credentials.SudoPassword, err = loadOptional("sudo-password")
	return credentials, err
}

func (service *Service) cancelled(ctx context.Context, runID string) bool {
	run, err := service.store.GetRun(ctx, runID)
	return err == nil && run.CancellationState == "requested"
}

func (service *Service) checkpoint(ctx context.Context, run state.RunRecord, checkpoint string) error {
	if err := service.store.UpdateRun(ctx, run.ID, "running", checkpoint, "", nil); err != nil {
		return err
	}
	_, err := service.store.AppendEvent(ctx, state.EventRecord{ProfileID: run.ProfileID, RunID: run.ID, Type: "run.checkpoint", MessageKey: "activity.run.checkpoint", Parameters: `{"checkpoint":"` + checkpoint + `"}`, OccurredAt: time.Now().UTC()})
	return err
}

func (service *Service) fail(ctx context.Context, run state.RunRecord, checkpoint, messageKey string) {
	_ = service.store.UpdateRun(ctx, run.ID, "failed", checkpoint, "", nil)
	_, _ = service.store.AppendEvent(ctx, state.EventRecord{ProfileID: run.ProfileID, RunID: run.ID, Type: "run.failed", MessageKey: messageKey, Parameters: `{}`, OccurredAt: time.Now().UTC()})
}

func OpenManagerAsset(manager *bootstrapassets.Manager) AssetOpener {
	return func(release, id string) (io.ReadCloser, bootstrapassets.Descriptor, error) {
		file, descriptor, err := manager.OpenVerified(release, id)
		if err != nil {
			return nil, bootstrapassets.Descriptor{}, fmt.Errorf("open bootstrap archive: %w", err)
		}
		return file, descriptor, nil
	}
}
