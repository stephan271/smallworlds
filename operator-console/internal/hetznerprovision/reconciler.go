package hetznerprovision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/fileprotection"
	"github.com/stephan271/smallworlds/operator-console/internal/tofu"
)

var (
	// ErrReconcileInterrupted marks a failure the run may retry after
	// re-inspecting: a dropped network, a killed launcher, a provider timeout.
	ErrReconcileInterrupted = errors.New("hetzner reconciliation interrupted")
	// ErrReconcilePrecondition marks a failure the run must not retry: the
	// project no longer matches what was approved.
	ErrReconcilePrecondition = errors.New("hetzner reconciliation precondition changed")
	// ErrReconcilerUnavailable is returned by the launcher default when no
	// verified toolchain has been resolved. The launcher refuses honestly rather
	// than reaching for an ambient tofu binary.
	ErrReconcilerUnavailable = errors.New("hetzner reconciliation requires the pinned verified toolchain")
)

// ReconcileRequest is one attempt at applying an approved plan.
type ReconcileRequest struct {
	Binding Binding
	Module  Module
	RunID   string
	// ProjectToken travels into the provider through the process environment
	// only. It is never written to the configuration, a variables file, or a
	// checkpoint, and every diagnostic passes through Redact before it is stored.
	ProjectToken string
	Checkpoint   func(checkpoint string) error
	Cancelled    func() bool
}

// Outcome is what one reconciliation observed. It carries no state contents and
// no credential material — only what the journey needs to continue.
type Outcome struct {
	Applied       bool      `json:"applied"`
	ServerAddress string    `json:"serverAddress,omitempty"`
	ServerID      string    `json:"serverId,omitempty"`
	StateDigest   string    `json:"stateDigest,omitempty"`
	ObservedAt    time.Time `json:"observedAt"`
}

// Reconciler applies an approved plan to the provider. It is an interface so the
// service's checkpoint, resume, and refusal behaviour is testable without a
// project, and so the production path stays a single, auditable implementation.
type Reconciler interface {
	Apply(ctx context.Context, request ReconcileRequest) (Outcome, error)
}

// UnavailableReconciler is the launcher's default. A launcher whose pinned
// toolchain has not been published cannot reconcile, and saying so is better
// than silently applying with whatever OpenTofu happens to be installed.
type UnavailableReconciler struct{}

// Apply always refuses.
func (UnavailableReconciler) Apply(context.Context, ReconcileRequest) (Outcome, error) {
	return Outcome{}, ErrReconcilerUnavailable
}

// BinaryResolver returns the filesystem path of a verified, pinned artifact.
// The launcher implements it over the bootstrap asset manager, so the path is
// always one the manager verified by digest and signature; no caller can point
// the reconciler at an executable of its choosing.
type BinaryResolver interface {
	VerifiedPath(release, id string) (string, error)
}

// TofuReconciler runs the pinned OpenTofu against a Cluster Profile's isolated
// workspace.
//
// The workspace lock is held for the whole apply and never broken: two
// reconciliations of one profile interleaving is a far worse failure than a
// blocked one, because the loser silently destroys what the winner created.
// State is written through the workspace so the previous generation is backed
// up first, and every line of OpenTofu's output is redacted before it can reach
// a checkpoint, an event, or an error.
type TofuReconciler struct {
	// Workspaces opens a profile's isolated state directory. It is a function so
	// the launcher supplies its data directory without this type knowing it.
	Workspaces func(profileID string) (*tofu.Workspace, error)
	// Binaries resolves the verified pinned OpenTofu executable.
	Binaries BinaryResolver
	// Timeout bounds one apply. An apply that hangs must not hold the workspace
	// lock forever.
	Timeout time.Duration
}

// Apply renders the module into the locked workspace, initialises the pinned
// provider offline, imports exactly the approved adoptions, and applies.
func (reconciler TofuReconciler) Apply(ctx context.Context, request ReconcileRequest) (Outcome, error) {
	if err := request.Binding.Validate(); err != nil {
		return Outcome{}, fmt.Errorf("%w: binding", ErrReconcilePrecondition)
	}
	if reconciler.Workspaces == nil || reconciler.Binaries == nil {
		return Outcome{}, ErrReconcilerUnavailable
	}
	binary, err := reconciler.Binaries.VerifiedPath(request.Binding.ToolchainRelease, openTofuArtifactID())
	if err != nil {
		return Outcome{}, fmt.Errorf("%w: %v", ErrReconcilerUnavailable, err)
	}
	workspace, err := reconciler.Workspaces(request.Binding.ProfileID)
	if err != nil {
		return Outcome{}, fmt.Errorf("%w: open workspace: %v", ErrReconcileInterrupted, err)
	}
	lock, err := workspace.Acquire("provisioning run " + request.RunID)
	if errors.Is(err, tofu.ErrWorkspaceLocked) {
		// Another reconciliation of this profile holds the workspace. Refusing is
		// the whole point: the lock is never broken.
		return Outcome{}, fmt.Errorf("%w: workspace is locked by another run", ErrReconcileInterrupted)
	}
	if err != nil {
		return Outcome{}, fmt.Errorf("%w: acquire workspace: %v", ErrReconcileInterrupted, err)
	}
	defer func() { _ = lock.Release() }()

	if err := request.Checkpoint("workspace-locked"); err != nil {
		return Outcome{}, err
	}
	directory, err := reconciler.stage(workspace, request)
	if err != nil {
		return Outcome{}, err
	}
	defer os.RemoveAll(directory)
	if err := request.Checkpoint("configuration-rendered"); err != nil {
		return Outcome{}, err
	}
	if request.Cancelled != nil && request.Cancelled() {
		return Outcome{}, ErrReconcileInterrupted
	}

	timeout := reconciler.Timeout
	if timeout <= 0 {
		timeout = 45 * time.Minute
	}
	applyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	run := func(checkpoint string, arguments ...string) error {
		if err := reconciler.execute(applyCtx, binary, directory, request, arguments...); err != nil {
			return err
		}
		return request.Checkpoint(checkpoint)
	}
	// Offline: the provider plugin is the verified artifact staged beside the
	// configuration, so init never reaches a registry and never resolves a
	// version other than the pinned one.
	if err := run("provider-initialised", "init", "-input=false", "-no-color", "-plugin-dir="+filepath.Join(directory, "plugins")); err != nil {
		return Outcome{}, err
	}
	if err := run("plan-applied", "apply", "-input=false", "-no-color", "-auto-approve", "-var-file="+filepath.Join(directory, "profile.tfvars.json")); err != nil {
		return Outcome{}, err
	}

	outcome, err := reconciler.persist(applyCtx, binary, directory, workspace, lock, request)
	if err != nil {
		return Outcome{}, err
	}
	if err := request.Checkpoint("state-recorded"); err != nil {
		return outcome, err
	}
	return outcome, nil
}

// stage writes the rendered configuration, the approved import blocks, and the
// non-secret variables into a private temporary directory beside the workspace.
// It is removed when the apply finishes: the configuration carries the cluster
// secrets in its cloud-init payload.
func (reconciler TofuReconciler) stage(workspace *tofu.Workspace, request ReconcileRequest) (string, error) {
	directory, err := os.MkdirTemp("", "smallworlds-tofu-")
	if err != nil {
		return "", fmt.Errorf("%w: stage configuration: %v", ErrReconcileInterrupted, err)
	}
	if err := fileprotection.SecureDirectory(directory); err != nil {
		os.RemoveAll(directory)
		return "", fmt.Errorf("%w: protect configuration: %v", ErrReconcileInterrupted, err)
	}
	for name, contents := range request.Module.Files {
		if err := fileprotection.WriteFileAtomically(filepath.Join(directory, name), []byte(contents)); err != nil {
			os.RemoveAll(directory)
			return "", fmt.Errorf("%w: write %s: %v", ErrReconcileInterrupted, name, err)
		}
	}
	if imports := renderImports(request.Module.Imports); imports != "" {
		if err := fileprotection.WriteFileAtomically(filepath.Join(directory, "imports.tf"), []byte(imports)); err != nil {
			os.RemoveAll(directory)
			return "", fmt.Errorf("%w: write imports: %v", ErrReconcileInterrupted, err)
		}
	}
	// Variables carry no secret: the token is supplied through the environment.
	variables, err := json.Marshal(map[string]string{})
	if err == nil {
		err = fileprotection.WriteFileAtomically(filepath.Join(directory, "profile.tfvars.json"), variables)
	}
	if err != nil {
		os.RemoveAll(directory)
		return "", fmt.Errorf("%w: write variables: %v", ErrReconcileInterrupted, err)
	}
	// Seed the working directory with the profile's own state, so OpenTofu reads
	// and writes the isolated copy rather than anything it might find elsewhere.
	if state, err := workspace.ReadState(); err == nil {
		if err := fileprotection.WriteFileAtomically(filepath.Join(directory, "terraform.tfstate"), state); err != nil {
			os.RemoveAll(directory)
			return "", fmt.Errorf("%w: seed state: %v", ErrReconcileInterrupted, err)
		}
	} else if !errors.Is(err, tofu.ErrNoState) {
		os.RemoveAll(directory)
		return "", fmt.Errorf("%w: read state: %v", ErrReconcileInterrupted, err)
	}
	return directory, nil
}

// persist reads the resulting state back into the profile's workspace — which
// backs up the previous generation — and reads the outputs the journey needs.
func (reconciler TofuReconciler) persist(ctx context.Context, binary, directory string, workspace *tofu.Workspace, lock *tofu.Lock, request ReconcileRequest) (Outcome, error) {
	state, err := os.ReadFile(filepath.Join(directory, "terraform.tfstate"))
	if err != nil {
		return Outcome{}, fmt.Errorf("%w: read resulting state: %v", ErrReconcileInterrupted, err)
	}
	if err := workspace.WriteState(lock, state); err != nil {
		return Outcome{}, fmt.Errorf("%w: record state: %v", ErrReconcileInterrupted, err)
	}
	status, err := workspace.Status()
	if err != nil {
		return Outcome{}, fmt.Errorf("%w: read workspace status: %v", ErrReconcileInterrupted, err)
	}
	outcome := Outcome{Applied: true, StateDigest: status.StateDigest, ObservedAt: time.Now().UTC()}
	raw, err := reconciler.capture(ctx, binary, directory, request, "output", "-json", "-no-color")
	if err != nil {
		// The apply succeeded; only the read-back of its outputs failed. The
		// address is recoverable by re-inspecting, so this is not fatal.
		return outcome, nil
	}
	var outputs map[string]struct {
		Value any `json:"value"`
	}
	if json.Unmarshal([]byte(raw), &outputs) == nil {
		if value, ok := outputs["server_ipv4"]; ok {
			outcome.ServerAddress = fmt.Sprintf("%v", value.Value)
		}
		if value, ok := outputs["server_id"]; ok {
			outcome.ServerID = fmt.Sprintf("%v", value.Value)
		}
	}
	return outcome, nil
}

func (reconciler TofuReconciler) execute(ctx context.Context, binary, directory string, request ReconcileRequest, arguments ...string) error {
	_, err := reconciler.capture(ctx, binary, directory, request, arguments...)
	return err
}

// capture runs one OpenTofu command. Every byte of its combined output passes
// through Redact before it can appear in an error, because the provider echoes
// the token it was given when it rejects it.
func (reconciler TofuReconciler) capture(ctx context.Context, binary, directory string, request ReconcileRequest, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, binary, arguments...)
	command.Dir = directory
	// A minimal environment: no inherited credentials, no ambient plugin cache,
	// no CLI configuration file that could redirect the provider source.
	command.Env = []string{
		"HOME=" + directory,
		"PATH=/usr/bin:/bin",
		"TF_IN_AUTOMATION=1",
		"TF_INPUT=0",
		"TF_CLI_CONFIG_FILE=" + filepath.Join(directory, "tofurc"),
		"TF_VAR_hcloud_token=" + request.ProjectToken,
		"TMPDIR=" + directory,
	}
	output, err := command.CombinedOutput()
	redacted := Redact(string(output), request.ProjectToken)
	if err != nil {
		if ctx.Err() != nil {
			return redacted, fmt.Errorf("%w: %s timed out", ErrReconcileInterrupted, arguments[0])
		}
		return redacted, fmt.Errorf("%w: %s failed: %s", ErrReconcileInterrupted, arguments[0], TailLines(redacted, 20))
	}
	return redacted, nil
}

// renderImports emits one config-driven import block per approved adoption. An
// import is how an existing resource comes under management without being
// re-created; nothing not listed in the binding reaches this file.
func renderImports(imports []Import) string {
	if len(imports) == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteString("# Explicit adoptions the Operator approved. Nothing else is imported.\n")
	for _, adoption := range imports {
		fmt.Fprintf(&out, "\nimport {\n  to = %s\n  id = %s\n}\n", adoption.Address, hclString(adoption.ProviderID))
	}
	return out.String()
}

// openTofuArtifactID is the descriptor id of the OpenTofu CLI for this platform.
func openTofuArtifactID() string {
	for _, id := range tofu.ArtifactIDs() {
		if strings.HasPrefix(id, "opentofu-") {
			return id
		}
	}
	return "opentofu"
}
