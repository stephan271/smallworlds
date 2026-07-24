package privatenetwork_test

import (
	"strings"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/privatenetwork"
)

func TestPlanDerivesStableLANOnlyHostnamesOntoGateway(t *testing.T) {
	reference, err := privatenetwork.Plan("profile-1", "smallworlds.internal")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if reference.Shape != "lan-only" || reference.CoordinationExposure != "private" || reference.Resolution != "magic-dns" {
		t.Fatalf("not a private LAN-only shape: %#v", reference)
	}
	if reference.CoordinationHost != "headscale.smallworlds.internal" {
		t.Fatalf("coordination host = %q", reference.CoordinationHost)
	}
	if reference.GatewayHostname != "gateway.smallworlds.internal" {
		t.Fatalf("gateway hostname = %q", reference.GatewayHostname)
	}
	wanted := map[string]string{
		"console": "console.smallworlds.internal",
		"grafana": "grafana.smallworlds.internal",
		"argocd":  "argocd.smallworlds.internal",
	}
	if len(reference.OperatorEndpoints) != len(wanted) {
		t.Fatalf("operator endpoints = %d", len(reference.OperatorEndpoints))
	}
	for _, endpoint := range reference.OperatorEndpoints {
		if wanted[endpoint.Name] != endpoint.FQDN {
			t.Fatalf("endpoint %q fqdn = %q", endpoint.Name, endpoint.FQDN)
		}
		if endpoint.Target != reference.GatewayHostname {
			t.Fatalf("endpoint %q target = %q, want gateway", endpoint.Name, endpoint.Target)
		}
	}

	// Derivation is deterministic and secret-free.
	again, _ := privatenetwork.Plan("profile-1", "smallworlds.internal")
	firstDigest, _ := reference.Digest()
	secondDigest, _ := again.Digest()
	if firstDigest != secondDigest {
		t.Fatal("digest is not deterministic")
	}
	encoded, _ := reference.Marshal()
	if strings.Contains(encoded, "secret") || strings.Contains(encoded, "KEY") {
		t.Fatalf("reference leaked secret material: %s", encoded)
	}
}

func TestReferenceRejectsPublicCoordinationAndForeignHostnames(t *testing.T) {
	for name, mutate := range map[string]func(*privatenetwork.Reference){
		"public coordination": func(reference *privatenetwork.Reference) { reference.CoordinationExposure = "public" },
		"foreign endpoint": func(reference *privatenetwork.Reference) {
			reference.OperatorEndpoints[0].FQDN = "console.evil.example"
		},
		"endpoint off gateway": func(reference *privatenetwork.Reference) {
			reference.OperatorEndpoints[1].Target = "console.smallworlds.internal"
		},
		"coordination off base": func(reference *privatenetwork.Reference) {
			reference.CoordinationHost = "headscale.other.internal"
		},
		"reordered services": func(reference *privatenetwork.Reference) {
			reference.OperatorEndpoints[0].Name = "grafana"
		},
	} {
		t.Run(name, func(t *testing.T) {
			reference, err := privatenetwork.Plan("profile-1", "smallworlds.internal")
			if err != nil {
				t.Fatal(err)
			}
			mutate(&reference)
			if err := reference.Validate(); err == nil {
				t.Fatal("invalid reference accepted")
			}
		})
	}
}

func TestPlanRejectsUnsafeBaseDomain(t *testing.T) {
	for name, baseDomain := range map[string]string{
		"empty":         "",
		"single label":  "internal",
		"double dot":    "smallworlds..internal",
		"shell unsafe":  "smallworlds.internal;rm",
		"leading space": " ",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := privatenetwork.Plan("profile-1", baseDomain); err == nil {
				t.Fatal("unsafe base domain accepted")
			}
		})
	}
}

func TestGenerateCoordinationSecretIsRandom(t *testing.T) {
	first, err := privatenetwork.GenerateCoordinationSecret()
	if err != nil || first == "" {
		t.Fatalf("secret = %q, err = %v", first, err)
	}
	second, _ := privatenetwork.GenerateCoordinationSecret()
	if first == second {
		t.Fatal("coordination secret is not random")
	}
}
