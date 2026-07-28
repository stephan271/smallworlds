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
	"github.com/stephan271/smallworlds/operator-console/internal/overlayadoption"
	"github.com/stephan271/smallworlds/operator-console/internal/state"
	"github.com/stephan271/smallworlds/operator-console/internal/vault"
)

var ErrInterrupted = errors.New("local bootstrap execution interrupted")
var ErrExecutionPrecondition = errors.New("local bootstrap execution precondition changed")

// ErrAttemptInFlight reports that the node is still running a bootstrap from an
// earlier attempt. It is deliberately not an ErrInterrupted: nothing failed and
// nothing should be retried against the machine yet — the right response is to
// wait for the installer that is already there.
var ErrAttemptInFlight = errors.New("local bootstrap attempt already running on the node")

type Observation struct {
	CommandCompleted        bool
	K3SReady                bool
	ArgoCDReady             bool
	OverlaySynced           bool
	DDNSReady               bool
	CertificatesReady       bool
	PublicApplicationsReady bool
	ObservedAt              time.Time
}

type RunRequest struct {
	Binding       Binding
	RunID         string
	Archive       io.Reader
	Credentials   nodeinspect.Credentials
	Secrets       string
	DNSCredential string
	Checkpoint    func(string) error
	Cancelled     func() bool
}

type Runner interface {
	Run(context.Context, RunRequest) (Observation, error)
	Observe(context.Context, RunRequest) (Observation, error)
	Detail(context.Context, RunRequest) (Detail, error)
	// Adopt repoints the root Application at a reviewed overlay commit and
	// returns the revision the cluster reports afterwards.
	Adopt(ctx context.Context, request RunRequest, revision string) (string, error)
}

type AssetOpener func(ctx context.Context, release, id string) (io.ReadCloser, bootstrapassets.Descriptor, error)
type SecretLoader func(key string) (string, error)

// A bootstrap attempt installs a cluster on a real machine. maxAttempts bounds
// how often that may be repeated unattended before the run ends as failed, and
// maxRetryDelay bounds how far the wait between attempts is allowed to grow.
const (
	defaultMaxAttempts = 5
	maxRetryDelay      = 5 * time.Minute
	// Waiting deliberately costs no attempt — a run that never got to try must
	// not be abandoned for someone else's installer. But an unbounded wait is
	// the same defect in a politer form: a node that can never be asked, or an
	// older run that never finishes, would leave a timer going round forever.
	maxWaitCheckpoints = 60
)

type Service struct {
	store       *state.Store
	openAsset   AssetOpener
	loadSecret  SecretLoader
	runner      Runner
	active      sync.Map
	retryDelay  time.Duration
	maxAttempts int
}

func NewService(store *state.Store, openAsset AssetOpener, loadSecret SecretLoader, runner Runner) *Service {
	return &Service{store: store, openAsset: openAsset, loadSecret: loadSecret, runner: runner, retryDelay: 10 * time.Second, maxAttempts: defaultMaxAttempts}
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
	// One installation per community at a time. Two approved plans for the same
	// profile could each be started, and after a launcher restart ResumeActive
	// woke every run that was mid-flight at once — both then installed onto the
	// same machine. The older run keeps the node; this one waits rather than
	// being thrown away, because the elder may yet fail and hand it over.
	if service.supersededByOlderRun(ctx, run) {
		if service.waitedTooLong(ctx, run, "awaiting-other-run") {
			service.fail(ctx, run, "abandoned-waiting", "local_bootstrap.waited_too_long")
			return
		}
		_ = service.checkpoint(ctx, run, "awaiting-other-run")
		service.scheduleRetryAfter(run.ID, service.retryDelay)
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
	executionCompleted := completedExecutionCheckpoint(run.CurrentCheckpoint)
	credentials, err := service.loadCredentials(binding)
	if errors.Is(err, vault.ErrLocked) {
		if !executionCompleted {
			_ = service.checkpoint(ctx, run, "waiting-for-vault")
		}
		return
	}
	if err != nil {
		service.fail(ctx, run, "credentials-unavailable", "local_bootstrap.credentials_unavailable")
		return
	}
	if executionCompleted {
		if service.cancelled(ctx, run.ID) {
			_ = service.store.CompleteRunCancellation(ctx, run.ID, run.CurrentCheckpoint)
			return
		}
		observation, observeErr := service.runner.Observe(ctx, RunRequest{
			Binding: binding, RunID: run.ID, Credentials: credentials,
			Cancelled: func() bool { return service.cancelled(ctx, run.ID) },
		})
		if observeErr != nil {
			if service.cancelled(ctx, run.ID) {
				_ = service.store.CompleteRunCancellation(ctx, run.ID, run.CurrentCheckpoint)
				return
			}
			if errors.Is(observeErr, ErrExecutionPrecondition) {
				service.fail(ctx, run, "execution-precondition-changed", "local_bootstrap.precondition_changed")
				return
			}
			_ = service.checkpoint(ctx, run, "awaiting-convergence")
			service.scheduleRetry(run.ID)
			return
		}
		service.completeOrRetryConvergence(ctx, run, observation)
		return
	}
	archive, descriptor, err := service.openAsset(ctx, binding.Release, binding.AssetID)
	if err != nil {
		_ = service.checkpoint(ctx, run, "waiting-for-assets")
		return
	}
	if descriptor.SHA256 != binding.AssetSHA256 {
		service.fail(ctx, run, "asset-unavailable", "local_bootstrap.asset_unavailable")
		return
	}
	defer archive.Close()
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
	dnsCredential := ""
	if binding.Configuration.Public != nil {
		dnsCredential, err = service.loadSecret(binding.Configuration.Public.DNSCredentialKey)
		if errors.Is(err, vault.ErrLocked) {
			_ = service.checkpoint(ctx, run, "waiting-for-vault")
			return
		}
		if err != nil {
			service.fail(ctx, run, "dns-credentials-unavailable", "local_bootstrap.dns_credentials_unavailable")
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
		Binding: binding, RunID: run.ID, Archive: archive, Credentials: credentials, Secrets: secrets, DNSCredential: dnsCredential,
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
		// A previous attempt is still installing on this node. Waiting is the only
		// safe answer: starting a second one alongside it is what turned a single
		// interrupted run into two bootstraps uninstalling each other's k3s. It is
		// not a failed attempt either, so it must not spend the budget.
		if errors.Is(err, ErrAttemptInFlight) {
			if service.waitedTooLong(ctx, run, "awaiting-previous-attempt") {
				service.fail(ctx, run, "abandoned-waiting", "local_bootstrap.waited_too_long")
				return
			}
			_ = service.checkpoint(ctx, run, "awaiting-previous-attempt")
			service.scheduleRetryAfter(run.ID, service.retryDelay)
			return
		}
		// Every attempt here re-runs the installer on a real machine. Repeating
		// that forever every ten seconds is not resilience — it is an unattended
		// loop that reinstalls a cluster on top of itself for as long as nobody
		// is watching. Count what already happened, back off, and end honestly.
		attempts, countErr := service.store.CountRunCheckpoints(ctx, run.ID, "interrupted")
		if countErr == nil && attempts+1 >= service.maxAttempts {
			_ = service.checkpoint(ctx, run, "interrupted")
			service.fail(ctx, run, "execution-abandoned", "local_bootstrap.execution_abandoned")
			return
		}
		_ = service.checkpoint(ctx, run, "interrupted")
		service.scheduleRetryAfter(run.ID, service.backoff(attempts+1))
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
	service.completeOrRetryConvergence(ctx, run, observation)
}

func completedExecutionCheckpoint(checkpoint string) bool {
	return checkpoint == "bootstrap-command-complete" || checkpoint == "execution-complete" || checkpoint == "awaiting-convergence"
}

func (service *Service) completeOrRetryConvergence(ctx context.Context, run state.RunRecord, observation Observation) {
	planRecord, err := service.store.GetBootstrapPlan(ctx, run.PlanID)
	if err != nil {
		service.fail(ctx, run, "binding-missing", "local_bootstrap.binding_missing")
		return
	}
	binding, err := ParseBinding(planRecord.Binding)
	if err != nil {
		service.fail(ctx, run, "binding-invalid", "local_bootstrap.binding_invalid")
		return
	}
	publicReady := binding.Configuration.Public == nil || observation.DDNSReady && observation.CertificatesReady && observation.PublicApplicationsReady
	if !observation.K3SReady || !observation.ArgoCDReady || !observation.OverlaySynced || !publicReady {
		_ = service.checkpoint(ctx, run, "awaiting-convergence")
		service.scheduleRetry(run.ID)
		return
	}
	observedAt := observation.ObservedAt.UTC()
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	_ = service.store.CompleteRunVerification(ctx, run.ID, "verification-complete", "cluster.gitops.converged", observedAt)
}

// supersededByOlderRun reports whether another still-running bootstrap for the
// same profile was created first. Deciding by creation time rather than by
// which goroutine got there first keeps the answer the same across restarts,
// so a resumed pair cannot swap places and both proceed.
func (service *Service) supersededByOlderRun(ctx context.Context, run state.RunRecord) bool {
	active, err := service.store.ListActiveRuns(ctx)
	if err != nil {
		return false
	}
	for _, other := range active {
		if other.ID != run.ID && other.ProfileID == run.ProfileID && other.CreatedAt.Before(run.CreatedAt) {
			return true
		}
	}
	return false
}

// waitedTooLong reports whether a run has been held back so often that it is
// no longer waiting for anything that is going to happen.
func (service *Service) waitedTooLong(ctx context.Context, run state.RunRecord, checkpoint string) bool {
	waits, err := service.store.CountRunCheckpoints(ctx, run.ID, checkpoint)
	return err == nil && waits >= maxWaitCheckpoints
}

func (service *Service) scheduleRetry(runID string) {
	service.scheduleRetryAfter(runID, service.retryDelay)
}

func (service *Service) scheduleRetryAfter(runID string, delay time.Duration) {
	time.AfterFunc(delay, func() { service.Execute(runID) })
}

// backoff spreads repeated installation attempts out instead of hammering the
// node at a fixed ten seconds. Convergence polling deliberately keeps the flat
// interval: watching a cluster settle changes nothing and may take a while.
func (service *Service) backoff(attempt int) time.Duration {
	delay := service.retryDelay
	for count := 1; count < attempt && delay < maxRetryDelay; count++ {
		delay *= 2
	}
	if delay > maxRetryDelay {
		delay = maxRetryDelay
	}
	return delay
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

// Detail reads what the installed cluster is doing right now. It mutates
// nothing and is safe to call while a run is in flight — which is exactly when
// it is wanted, because "awaiting-convergence" on its own does not distinguish
// a cluster that is still starting from one that never will.
func (service *Service) Detail(ctx context.Context, profileID string) (Detail, error) {
	planRecord, err := service.store.LatestBootstrapPlanForProfile(ctx, profileID)
	if err != nil {
		return Detail{}, err
	}
	binding, err := ParseBinding(planRecord.Binding)
	if err != nil || binding.ProfileID != profileID {
		return Detail{}, fmt.Errorf("bootstrap binding is unusable")
	}
	credentials, err := service.loadCredentials(binding)
	if err != nil {
		return Detail{}, err
	}
	return service.runner.Detail(ctx, RunRequest{Binding: binding, Credentials: credentials, Cancelled: func() bool { return false }})
}

// Adopt carries a reviewed and merged overlay commit to the cluster. It is the
// last step of a release update and the only one that had no path through the
// console: everything before it — verifying a signed release, reviewing the
// Change Plan, opening the proposal, merging it — was supported, and then the
// root Application stayed pinned to the previous commit until somebody patched
// Kubernetes by hand.
func (service *Service) Adopt(ctx context.Context, profileID, revision string) (string, error) {
	if err := overlayadoption.ValidateRevision(revision); err != nil {
		return "", err
	}
	planRecord, err := service.store.LatestBootstrapPlanForProfile(ctx, profileID)
	if err != nil {
		return "", err
	}
	binding, err := ParseBinding(planRecord.Binding)
	if err != nil || binding.ProfileID != profileID {
		return "", fmt.Errorf("bootstrap binding is unusable")
	}
	credentials, err := service.loadCredentials(binding)
	if err != nil {
		return "", err
	}
	return service.runner.Adopt(ctx, RunRequest{Binding: binding, Credentials: credentials, Cancelled: func() bool { return false }}, revision)
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
	return func(ctx context.Context, release, id string) (io.ReadCloser, bootstrapassets.Descriptor, error) {
		file, descriptor, err := manager.OpenVerified(ctx, release, id)
		if err != nil {
			return nil, bootstrapassets.Descriptor{}, fmt.Errorf("open bootstrap archive: %w", err)
		}
		return file, descriptor, nil
	}
}
