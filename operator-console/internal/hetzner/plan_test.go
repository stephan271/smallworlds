package hetzner

import (
	"errors"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/capability"
)

func planFixture(t *testing.T, resources []Resource, adoptions []string, observedNameservers []string) ChangePlan {
	t.Helper()
	inventory, err := Classify(testNaming(), resources)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	resolution, err := ResolveChoice(assessment(t, capability.Minimal), testCatalog(), Choice{Tier: PresetRecommended, Location: "nbg1"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	plan, err := BuildPlan(PlanInput{
		Naming:     testNaming(),
		ProjectID:  "project-a",
		Inventory:  inventory,
		Resolution: resolution,
		Delegation: CheckDelegation("example.org", observedNameservers, capability.Hetzner),
		Adoptions:  adoptions,
		CreatedAt:  time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	return plan
}

func itemFor(t *testing.T, plan ChangePlan, kind ResourceKind, name string) PlanItem {
	t.Helper()
	for _, item := range plan.Items {
		if item.Kind == kind && item.Name == name {
			return item
		}
	}
	t.Fatalf("no plan item for %s/%s", kind, name)
	return PlanItem{}
}

// projectPrerequisites are the two resources the whole project shares: the DNS
// zone and the admin SSH key. An installation reuses them and never owns them,
// so a project without them is not clean — it is incomplete.
func projectPrerequisites() []Resource {
	return []Resource{
		{Kind: KindDNSZone, ProviderID: "zone-1", Name: "example.org"},
		{Kind: KindSSHKey, ProviderID: "key-1", Name: SharedAdminSSHKeyName},
	}
}

func TestBuildPlanIsApprovableOnACleanProject(t *testing.T) {
	plan := planFixture(t, projectPrerequisites(), nil, HetznerNameservers)
	if !plan.Approvable() {
		t.Fatalf("blockers on a clean project: %+v", plan.Blockers)
	}
	if item := itemFor(t, plan, KindServer, "cc-pilot-node-01-dev"); item.Action != ActionCreate {
		t.Fatalf("server action %s", item.Action)
	}
	if item := itemFor(t, plan, KindDNSZone, "example.org"); item.Action != ActionReuseShared || item.ProviderID != "zone-1" {
		t.Fatalf("zone item %+v", item)
	}
	if plan.Cost.TotalMonthlyEUR <= 0 || plan.Digest == "" || plan.InventoryDigest == "" {
		t.Fatalf("plan is missing cost or identity: %+v", plan)
	}
}

func TestBuildPlanBlocksUntilOwnershipIsResolved(t *testing.T) {
	resources := []Resource{
		{Kind: KindFirewall, ProviderID: "fw-1", Name: "smallworlds-firewall-dev"},
		{Kind: KindVolume, ProviderID: "vol-1", Name: "smallworlds-data-dev", Labels: map[string]string{LabelProfile: "profile-2"}},
	}
	plan := planFixture(t, resources, nil, HetznerNameservers)
	if plan.Approvable() {
		t.Fatal("an unresolved adoption and conflict must block approval")
	}
	codes := map[string]bool{}
	for _, blocker := range plan.Blockers {
		codes[blocker.Code] = true
	}
	if !codes["adoption-decision-required"] || !codes["ownership-conflict"] {
		t.Fatalf("blockers %+v", plan.Blockers)
	}
	if item := itemFor(t, plan, KindFirewall, "smallworlds-firewall-dev"); item.Action != ActionBlocked {
		t.Fatalf("unadopted firewall action %s", item.Action)
	}

	// Explicit adoption of the firewall clears only that blocker; a resource
	// owned by another profile is never adoptable by choice.
	adopted := planFixture(t, resources, []string{"fw-1"}, HetznerNameservers)
	if item := itemFor(t, adopted, KindFirewall, "smallworlds-firewall-dev"); item.Action != ActionAdopt {
		t.Fatalf("adopted firewall action %s", item.Action)
	}
	if adopted.Approvable() {
		t.Fatal("the ownership conflict must still block approval")
	}
	if adopted.Digest == plan.Digest {
		t.Fatal("an adoption decision must change the plan digest")
	}
}

func TestBuildPlanBlocksWithoutNameserverDelegation(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		observed  []string
		blocked   bool
		wantState DelegationStatus
	}{
		{name: "delegated", observed: HetznerNameservers, wantState: DelegationConfirmed},
		{name: "elsewhere", observed: []string{"ns1.registrar.example"}, blocked: true, wantState: DelegationMissing},
		{name: "partially delegated", observed: []string{"ns1.registrar.example", "hydrogen.ns.hetzner.com"}, blocked: true, wantState: DelegationPartial},
		{name: "lookup inconclusive", observed: nil, blocked: true, wantState: DelegationUnknown},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			plan := planFixture(t, nil, nil, testCase.observed)
			if plan.Delegation.Status != testCase.wantState {
				t.Fatalf("delegation %s, want %s", plan.Delegation.Status, testCase.wantState)
			}
			blocked := false
			for _, blocker := range plan.Blockers {
				blocked = blocked || blocker.Code == "nameserver-delegation-required"
			}
			if blocked != testCase.blocked {
				t.Fatalf("delegation blocker=%v for %s", blocked, testCase.wantState)
			}
		})
	}
}

func TestDelegationNotRequiredForLANOnly(t *testing.T) {
	delegation := CheckDelegation("example.org", nil, capability.LocalLAN)
	if delegation.Status != DelegationNotRequired || !delegation.Satisfied() {
		t.Fatalf("LAN-only delegation %+v", delegation)
	}
	if !DelegationRequired(capability.LocalPublic) || !DelegationRequired(capability.Hetzner) {
		t.Fatal("public installations must require delegation")
	}
}

// The DNS zone and the admin SSH key belong to the project, not to this
// installation. Planning must not quietly create them: an installation that
// owned them would take them down with it when it was torn down, stranding
// every other profile in the project.
func TestBuildPlanBlocksOnMissingProjectPrerequisites(t *testing.T) {
	plan := planFixture(t, nil, nil, HetznerNameservers)
	missing := map[ResourceKind]bool{}
	for _, blocker := range plan.Blockers {
		if blocker.Code == "shared-prerequisite-missing" {
			missing[blocker.Kind] = true
		}
	}
	if !missing[KindDNSZone] || !missing[KindSSHKey] {
		t.Fatalf("blockers %+v, want both project prerequisites reported", plan.Blockers)
	}
	if plan.Approvable() {
		t.Fatal("a plan may not be approved while a project prerequisite is missing")
	}
	for _, kind := range []ResourceKind{KindDNSZone, KindSSHKey} {
		for _, item := range plan.Items {
			if item.Kind == kind && item.Action == ActionCreate {
				t.Fatalf("%s is planned for creation; it is shared and must be reused", kind)
			}
		}
	}

	// With both present, the same plan is approvable and reuses them.
	complete := planFixture(t, projectPrerequisites(), nil, HetznerNameservers)
	if !complete.Approvable() {
		t.Fatalf("blockers with the prerequisites present: %+v", complete.Blockers)
	}
	if item := itemFor(t, complete, KindSSHKey, SharedAdminSSHKeyName); item.Action != ActionReuseShared {
		t.Fatalf("admin key item %+v, want it reused", item)
	}
}

func TestBuildPlanBlocksUnavailableAndUndersizedInfrastructure(t *testing.T) {
	inventory, err := Classify(testNaming(), projectPrerequisites())
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	selected, catalog := assessment(t, capability.Minimal), testCatalog()
	unavailable, err := ResolveChoice(selected, catalog, Choice{Tier: PresetRecommended, Location: "hel1"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	plan, err := BuildPlan(PlanInput{Naming: testNaming(), Inventory: inventory, Resolution: unavailable, Delegation: CheckDelegation("example.org", HetznerNameservers, capability.Hetzner)})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	codes := map[string]bool{}
	for _, blocker := range plan.Blockers {
		codes[blocker.Code] = true
	}
	if !codes["server-type-unavailable"] {
		t.Fatalf("blockers %+v", plan.Blockers)
	}

	// An advanced override that knowingly runs tight warns instead of blocking.
	tight, err := ResolveChoice(selected, catalog, Choice{Tier: PresetAdvanced, Location: "nbg1", ServerType: "cx23", VolumeGB: 40})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	advanced, err := BuildPlan(PlanInput{Naming: testNaming(), Inventory: inventory, Resolution: tight, Delegation: CheckDelegation("example.org", HetznerNameservers, capability.Hetzner)})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if !advanced.Approvable() || len(advanced.WarningKeys) == 0 {
		t.Fatalf("advanced override plan %+v", advanced)
	}
}

func TestPlanStaysBoundToTheInspectionItCameFrom(t *testing.T) {
	resources := []Resource{{Kind: KindDNSZone, ProviderID: "zone-1", Name: "example.org"}}
	plan := planFixture(t, resources, nil, HetznerNameservers)
	current, err := Classify(testNaming(), resources)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if !plan.StillCurrent(current) {
		t.Fatal("an unchanged project must keep the plan current")
	}
	drifted, err := Classify(testNaming(), append(resources, Resource{Kind: KindServer, ProviderID: "srv-1", Name: "cc-pilot-node-01-dev"}))
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if plan.StillCurrent(drifted) {
		t.Fatal("a resource appearing in the project must make the plan stale")
	}
	if (ChangePlan{}).StillCurrent(current) {
		t.Fatal("a plan without an inventory digest is never current")
	}
}

func TestBuildPlanRefusesIncompleteInput(t *testing.T) {
	inventory, err := Classify(testNaming(), nil)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	resolution, err := ResolveChoice(assessment(t, capability.Minimal), testCatalog(), Choice{Tier: PresetRecommended, Location: "nbg1"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := BuildPlan(PlanInput{Naming: Naming{}, Inventory: inventory, Resolution: resolution}); !errors.Is(err, ErrInvalidNaming) {
		t.Fatalf("naming error was %v", err)
	}
	if _, err := BuildPlan(PlanInput{Naming: testNaming(), Inventory: Inventory{}, Resolution: resolution}); !errors.Is(err, ErrInvalidChoice) {
		t.Fatalf("inventory error was %v", err)
	}
	if _, err := BuildPlan(PlanInput{Naming: testNaming(), Inventory: inventory}); !errors.Is(err, ErrInvalidChoice) {
		t.Fatalf("resolution error was %v", err)
	}
}

func TestBuildPlanBlocksOnAnIncompleteInspection(t *testing.T) {
	inventory, err := Classify(testNaming(), nil)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	inventory.Incomplete = true
	resolution, err := ResolveChoice(assessment(t, capability.Minimal), testCatalog(), Choice{Tier: PresetRecommended, Location: "nbg1"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	plan, err := BuildPlan(PlanInput{Naming: testNaming(), Inventory: inventory, Resolution: resolution, Delegation: CheckDelegation("example.org", HetznerNameservers, capability.Hetzner)})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if plan.Approvable() {
		t.Fatal("a partial inspection must block approval")
	}
}
