package capability_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/addcapability"
	"github.com/stephan271/smallworlds/operator-console/internal/assessment"
	"github.com/stephan271/smallworlds/operator-console/internal/capability"
	"github.com/stephan271/smallworlds/operator-console/internal/releaseupdate"
)

// Every flow that writes overlay content has to write the overlay this package
// establishes. Three of them had drifted apart without anyone noticing, because
// each was only ever tested against itself:
//
//   - add-capability wrote applications/<app>.yaml, a directory no overlay root
//     has ever included, and tenant units with no domain patches. Its proposals
//     would have merged and created nothing, on the project's own hostnames.
//   - release-update wrote smallworlds-release.yaml, a file with no reader
//     anywhere in the repository. Its proposals would have merged without
//     moving the cluster to the new release.
//
// Both are now rendered by capability.RenderChange. These tests compare their
// output against a freshly established overlay, file by file, so a fourth
// opinion cannot quietly appear.

func overlayInput(release string, communityIDs []string) capability.OverlayInput {
	return capability.OverlayInput{
		Selection:     capability.Selection{Mode: capability.Custom, DeploymentMode: capability.LocalLAN, CommunityIDs: communityIDs},
		Release:       release,
		RepositoryURL: "https://github.com/community/overlay.git",
		Domain:        "home.example",
	}
}

func compareWithEstablished(t *testing.T, what string, proposed map[string]string, input capability.OverlayInput) {
	t.Helper()
	established, err := capability.DefaultCatalog().RenderOverlay(input)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range established.Files {
		got, present := proposed[path]
		if !present {
			t.Errorf("%s omits %q, which establishing the overlay writes", what, path)
			continue
		}
		if got != want {
			t.Errorf("%s renders %q differently from establishing it:\n--- established ---\n%s\n--- proposed ---\n%s", what, path, want, got)
		}
	}
	for path := range proposed {
		if _, present := established.Files[path]; !present {
			t.Errorf("%s writes %q, which no established overlay contains", what, path)
		}
	}
}

func TestAddingACapabilityProposesTheOverlayEstablishingItWouldProduce(t *testing.T) {
	catalog := capability.DefaultCatalog()
	// A community that already runs forgejo, adding excalidraw.
	states := map[string]assessment.CapabilityState{}
	for _, entry := range catalog.Capabilities {
		switch {
		case entry.Required, entry.ID == "forgejo":
			states[entry.ID] = assessment.StateHealthy
		default:
			states[entry.ID] = assessment.StateDisabled
		}
	}
	plan, err := addcapability.BuildPlan(
		catalog, "excalidraw", states,
		addcapability.Capacity{AllocatableMemoryMi: 100000, AllocatableStorageGi: 10000},
		addcapability.OverlayTarget{Release: "v1.2.30", RepositoryURL: "https://github.com/community/overlay.git", Domain: "home.example"},
		capability.LocalLAN,
	)
	if err != nil {
		t.Fatal(err)
	}
	compareWithEstablished(t, "the add-capability proposal", plan.Files, overlayInput("v1.2.30", []string{"excalidraw", "forgejo"}))
}

func TestAReleaseUpdateProposesTheOverlayEstablishingItWouldProduce(t *testing.T) {
	profile := releaseupdate.ClusterProfile{
		LauncherVersion: "v1.2.30", ClusterVersion: "v1.2.30",
		BaseTag: "v1.2.29", CatalogVersion: 1,
		DeploymentMode: string(capability.LocalLAN),
		Capabilities:   []string{"forgejo", "immich", "keycloak", "garage"},
	}
	metadata := releaseupdate.Metadata{
		Release: "v1.2.30", BaseTag: "v1.2.30", CatalogVersion: 1,
		Images: map[string]string{"operator-console": "sha256:" + repeat("a", 64)},
		Tools:  map[string]string{"opentofu": "sha256:" + repeat("b", 64)},
		Compatibility: releaseupdate.Compatibility{
			LauncherMin: "v1.0.0", LauncherMax: "v2.0.0",
			ClusterMin: "v1.0.0", ClusterMax: "v2.0.0",
			CatalogMin: 1, CatalogMax: 1,
		},
		ReleaseNotes: []string{"note"},
		Recovery:     releaseupdate.Recovery{Expected: "roll back by reverting the merge"},
	}
	plan, err := releaseupdate.BuildPlan(profile, metadata, capability.DefaultCatalog(),
		releaseupdate.Overlay{RepositoryURL: "https://github.com/community/overlay.git", Domain: "home.example"})
	if err != nil {
		t.Fatal(err)
	}
	// keycloak and garage are platform services: always installed, never named
	// per application in the overlay. Only the community applications are.
	compareWithEstablished(t, "the release-update proposal", plan.Files, overlayInput("v1.2.30", []string{"forgejo", "immich"}))
}

// A proposal is reviewed as a diff, so the diff has to describe the files it
// carries — no change without a diff, and no diff without a change.
func TestAProposedChangeDiffsExactlyTheFilesThatMoved(t *testing.T) {
	catalog := capability.DefaultCatalog()
	change, err := catalog.RenderChange(overlayInput("v1.2.29", []string{"forgejo"}), overlayInput("v1.2.30", []string{"forgejo", "excalidraw"}))
	if err != nil {
		t.Fatal(err)
	}
	before, err := catalog.RenderOverlay(overlayInput("v1.2.29", []string{"forgejo"}))
	if err != nil {
		t.Fatal(err)
	}
	moved := make([]string, 0)
	for path, contents := range change.Files {
		if before.Files[path] != contents {
			moved = append(moved, path)
		}
	}
	sort.Strings(moved)
	for _, path := range moved {
		if !strings.Contains(change.Diff, "diff --git a/"+path+" b/"+path) {
			t.Errorf("%q changed but is absent from the diff", path)
		}
	}
	for path := range before.Files {
		if before.Files[path] == change.Files[path] && strings.Contains(change.Diff, "diff --git a/"+path+" b/"+path) {
			t.Errorf("%q is unchanged but appears in the diff", path)
		}
	}
	if len(moved) == 0 {
		t.Fatal("the fixture changed nothing, so it proves nothing")
	}
}

func repeat(character string, count int) string {
	value := make([]byte, count)
	for i := range value {
		value[i] = character[0]
	}
	return string(value)
}
