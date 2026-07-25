package decommission

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// ProtectionEvidence is the fresh, secret-free evidence a Lifecycle Authority
// reviews before irreversibly deleting storage. Adapters report observations;
// they must never manufacture a healthy verdict from a successful backup job.
type ProtectionEvidence struct {
	BackupFreshness       string   `json:"backupFreshness"`
	OffsiteRecoveryPoints []string `json:"offsiteRecoveryPoints"`
	RecoveryBundleStatus  string   `json:"recoveryBundleStatus"`
	Sufficient            bool     `json:"sufficient"`
	Warnings              []string `json:"warnings,omitempty"`
}

// FullInspection is intentionally distinct from the preserve-data inspection.
// Persistent resources may be profile-owned here, while they are retained by
// contract in a preserve-data plan.
type FullInspection struct {
	ProfileID       string              `json:"profileId"`
	DeploymentMode  string              `json:"deploymentMode"`
	ProfileRevision int64               `json:"profileRevision"`
	ObservedAt      time.Time           `json:"observedAt"`
	Resources       []InspectedResource `json:"resources"`
	Protection      ProtectionEvidence  `json:"protection"`
	Digest          string              `json:"digest"`
}

// ClassifyFull uses stable provider identity, profile tag and cluster identity;
// a familiar name is never evidence of ownership. The shared DNS zone and the
// GitOps overlay are retained even when they carry this profile's tag.
func ClassifyFull(profileID string, resources []Resource) ([]InspectedResource, error) {
	if strings.TrimSpace(profileID) == "" {
		return nil, ErrInvalidInspection
	}
	classified := make([]InspectedResource, 0, len(resources))
	for _, resource := range resources {
		if resource.Kind == "" || resource.ProviderID == "" {
			return nil, ErrInvalidInspection
		}
		ownership := Unknown
		switch {
		case resource.Kind == "gitops-overlay" || resource.Kind == "dns-zone":
			ownership = Retained
		case resource.Shared:
			ownership = Shared
		case resource.Tags["smallworlds-profile"] == profileID && resource.ClusterIdentity == profileID:
			ownership = ProfileOwned
		}
		classified = append(classified, InspectedResource{Resource: resource, Ownership: ownership})
	}
	sort.Slice(classified, func(i, j int) bool { return key(classified[i]) < key(classified[j]) })
	return classified, nil
}

func (inspection *FullInspection) Finalize() error {
	if inspection.ProfileID == "" || inspection.ProfileRevision < 1 || inspection.ObservedAt.IsZero() || inspection.Protection.BackupFreshness == "" || inspection.Protection.RecoveryBundleStatus == "" {
		return ErrInvalidInspection
	}
	for _, resource := range inspection.Resources {
		if resource.Kind == "" || resource.ProviderID == "" || resource.Ownership == "" {
			return ErrInvalidInspection
		}
	}
	sort.Strings(inspection.Protection.OffsiteRecoveryPoints)
	sort.Strings(inspection.Protection.Warnings)
	inspection.Digest = fullInspectionDigest(*inspection)
	return nil
}

type Stage string

const (
	ComputeStage    Stage = "compute"
	StorageStage    Stage = "storage"
	NetworkingStage Stage = "networking"
	DNSStage        Stage = "dns"
)

type FullItem struct {
	Item
	Stage       Stage  `json:"stage,omitempty"`
	Consequence string `json:"consequence"`
}

type FullPlan struct {
	ProfileID                string             `json:"profileId"`
	DeploymentMode           string             `json:"deploymentMode"`
	InspectionDigest         string             `json:"inspectionDigest"`
	Protection               ProtectionEvidence `json:"protection"`
	Items                    []FullItem         `json:"items"`
	IrreversibleConsequences []string           `json:"irreversibleConsequences"`
	RequiresOwnerOverride    bool               `json:"requiresOwnerOverride"`
	TypedConfirmation        string             `json:"typedConfirmation"`
	Digest                   string             `json:"digest"`
}

// BuildFullPlan only deletes resources proven profile-owned. Retained/shared
// and unknown resources remain visible in the plan, but cannot reach the
// executor. Missing protection warns and requires Owner override rather than
// trapping an operator in ongoing paid infrastructure.
func BuildFullPlan(inspection FullInspection) (FullPlan, error) {
	if err := inspection.Finalize(); err != nil {
		return FullPlan{}, err
	}
	plan := FullPlan{
		ProfileID: inspection.ProfileID, DeploymentMode: inspection.DeploymentMode, InspectionDigest: inspection.Digest,
		Protection: inspection.Protection, RequiresOwnerOverride: !inspection.Protection.Sufficient,
	}
	for _, resource := range inspection.Resources {
		item := FullItem{Item: Item{Resource: resource.Resource, Ownership: resource.Ownership, Action: Retain, Reason: "ownership-not-proven-retained"}, Consequence: "Resource is retained; no external mutation is authorized."}
		switch resource.Ownership {
		case ProfileOwned:
			if removableFull(resource.Kind) {
				item.Action = Remove
				item.Stage = removalStage(resource.Kind, resource.Persistent)
				item.Reason = "proven-profile-owned-full-decommission"
				item.Consequence = fullConsequence(item.Stage, resource.Persistent)
				plan.IrreversibleConsequences = append(plan.IrreversibleConsequences, resource.ProviderID+": "+item.Consequence)
			} else {
				item.Reason = "profile-owned-but-contract-retained"
			}
		case Shared:
			item.Reason = "shared-resource-retained"
		case Retained:
			item.Reason = "shared-dns-zone-or-gitops-overlay-retained"
		case Unknown:
			item.Reason = "ownership-unknown-retained"
		}
		plan.Items = append(plan.Items, item)
	}
	sort.Slice(plan.Items, func(i, j int) bool {
		left, right := stageOrder(plan.Items[i].Stage), stageOrder(plan.Items[j].Stage)
		if left != right {
			return left < right
		}
		return key(InspectedResource{Resource: plan.Items[i].Resource}) < key(InspectedResource{Resource: plan.Items[j].Resource})
	})
	sort.Strings(plan.IrreversibleConsequences)
	plan.Digest = fullPlanDigest(plan)
	plan.TypedConfirmation = "FULL DECOMMISSION " + plan.ProfileID + " " + plan.Digest
	return plan, nil
}

func removableFull(kind string) bool {
	switch kind {
	case "gitops-overlay", "dns-zone":
		return false
	default:
		return true
	}
}

func removalStage(kind string, persistent bool) Stage {
	if persistent || strings.Contains(kind, "volume") || strings.Contains(kind, "storage") || strings.Contains(kind, "database") || kind == "data" {
		return StorageStage
	}
	if kind == "dns-record" {
		return DNSStage
	}
	if strings.Contains(kind, "network") || strings.Contains(kind, "firewall") || strings.Contains(kind, "ip") || strings.Contains(kind, "load-balancer") {
		return NetworkingStage
	}
	return ComputeStage
}

func stageOrder(stage Stage) int {
	switch stage {
	case ComputeStage:
		return 1
	case StorageStage:
		return 2
	case NetworkingStage:
		return 3
	case DNSStage:
		return 4
	default:
		return 5
	}
}

func fullConsequence(stage Stage, persistent bool) string {
	if stage == StorageStage || persistent {
		return "Persistent data is permanently deleted and can only be recovered from the reviewed Recovery Points or Recovery Bundle."
	}
	switch stage {
	case ComputeStage:
		return "Cluster workloads stop permanently."
	case NetworkingStage:
		return "Cluster networking and reserved provider connectivity are removed."
	case DNSStage:
		return "Profile-owned DNS records are removed and service names stop resolving."
	default:
		return "Resource is permanently deleted."
	}
}

// ValidateFullResume permits completed deletions to be absent but proves every
// remaining approved deletion is still the same profile-owned identity. Fresh
// discoveries are never added to the deletion set; ambiguity stops a resume.
func ValidateFullResume(plan FullPlan, inspection FullInspection, completed int) error {
	if err := inspection.Finalize(); err != nil || inspection.ProfileID != plan.ProfileID {
		return ErrChanged
	}
	approved := map[string]FullItem{}
	for _, item := range plan.Items {
		approved[key(InspectedResource{Resource: item.Resource})] = item
	}
	current := map[string]InspectedResource{}
	for _, resource := range inspection.Resources {
		// An unknown resource already visible in the plan remains retained. A
		// newly discovered unknown resource stops the resume for review.
		if resource.Ownership == Unknown && (approved[key(resource)].Ownership != Unknown) {
			return ErrChanged
		}
		current[key(resource)] = resource
	}
	removed := 0
	for _, item := range plan.Items {
		resource, found := current[key(InspectedResource{Resource: item.Resource})]
		if item.Action == Remove {
			if removed >= completed && (!found || resource.Ownership != ProfileOwned) {
				return ErrChanged
			}
			removed++
			continue
		}
		if !found || resource.Ownership != item.Ownership {
			return ErrChanged
		}
	}
	return nil
}

func fullInspectionDigest(inspection FullInspection) string {
	copy := inspection
	copy.Digest = ""
	encoded, _ := json.Marshal(copy)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
func fullPlanDigest(plan FullPlan) string {
	copy := plan
	copy.Digest = ""
	copy.TypedConfirmation = ""
	encoded, _ := json.Marshal(copy)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
