package localbootstrap_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/localbootstrap"
	"github.com/stephan271/smallworlds/operator-console/internal/nodeinspect"
)

func validBinding() localbootstrap.Binding {
	return localbootstrap.Binding{
		PlanID: "plan-1", ProfileID: "profile-1", ProfileRevision: 2,
		Target:          nodeinspect.Target{Kind: nodeinspect.RemoteTarget, Host: "node.internal", Port: 22, Username: "operator"},
		HostFingerprint: "SHA256:pinned", InspectionDigest: strings.Repeat("a", 64), InspectedAt: time.Now().UTC(),
		NodeIdentity: "SHA256:pinned",
		Release:      "v1.2.27", AssetID: "bootstrap-linux-amd64", AssetSHA256: strings.Repeat("b", 64),
		OverlayRepositoryURL: "https://github.com/example/community-config", OverlayCommit: strings.Repeat("c", 40), OverlayRelease: "v1.2.27",
		AuthenticationKind: "private-key", SecretsVaultKey: "profile-1/cluster-secrets-manifest",
		Configuration: localbootstrap.Configuration{Domain: "example.internal", EnvironmentExtension: ".dev", DataDirectory: "/var/lib/smallworlds-data", NodeName: "smallworlds-node", ACMEEmail: "admin@example.internal"},
	}
}

func TestBindingRoundTripsOnlyImmutableNonSecretPlanData(t *testing.T) {
	binding := validBinding()
	encoded, err := binding.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encoded, "PRIVATE KEY") || strings.Contains(encoded, "token") {
		t.Fatalf("binding contains secret material: %s", encoded)
	}
	parsed, err := localbootstrap.ParseBinding(encoded)
	if err != nil || parsed.HostFingerprint != binding.HostFingerprint || parsed.OverlayCommit != binding.OverlayCommit {
		t.Fatalf("parsed binding = %#v, err = %v", parsed, err)
	}
	firstDigest := binding.PlanDigest("BootstrapLocalNode")
	binding.PlanID = "another-plan"
	if binding.PlanDigest("BootstrapLocalNode") != firstDigest || binding.PlanDigest("DifferentIntent") == firstDigest {
		t.Fatal("plan digest does not bind immutable execution inputs")
	}
}

func TestBindingRejectsMutableOverlayAndShellUnsafeConfiguration(t *testing.T) {
	for name, mutate := range map[string]func(*localbootstrap.Binding){
		"mutable overlay": func(binding *localbootstrap.Binding) { binding.OverlayCommit = "HEAD" },
		"http repository": func(binding *localbootstrap.Binding) { binding.OverlayRepositoryURL = "http://git.example/config" },
		"relative data":   func(binding *localbootstrap.Binding) { binding.Configuration.DataDirectory = "../../etc" },
		"unsafe email":    func(binding *localbootstrap.Binding) { binding.Configuration.ACMEEmail = "admin@example.test'\nROOT=1" },
		"legacy payload":  func(binding *localbootstrap.Binding) { binding.Release = "v1.2.25" },
	} {
		t.Run(name, func(t *testing.T) {
			binding := validBinding()
			mutate(&binding)
			if _, err := binding.Marshal(); err == nil {
				t.Fatal("unsafe binding accepted")
			}
		})
	}
}

func TestPublicConfigurationRequiresExactSecretFreeExposureContract(t *testing.T) {
	binding := validBinding()
	binding.Configuration = localbootstrap.Configuration{
		Domain: "community.example", DataDirectory: "/var/lib/smallworlds-data",
		NodeName: "smallworlds-node", ACMEEmail: "operator@community.example", ManageDNS: true,
		Public: &localbootstrap.PublicConfiguration{
			DNS01Provider: "hetzner", DNSZone: "community.example",
			DNSCredentialKey: "profile-1/local-public-dns-token",
			PublicIPBehavior: "dynamic-ddns", RouterAcknowledged: true,
		},
	}
	encoded, err := binding.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encoded, "dns-secret-value") {
		t.Fatalf("binding contains DNS credential: %s", encoded)
	}
	for name, mutate := range map[string]func(*localbootstrap.PublicConfiguration){
		"provider":       func(public *localbootstrap.PublicConfiguration) { public.DNS01Provider = "unknown" },
		"zone":           func(public *localbootstrap.PublicConfiguration) { public.DNSZone = "other.example" },
		"credential key": func(public *localbootstrap.PublicConfiguration) { public.DNSCredentialKey = "other/token" },
		"public IP":      func(public *localbootstrap.PublicConfiguration) { public.PublicIPBehavior = "static" },
		"router ack":     func(public *localbootstrap.PublicConfiguration) { public.RouterAcknowledged = false },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := binding
			public := *binding.Configuration.Public
			candidate.Configuration.Public = &public
			mutate(&public)
			if _, err := candidate.Marshal(); err == nil {
				t.Fatal("invalid public exposure contract accepted")
			}
		})
	}
}
