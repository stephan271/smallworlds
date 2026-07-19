package capability

import (
	"fmt"
	"sort"
)

var displayTranslations = map[string]map[string]string{
	"en": {},
	"de": {},
}

func init() {
	for _, id := range []string{"cert-manager", "cert-manager-webhook-hetzner", "cloudnative-pg", "argocd-ingress", "garage", "persistent-storage", "traefik", "kube-prometheus-stack", "loki-stack", "hermes", "remediation", "velero", "backup-replicator", "alertmanager-config", "backup-alerts", "trivy-operator", "trivy-dashboard", "renovate-cronjob", "headscale", "dashboard", "keycloak", "stalwart", "forgejo", "immich", "nextcloud", "bulwark", "excalidraw", "jitsi", "collabora", "plane"} {
		displayTranslations["en"]["capability."+id] = id
		displayTranslations["de"]["capability."+id] = id
	}
}

type Category string

const (
	PlatformService      Category = "platform-service"
	CommunityApplication Category = "community-application"
)

type DeploymentMode string

const (
	Hetzner     DeploymentMode = "hetzner"
	LocalLAN    DeploymentMode = "local-lan"
	LocalPublic DeploymentMode = "local-public"
)

type Resources struct {
	MemoryMi  int `json:"memoryMi"`
	StorageGi int `json:"storageGi"`
}

type Entry struct {
	ID                       string           `json:"id"`
	DisplayKey               string           `json:"displayKey"`
	Category                 Category         `json:"category"`
	Required                 bool             `json:"required"`
	Dependencies             []string         `json:"dependencies"`
	SupportedDeploymentModes []DeploymentMode `json:"supportedDeploymentModes"`
	Resources                Resources        `json:"resources"`
	Exposure                 string           `json:"exposure"`
	Protection               string           `json:"protection"`
	Observer                 string           `json:"observer"`
}

type Catalog struct {
	Version      int     `json:"version"`
	Capabilities []Entry `json:"capabilities"`
}

type SelectionMode string

const (
	Minimal       SelectionMode = "minimal"
	Collaboration SelectionMode = "collaboration"
	Full          SelectionMode = "full"
	Custom        SelectionMode = "custom"
)

type Selection struct {
	Mode           SelectionMode  `json:"mode"`
	DeploymentMode DeploymentMode `json:"deploymentMode"`
	CommunityIDs   []string       `json:"communityIds"`
}
type Assessment struct {
	Selected     map[string]bool `json:"selected"`
	CommunityIDs []string        `json:"communityIds"`
	Resources    Resources       `json:"resources"`
	Exposure     []string        `json:"exposure"`
	Protection   []string        `json:"protection"`
}

func DefaultCatalog() Catalog {
	platform := func(id string, memory, storage int) Entry {
		return Entry{ID: id, DisplayKey: "capability." + id, Category: PlatformService, Required: true, SupportedDeploymentModes: []DeploymentMode{Hetzner, LocalLAN, LocalPublic}, Resources: Resources{MemoryMi: memory, StorageGi: storage}, Exposure: "private-gateway", Protection: "cluster-backup", Observer: "argocd-and-kubernetes"}
	}
	community := func(id string, memory, storage int, dependencies ...string) Entry {
		return Entry{ID: id, DisplayKey: "capability." + id, Category: CommunityApplication, Dependencies: dependencies, SupportedDeploymentModes: []DeploymentMode{Hetzner, LocalLAN, LocalPublic}, Resources: Resources{MemoryMi: memory, StorageGi: storage}, Exposure: "application-policy", Protection: "capability-backup", Observer: "argocd-and-kubernetes"}
	}
	entries := []Entry{
		platform("cert-manager", 128, 1), platform("cert-manager-webhook-hetzner", 64, 1), platform("cloudnative-pg", 256, 2), platform("argocd-ingress", 64, 1), platform("garage", 512, 120), platform("persistent-storage", 64, 100), platform("traefik", 128, 1), platform("kube-prometheus-stack", 1024, 42), platform("loki-stack", 256, 20), platform("hermes", 128, 1), platform("remediation", 128, 1), platform("velero", 256, 5), platform("backup-replicator", 128, 2), platform("alertmanager-config", 32, 1), platform("backup-alerts", 32, 1), platform("trivy-operator", 256, 2), platform("trivy-dashboard", 128, 1), platform("renovate-cronjob", 128, 1), platform("headscale", 128, 5), platform("dashboard", 128, 1), platform("keycloak", 768, 40), platform("stalwart", 256, 20),
		community("forgejo", 768, 30, "keycloak", "cloudnative-pg"), community("immich", 2048, 100, "keycloak", "cloudnative-pg", "garage"), community("nextcloud", 1024, 28, "keycloak", "cloudnative-pg", "garage"), community("bulwark", 256, 2, "stalwart", "keycloak"), community("excalidraw", 128, 1, "keycloak"), community("jitsi", 1024, 8, "keycloak"), community("collabora", 768, 4, "nextcloud"), community("plane", 1024, 30, "keycloak", "cloudnative-pg"),
	}
	return Catalog{Version: 1, Capabilities: entries}
}

func (catalog Catalog) Validate() error {
	if catalog.Version != 1 || len(catalog.Capabilities) == 0 {
		return fmt.Errorf("unsupported catalog version")
	}
	entries := make(map[string]Entry, len(catalog.Capabilities))
	for _, entry := range catalog.Capabilities {
		if entry.ID == "" || entry.DisplayKey == "" || (entry.Category != PlatformService && entry.Category != CommunityApplication) || len(entry.SupportedDeploymentModes) == 0 || entry.Exposure == "" || entry.Protection == "" || entry.Observer != "argocd-and-kubernetes" || displayTranslations["en"][entry.DisplayKey] == "" || displayTranslations["de"][entry.DisplayKey] == "" {
			return fmt.Errorf("invalid catalog entry %q", entry.ID)
		}
		if _, exists := entries[entry.ID]; exists {
			return fmt.Errorf("duplicate capability %q", entry.ID)
		}
		modes := make(map[DeploymentMode]bool, len(entry.SupportedDeploymentModes))
		for _, mode := range entry.SupportedDeploymentModes {
			if mode != Hetzner && mode != LocalLAN && mode != LocalPublic {
				return fmt.Errorf("capability %q has unsupported mode %q", entry.ID, mode)
			}
			modes[mode] = true
		}
		entries[entry.ID] = entry
	}
	for _, entry := range catalog.Capabilities {
		for _, dependency := range entry.Dependencies {
			if _, exists := entries[dependency]; !exists {
				return fmt.Errorf("capability %q depends on unknown %q", entry.ID, dependency)
			}
		}
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("capability dependency cycle at %q", id)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, dependency := range entries[id].Dependencies {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		delete(visiting, id)
		visited[id] = true
		return nil
	}
	for id := range entries {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func (catalog Catalog) Entry(id string) (Entry, bool) {
	for _, entry := range catalog.Capabilities {
		if entry.ID == id {
			return entry, true
		}
	}
	return Entry{}, false
}

func (catalog Catalog) IDs() []string {
	ids := make([]string, 0, len(catalog.Capabilities))
	for _, entry := range catalog.Capabilities {
		ids = append(ids, entry.ID)
	}
	sort.Strings(ids)
	return ids
}

func (catalog Catalog) Assess(selection Selection) (Assessment, error) {
	if err := catalog.Validate(); err != nil {
		return Assessment{}, err
	}
	if selection.DeploymentMode != Hetzner && selection.DeploymentMode != LocalLAN && selection.DeploymentMode != LocalPublic {
		return Assessment{}, fmt.Errorf("unsupported deployment mode")
	}
	selected := map[string]bool{}
	for _, entry := range catalog.Capabilities {
		if entry.Required {
			selected[entry.ID] = true
		}
	}
	requested := selection.CommunityIDs
	switch selection.Mode {
	case Minimal:
		requested = nil
	case Collaboration:
		requested = []string{"nextcloud", "collabora", "excalidraw", "jitsi"}
	case Full:
		requested = nil
		for _, entry := range catalog.Capabilities {
			if entry.Category == CommunityApplication {
				requested = append(requested, entry.ID)
			}
		}
	case Custom:
	default:
		return Assessment{}, fmt.Errorf("unsupported selection mode")
	}
	var add func(string) error
	add = func(id string) error {
		entry, found := catalog.Entry(id)
		if !found {
			return fmt.Errorf("unknown capability %q", id)
		}
		if selected[id] {
			return nil
		}
		selected[id] = true
		for _, dep := range entry.Dependencies {
			if err := add(dep); err != nil {
				return err
			}
		}
		return nil
	}
	for _, id := range requested {
		entry, found := catalog.Entry(id)
		if !found || entry.Category != CommunityApplication {
			return Assessment{}, fmt.Errorf("invalid community selection %q", id)
		}
		if err := add(id); err != nil {
			return Assessment{}, err
		}
	}
	result := Assessment{Selected: selected}
	exposures, protections := map[string]bool{}, map[string]bool{}
	for _, entry := range catalog.Capabilities {
		if !selected[entry.ID] {
			continue
		}
		result.Resources.MemoryMi += entry.Resources.MemoryMi
		result.Resources.StorageGi += entry.Resources.StorageGi
		exposures[entry.Exposure] = true
		protections[entry.Protection] = true
		if entry.Category == CommunityApplication {
			result.CommunityIDs = append(result.CommunityIDs, entry.ID)
		}
	}
	for value := range exposures {
		result.Exposure = append(result.Exposure, value)
	}
	for value := range protections {
		result.Protection = append(result.Protection, value)
	}
	sort.Strings(result.CommunityIDs)
	sort.Strings(result.Exposure)
	sort.Strings(result.Protection)
	return result, nil
}
