package addcapability

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/assessment"
	"github.com/stephan271/smallworlds/operator-console/internal/capability"
)

// states builds an observed-state map with every platform service present and
// healthy (the post-bootstrap baseline), overlaying the given community states.
func states(catalog capability.Catalog, community map[string]assessment.CapabilityState) map[string]assessment.CapabilityState {
	result := map[string]assessment.CapabilityState{}
	for _, entry := range catalog.Capabilities {
		if entry.Category == capability.PlatformService {
			result[entry.ID] = assessment.StateHealthy
		} else {
			result[entry.ID] = assessment.StateDisabled
		}
	}
	for id, state := range community {
		result[id] = state
	}
	return result
}

var overlay = OverlayTarget{Release: "v1.2.20", RepositoryURL: "https://github.com/community/overlay", Domain: "home.example"}

func TestOffersOnlyDisabledOptionalCommunityApps(t *testing.T) {
	catalog := capability.DefaultCatalog()
	// nextcloud is already running, so it must not be offered again.
	observed := states(catalog, map[string]assessment.CapabilityState{"nextcloud": assessment.StateHealthy})

	offers, err := Offers(catalog, observed, capability.Hetzner)
	if err != nil {
		t.Fatalf("Offers: %v", err)
	}
	got := map[string]Offer{}
	for _, offer := range offers {
		got[offer.ID] = offer
	}
	if _, offered := got["nextcloud"]; offered {
		t.Fatal("a present capability must not be offered for addition")
	}
	if _, offered := got["keycloak"]; offered {
		t.Fatal("a required platform service must never be offered as a Community Application")
	}
	for _, want := range []string{"forgejo", "immich", "bulwark", "excalidraw", "jitsi", "collabora", "plane"} {
		if _, offered := got[want]; !offered {
			t.Errorf("disabled optional community app %q was not offered", want)
		}
	}
	// collabora depends on the still-disabled nextcloud... but nextcloud is
	// present here, so collabora has no disabled dependencies.
	if deps := got["collabora"].DisabledDependencies; len(deps) != 0 {
		t.Errorf("collabora disabled dependencies = %v, want none when nextcloud is present", deps)
	}
}

func TestOffersSortedAndDisabledDependenciesExplained(t *testing.T) {
	catalog := capability.DefaultCatalog()
	observed := states(catalog, nil) // every community app disabled

	offers, err := Offers(catalog, observed, capability.Hetzner)
	if err != nil {
		t.Fatalf("Offers: %v", err)
	}
	for i := 1; i < len(offers); i++ {
		if offers[i-1].ID >= offers[i].ID {
			t.Fatalf("offers not sorted by id: %q before %q", offers[i-1].ID, offers[i].ID)
		}
	}
	var collabora Offer
	for _, offer := range offers {
		if offer.ID == "collabora" {
			collabora = offer
		}
	}
	if !reflect.DeepEqual(collabora.DisabledDependencies, []string{"nextcloud"}) {
		t.Fatalf("collabora disabled dependencies = %v, want [nextcloud]", collabora.DisabledDependencies)
	}
	if !collabora.Stateful {
		t.Fatal("collabora holds persistent data and must be flagged stateful")
	}
}

func TestOffersRespectDeploymentMode(t *testing.T) {
	catalog := capability.Catalog{Version: 1, Capabilities: []capability.Entry{
		{ID: "keycloak", DisplayKey: "capability.keycloak", Category: capability.PlatformService, Required: true, SupportedDeploymentModes: []capability.DeploymentMode{capability.Hetzner, capability.LocalLAN, capability.LocalPublic}, Exposure: "private-gateway", Protection: "cluster-backup", Observer: "argocd-and-kubernetes"},
		{ID: "jitsi", DisplayKey: "capability.jitsi", Category: capability.CommunityApplication, SupportedDeploymentModes: []capability.DeploymentMode{capability.Hetzner}, Resources: capability.Resources{MemoryMi: 1024, StorageGi: 8}, Exposure: "application-policy", Protection: "capability-backup", Observer: "argocd-and-kubernetes", Dependencies: []string{"keycloak"}},
	}}
	observed := states(catalog, nil)

	if offers, _ := Offers(catalog, observed, capability.Hetzner); len(offers) != 1 || offers[0].ID != "jitsi" {
		t.Fatalf("Hetzner offers = %v, want [jitsi]", offers)
	}
	if offers, _ := Offers(catalog, observed, capability.LocalLAN); len(offers) != 0 {
		t.Fatalf("LocalLAN offers = %v, want none (jitsi unsupported)", offers)
	}
}

func TestBuildPlanPullsInDisabledDependencies(t *testing.T) {
	catalog := capability.DefaultCatalog()
	observed := states(catalog, nil) // nextcloud disabled, so collabora pulls it in

	plan, err := BuildPlan(catalog, "collabora", observed, Capacity{AllocatableMemoryMi: 8192, AllocatableStorageGi: 200, UsedMemoryMi: 2048, UsedStorageGi: 50}, overlay, capability.Hetzner)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if !reflect.DeepEqual(plan.AddedCapabilities, []string{"nextcloud", "collabora"}) {
		t.Fatalf("added = %v, want [nextcloud collabora] in dependency order", plan.AddedCapabilities)
	}
	if !reflect.DeepEqual(plan.PresentDependencies, []string{"cloudnative-pg", "garage", "keycloak"}) {
		t.Fatalf("present deps = %v, want the already-running platform deps", plan.PresentDependencies)
	}
	// nextcloud 1024/28 + collabora 768/4.
	if plan.Resources.RequiredMemoryMi != 1792 || plan.Resources.RequiredStorageGi != 32 {
		t.Fatalf("required = %dMi/%dGi, want 1792Mi/32Gi", plan.Resources.RequiredMemoryMi, plan.Resources.RequiredStorageGi)
	}
	if plan.Resources.AvailableMemoryMi != 6144 || plan.Resources.AvailableStorageGi != 150 {
		t.Fatalf("available = %dMi/%dGi, want 6144Mi/150Gi", plan.Resources.AvailableMemoryMi, plan.Resources.AvailableStorageGi)
	}
	if !plan.Resources.Fits() {
		t.Fatal("plan should fit the available capacity")
	}
	if !reflect.DeepEqual(plan.PersistentData, []string{"collabora", "nextcloud"}) {
		t.Fatalf("persistent data = %v, want [collabora nextcloud]", plan.PersistentData)
	}
	if !reflect.DeepEqual(plan.Exposure, []string{"application-policy"}) {
		t.Fatalf("exposure = %v", plan.Exposure)
	}
	if !reflect.DeepEqual(plan.Protection, []string{"capability-backup"}) {
		t.Fatalf("protection = %v", plan.Protection)
	}
}

func TestBuildPlanReportsCapacityShortfall(t *testing.T) {
	catalog := capability.DefaultCatalog()
	observed := states(catalog, nil)

	plan, err := BuildPlan(catalog, "immich", observed, Capacity{AllocatableMemoryMi: 4096, AllocatableStorageGi: 120, UsedMemoryMi: 3072, UsedStorageGi: 40}, overlay, capability.Hetzner)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	// immich needs 2048Mi/100Gi; only 1024Mi/80Gi headroom.
	if plan.Resources.FitsMemory {
		t.Error("memory shortfall should be reported, not hidden")
	}
	if plan.Resources.FitsStorage {
		t.Error("storage shortfall should be reported, not hidden")
	}
	if plan.Resources.Fits() {
		t.Error("plan must not report a fit when a resource is short")
	}
}

func TestBuildPlanRendersCatalogDerivedAdditiveDiff(t *testing.T) {
	catalog := capability.DefaultCatalog()
	observed := states(catalog, nil)

	plan, err := BuildPlan(catalog, "excalidraw", observed, Capacity{AllocatableMemoryMi: 8192, AllocatableStorageGi: 200}, overlay, capability.Hetzner)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	// excalidraw depends only on the present keycloak, so it is the sole addition.
	if !reflect.DeepEqual(plan.AddedCapabilities, []string{"excalidraw"}) {
		t.Fatalf("added = %v, want [excalidraw]", plan.AddedCapabilities)
	}
	// The proposal is the whole overlay as it would stand, so the new
	// application has a file and the root kustomization gains its entry.
	for _, path := range []string{"excalidraw/kustomization.yaml", "kustomization.yaml", "overlay-config.yaml"} {
		if _, ok := plan.Files[path]; !ok {
			t.Errorf("proposal missing file %q", path)
		}
	}
	if _, ok := plan.Files["applications/excalidraw.yaml"]; ok {
		t.Error("proposal writes an applications/ directory no overlay root includes")
	}
	// Adding a capability never removes anything. The root kustomization gains
	// lines; nothing that was there disappears.
	for _, line := range strings.Split(strings.TrimSpace(plan.GitDiff), "\n") {
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "--- ") {
			t.Fatalf("diff has a removal line %q; adding a capability must be additive", line)
		}
	}
	// The hostnames belong to the operator, not to the project.
	if !strings.Contains(plan.Files["excalidraw/kustomization.yaml"], "whiteboard.home.example") {
		t.Errorf("the added application keeps the project's own hostnames:\n%s", plan.Files["excalidraw/kustomization.yaml"])
	}
	if !strings.Contains(plan.GitDiff, "?ref=v1.2.20") {
		t.Error("diff must pin the tenant at the overlay release")
	}
	if !strings.Contains(plan.GitDiff, "https://github.com/community/overlay") {
		t.Error("diff must repoint the ArgoCD Application at the operator overlay")
	}
	// No credential-like material leaks into the reviewed diff. Checked with the
	// overlay package's own rule rather than a substring search: a secret NAME
	// belongs in an overlay (existingSecret: grafana-admin-creds is the safe
	// thing to write), only a secret VALUE does not, and a second cruder copy of
	// that distinction is how the two drift.
	if err := capability.ValidateOverlay(capability.Overlay{Files: plan.Files, Diff: plan.GitDiff}); err != nil {
		t.Errorf("proposal is not a valid overlay: %v", err)
	}
}

func TestBuildPlanRejectsNonOffered(t *testing.T) {
	catalog := capability.DefaultCatalog()

	// Required platform service.
	if _, err := BuildPlan(catalog, "keycloak", states(catalog, nil), Capacity{}, overlay, capability.Hetzner); !errors.Is(err, ErrNotOffered) {
		t.Errorf("adding a platform service: err = %v, want ErrNotOffered", err)
	}
	// Already-present community app.
	present := states(catalog, map[string]assessment.CapabilityState{"forgejo": assessment.StateHealthy})
	if _, err := BuildPlan(catalog, "forgejo", present, Capacity{}, overlay, capability.Hetzner); !errors.Is(err, ErrNotOffered) {
		t.Errorf("adding a present app: err = %v, want ErrNotOffered", err)
	}
	// Unknown capability.
	if _, err := BuildPlan(catalog, "does-not-exist", states(catalog, nil), Capacity{}, overlay, capability.Hetzner); !errors.Is(err, ErrNotOffered) {
		t.Errorf("adding an unknown capability: err = %v, want ErrNotOffered", err)
	}
}

func TestBuildPlanRejectsInvalidOverlayTarget(t *testing.T) {
	catalog := capability.DefaultCatalog()
	observed := states(catalog, nil)

	for _, bad := range []OverlayTarget{
		{Release: "main", RepositoryURL: "https://github.com/community/overlay"},
		{Release: "v1.2.20", RepositoryURL: "http://insecure/overlay"},
		{Release: "v1.2.20", RepositoryURL: "https://user:pw@github.com/community/overlay"},
	} {
		if _, err := BuildPlan(catalog, "excalidraw", observed, Capacity{}, bad, capability.Hetzner); !errors.Is(err, ErrInvalidOverlayTarget) {
			t.Errorf("overlay %+v: err = %v, want ErrInvalidOverlayTarget", bad, err)
		}
	}
}
