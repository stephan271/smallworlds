package localbootstrap

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
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
