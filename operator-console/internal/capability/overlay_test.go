package capability_test

import (
	"strings"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/capability"
)

func TestRendererCreatesDeterministicSecretFreeOverlayAndReadableDiff(t *testing.T) {
	catalog := capability.DefaultCatalog()
	selection := capability.Selection{Mode: capability.Custom, DeploymentMode: capability.LocalLAN, CommunityIDs: []string{"nextcloud", "collabora"}}
	first, err := catalog.RenderOverlay(capability.OverlayInput{Selection: selection, Release: "v1.2.3", RepositoryURL: "https://github.com/example/private-overlay.git", Domain: "home.example"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := catalog.RenderOverlay(capability.OverlayInput{Selection: selection, Release: "v1.2.3", RepositoryURL: "https://github.com/example/private-overlay.git", Domain: "home.example"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Diff != second.Diff || !strings.Contains(first.Diff, "v1.2.3") || !strings.Contains(first.Diff, "nextcloud") || strings.Contains(first.Diff, "secret") {
		t.Fatalf("unexpected overlay diff:\n%s", first.Diff)
	}
	if _, ok := first.Files["nextcloud/kustomization.yaml"]; !ok {
		t.Fatal("overlay omits selected community app")
	}
	if _, ok := first.Files["collabora/kustomization.yaml"]; !ok {
		t.Fatal("overlay omits selected dependent app")
	}
	if strings.Contains(first.Files["kustomization.yaml"], "token") {
		t.Fatal("overlay root contains a credential-like value")
	}
}

func TestRendererRejectsUnpinnedReleaseAndUnsafeRepositoryURL(t *testing.T) {
	catalog := capability.DefaultCatalog()
	for _, input := range []capability.OverlayInput{
		{Selection: capability.Selection{Mode: capability.Minimal, DeploymentMode: capability.Hetzner}, Release: "HEAD", RepositoryURL: "https://github.com/example/repo.git", Domain: "example.com"},
		{Selection: capability.Selection{Mode: capability.Minimal, DeploymentMode: capability.Hetzner}, Release: "v1.2.3", RepositoryURL: "https://token@example/repo.git", Domain: "example.com"},
	} {
		if _, err := catalog.RenderOverlay(input); err == nil {
			t.Fatal("unsafe overlay input was accepted")
		}
	}
}

func TestRendererGoldenShapesCoverDeploymentModesAndPresets(t *testing.T) {
	catalog := capability.DefaultCatalog()
	for _, mode := range []capability.DeploymentMode{capability.Hetzner, capability.LocalLAN, capability.LocalPublic} {
		for _, preset := range []capability.SelectionMode{capability.Minimal, capability.Collaboration, capability.Full, capability.Custom} {
			t.Run(string(mode)+"/"+string(preset), func(t *testing.T) {
				overlay, err := catalog.RenderOverlay(capability.OverlayInput{Selection: capability.Selection{Mode: preset, DeploymentMode: mode, CommunityIDs: []string{"forgejo"}}, Release: "v1.2.3", RepositoryURL: "https://github.com/example/private-overlay.git", Domain: "cluster.example"})
				if err != nil {
					t.Fatal(err)
				}
				if err := capability.ValidateOverlay(overlay); err != nil {
					t.Fatalf("rendered overlay is invalid: %v", err)
				}
			})
		}
	}
}
