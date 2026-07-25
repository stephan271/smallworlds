package hetznerprovision_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/hetznerprovision"
	"gopkg.in/yaml.v3"
)

func bootstrapInput() hetznerprovision.BootstrapInput {
	return hetznerprovision.BootstrapInput{
		NodeName:       "cc-pilot-node-01",
		Domain:         "example.org",
		ServerAddress:  "203.0.113.9",
		ACMEEmail:      "operator@example.org",
		ProjectToken:   projectToken,
		OverlayGitURL:  "https://github.com/example/my-community-config.git",
		OverlayCommit:  overlayCommit,
		ClusterSecrets: "apiVersion: v1\nkind: Secret\nmetadata:\n  name: shared\ndata:\n  token: c2VjcmV0\n",
		RecordNames:    []string{"@", "identity", "files", "mail"},
	}
}

func renderBootstrap(t *testing.T, input hetznerprovision.BootstrapInput) string {
	t.Helper()
	payload, err := hetznerprovision.RenderCloudInit(input)
	if err != nil {
		t.Fatalf("RenderCloudInit: %v", err)
	}
	return payload
}

// cloud-init silently ignores a payload it cannot parse, which would leave a
// paid server booted and doing nothing. Parsing it here is the only way to know
// the generated document is real YAML.
func TestCloudInitIsValidYAML(t *testing.T) {
	payload := renderBootstrap(t, bootstrapInput())
	if !strings.HasPrefix(payload, "#cloud-config\n") {
		t.Fatal("the payload must start with the cloud-config header")
	}
	var document struct {
		Packages   []string `yaml:"packages"`
		WriteFiles []struct {
			Path        string `yaml:"path"`
			Permissions string `yaml:"permissions"`
			Content     string `yaml:"content"`
		} `yaml:"write_files"`
		RunCmd []any `yaml:"runcmd"`
	}
	if err := yaml.Unmarshal([]byte(payload), &document); err != nil {
		t.Fatalf("payload is not valid YAML: %v\n%s", err, payload)
	}
	if len(document.RunCmd) == 0 || len(document.WriteFiles) == 0 {
		t.Fatal("the payload must write files and run commands")
	}
	// Anything staged on disk that carries credential material is owner-only.
	for _, file := range document.WriteFiles {
		if strings.Contains(file.Path, "/var/lib/smallworlds/") && file.Permissions != "0600" {
			t.Fatalf("%s has permissions %q, want owner-only", file.Path, file.Permissions)
		}
	}
}

// The console-provisioned node must be indistinguishable from one the
// repository's own Terraform provisions, so the bootstrap contract is asserted
// piece by piece.
func TestCloudInitImplementsTheBootstrapContract(t *testing.T) {
	payload := renderBootstrap(t, bootstrapInput())
	for name, fragment := range map[string]string{
		"publicly trusted ACME issuer": "name: letsencrypt-prod",
		"ACME DNS01 solver":            "solverName: hetzner",
		"in-cluster DNS override":      "name: coredns-custom",
		"persistent data layout":       hetznerprovision.DataDirectory + "/garage",
		"k3s server":                   "server --cluster-init",
		"traefik disabled":             "--disable traefik",
		"Argo CD install":              "argo-cd/stable/manifests/install.yaml",
		"Argo CD insecure upstream":    `{"data":{"server.insecure":"true"}}`,
		"root application":             "name: smallworlds-root",
		"tailscale client":             "tailscale.com/install.sh",
	} {
		if !strings.Contains(payload, fragment) {
			t.Fatalf("payload is missing the %s (%q)", name, fragment)
		}
	}
	// A Hetzner installation always serves a public domain, so a self-signed
	// issuer — valid for LAN-only — would produce certificates no browser
	// accepts.
	if strings.Contains(payload, "selfSigned") {
		t.Fatal("a Hetzner installation must never fall back to a self-signed issuer")
	}
}

// The root application is pinned to the approved commit. Tracking HEAD would
// deploy whatever the overlay advanced to between approval and boot.
func TestCloudInitPinsTheOverlayToTheApprovedCommit(t *testing.T) {
	payload := renderBootstrap(t, bootstrapInput())
	if !strings.Contains(payload, "targetRevision: '"+overlayCommit+"'") {
		t.Fatalf("the root application is not pinned to the approved commit:\n%s", payload)
	}
	if strings.Contains(payload, "targetRevision: HEAD") {
		t.Fatal("the root application must never track HEAD")
	}
}

// Convergence is observed through these markers, so each stage must write the
// one that stands for it — and the overlay marker must come after Argo CD is up,
// or the launcher would call a half-built cluster converged.
func TestCloudInitWritesReadinessMarkersInOrder(t *testing.T) {
	payload := renderBootstrap(t, bootstrapInput())
	k3s := strings.Index(payload, hetznerprovision.K3SReadyMarker)
	argocd := strings.Index(payload, hetznerprovision.ArgoCDReadyMarker)
	overlay := strings.Index(payload, hetznerprovision.OverlayReadyMarker)
	if k3s < 0 || argocd < 0 || overlay < 0 {
		t.Fatalf("a readiness marker is missing:\n%s", payload)
	}
	if !(k3s < argocd && argocd < overlay) {
		t.Fatal("readiness markers must be written in bootstrap order")
	}
}

// Cluster Secrets are applied before Argo CD starts reconciling, so an
// application whose secret is missing never reaches a crash loop — and the file
// does not linger on the node afterwards.
func TestCloudInitAppliesAndRemovesClusterSecrets(t *testing.T) {
	payload := renderBootstrap(t, bootstrapInput())
	apply := strings.Index(payload, "kubectl apply -f /var/lib/smallworlds/cluster-secrets.yaml")
	remove := strings.Index(payload, "cluster-secrets.yaml\n  - k3s kubectl create namespace argocd")
	if apply < 0 {
		t.Fatalf("cluster secrets are never applied:\n%s", payload)
	}
	if !strings.Contains(payload, "shred -u /var/lib/smallworlds/cluster-secrets.yaml") {
		t.Fatal("the staged cluster secrets must be removed after they are applied")
	}
	if remove >= 0 && remove < apply {
		t.Fatal("cluster secrets must be applied before Argo CD is installed")
	}

	// An installation with no Cluster Secrets stages nothing at all.
	input := bootstrapInput()
	input.ClusterSecrets = ""
	if bare := renderBootstrap(t, input); strings.Contains(bare, "cluster-secrets.yaml") {
		t.Fatal("no cluster-secrets file may be staged when the Operator supplied none")
	}
}

// Every value is interpolated into a YAML document and a shell script, so a
// newline or a quote in the wrong field is a code-execution boundary.
func TestCloudInitRefusesInjectableInput(t *testing.T) {
	for name, mutate := range map[string]func(*hetznerprovision.BootstrapInput){
		"newline in node name":       func(i *hetznerprovision.BootstrapInput) { i.NodeName = "node\nruncmd:" },
		"quote in ACME email":        func(i *hetznerprovision.BootstrapInput) { i.ACMEEmail = "a\"@b.test" },
		"newline in ACME email":      func(i *hetznerprovision.BootstrapInput) { i.ACMEEmail = "a@b.test\n  token: x" },
		"missing ACME email":         func(i *hetznerprovision.BootstrapInput) { i.ACMEEmail = "" },
		"newline in project token":   func(i *hetznerprovision.BootstrapInput) { i.ProjectToken = projectToken[:60] + "\nx" },
		"non-https overlay":          func(i *hetznerprovision.BootstrapInput) { i.OverlayGitURL = "git@github.com:x/y.git" },
		"quote in overlay url":       func(i *hetznerprovision.BootstrapInput) { i.OverlayGitURL = "https://x/'y.git" },
		"branch instead of a commit": func(i *hetznerprovision.BootstrapInput) { i.OverlayCommit = "main" },
		"injectable record name":     func(i *hetznerprovision.BootstrapInput) { i.RecordNames = []string{"a\n  b"} },
		"malformed server address":   func(i *hetznerprovision.BootstrapInput) { i.ServerAddress = "$(id)" },
		"no service hostnames":       func(i *hetznerprovision.BootstrapInput) { i.RecordNames = nil },
	} {
		t.Run(name, func(t *testing.T) {
			input := bootstrapInput()
			mutate(&input)
			if _, err := hetznerprovision.RenderCloudInit(input); !errors.Is(err, hetznerprovision.ErrInvalidBootstrap) {
				t.Fatalf("err = %v, want ErrInvalidBootstrap", err)
			}
		})
	}
}

// The root domain and every service hostname must resolve to this node from
// inside the cluster, or pods reach whichever server public DNS still points at.
func TestCloudInitOverridesResolutionForEveryServiceHostname(t *testing.T) {
	input := bootstrapInput()
	input.EnvExt = ".dev"
	payload := renderBootstrap(t, input)
	for _, host := range []string{"identity.dev.example.org", "files.dev.example.org", "mail.dev.example.org"} {
		if !strings.Contains(payload, host) {
			t.Fatalf("%s is not overridden in the in-cluster resolver:\n%s", host, payload)
		}
	}
	if !strings.Contains(payload, "$NODE_ADDRESS "+strings.Join([]string{"example.org"}, "")) && !strings.Contains(payload, "$NODE_ADDRESS ") {
		t.Fatal("the resolver override must point the hostnames at this node's own address")
	}
	if !strings.Contains(payload, "echo "+input.ServerAddress+" > /etc/smallworlds/node-address") {
		t.Fatalf("the approved address is not the one the node uses:\n%s", payload)
	}
}

// On a first install the Primary IP does not exist until OpenTofu creates it,
// so the node discovers its own address at boot rather than being given one —
// the same fallback the shared k3s-node template has. A rendered payload that
// hard-coded an empty address would leave k3s bound to nothing.
func TestCloudInitDiscoversItsAddressWhenTheIPDoesNotExistYet(t *testing.T) {
	input := bootstrapInput()
	input.ServerAddress = ""
	payload := renderBootstrap(t, input)
	if !strings.Contains(payload, "hostname -I | awk '{print $1}' > /etc/smallworlds/node-address") {
		t.Fatalf("the node does not discover its own address:\n%s", payload)
	}
	if strings.Contains(payload, "--node-ip= ") || strings.Contains(payload, "--node-ip=$(cat /etc/smallworlds/node-address)") == false {
		t.Fatalf("k3s does not bind to the discovered address:\n%s", payload)
	}
}
