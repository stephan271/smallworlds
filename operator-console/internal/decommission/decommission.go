// Package decommission builds the preserve-data retirement contract. It is
// intentionally provider-neutral: adapters inspect provider identities, labels,
// tags and cluster identity; this package classifies and freezes the only set an
// approved executor may act on.
package decommission

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

var (
	ErrInvalidInspection = errors.New("decommission: invalid inspection")
	ErrBlocked           = errors.New("decommission: unresolved ownership")
	ErrChanged           = errors.New("decommission: inspected resources changed")
)

type Ownership string

const (
	ProfileOwned Ownership = "profile-owned"
	Shared       Ownership = "shared"
	Retained     Ownership = "retained"
	Unknown      Ownership = "unknown"
)

type Action string

const (
	Remove Action = "remove"
	Retain Action = "retain"
)

// Resource uses a stable provider ID as its identity. Name is presentation
// only; planning never infers ownership from it.
type Resource struct {
	Kind            string            `json:"kind"`
	ProviderID      string            `json:"providerId"`
	Name            string            `json:"name"`
	State           string            `json:"state"`
	Tags            map[string]string `json:"tags,omitempty"`
	ClusterIdentity string            `json:"clusterIdentity,omitempty"`
	Persistent      bool              `json:"persistent"`
	Shared          bool              `json:"shared"`
	MonthlyEUR      float64           `json:"monthlyEur,omitempty"`
	Detail          string            `json:"detail,omitempty"`
}

type InspectedResource struct {
	Resource
	Ownership Ownership `json:"ownership"`
}

// Inspection is fresh, non-secret evidence collected by a provider/local-node
// adapter. Overlay and DNS zones are explicit resources so they cannot vanish
// from the review merely because they need no mutation.
type Inspection struct {
	ProfileID       string              `json:"profileId"`
	DeploymentMode  string              `json:"deploymentMode"`
	ProfileRevision int64               `json:"profileRevision"`
	ObservedAt      time.Time           `json:"observedAt"`
	Resources       []InspectedResource `json:"resources"`
	RetainedData    []string            `json:"retainedData"`
	RecoveryPath    string              `json:"recoveryPath"`
	Protection      []string            `json:"protectionEvidence"`
	Digest          string              `json:"digest"`
}

// Classify is the single ownership rule used by both Hetzner and local
// adapters. A matching profile tag and cluster identity prove ownership.
// Persistent data, the GitOps overlay, and DNS zones are retained by contract;
// anything else with incomplete identity is unknown and therefore retained.
func Classify(profileID string, resources []Resource) ([]InspectedResource, error) {
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
		case resource.Persistent || resource.Kind == "gitops-overlay" || resource.Kind == "dns-zone":
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

func (inspection *Inspection) Finalize() error {
	if inspection.ProfileID == "" || inspection.ProfileRevision < 1 || inspection.ObservedAt.IsZero() || inspection.RecoveryPath == "" {
		return ErrInvalidInspection
	}
	for _, resource := range inspection.Resources {
		if resource.Kind == "" || resource.ProviderID == "" || resource.Ownership == "" {
			return ErrInvalidInspection
		}
	}
	inspection.Digest = inspectionDigest(*inspection)
	return nil
}

type Item struct {
	Resource
	Ownership Ownership `json:"ownership"`
	Action    Action    `json:"action"`
	Reason    string    `json:"reason"`
}

type Cost struct {
	ProviderID string  `json:"providerId"`
	Kind       string  `json:"kind"`
	MonthlyEUR float64 `json:"monthlyEur"`
}

type Plan struct {
	ProfileID        string   `json:"profileId"`
	DeploymentMode   string   `json:"deploymentMode"`
	InspectionDigest string   `json:"inspectionDigest"`
	Items            []Item   `json:"items"`
	RetainedData     []string `json:"retainedData"`
	ContinuingCosts  []Cost   `json:"continuingCosts"`
	ExpectedDowntime string   `json:"expectedDowntime"`
	RecoveryPath     string   `json:"recoveryPath"`
	Blockers         []string `json:"blockers,omitempty"`
	Digest           string   `json:"digest"`
}

// BuildPlan is conservative by construction: only profile-owned compute,
// workload, firewall and proven profile-owned DNS-record resources are removal
// candidates. Persistent volumes/data, IPs, DNS zones and overlays remain.
func BuildPlan(inspection Inspection) (Plan, error) {
	if err := inspection.Finalize(); err != nil {
		return Plan{}, err
	}
	plan := Plan{ProfileID: inspection.ProfileID, DeploymentMode: inspection.DeploymentMode, InspectionDigest: inspection.Digest, RetainedData: append([]string(nil), inspection.RetainedData...), RecoveryPath: inspection.RecoveryPath, ExpectedDowntime: "SmallWorlds workloads become unavailable while retained data remains recoverable."}
	for _, resource := range inspection.Resources {
		item := Item{Resource: resource.Resource, Ownership: resource.Ownership, Action: Retain, Reason: "retained-by-preserve-data-policy"}
		switch resource.Ownership {
		case Unknown:
			item.Reason = "ownership-unknown-retained"
			plan.Blockers = append(plan.Blockers, resource.ProviderID+": ownership unknown")
		case Shared:
			item.Reason = "shared-resource-retained"
		case Retained:
			item.Reason = "persistent-or-shared-contract-retained"
		case ProfileOwned:
			if removable(resource.Kind, resource.Persistent) {
				item.Action, item.Reason = Remove, "proven-profile-owned-compute-or-workload"
			} else {
				item.Reason = "profile-owned-but-retained-by-preserve-data-policy"
			}
		default:
			plan.Blockers = append(plan.Blockers, resource.ProviderID+": ownership unclassified")
		}
		if item.Action == Retain && resource.MonthlyEUR > 0 {
			plan.ContinuingCosts = append(plan.ContinuingCosts, Cost{ProviderID: resource.ProviderID, Kind: resource.Kind, MonthlyEUR: resource.MonthlyEUR})
		}
		plan.Items = append(plan.Items, item)
	}
	sort.Strings(plan.RetainedData)
	sort.Strings(plan.Blockers)
	sort.Slice(plan.ContinuingCosts, func(i, j int) bool { return plan.ContinuingCosts[i].ProviderID < plan.ContinuingCosts[j].ProviderID })
	plan.Digest = planDigest(plan)
	return plan, nil
}

func removable(kind string, persistent bool) bool {
	if persistent {
		return false
	}
	switch kind {
	case "server", "firewall", "k3s", "smallworlds-workloads", "dns-record":
		return true
	default:
		return false
	}
}

func (plan Plan) Approvable() bool { return len(plan.Blockers) == 0 }

// ValidateResume proves that a reinspection cannot enlarge an already-approved
// deletion set. Completed removals may be absent; every remaining removal and
// every retained/unknown resource must still have the same identity/class.
func ValidateResume(plan Plan, inspection Inspection, completed int) error {
	if err := inspection.Finalize(); err != nil {
		return err
	}
	if inspection.ProfileID != plan.ProfileID {
		return ErrChanged
	}
	current := map[string]InspectedResource{}
	for _, resource := range inspection.Resources {
		// A newly discovered ambiguous resource is retained and stops a resume.
		// It could share provider scope with a planned action, so continuing until
		// an Operator has reviewed a new plan would be unsafe.
		if resource.Ownership == Unknown {
			return ErrChanged
		}
		current[key(resource)] = resource
	}
	removals := 0
	for _, item := range plan.Items {
		resource, found := current[key(InspectedResource{Resource: item.Resource})]
		if item.Action == Remove {
			if removals >= completed && (!found || resource.Ownership != ProfileOwned) {
				return ErrChanged
			}
			removals++
			continue
		}
		if !found || resource.Ownership != item.Ownership {
			return ErrChanged
		}
	}
	return nil
}

func key(resource InspectedResource) string { return resource.Kind + "/" + resource.ProviderID }

func inspectionDigest(inspection Inspection) string {
	copy := inspection
	copy.Digest = ""
	encoded, _ := json.Marshal(copy)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
func planDigest(plan Plan) string {
	copy := plan
	copy.Digest = ""
	encoded, _ := json.Marshal(copy)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
