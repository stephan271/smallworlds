package operatordevice

import (
	"errors"
	"testing"
)

func stepKinds(g Guidance) []StepKind {
	kinds := make([]StepKind, len(g.Steps))
	for i, step := range g.Steps {
		kinds[i] = step.Kind
	}
	return kinds
}

func hasStep(g Guidance, kind StepKind) bool {
	for _, step := range g.Steps {
		if step.Kind == kind {
			return true
		}
	}
	return false
}

func TestGuidanceLANRequiresClusterCATrust(t *testing.T) {
	g, err := EnrollmentGuidance(LocalLAN, "gateway.sw.example.internal",
		[]string{"console.sw.example.internal", "grafana.sw.example.internal", "argocd.sw.example.internal"})
	if err != nil {
		t.Fatalf("EnrollmentGuidance: %v", err)
	}
	if !g.ClusterCATrustRequired || !hasStep(g, StepInstallClusterCA) {
		t.Fatalf("LAN-only guidance must require Cluster CA trust; steps=%v", stepKinds(g))
	}
	// Ordering: acquire → join → dns → install-ca → verify (verify is last).
	want := []StepKind{StepAcquireTailscaleClient, StepJoinPrivateNetwork, StepConfigurePrivateDNS, StepInstallClusterCA, StepVerifyGatewayAccess}
	got := stepKinds(g)
	if len(got) != len(want) {
		t.Fatalf("steps = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("step %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestGuidancePublicModesSkipCATrust(t *testing.T) {
	for _, mode := range []DeploymentMode{Hetzner, LocalPublic} {
		g, err := EnrollmentGuidance(mode, "gateway.example.com", []string{"console.example.com"})
		if err != nil {
			t.Fatalf("EnrollmentGuidance(%s): %v", mode, err)
		}
		if g.ClusterCATrustRequired || hasStep(g, StepInstallClusterCA) {
			t.Fatalf("%s must not require Cluster CA trust; steps=%v", mode, stepKinds(g))
		}
		// Verify step is always present and last, proving gateway reachability.
		last := g.Steps[len(g.Steps)-1]
		if last.Kind != StepVerifyGatewayAccess {
			t.Fatalf("%s last step = %q, want verify-gateway-access", mode, last.Kind)
		}
	}
}

func TestGuidanceDisclosesElevation(t *testing.T) {
	g, _ := EnrollmentGuidance(LocalLAN, "gateway.sw.example.internal", []string{"console.sw.example.internal"})
	elevated := map[StepKind]bool{}
	for _, step := range g.Steps {
		elevated[step.Kind] = step.ElevationRequired
	}
	if !elevated[StepAcquireTailscaleClient] || !elevated[StepJoinPrivateNetwork] || !elevated[StepInstallClusterCA] {
		t.Fatalf("elevation not disclosed on privileged steps: %+v", elevated)
	}
	if elevated[StepConfigurePrivateDNS] || elevated[StepVerifyGatewayAccess] {
		t.Fatalf("non-privileged steps must not claim elevation: %+v", elevated)
	}
}

func TestGuidanceRejectsBadInput(t *testing.T) {
	if _, err := EnrollmentGuidance("bogus", "gateway.example.com", []string{"console.example.com"}); !errors.Is(err, ErrInvalidGuidance) {
		t.Fatalf("bad mode err = %v, want ErrInvalidGuidance", err)
	}
	if _, err := EnrollmentGuidance(Hetzner, "gateway.example.com", nil); !errors.Is(err, ErrInvalidGuidance) {
		t.Fatalf("no hosts err = %v, want ErrInvalidGuidance", err)
	}
	if _, err := EnrollmentGuidance(Hetzner, "bad_host!", []string{"console.example.com"}); !errors.Is(err, ErrInvalidGuidance) {
		t.Fatalf("bad gateway err = %v, want ErrInvalidGuidance", err)
	}
	if _, err := EnrollmentGuidance(Hetzner, "gateway.example.com", []string{"console.example.com", "console.example.com"}); !errors.Is(err, ErrInvalidGuidance) {
		t.Fatalf("duplicate host err = %v, want ErrInvalidGuidance", err)
	}
}
