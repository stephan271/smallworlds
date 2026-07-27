package localbootstrap

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/nodeinspect"
	"golang.org/x/crypto/ssh"
)

type ProductionRunner struct {
	ConvergenceTimeout time.Duration
	PollInterval       time.Duration
	SameHostInspector  func(string, string) (nodeinspect.Report, error)
}

func (runner ProductionRunner) Run(ctx context.Context, request RunRequest) (Observation, error) {
	if err := request.Binding.Validate(); err != nil {
		return Observation{}, err
	}
	if request.Binding.Target.Kind == nodeinspect.RemoteTarget {
		return runner.runRemote(ctx, request)
	}
	return runner.runSameHost(ctx, request)
}

func (runner ProductionRunner) Observe(ctx context.Context, request RunRequest) (Observation, error) {
	if err := request.Binding.Validate(); err != nil {
		return Observation{}, err
	}
	if request.Binding.Target.Kind == nodeinspect.RemoteTarget {
		return runner.observeRemote(ctx, request)
	}
	return runner.observeSameHost(ctx, request)
}

// Detail reads what the cluster is doing without changing anything. It repeats
// the same identity and authorisation checks as an observation: a diagnosis
// taken from a machine that is no longer the one this profile pinned would be
// worse than no diagnosis at all.
func (runner ProductionRunner) Detail(ctx context.Context, request RunRequest) (Detail, error) {
	if err := request.Binding.Validate(); err != nil {
		return Detail{}, err
	}
	if request.Binding.Target.Kind == nodeinspect.RemoteTarget {
		return runner.detailRemote(ctx, request)
	}
	return runner.detailSameHost(ctx, request)
}

func (runner ProductionRunner) detailRemote(ctx context.Context, request RunRequest) (Detail, error) {
	client, err := nodeinspect.DialTrusted(ctx, request.Binding.Target, request.Credentials, request.Binding.HostFingerprint)
	if err != nil {
		return Detail{}, fmt.Errorf("%w: connect trusted node", ErrInterrupted)
	}
	defer client.Close()
	if err := nodeinspect.ValidateSudoCredential(client, request.Credentials.SudoPassword); err != nil {
		return Detail{}, fmt.Errorf("%w: sudo authorization", ErrExecutionPrecondition)
	}
	identity, err := readRemoteNodeIdentity(client)
	if err != nil || identity != request.Binding.NodeIdentity {
		return Detail{}, fmt.Errorf("%w: remote node identity changed", ErrExecutionPrecondition)
	}
	command := detailCommand
	if request.Binding.Target.Username != "root" {
		command = "sudo -n sh -c " + shellQuote(command)
	} else {
		command = "sh -c " + shellQuote(command)
	}
	output, err := readSSHOutput(client, command)
	if err != nil {
		return Detail{}, fmt.Errorf("%w: read cluster detail", ErrInterrupted)
	}
	return ParseDetail(output, time.Now()), nil
}

func (runner ProductionRunner) detailSameHost(ctx context.Context, request RunRequest) (Detail, error) {
	inspect := runner.SameHostInspector
	if inspect == nil {
		inspect = nodeinspect.InspectSameHost
	}
	report, err := inspect(request.Binding.ProfileID, request.Binding.Configuration.DataDirectory)
	if err != nil || report.NodeIdentity != request.Binding.NodeIdentity {
		return Detail{}, fmt.Errorf("%w: same-host node identity changed", ErrExecutionPrecondition)
	}
	name, arguments := "sh", []string{"-c", detailCommand}
	if os.Geteuid() != 0 {
		if err := validateLocalSudo(ctx, request.Credentials.SudoPassword); err != nil {
			return Detail{}, fmt.Errorf("%w: sudo authorization", ErrExecutionPrecondition)
		}
		name, arguments = "sudo", []string{"-n", "sh", "-c", detailCommand}
	}
	process := exec.CommandContext(ctx, name, arguments...)
	var stdout bytes.Buffer
	process.Stdout = &stdout
	process.Stderr = io.Discard
	if err := process.Run(); err != nil {
		return Detail{}, fmt.Errorf("%w: read cluster detail", ErrInterrupted)
	}
	return ParseDetail(stdout.String(), time.Now()), nil
}

func (runner ProductionRunner) observeRemote(ctx context.Context, request RunRequest) (Observation, error) {
	client, err := nodeinspect.DialTrusted(ctx, request.Binding.Target, request.Credentials, request.Binding.HostFingerprint)
	if err != nil {
		return Observation{}, fmt.Errorf("%w: connect trusted node", ErrInterrupted)
	}
	defer client.Close()
	if err := nodeinspect.ValidateSudoCredential(client, request.Credentials.SudoPassword); err != nil {
		return Observation{}, fmt.Errorf("%w: sudo authorization", ErrExecutionPrecondition)
	}
	identity, err := readRemoteNodeIdentity(client)
	if err != nil || identity != request.Binding.NodeIdentity {
		return Observation{}, fmt.Errorf("%w: remote node identity changed", ErrExecutionPrecondition)
	}
	privileged := func(command string) error {
		if request.Binding.Target.Username != "root" {
			command = "sudo -n sh -c " + shellQuote(command)
		} else {
			command = "sh -c " + shellQuote(command)
		}
		return runSSHSession(client, command, nil)
	}
	return runner.observe(ctx, request.Binding, privileged, request.Cancelled)
}

func (runner ProductionRunner) observeSameHost(ctx context.Context, request RunRequest) (Observation, error) {
	inspect := runner.SameHostInspector
	if inspect == nil {
		inspect = nodeinspect.InspectSameHost
	}
	report, err := inspect(request.Binding.ProfileID, request.Binding.Configuration.DataDirectory)
	if err != nil || report.NodeIdentity != request.Binding.NodeIdentity {
		return Observation{}, fmt.Errorf("%w: same-host node identity changed", ErrExecutionPrecondition)
	}
	if os.Geteuid() != 0 {
		if err := validateLocalSudo(ctx, request.Credentials.SudoPassword); err != nil {
			return Observation{}, fmt.Errorf("%w: sudo authorization", ErrExecutionPrecondition)
		}
	}
	privileged := func(command string) error {
		arguments := []string{"-c", command}
		name := "sh"
		if os.Geteuid() != 0 {
			name = "sudo"
			arguments = []string{"-n", "sh", "-c", command}
		}
		process := exec.CommandContext(ctx, name, arguments...)
		process.Stdout = io.Discard
		process.Stderr = io.Discard
		return process.Run()
	}
	return runner.observe(ctx, request.Binding, privileged, request.Cancelled)
}

func (runner ProductionRunner) runRemote(ctx context.Context, request RunRequest) (Observation, error) {
	client, err := nodeinspect.DialTrusted(ctx, request.Binding.Target, request.Credentials, request.Binding.HostFingerprint)
	if err != nil {
		return Observation{}, fmt.Errorf("%w: connect trusted node", ErrInterrupted)
	}
	defer client.Close()
	if err := nodeinspect.ValidateSudoCredential(client, request.Credentials.SudoPassword); err != nil {
		return Observation{}, fmt.Errorf("%w: sudo authorization", ErrExecutionPrecondition)
	}
	identity, err := readRemoteNodeIdentity(client)
	if err != nil || identity != request.Binding.NodeIdentity {
		return Observation{}, fmt.Errorf("%w: remote node identity changed", ErrExecutionPrecondition)
	}
	privileged := func(command string, stdin io.Reader) error {
		if request.Binding.Target.Username != "root" {
			command = "sudo -n sh -c " + shellQuote(command)
		} else {
			command = "sh -c " + shellQuote(command)
		}
		return runSSHSession(client, command, stdin)
	}
	assetDirectory := remoteAssetDirectory(request.Binding)
	if err := privileged(stageStatusCommand(assetDirectory, request.Binding.Release), nil); err != nil {
		if err := privileged(stageAssetCommand(assetDirectory, request.Binding.Release), request.Archive); err != nil {
			return Observation{}, fmt.Errorf("%w: stage verified payload", ErrInterrupted)
		}
	}
	if err := request.Checkpoint("payload-staged"); err != nil {
		return Observation{}, err
	}
	if request.Cancelled() {
		return Observation{}, ErrInterrupted
	}
	runtimeArchive, err := buildRuntimeArchive(request)
	if err != nil {
		return Observation{}, err
	}
	runDirectory := remoteRunDirectory(request.RunID)
	if err := privileged(stageRuntimeCommand(runDirectory), runtimeArchive); err != nil {
		return Observation{}, fmt.Errorf("%w: stage runtime configuration", ErrInterrupted)
	}
	if err := request.Checkpoint("configuration-staged"); err != nil {
		return Observation{}, err
	}
	if request.Cancelled() {
		return Observation{}, ErrInterrupted
	}
	// Nothing may start a second installer beside one that is still running. The
	// bootstrap lives on the SSH session, so a dropped connection leaves it alive
	// on the node while the launcher sees a failure and retries — that is how one
	// interrupted run became two bootstraps uninstalling each other's k3s. The
	// bracket in the pattern keeps it from matching the shell that evaluates it,
	// and the negation means the check has to succeed *and* find nothing: a node
	// that cannot be asked counts as busy, because guessing wrong here starts a
	// second installation.
	if err := privileged(noBootstrapInFlightCommand, nil); err != nil {
		return Observation{}, ErrAttemptInFlight
	}
	if err := request.Checkpoint("bootstrap-atomic-operation"); err != nil {
		return Observation{}, err
	}
	bootstrapCommand := assetDirectory + "/run-local-node-bootstrap.sh " + runDirectory + "/config.env"
	if err := privileged(bootstrapCommand, nil); err != nil {
		return Observation{}, fmt.Errorf("%w: bootstrap command", ErrInterrupted)
	}
	if err := request.Checkpoint("bootstrap-command-complete"); err != nil {
		return Observation{}, err
	}
	if request.Cancelled() {
		return Observation{}, ErrInterrupted
	}
	return runner.observe(ctx, request.Binding, func(command string) error { return privileged(command, nil) }, request.Cancelled)
}

func (runner ProductionRunner) runSameHost(ctx context.Context, request RunRequest) (Observation, error) {
	inspect := runner.SameHostInspector
	if inspect == nil {
		inspect = nodeinspect.InspectSameHost
	}
	report, err := inspect(request.Binding.ProfileID, request.Binding.Configuration.DataDirectory)
	if err != nil || report.NodeIdentity != request.Binding.NodeIdentity {
		return Observation{}, fmt.Errorf("%w: same-host node identity changed", ErrExecutionPrecondition)
	}
	if os.Geteuid() != 0 {
		if err := validateLocalSudo(ctx, request.Credentials.SudoPassword); err != nil {
			return Observation{}, fmt.Errorf("%w: sudo authorization", ErrExecutionPrecondition)
		}
	}
	privileged := func(command string, stdin io.Reader) error {
		arguments := []string{"-c", command}
		name := "sh"
		if os.Geteuid() != 0 {
			name = "sudo"
			arguments = []string{"-n", "sh", "-c", command}
		}
		process := exec.CommandContext(ctx, name, arguments...)
		process.Stdin = stdin
		process.Stdout = io.Discard
		process.Stderr = io.Discard
		return process.Run()
	}
	assetDirectory := remoteAssetDirectory(request.Binding)
	if err := privileged(stageStatusCommand(assetDirectory, request.Binding.Release), nil); err != nil {
		if err := privileged(stageAssetCommand(assetDirectory, request.Binding.Release), request.Archive); err != nil {
			return Observation{}, fmt.Errorf("%w: stage verified payload", ErrInterrupted)
		}
	}
	if err := request.Checkpoint("payload-staged"); err != nil {
		return Observation{}, err
	}
	if request.Cancelled() {
		return Observation{}, ErrInterrupted
	}
	runtimeArchive, err := buildRuntimeArchive(request)
	if err != nil {
		return Observation{}, err
	}
	runDirectory := remoteRunDirectory(request.RunID)
	if err := privileged(stageRuntimeCommand(runDirectory), runtimeArchive); err != nil {
		return Observation{}, fmt.Errorf("%w: stage runtime configuration", ErrInterrupted)
	}
	if err := request.Checkpoint("configuration-staged"); err != nil {
		return Observation{}, err
	}
	if request.Cancelled() {
		return Observation{}, ErrInterrupted
	}
	// Nothing may start a second installer beside one that is still running. The
	// bootstrap lives on the SSH session, so a dropped connection leaves it alive
	// on the node while the launcher sees a failure and retries — that is how one
	// interrupted run became two bootstraps uninstalling each other's k3s. The
	// bracket in the pattern keeps it from matching the shell that evaluates it,
	// and the negation means the check has to succeed *and* find nothing: a node
	// that cannot be asked counts as busy, because guessing wrong here starts a
	// second installation.
	if err := privileged(noBootstrapInFlightCommand, nil); err != nil {
		return Observation{}, ErrAttemptInFlight
	}
	if err := request.Checkpoint("bootstrap-atomic-operation"); err != nil {
		return Observation{}, err
	}
	if err := privileged(assetDirectory+"/run-local-node-bootstrap.sh "+runDirectory+"/config.env", nil); err != nil {
		return Observation{}, fmt.Errorf("%w: bootstrap command", ErrInterrupted)
	}
	if err := request.Checkpoint("bootstrap-command-complete"); err != nil {
		return Observation{}, err
	}
	if request.Cancelled() {
		return Observation{}, ErrInterrupted
	}
	return runner.observe(ctx, request.Binding, func(command string) error { return privileged(command, nil) }, request.Cancelled)
}

func (runner ProductionRunner) observe(ctx context.Context, binding Binding, execute func(string) error, cancelled func() bool) (Observation, error) {
	timeout := runner.ConvergenceTimeout
	if timeout <= 0 {
		timeout = 20 * time.Minute
	}
	interval := runner.PollInterval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	deadline := time.Now().Add(timeout)
	observation := Observation{CommandCompleted: true}
	for {
		if cancelled() {
			return observation, ErrInterrupted
		}
		observation.K3SReady = execute(observeK3SCommand) == nil
		observation.ArgoCDReady = execute(observeArgoCDCommand) == nil
		observation.OverlaySynced = execute(observeOverlayCommand) == nil
		if binding.Configuration.Public != nil {
			observation.DDNSReady = execute(observeDDNSCommand) == nil
			observation.CertificatesReady = execute(observeCertificatesCommand) == nil
			observation.PublicApplicationsReady = execute(observePublicApplicationsCommand) == nil
		} else {
			observation.DDNSReady = true
			observation.CertificatesReady = true
			observation.PublicApplicationsReady = true
		}
		observation.ObservedAt = time.Now().UTC()
		if observation.K3SReady && observation.ArgoCDReady && observation.OverlaySynced && observation.DDNSReady && observation.CertificatesReady && observation.PublicApplicationsReady {
			return observation, nil
		}
		if time.Now().After(deadline) {
			return observation, nil
		}
		select {
		case <-ctx.Done():
			return observation, fmt.Errorf("%w: observation cancelled", ErrInterrupted)
		case <-time.After(interval):
		}
	}
}

// noBootstrapInFlightCommand exits zero only when the node could be asked and
// nothing is running. Both halves matter: a bare negated pgrep would turn a
// missing pgrep (exit 127) into "all clear" and start a second installer, which
// is precisely the failure this guard exists to prevent. Anything else — a
// match, a node that cannot answer — means do not start one.
//
// The bracket keeps the pattern from matching the shell that evaluates it: the
// checking command line contains "[.]", which the regex does not match. The
// trailing boundary keeps it from matching a command line that merely mentions
// the file — the installer is always invoked as the script followed by its
// config argument, so anything with the name buried mid-string is somebody
// reading or copying it, not running it.
const noBootstrapInFlightCommand = `command -v pgrep >/dev/null 2>&1 && ! pgrep -f 'bootstrap-local-node[.]sh( |$)' >/dev/null 2>&1`

const observeK3SCommand = `test -f /etc/smallworlds/k3s-ready && k3s kubectl get nodes -o jsonpath='{range .items[*]}{range .status.conditions[?(@.type=="Ready")]}{.status}{end}{end}' | grep -q True`
const observeArgoCDCommand = `test -f /etc/smallworlds/argocd-ready && k3s kubectl -n argocd rollout status deployment/argocd-server --timeout=1s >/dev/null 2>&1`
const observeOverlayCommand = `test -f /etc/smallworlds/overlay-applied && test "$(k3s kubectl -n argocd get application smallworlds-root -o jsonpath='{.status.sync.status}:{.status.health.status}' 2>/dev/null)" = 'Synced:Healthy'`
const observeDDNSCommand = `test -n "$(k3s kubectl -n ddns get cronjob ddns -o jsonpath='{.status.lastSuccessfulTime}' 2>/dev/null)"`
const observeCertificatesCommand = `test "$(k3s kubectl get certificate -A -o jsonpath='{range .items[*]}{range .status.conditions[?(@.type=="Ready")]}{.status}{"\n"}{end}{end}' 2>/dev/null | grep -vc '^True$')" = 0 && test "$(k3s kubectl get certificate -A --no-headers 2>/dev/null | wc -l)" -gt 0`
const observePublicApplicationsCommand = `k3s kubectl get ingress -A -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null | grep -q .`

func remoteAssetDirectory(binding Binding) string {
	return "/var/lib/smallworlds-launcher/bootstrap/" + binding.AssetSHA256
}

func remoteRunDirectory(runID string) string {
	return "/var/lib/smallworlds-launcher/runs/" + runID
}

func stageStatusCommand(directory, release string) string {
	return "test -x " + directory + "/run-local-node-bootstrap.sh && test \"$(cat " + directory + "/VERSION)\" = " + shellQuote(release)
}

func stageAssetCommand(directory, release string) string {
	partial := directory + ".partial"
	return "set -eu; umask 077; mkdir -p /var/lib/smallworlds-launcher/bootstrap; rm -rf -- " + directory + " " + partial + "; mkdir -p " + partial + "; tar -xzf - -C " + partial + "; test -x " + partial + "/run-local-node-bootstrap.sh; test \"$(cat " + partial + "/VERSION)\" = " + shellQuote(release) + "; mv " + partial + " " + directory
}

func stageRuntimeCommand(directory string) string {
	return "set -eu; umask 077; mkdir -p /var/lib/smallworlds-launcher/runs; rm -rf -- " + directory + "; mkdir -p " + directory + "; tar -xzf - -C " + directory
}

func buildRuntimeArchive(request RunRequest) (*bytes.Reader, error) {
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	configuration := renderConfiguration(request)
	entries := []struct {
		name     string
		contents string
	}{{name: "config.env", contents: configuration}}
	if request.Secrets != "" {
		entries = append(entries, struct{ name, contents string }{name: "secrets.yaml", contents: request.Secrets})
	}
	if request.DNSCredential != "" {
		if len(entries) == 1 {
			entries = append(entries, struct{ name, contents string }{name: "secrets.yaml"})
		}
		index := len(entries) - 1
		if entries[index].name != "secrets.yaml" {
			return nil, fmt.Errorf("internal secrets archive layout")
		}
		if entries[index].contents != "" && !strings.HasSuffix(entries[index].contents, "\n") {
			entries[index].contents += "\n"
		}
		entries[index].contents += "---\napiVersion: v1\nkind: Namespace\nmetadata:\n  name: ddns\n---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: hetzner-dns-token\n  namespace: ddns\ntype: Opaque\ndata:\n  HCLOUD_TOKEN: " + base64.StdEncoding.EncodeToString([]byte(request.DNSCredential)) + "\n"
	}
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: 0600, Size: int64(len(entry.contents)), ModTime: time.Unix(0, 0)}
		if err := tarWriter.WriteHeader(header); err != nil {
			return nil, err
		}
		if _, err := io.WriteString(tarWriter, entry.contents); err != nil {
			return nil, err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return nil, err
	}
	if err := gzipWriter.Close(); err != nil {
		return nil, err
	}
	return bytes.NewReader(compressed.Bytes()), nil
}

func renderConfiguration(request RunRequest) string {
	configuration := request.Binding.Configuration
	values := [][2]string{
		{"DOMAIN", configuration.Domain},
		{"ENV_EXT", configuration.EnvironmentExtension},
		{"ROOT_APP_GIT_URL", request.Binding.OverlayRepositoryURL},
		{"ROOT_APP_GIT_REVISION", request.Binding.OverlayCommit},
		{"ACME_EMAIL", configuration.ACMEEmail},
		{"MANAGE_DNS", fmt.Sprintf("%t", configuration.ManageDNS)},
		{"DATA_DIR", configuration.DataDirectory},
		{"NODE_NAME", configuration.NodeName},
		{"PROFILE_ID", request.Binding.ProfileID},
		{"BOOTSTRAP_RUN_ID", request.RunID},
	}
	if request.Secrets != "" || request.DNSCredential != "" {
		values = append(values, [2]string{"SECRETS_MANIFEST", remoteRunDirectory(request.RunID) + "/secrets.yaml"})
	}
	var result strings.Builder
	for _, value := range values {
		result.WriteString(value[0])
		result.WriteByte('=')
		result.WriteString(shellQuote(value[1]))
		result.WriteByte('\n')
	}
	return result.String()
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func runSSHSession(client *ssh.Client, command string, stdin io.Reader) error {
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	session.Stdin = stdin
	session.Stdout = io.Discard
	session.Stderr = io.Discard
	return session.Run(command)
}

// readSSHOutput runs one command and keeps what it printed. The observation
// path deliberately discards output — it only needs an exit status — but a
// diagnosis is nothing but the output.
func readSSHOutput(client *ssh.Client, command string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	var stdout bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = io.Discard
	if err := session.Run(command); err != nil {
		return "", err
	}
	return stdout.String(), nil
}

func readRemoteNodeIdentity(client *ssh.Client) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	output, err := session.Output("cat /etc/machine-id")
	if err != nil || strings.TrimSpace(string(output)) == "" {
		return "", fmt.Errorf("read remote machine identity")
	}
	return nodeinspect.HashNodeIdentity(string(output)), nil
}

func validateLocalSudo(ctx context.Context, password string) error {
	arguments := []string{"-n", "-v"}
	var stdin io.Reader
	if password != "" {
		arguments = []string{"-S", "-p", "", "-v"}
		stdin = strings.NewReader(password + "\n")
	}
	process := exec.CommandContext(ctx, "sudo", arguments...)
	process.Stdin = stdin
	process.Stdout = io.Discard
	process.Stderr = io.Discard
	return process.Run()
}
