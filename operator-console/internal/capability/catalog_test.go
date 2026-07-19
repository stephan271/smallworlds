package capability_test

import (
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/capability"
)

func TestCatalogRepresentsEveryDeclaredCapabilityWithLocalizedStableMetadata(t *testing.T) {
	catalog := capability.DefaultCatalog()
	if err := catalog.Validate(); err != nil {
		t.Fatalf("catalog validation failed: %v", err)
	}
	want := []string{
		"cert-manager", "cert-manager-webhook-hetzner", "cloudnative-pg", "argocd-ingress", "garage", "persistent-storage", "traefik", "kube-prometheus-stack", "loki-stack", "hermes", "remediation", "velero", "backup-replicator", "alertmanager-config", "backup-alerts", "trivy-operator", "trivy-dashboard", "renovate-cronjob", "headscale", "dashboard", "keycloak", "stalwart",
		"forgejo", "immich", "nextcloud", "bulwark", "excalidraw", "jitsi", "collabora", "plane",
	}
	if len(catalog.Capabilities) != len(want) {
		t.Fatalf("catalog has %d entries, want %d", len(catalog.Capabilities), len(want))
	}
	seen := make(map[string]bool, len(want))
	for _, entry := range catalog.Capabilities {
		if entry.ID == "" || entry.DisplayKey == "" || entry.Category == "" || len(entry.SupportedDeploymentModes) == 0 || entry.Exposure == "" || entry.Observer == "" {
			t.Fatalf("catalog entry is incomplete: %#v", entry)
		}
		if seen[entry.ID] {
			t.Fatalf("catalog repeats %q", entry.ID)
		}
		seen[entry.ID] = true
	}
	for _, id := range want {
		if !seen[id] {
			t.Errorf("catalog is missing declared capability %q", id)
		}
	}
}

func TestCatalogRejectsBrokenReferencesAndMissingMetadata(t *testing.T) {
	catalog := capability.Catalog{Version: 1, Capabilities: []capability.Entry{{
		ID:                       "broken",
		DisplayKey:               "",
		Category:                 capability.CommunityApplication,
		Dependencies:             []string{"absent"},
		SupportedDeploymentModes: []capability.DeploymentMode{"unsupported"},
		Observer:                 "absent-observer",
	}}}
	if err := catalog.Validate(); err == nil {
		t.Fatal("expected invalid catalog to be rejected")
	}
}

func TestCatalogRejectsUnknownLocalizedDisplayKeys(t *testing.T) {
	catalog := capability.DefaultCatalog()
	catalog.Capabilities[0].DisplayKey = "capability.not-translated"
	if err := catalog.Validate(); err == nil {
		t.Fatal("expected missing localized display key to be rejected")
	}
}
