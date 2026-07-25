package hetzner

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Action is what a plan will do to one expected resource. Every action except
// Create is a decision *not* to create something, which is why adoption is its
// own action rather than an invisible reuse.
type Action string

const (
	// ActionCreate provisions a resource that does not exist.
	ActionCreate Action = "create"
	// ActionAdopt takes over an existing resource the operator explicitly chose.
	ActionAdopt Action = "adopt"
	// ActionReuseShared uses a project-wide resource without owning it.
	ActionReuseShared Action = "reuse-shared"
	// ActionKeep leaves a resource this profile already owns as it is.
	ActionKeep Action = "keep"
	// ActionBlocked marks a resource that cannot be planned until the operator
	// resolves its ownership in the provider.
	ActionBlocked Action = "blocked"
)

// PlanItem is one resource's planned outcome, bound to the stable provider
// identity it was classified from.
type PlanItem struct {
	Kind       ResourceKind `json:"kind"`
	Name       string       `json:"name"`
	Action     Action       `json:"action"`
	Ownership  Ownership    `json:"ownership"`
	ProviderID string       `json:"providerId,omitempty"`
	Detail     string       `json:"detail,omitempty"`
	ReasonKey  string       `json:"reasonKey"`
}

// Blocker is a reason the plan may not be approved, naming the resource it
// concerns where one applies.
type Blocker struct {
	Code       string       `json:"code"`
	Kind       ResourceKind `json:"kind,omitempty"`
	Name       string       `json:"name,omitempty"`
	ProviderID string       `json:"providerId,omitempty"`
}

// PlanInput is everything a plan is derived from. Each part is observed
// separately — the token, the inventory, the catalog, the DNS lookup — and the
// plan is the only place they are combined.
type PlanInput struct {
	Naming     Naming     `json:"naming"`
	ProjectID  string     `json:"projectId"`
	Inventory  Inventory  `json:"inventory"`
	Resolution Resolution `json:"resolution"`
	Delegation Delegation `json:"delegation"`
	// Adoptions are the stable provider identities the operator explicitly
	// chose to adopt. An adoptable resource that is not listed here blocks the
	// plan: similarly named resources are never silently taken over.
	Adoptions []string  `json:"adoptions,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// ChangePlan is the immutable, cost-bearing result of planning. It mutates
// nothing: it states what would be created, adopted, reused, or kept, what it
// would cost per month, and what still blocks approval. Its digest binds the
// inventory it was derived from, so provisioning can refuse a stale plan.
type ChangePlan struct {
	ProfileID       string       `json:"profileId"`
	ProjectID       string       `json:"projectId"`
	Domain          string       `json:"domain"`
	EnvExt          string       `json:"envExt"`
	Choice          Choice       `json:"choice"`
	Requirement     Requirement  `json:"requirement"`
	Items           []PlanItem   `json:"items"`
	Cost            CostEstimate `json:"cost"`
	Delegation      Delegation   `json:"delegation"`
	Blockers        []Blocker    `json:"blockers,omitempty"`
	WarningKeys     []string     `json:"warningKeys,omitempty"`
	InventoryDigest string       `json:"inventoryDigest"`
	Digest          string       `json:"digest"`
	CreatedAt       time.Time    `json:"createdAt"`
}

// Approvable reports whether nothing blocks the operator approving this plan.
func (plan ChangePlan) Approvable() bool { return len(plan.Blockers) == 0 }

// StillCurrent reports whether the plan still describes the project as it is
// now. A plan built against a different inventory is stale and must be rebuilt
// before any provider resource is changed.
func (plan ChangePlan) StillCurrent(inventory Inventory) bool {
	return plan.InventoryDigest != "" && plan.InventoryDigest == inventory.Digest
}

// BuildPlan combines an inventory, a resolved infrastructure choice, and the
// delegation check into an immutable plan. It never contacts the provider and
// never changes anything: planning is a pure function of observed evidence.
func BuildPlan(input PlanInput) (ChangePlan, error) {
	if err := input.Naming.Validate(); err != nil {
		return ChangePlan{}, err
	}
	if input.Inventory.Digest == "" {
		return ChangePlan{}, fmt.Errorf("%w: plan requires a completed inspection", ErrInvalidChoice)
	}
	if input.Resolution.Offering.Name == "" {
		return ChangePlan{}, fmt.Errorf("%w: plan requires a resolved server type", ErrInvalidChoice)
	}
	adopted := map[string]bool{}
	for _, providerID := range input.Adoptions {
		adopted[providerID] = true
	}
	createdAt := input.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	plan := ChangePlan{
		ProfileID:       input.Naming.ProfileID,
		ProjectID:       input.ProjectID,
		Domain:          input.Naming.Domain,
		EnvExt:          input.Naming.EnvExt,
		Choice:          input.Resolution.Choice,
		Requirement:     input.Resolution.Requirement,
		Cost:            input.Resolution.Cost,
		Delegation:      input.Delegation,
		WarningKeys:     append([]string(nil), input.Resolution.WarningKeys...),
		InventoryDigest: input.Inventory.Digest,
		CreatedAt:       createdAt.UTC(),
		Items:           make([]PlanItem, 0, len(input.Inventory.Findings)),
	}
	for _, finding := range input.Inventory.Findings {
		item := PlanItem{Kind: finding.Expectation.Kind, Name: finding.Expectation.Name, Ownership: finding.Ownership, ReasonKey: finding.ReasonKey}
		if finding.Match != nil {
			item.ProviderID, item.Detail = finding.Match.ProviderID, finding.Match.Detail
		}
		switch finding.Ownership {
		case OwnershipAbsent:
			item.Action, item.ReasonKey = ActionCreate, "resource-will-be-created"
		case OwnershipProfileOwned:
			item.Action, item.ReasonKey = ActionKeep, "resource-already-owned-by-this-profile"
		case OwnershipShared:
			item.Action, item.ReasonKey = ActionReuseShared, "resource-reused-without-ownership"
		case OwnershipAdoptable:
			if adopted[finding.Match.ProviderID] {
				item.Action, item.ReasonKey = ActionAdopt, "resource-adopted-by-explicit-choice"
			} else {
				item.Action, item.ReasonKey = ActionBlocked, "adoption-decision-required"
				plan.Blockers = append(plan.Blockers, Blocker{Code: "adoption-decision-required", Kind: item.Kind, Name: item.Name, ProviderID: item.ProviderID})
			}
		case OwnershipConflicting:
			item.Action, item.ReasonKey = ActionBlocked, "resource-owned-by-another-profile"
			plan.Blockers = append(plan.Blockers, Blocker{Code: "ownership-conflict", Kind: item.Kind, Name: item.Name, ProviderID: item.ProviderID})
		case OwnershipUnknown:
			item.Action, item.ReasonKey = ActionBlocked, "similar-name-requires-manual-resolution"
			plan.Blockers = append(plan.Blockers, Blocker{Code: "similar-name-unresolved", Kind: item.Kind, Name: item.Name})
		default:
			item.Action, item.ReasonKey = ActionBlocked, "ownership-unclassified"
			plan.Blockers = append(plan.Blockers, Blocker{Code: "ownership-unclassified", Kind: item.Kind, Name: item.Name})
		}
		plan.Items = append(plan.Items, item)
	}
	if input.Inventory.Incomplete {
		plan.Blockers = append(plan.Blockers, Blocker{Code: "inspection-incomplete"})
	}
	if !input.Delegation.Satisfied() {
		plan.Blockers = append(plan.Blockers, Blocker{Code: "nameserver-delegation-required", Kind: KindDNSZone, Name: input.Naming.Domain})
	}
	if !input.Resolution.Available {
		plan.Blockers = append(plan.Blockers, Blocker{Code: "server-type-unavailable", Kind: KindServer, Name: input.Resolution.Offering.Name})
	}
	// An undersized preset is a mistake; an undersized advanced override is a
	// choice, so it only warns.
	if !input.Resolution.Fits && input.Resolution.Choice.Tier != PresetAdvanced {
		plan.Blockers = append(plan.Blockers, Blocker{Code: "capacity-below-selected-capabilities", Kind: KindServer, Name: input.Resolution.Offering.Name})
	}
	sort.SliceStable(plan.Blockers, func(left, right int) bool {
		return plan.Blockers[left].Code+plan.Blockers[left].Name < plan.Blockers[right].Code+plan.Blockers[right].Name
	})
	plan.Digest = planDigest(plan)
	return plan, nil
}

// planDigest binds every decision the operator approves: the profile and
// project, the naming, the resolved choice, each item with its stable provider
// identity, the total cost, the delegation verdict, and the inventory the plan
// was derived from.
func planDigest(plan ChangePlan) string {
	lines := []string{
		"profile=" + plan.ProfileID,
		"project=" + plan.ProjectID,
		"domain=" + plan.Domain + plan.EnvExt,
		fmt.Sprintf("choice=%s/%s/%s/%dGB", plan.Choice.Tier, plan.Choice.Location, plan.Choice.ServerType, plan.Choice.VolumeGB),
		fmt.Sprintf("cost=%.2f", plan.Cost.TotalMonthlyEUR),
		"delegation=" + string(plan.Delegation.Status),
		"inventory=" + plan.InventoryDigest,
	}
	for _, item := range plan.Items {
		lines = append(lines, fmt.Sprintf("item=%s|%s|%s|%s", item.Kind, item.Name, item.Action, item.ProviderID))
	}
	for _, blocker := range plan.Blockers {
		lines = append(lines, fmt.Sprintf("blocker=%s|%s|%s", blocker.Code, blocker.Kind, blocker.Name))
	}
	return digestOf(strings.Join(lines, "\n"))
}
