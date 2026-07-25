package hetznerprovision

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/hetzner"
	"github.com/stephan271/smallworlds/operator-console/internal/state"
	"github.com/stephan271/smallworlds/operator-console/internal/vault"
)

// Intent is the fixed workflow intent an approved infrastructure plan carries.
// Browser input never selects it.
const Intent = "ProvisionHetznerInfrastructure"

// The checkpoints a run passes through. They are durable and ordered, and a
// resumed run reads its last checkpoint to decide what still has to happen —
// which is what makes a launcher restart, a lost network, or a locked vault
// survivable rather than a reason to apply the same plan twice.
const (
	// CheckpointRevalidated means the approved facts were re-checked against the
	// project and still hold.
	CheckpointRevalidated = "plan-revalidated"
	// CheckpointApplied means OpenTofu finished and the resulting state is
	// recorded in the profile's workspace.
	CheckpointApplied = "infrastructure-applied"
	// CheckpointAwaitingConvergence means the node is up and the launcher is
	// waiting for k3s, Argo CD, and the overlay to converge.
	CheckpointAwaitingConvergence = "awaiting-convergence"
	// CheckpointWaitingForVault means the run cannot proceed until the Operator
	// unlocks the Launcher Vault. It is not a failure: the run resumes.
	CheckpointWaitingForVault = "waiting-for-vault"
	// CheckpointInterrupted means an attempt stopped part-way and the run will
	// re-inspect before retrying.
	CheckpointInterrupted = "interrupted"
)

// appliedCheckpoints are the checkpoints after which the provider has already
// been changed. A run resuming from one of these must never apply again — it
// observes convergence instead, because a second apply of a plan whose state was
// not recorded is exactly how a paid resource gets created twice.
var appliedCheckpoints = map[string]bool{
	CheckpointApplied:             true,
	CheckpointAwaitingConvergence: true,
}

// Convergence is what the launcher observed of the cluster coming up.
type Convergence struct {
	K3SReady      bool      `json:"k3sReady"`
	ArgoCDReady   bool      `json:"argocdReady"`
	OverlaySynced bool      `json:"overlaySynced"`
	ObservedAt    time.Time `json:"observedAt"`
}

// Complete reports whether the cluster reached the selected GitOps Overlay.
func (convergence Convergence) Complete() bool {
	return convergence.K3SReady && convergence.ArgoCDReady && convergence.OverlaySynced
}

// ConvergenceObserver watches a provisioned node come up. It must not mutate
// the cluster, and it reports an unreachable node as an unmet observation
// rather than an error, so a node still booting is a retry and not a failure.
type ConvergenceObserver interface {
	Observe(ctx context.Context, binding Binding, address string) (Convergence, error)
}

// Inspector re-runs the read-only inspection the preflight compares against.
// The service holds it as an interface so a run can re-inspect before every
// attempt without this package knowing how the provider is reached.
type Inspector interface {
	Observe(ctx context.Context, binding Binding) (Observed, error)
}

// Service executes approved Hetzner infrastructure plans.
//
// Its shape follows the Local Cluster Node bootstrap service, and for the same
// reason: the interesting behaviour is not the happy path but what happens when
// the launcher is killed, the network drops, or the Operator re-locks the vault
// mid-run. Every such interruption leaves a durable checkpoint, and every resume
// re-inspects the project before doing anything — a checkpoint records what the
// launcher did, and only a fresh inspection records what the provider actually
// has.
type Service struct {
	store      *state.Store
	inspector  Inspector
	reconciler Reconciler
	observer   ConvergenceObserver
	loadSecret func(key string) (string, error)
	loadPlan   func(ctx context.Context, binding Binding) (hetzner.ChangePlan, error)
	buildInput func(ctx context.Context, binding Binding) (ModuleInput, error)
	active     sync.Map
	retryDelay time.Duration
}

// NewService wires the execution path. loadSecret reads the custodied project
// token; loadPlan reads back the approved Change Plan the binding was derived
// from; buildInput renders the cloud-init payload and resolves the current
// administration access scope.
func NewService(store *state.Store, inspector Inspector, reconciler Reconciler, observer ConvergenceObserver, loadSecret func(string) (string, error), loadPlan func(context.Context, Binding) (hetzner.ChangePlan, error), buildInput func(context.Context, Binding) (ModuleInput, error)) *Service {
	return &Service{
		store: store, inspector: inspector, reconciler: reconciler, observer: observer,
		loadSecret: loadSecret, loadPlan: loadPlan, buildInput: buildInput, retryDelay: 30 * time.Second,
	}
}

// SetRetryDelay overrides the retry interval. Tests use it to keep a resumed run
// from waiting; production keeps the default.
func (service *Service) SetRetryDelay(delay time.Duration) { service.retryDelay = delay }

// Execute runs, or resumes, one approved plan. It is safe to call repeatedly:
// only one attempt per run is ever in flight.
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
	binding, ok := service.loadBinding(ctx, run)
	if !ok {
		return
	}

	// The token is needed for both applying and re-inspecting. A locked vault is
	// a pause, not a failure — the Operator locked it, and the run resumes when
	// they unlock it.
	token, err := service.loadSecret(vaultKey(run.ProfileID))
	if errors.Is(err, vault.ErrLocked) {
		_ = service.checkpoint(ctx, run, CheckpointWaitingForVault)
		return
	}
	if err != nil {
		service.fail(ctx, run, "token-unavailable", "hetzner_provisioning.token_unavailable")
		return
	}

	// Every attempt re-inspects, including a resumed one. A run interrupted an
	// hour ago cannot assume the project is as it left it.
	observed, err := service.inspector.Observe(ctx, binding)
	if err != nil {
		_ = service.checkpoint(ctx, run, CheckpointInterrupted)
		service.scheduleRetry(run.ID)
		return
	}
	preflight := Revalidate(binding, observed)
	if !preflight.Permitted {
		// A moved fact is not retried: the Operator must review and approve a
		// plan built against what the project actually looks like now.
		service.fail(ctx, run, "revalidation-failed:"+preflight.FirstFailure, "hetzner_provisioning.plan_stale")
		return
	}
	if err := service.checkpoint(ctx, run, CheckpointRevalidated); err != nil {
		return
	}
	if service.cancelled(ctx, run.ID) {
		_ = service.store.CompleteRunCancellation(ctx, run.ID, CheckpointRevalidated)
		return
	}

	// A run that already changed the provider observes; it never applies again.
	if appliedCheckpoints[run.CurrentCheckpoint] {
		service.converge(ctx, run, binding, PublicAddressOf(observed.Inventory))
		return
	}

	plan, err := service.loadPlan(ctx, binding)
	if err != nil {
		service.fail(ctx, run, "plan-missing", "hetzner_provisioning.binding_missing")
		return
	}
	input, err := service.buildInput(ctx, binding)
	if err != nil {
		service.fail(ctx, run, "bootstrap-payload-unavailable", "hetzner_provisioning.bootstrap_unavailable")
		return
	}
	// RenderModule re-checks the plan against the binding's digest, so a plan
	// that changed on disk after approval cannot be the one that gets applied.
	module, err := RenderModule(binding, plan, input)
	if err != nil {
		service.fail(ctx, run, "configuration-invalid", "hetzner_provisioning.configuration_invalid")
		return
	}

	outcome, err := service.reconciler.Apply(ctx, ReconcileRequest{
		Binding: binding, Module: module, RunID: run.ID, ProjectToken: token,
		Checkpoint: func(checkpoint string) error { return service.checkpoint(ctx, run, checkpoint) },
		Cancelled:  func() bool { return service.cancelled(ctx, run.ID) },
	})
	switch {
	case errors.Is(err, ErrReconcilerUnavailable):
		service.fail(ctx, run, "toolchain-unavailable", "hetzner_provisioning.toolchain_unavailable")
		return
	case errors.Is(err, ErrReconcilePrecondition):
		service.fail(ctx, run, "precondition-changed", "hetzner_provisioning.plan_stale")
		return
	case err != nil:
		if service.cancelled(ctx, run.ID) {
			latest, loadErr := service.store.GetRun(ctx, run.ID)
			if loadErr == nil {
				_ = service.store.CompleteRunCancellation(ctx, run.ID, latest.CurrentCheckpoint)
			}
			return
		}
		// Interrupted part-way: the next attempt re-inspects first, so whatever
		// the interrupted apply managed to create is seen before anything else
		// is attempted.
		_ = service.checkpoint(ctx, run, CheckpointInterrupted)
		service.scheduleRetry(run.ID)
		return
	case !outcome.Applied:
		_ = service.checkpoint(ctx, run, CheckpointInterrupted)
		service.scheduleRetry(run.ID)
		return
	}
	if err := service.checkpoint(ctx, run, CheckpointApplied); err != nil {
		return
	}
	address := outcome.ServerAddress
	if address == "" {
		address = PublicAddressOf(observed.Inventory)
	}
	service.converge(ctx, run, binding, address)
}

// converge polls the node until k3s, Argo CD, and the selected overlay are all
// up, checkpointing and retrying in between so a node that is merely still
// booting does not fail the run.
func (service *Service) converge(ctx context.Context, run state.RunRecord, binding Binding, address string) {
	if service.cancelled(ctx, run.ID) {
		_ = service.store.CompleteRunCancellation(ctx, run.ID, run.CurrentCheckpoint)
		return
	}
	convergence, err := service.observer.Observe(ctx, binding, address)
	if err != nil || !convergence.Complete() {
		_ = service.checkpoint(ctx, run, CheckpointAwaitingConvergence)
		service.scheduleRetry(run.ID)
		return
	}
	observedAt := convergence.ObservedAt.UTC()
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	_ = service.store.CompleteRunVerification(ctx, run.ID, "verification-complete", "cluster.gitops.converged", observedAt)
}

// loadBinding reads the immutable binding the plan was approved under and
// refuses anything that does not match the approved workflow plan's digest — a
// binding swapped after approval would apply something else entirely.
func (service *Service) loadBinding(ctx context.Context, run state.RunRecord) (Binding, bool) {
	record, err := service.store.GetHetznerProvisioningPlan(ctx, run.PlanID)
	if err != nil {
		service.fail(ctx, run, "binding-missing", "hetzner_provisioning.binding_missing")
		return Binding{}, false
	}
	binding, err := ParseBinding(record.Binding)
	if err != nil || binding.PlanID != run.PlanID || binding.ProfileID != run.ProfileID {
		service.fail(ctx, run, "binding-invalid", "hetzner_provisioning.binding_invalid")
		return Binding{}, false
	}
	plan, err := service.store.GetPlan(ctx, run.PlanID)
	if err != nil || plan.Intent != Intent || plan.Digest != binding.PlanDigestFor(Intent) {
		service.fail(ctx, run, "binding-digest-mismatch", "hetzner_provisioning.binding_invalid")
		return Binding{}, false
	}
	return binding, true
}

func (service *Service) scheduleRetry(runID string) {
	time.AfterFunc(service.retryDelay, func() { service.Execute(runID) })
}

func (service *Service) cancelled(ctx context.Context, runID string) bool {
	run, err := service.store.GetRun(ctx, runID)
	return err == nil && run.CancellationState == "requested"
}

func (service *Service) checkpoint(ctx context.Context, run state.RunRecord, checkpoint string) error {
	if err := service.store.UpdateRun(ctx, run.ID, "running", checkpoint, "", nil); err != nil {
		return err
	}
	_, err := service.store.AppendEvent(ctx, state.EventRecord{
		ProfileID: run.ProfileID, RunID: run.ID, Type: "run.checkpoint",
		MessageKey: "activity.run.checkpoint", Parameters: `{"checkpoint":"` + checkpoint + `"}`,
		OccurredAt: time.Now().UTC(),
	})
	return err
}

func (service *Service) fail(ctx context.Context, run state.RunRecord, checkpoint, messageKey string) {
	_ = service.store.UpdateRun(ctx, run.ID, "failed", checkpoint, "", nil)
	_, _ = service.store.AppendEvent(ctx, state.EventRecord{
		ProfileID: run.ProfileID, RunID: run.ID, Type: "run.failed",
		MessageKey: messageKey, Parameters: `{}`, OccurredAt: time.Now().UTC(),
	})
}

// vaultKey is where the profile's project token is custodied. It matches the
// launcher's own key so both sides read one credential.
func vaultKey(profileID string) string { return profileID + "/hetzner-project-token" }
