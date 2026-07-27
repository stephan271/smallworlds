package localbootstrap

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/nodeinspect"
)

func TestSameHostExecutionRejectsChangedNodeIdentityBeforePrivilegeUse(t *testing.T) {
	binding := Binding{
		PlanID: "plan-1", ProfileID: "profile-1", ProfileRevision: 1,
		Target: nodeinspect.Target{Kind: nodeinspect.SameHostTarget}, NodeIdentity: "sha256:expected",
		InspectionDigest: strings.Repeat("a", 64), InspectedAt: time.Now().UTC(), Release: "v1.2.27", AssetID: "bootstrap-linux-amd64", AssetSHA256: strings.Repeat("b", 64),
		OverlayRepositoryURL: "https://github.com/example/config", OverlayCommit: strings.Repeat("c", 40), OverlayRelease: "v1.2.27", AuthenticationKind: "same-host",
		Configuration: Configuration{Domain: "example.internal", DataDirectory: "/var/lib/smallworlds-data", NodeName: "node-1"},
	}
	runner := ProductionRunner{SameHostInspector: func(profileID, dataDirectory string) (nodeinspect.Report, error) {
		if profileID != binding.ProfileID {
			t.Fatalf("profile ID = %q", profileID)
		}
		if dataDirectory != binding.Configuration.DataDirectory {
			t.Fatalf("data directory = %q", dataDirectory)
		}
		return nodeinspect.Report{NodeIdentity: "sha256:different"}, nil
	}}

	_, err := runner.Run(context.Background(), RunRequest{Binding: binding})
	if !errors.Is(err, ErrExecutionPrecondition) {
		t.Fatalf("expected changed identity precondition error, got %v", err)
	}
}

func TestRuntimeArchiveGeneratesWriteOnlyDNSSecret(t *testing.T) {
	request := RunRequest{RunID: "run-public", DNSCredential: "dns-secret-value", Binding: Binding{
		PlanID: "plan-public", ProfileID: "profile-public", ProfileRevision: 1,
		Target: nodeinspect.Target{Kind: nodeinspect.SameHostTarget}, NodeIdentity: "sha256:pinned",
		InspectionDigest: strings.Repeat("a", 64), InspectedAt: time.Now().UTC(), Release: "v1.2.27", AssetID: "bootstrap-linux-amd64", AssetSHA256: strings.Repeat("b", 64),
		OverlayRepositoryURL: "https://github.com/example/config", OverlayCommit: strings.Repeat("c", 40), OverlayRelease: "v1.2.27", AuthenticationKind: "same-host",
		Configuration: Configuration{Domain: "community.example", DataDirectory: "/var/lib/smallworlds-data", NodeName: "node-1", ACMEEmail: "operator@community.example", ManageDNS: true,
			Public: &PublicConfiguration{DNS01Provider: "hetzner", DNSZone: "community.example", DNSCredentialKey: "profile-public/local-public-dns-token", PublicIPBehavior: "dynamic-ddns", RouterAcknowledged: true}},
	}}
	archive, err := buildRuntimeArchive(request)
	if err != nil {
		t.Fatal(err)
	}
	gzipReader, err := gzip.NewReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	tarReader := tar.NewReader(gzipReader)
	files := map[string]string{}
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		contents, err := io.ReadAll(tarReader)
		if err != nil {
			t.Fatal(err)
		}
		files[header.Name] = string(contents)
	}
	if strings.Contains(files["config.env"], request.DNSCredential) {
		t.Fatal("DNS credential leaked into shell configuration")
	}
	if !strings.Contains(files["secrets.yaml"], "name: hetzner-dns-token") ||
		!strings.Contains(files["secrets.yaml"], base64.StdEncoding.EncodeToString([]byte(request.DNSCredential))) {
		t.Fatalf("generated DNS Secret missing: %s", files["secrets.yaml"])
	}
}

func TestRuntimeArchiveKeepsSecretsOutOfShellConfiguration(t *testing.T) {
	request := RunRequest{RunID: "run-1", Secrets: "apiVersion: v1\nkind: Secret\ndata:\n  token: sensitive-value\n", Binding: Binding{
		PlanID: "plan-1", ProfileID: "profile-1", ProfileRevision: 1,
		Target: nodeinspect.Target{Kind: nodeinspect.RemoteTarget, Host: "node.internal", Port: 22, Username: "operator"}, HostFingerprint: "SHA256:pinned", NodeIdentity: "SHA256:pinned",
		InspectionDigest: strings.Repeat("a", 64), InspectedAt: time.Now().UTC(), Release: "v1.2.27", AssetID: "bootstrap-linux-amd64", AssetSHA256: strings.Repeat("b", 64),
		OverlayRepositoryURL: "https://github.com/example/config", OverlayCommit: strings.Repeat("c", 40), OverlayRelease: "v1.2.27", AuthenticationKind: "password", SecretsVaultKey: "profile-1/cluster-secrets-manifest",
		Configuration: Configuration{Domain: "example.internal", EnvironmentExtension: ".dev", DataDirectory: "/var/lib/smallworlds-data", NodeName: "node-1", ACMEEmail: "admin@example.internal", ManageDNS: true},
	}}
	archive, err := buildRuntimeArchive(request)
	if err != nil {
		t.Fatal(err)
	}
	gzipReader, err := gzip.NewReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	tarReader := tar.NewReader(gzipReader)
	files := map[string]string{}
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		contents, err := io.ReadAll(tarReader)
		if err != nil {
			t.Fatal(err)
		}
		files[header.Name] = string(contents)
		if header.Mode != 0600 {
			t.Fatalf("%s mode = %o", header.Name, header.Mode)
		}
	}
	if strings.Contains(files["config.env"], "sensitive-value") || !strings.Contains(files["config.env"], "ROOT_APP_GIT_REVISION='"+request.Binding.OverlayCommit+"'") {
		t.Fatalf("unsafe or unpinned config: %s", files["config.env"])
	}
	if files["secrets.yaml"] != request.Secrets {
		t.Fatalf("secrets payload changed")
	}
}

func TestShellQuoteCannotCreateAdditionalConfigurationStatements(t *testing.T) {
	quoted := shellQuote("value'\nINJECTED=yes")
	if quoted != "'value'\"'\"'\nINJECTED=yes'" {
		t.Fatalf("quoted value = %q", quoted)
	}
}

// The guard that stops a retry from starting a second installer beside a live
// one is three subtleties in one line — it must not match the shell that runs
// it, it must invert pgrep's answer, and it must not read a missing pgrep as
// "all clear". Getting any of them wrong reinstalls a cluster on top of itself,
// so the line is exercised through a real shell rather than read.
func TestNoBootstrapInFlightCommandAnswersThroughARealShell(t *testing.T) {
	if _, err := exec.LookPath("pgrep"); err != nil {
		t.Skip("pgrep is unavailable on this machine")
	}
	ask := func() error {
		return exec.Command("sh", "-c", noBootstrapInFlightCommand).Run()
	}
	// Nothing is installing: the command must succeed, which also proves it does
	// not match its own command line.
	if err := ask(); err != nil {
		t.Fatalf("a quiet node was reported busy: %v", err)
	}
	directory := t.TempDir()
	script := filepath.Join(directory, "bootstrap-local-node.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	installer := exec.Command("/bin/sh", script)
	if err := installer.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = installer.Process.Kill()
		_, _ = installer.Process.Wait()
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ask() != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("a running installer was not noticed")
}

// Merely naming the script is not running it. Anything on the node whose
// command line quotes the path — an editor, a copy, a shell reading this very
// test — would otherwise be mistaken for an installation in progress and make
// every attempt wait for a process that does not exist.
func TestACommandLineThatOnlyMentionsTheInstallerIsNotMistakenForOne(t *testing.T) {
	if _, err := exec.LookPath("pgrep"); err != nil {
		t.Skip("pgrep is unavailable on this machine")
	}
	mention := exec.Command("sleep", "30", "--", `printf '%s' "bootstrap-local-node.sh"`)
	if err := mention.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = mention.Process.Kill()
		_, _ = mention.Process.Wait()
	}()
	time.Sleep(100 * time.Millisecond)
	if err := exec.Command("sh", "-c", noBootstrapInFlightCommand).Run(); err != nil {
		t.Fatalf("a command that only mentions the installer was read as one running: %v", err)
	}
}
