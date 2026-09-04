package capability_test

import (
	"reflect"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/capability"
)

func TestSelectionModesAreExplainableAndKeepRequiredPlatformServices(t *testing.T) {
	catalog := capability.DefaultCatalog()
	minimal, err := catalog.Assess(capability.Selection{Mode: capability.Minimal, DeploymentMode: capability.LocalLAN})
	if err != nil {
		t.Fatal(err)
	}
	if len(minimal.CommunityIDs) != 0 || !minimal.Selected["keycloak"] || minimal.Resources.MemoryMi == 0 {
		t.Fatalf("minimal assessment = %#v", minimal)
	}
	collaboration, err := catalog.Assess(capability.Selection{Mode: capability.Collaboration, DeploymentMode: capability.Hetzner})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"nextcloud", "collabora", "excalidraw", "jitsi"} {
		if !collaboration.Selected[id] {
			t.Errorf("collaboration omits %s", id)
		}
	}
	full, err := catalog.Assess(capability.Selection{Mode: capability.Full, DeploymentMode: capability.LocalPublic})
	if err != nil {
		t.Fatal(err)
	}
	// Full includes one of the two mutually exclusive Office editors (Collabora),
	// not both. Euro-Office is available through Custom.
	if len(full.CommunityIDs) != 9 {
		t.Fatalf("full community apps = %v, want 9", full.CommunityIDs)
	}
	custom, err := catalog.Assess(capability.Selection{Mode: capability.Custom, DeploymentMode: capability.LocalLAN, CommunityIDs: []string{"collabora"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(custom.CommunityIDs, []string{"collabora", "nextcloud"}) {
		t.Fatalf("custom dependencies = %v, want collabora and nextcloud", custom.CommunityIDs)
	}
	euroOffice, err := catalog.Assess(capability.Selection{Mode: capability.Custom, DeploymentMode: capability.LocalLAN, CommunityIDs: []string{"euro-office"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(euroOffice.CommunityIDs, []string{"euro-office", "nextcloud"}) {
		t.Fatalf("Euro-Office dependencies = %v, want euro-office and nextcloud", euroOffice.CommunityIDs)
	}
	if _, err := catalog.Assess(capability.Selection{Mode: capability.Custom, DeploymentMode: capability.LocalLAN, CommunityIDs: []string{"collabora", "euro-office"}}); err == nil {
		t.Fatal("mutually exclusive Office editors were accepted together")
	}
}
