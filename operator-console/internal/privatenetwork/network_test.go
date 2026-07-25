package privatenetwork_test

import (
	"strings"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/privatenetwork"
)

func lanOnly() privatenetwork.Input {
	return privatenetwork.Input{Shape: privatenetwork.LANOnly, ProfileID: "profile-1", BaseDomain: "smallworlds.internal"}
}

// publicCoordination is the shape a Hetzner installation uses: operator
// interfaces under a private base domain, coordination published under the
// community's public one.
func publicCoordination() privatenetwork.Input {
	return privatenetwork.Input{
		Shape:              privatenetwork.PublicCoordination,
		ProfileID:          "profile-1",
		BaseDomain:         "ops.smallworlds.internal",
		PublicDomain:       "example.org",
		PublishedHostnames: []string{"example.org", "files.example.org", "monitoring.example.org", "deploy.example.org", "vpn.example.org"},
	}
}

func TestPlanDerivesStableLANOnlyHostnamesOntoGateway(t *testing.T) {
	reference, err := privatenetwork.Plan(lanOnly())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if reference.Shape != privatenetwork.LANOnly || reference.CoordinationExposure != "private" || reference.Resolution != "magic-dns" {
		t.Fatalf("not a private LAN-only shape: %#v", reference)
	}
	if reference.CoordinationHost != "headscale.smallworlds.internal" {
		t.Fatalf("coordination host = %q", reference.CoordinationHost)
	}
	if reference.PublicDomain != "" {
		t.Fatal("a LAN-only network must carry no public domain")
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
	again, _ := privatenetwork.Plan(lanOnly())
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

// A publicly addressed installation publishes coordination so an Operator can
// join a device from anywhere — but the operator interfaces themselves stay
// exactly as private as in the LAN-only shape.
func TestPlanPublishesCoordinationWithoutPublishingOperatorInterfaces(t *testing.T) {
	reference, err := privatenetwork.Plan(publicCoordination())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if reference.CoordinationExposure != "public" || reference.CoordinationHost != "vpn.example.org" {
		t.Fatalf("coordination = %q at %q, want it published under the public domain", reference.CoordinationExposure, reference.CoordinationHost)
	}
	if reference.PublicDomain != "example.org" {
		t.Fatalf("public domain = %q", reference.PublicDomain)
	}
	published := map[string]bool{}
	for _, host := range reference.PublishedHostnames {
		published[host] = true
	}
	for _, endpoint := range reference.OperatorEndpoints {
		if published[endpoint.FQDN] {
			t.Fatalf("operator hostname %q has a public DNS record", endpoint.FQDN)
		}
		if !strings.HasSuffix(endpoint.FQDN, ".ops.smallworlds.internal") {
			t.Fatalf("operator hostname %q is not under the private base domain", endpoint.FQDN)
		}
		if endpoint.Target != reference.GatewayHostname {
			t.Fatalf("endpoint %q target = %q, want the Private Gateway", endpoint.Name, endpoint.Target)
		}
	}
	// Resolution stays MagicDNS in both shapes: a public address must not turn
	// into a permanent hosts-file entry or a public record.
	if reference.Resolution != "magic-dns" {
		t.Fatalf("resolution = %q", reference.Resolution)
	}
}

// The invariant that keeps a publicly addressed installation from acquiring a
// public console: an operator hostname that collides with a published record has
// a public route to it, so the reference is refused outright.
func TestPlanRefusesOperatorHostnamesWithPublicRecords(t *testing.T) {
	input := publicCoordination()
	// The community's public domain used directly as the operator base domain
	// puts grafana at monitoring's neighbour and argocd next to deploy — and, in
	// this configuration, console/grafana/argocd would resolve publicly.
	input.BaseDomain = "example.org"
	input.PublishedHostnames = append(input.PublishedHostnames, "console.example.org")
	if _, err := privatenetwork.Plan(input); err == nil {
		t.Fatal("an operator hostname with a public DNS record was accepted")
	}

	gatewayPublished := publicCoordination()
	gatewayPublished.PublishedHostnames = append(gatewayPublished.PublishedHostnames, "gateway.ops.smallworlds.internal")
	if _, err := privatenetwork.Plan(gatewayPublished); err == nil {
		t.Fatal("a publicly published Private Gateway was accepted")
	}
}

// A shape and its coordination exposure must agree. A reference claiming to be
// LAN-only while publishing coordination would misrepresent the installation's
// exposure to every later step that reads it.
func TestReferenceRejectsShapeAndExposureDisagreeing(t *testing.T) {
	for name, mutate := range map[string]func(*privatenetwork.Reference){
		"lan-only claiming public coordination": func(reference *privatenetwork.Reference) {
			reference.CoordinationExposure = "public"
		},
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
		"lan-only carrying a public domain": func(reference *privatenetwork.Reference) {
			reference.PublicDomain = "example.org"
		},
	} {
		t.Run(name, func(t *testing.T) {
			reference, err := privatenetwork.Plan(lanOnly())
			if err != nil {
				t.Fatal(err)
			}
			mutate(&reference)
			if err := reference.Validate(); err == nil {
				t.Fatal("invalid reference accepted")
			}
		})
	}

	for name, mutate := range map[string]func(*privatenetwork.Reference){
		"public claiming private coordination": func(reference *privatenetwork.Reference) {
			reference.CoordinationExposure = "private"
		},
		"public without a public domain": func(reference *privatenetwork.Reference) {
			reference.PublicDomain = ""
		},
		"coordination outside the public domain": func(reference *privatenetwork.Reference) {
			reference.CoordinationHost = "vpn.elsewhere.test"
		},
		"operator hostname published": func(reference *privatenetwork.Reference) {
			reference.PublishedHostnames = append(reference.PublishedHostnames, reference.OperatorEndpoints[0].FQDN)
		},
	} {
		t.Run(name, func(t *testing.T) {
			reference, err := privatenetwork.Plan(publicCoordination())
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

func TestPlanRejectsUnsafeInput(t *testing.T) {
	for name, mutate := range map[string]func(*privatenetwork.Input){
		"empty base domain":         func(i *privatenetwork.Input) { i.BaseDomain = "" },
		"single label":              func(i *privatenetwork.Input) { i.BaseDomain = "internal" },
		"double dot":                func(i *privatenetwork.Input) { i.BaseDomain = "smallworlds..internal" },
		"shell unsafe":              func(i *privatenetwork.Input) { i.BaseDomain = "smallworlds.internal;rm" },
		"leading space":             func(i *privatenetwork.Input) { i.BaseDomain = " " },
		"unknown shape":             func(i *privatenetwork.Input) { i.Shape = "somewhere-else" },
		"lan-only with public name": func(i *privatenetwork.Input) { i.PublicDomain = "example.org" },
	} {
		t.Run(name, func(t *testing.T) {
			input := lanOnly()
			mutate(&input)
			if _, err := privatenetwork.Plan(input); err == nil {
				t.Fatal("unsafe input accepted")
			}
		})
	}

	// The public shape needs a usable public domain; without one there is
	// nowhere to publish coordination.
	for name, domain := range map[string]string{"missing": "", "single label": "org", "double dot": "ex..org"} {
		t.Run("public domain "+name, func(t *testing.T) {
			input := publicCoordination()
			input.PublicDomain = domain
			if _, err := privatenetwork.Plan(input); err == nil {
				t.Fatal("unusable public domain accepted")
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
