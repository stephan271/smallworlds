package capability_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/capability"
)

// The same knowledge exists twice: here in Go for the console, and in
// admin-tools/generate_domain_patches.py for the shell path. Two copies of an
// app-by-app hostname map drift the moment one side gains an application or a
// tenant renames an Ingress, and the symptom is a cluster that silently keeps
// somebody else's hostnames. So the script is treated as the source of truth and
// compared output for output.
func TestDomainPatchesMatchTheShellPathExactly(t *testing.T) {
	script, err := filepath.Abs("../../../admin-tools/generate_domain_patches.py")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Skipf("shell path generator is not present: %v", err)
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is unavailable, cannot compare against the shell path")
	}

	// Console capability IDs on the left, the script's --app names on the right.
	// They differ for the two root-level components.
	apps := map[string]string{
		"dashboard": "dashboard", "keycloak": "keycloak", "stalwart": "stalwart",
		"bulwark": "bulwark", "nextcloud": "nextcloud", "immich": "immich",
		"forgejo": "forgejo", "jitsi": "jitsi", "collabora": "collabora",
		"excalidraw": "excalidraw", "plane": "plane",
		"argocd-ingress": "argocd", "kube-prometheus-stack": "monitoring",
		"headscale": "headscale", "operator-console": "operator-console",
		// Not a selectable capability yet — only the console's catalog is
		// missing it — but its hostname knowledge already exists on both sides
		// and is exactly the kind that drifts unwatched.
		"pod-gateway": "pod-gateway",
	}
	for _, environment := range []struct{ domain, ext string }{
		{"home.example", ""},
		{"smallworlds.network", ".dev"},
		{"my-community.org", ".staging"},
	} {
		for id, scriptApp := range apps {
			t.Run(id+environment.ext, func(t *testing.T) {
				// The script appends to a kustomization file and adds a patches: key
				// only when the file has none. Seed it with exactly the sequence it
				// looks for, so what is compared is the patch list and not a header.
				const seed = "\npatches:\n"
				target := filepath.Join(t.TempDir(), "kustomization.yaml")
				if err := os.WriteFile(target, []byte(seed), 0o600); err != nil {
					t.Fatal(err)
				}
				command := exec.Command("python3", script, "--app", scriptApp, "--domain", environment.domain, "--ext="+environment.ext, "--kustomization-file", target)
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("shell path generator failed: %v\n%s", err, output)
				}
				written, err := os.ReadFile(target)
				if err != nil {
					t.Fatal(err)
				}
				want := string(written[len(seed):])
				got := capability.DomainPatches(id, environment.domain, environment.ext)
				if got != want {
					t.Fatalf("patches for %s differ from the shell path.\n--- go ---\n%s\n--- python ---\n%s", id, got, want)
				}
			})
		}
	}
}

// The console renders the overlay; the patches have to actually land in it, in
// the file kustomize will read them from.
func TestRenderedOverlayCarriesTheOperatorsHostnames(t *testing.T) {
	overlay, err := capability.DefaultCatalog().RenderOverlay(capability.OverlayInput{
		Selection:            capability.Selection{Mode: "custom", DeploymentMode: capability.LocalLAN, CommunityIDs: []string{"nextcloud"}},
		Release:              "v1.2.27",
		RepositoryURL:        "https://github.com/octocat/overlay.git",
		Domain:               "home.example",
		EnvironmentExtension: ".dev",
	})
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"kustomization.yaml":           "value: deploy.dev.home.example",
		"nextcloud/kustomization.yaml": "value: files.dev.home.example",
		"keycloak/kustomization.yaml":  `value: "https://identity.dev.home.example"`,
	} {
		if contents, found := overlay.Files[path]; !found || !contains(contents, want) {
			t.Fatalf("%s does not carry %q:\n%s", path, want, contents)
		}
	}
	// Nothing may claim the project's own hostnames once a domain was chosen.
	for path, contents := range overlay.Files {
		if contains(contents, "smallworlds.network") {
			t.Fatalf("%s still points at the project's domain:\n%s", path, contents)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for index := 0; index+len(needle) <= len(haystack); index++ {
			if haystack[index:index+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
