package hetznerprovision_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/hetzner"
	"github.com/stephan271/smallworlds/operator-console/internal/hetznerprovision"
)

const (
	planDigest      = "1111111111111111111111111111111111111111111111111111111111111111"
	inventoryDigest = "2222222222222222222222222222222222222222222222222222222222222222"
	stateDigest     = "3333333333333333333333333333333333333333333333333333333333333333"
	overlayCommit   = "4444444444444444444444444444444444444444"
)

func approvedPlan() hetzner.ChangePlan {
	return hetzner.ChangePlan{
		ProfileID:  "profile-1",
		ProjectID:  "project-9",
		Domain:     "example.org",
		Delegation: hetzner.Delegation{Domain: "example.org", Status: hetzner.DelegationConfirmed},
		Items: []hetzner.PlanItem{
			{Kind: hetzner.KindPrimaryIP, Name: "smallworlds-ip", Action: hetzner.ActionCreate},
			{Kind: hetzner.KindVolume, Name: "smallworlds-data", Action: hetzner.ActionAdopt, ProviderID: "vol-77"},
			{Kind: hetzner.KindDNSZone, Name: "example.org", Action: hetzner.ActionReuseShared, ProviderID: "zone-1"},
		},
		InventoryDigest: inventoryDigest,
		Digest:          planDigest,
	}
}

func environment() hetznerprovision.Environment {
	return hetznerprovision.Environment{
		PlanID:               "plan-1",
		ProfileRevision:      4,
		Release:              "v1.2.27",
		OverlayRepositoryURL: "https://github.com/example/my-community-config.git",
		OverlayCommit:        overlayCommit,
		OverlayRelease:       "v1.2.27",
		ToolchainRelease:     "tofu-1.10.6+hcloud-1.54.0",
		StateDigest:          "",
		CreatedAt:            time.Date(2026, 7, 25, 9, 0, 0, 0, time.UTC),
	}
}

func mustBind(t *testing.T) hetznerprovision.Binding {
	t.Helper()
	binding, err := hetznerprovision.BindPlan(approvedPlan(), environment())
	if err != nil {
		t.Fatalf("BindPlan: %v", err)
	}
	return binding
}

func TestBindPlanCarriesOnlyExplicitAdoptions(t *testing.T) {
	binding := mustBind(t)
	if len(binding.Adoptions) != 1 || binding.Adoptions[0] != "vol-77" {
		t.Fatalf("adoptions = %v, want only the adopted volume", binding.Adoptions)
	}
	if !binding.Adopted("vol-77") {
		t.Fatal("the adopted volume should be reported as adopted")
	}
	// A shared resource is reused, not taken over; treating it as an adoption
	// would let execution claim a project-wide resource nobody chose to give up.
	if binding.Adopted("zone-1") {
		t.Fatal("a reused shared resource must not count as an adoption")
	}
	if binding.PlanDigest != planDigest || binding.InventoryDigest != inventoryDigest {
		t.Fatal("the binding must carry the reviewed plan and inventory digests")
	}
}

func TestBindPlanRefusesBlockedPlan(t *testing.T) {
	plan := approvedPlan()
	plan.Blockers = []hetzner.Blocker{{Code: "ownership-conflict"}}
	if _, err := hetznerprovision.BindPlan(plan, environment()); !errors.Is(err, hetznerprovision.ErrInvalidBinding) {
		t.Fatalf("err = %v, want a blocked plan to be unbindable", err)
	}
}

func TestBindPlanRefusesUnsatisfiedDelegation(t *testing.T) {
	// An approvable plan can only carry a satisfied delegation, but the binding
	// re-checks it: a binding recording "unknown" would make the preflight
	// compare unknown to unknown and pass.
	plan := approvedPlan()
	plan.Delegation.Status = hetzner.DelegationUnknown
	if _, err := hetznerprovision.BindPlan(plan, environment()); !errors.Is(err, hetznerprovision.ErrInvalidBinding) {
		t.Fatalf("err = %v, want an unsatisfied delegation to be unbindable", err)
	}
}

func TestBindingValidationRejectsIncompleteEnvironment(t *testing.T) {
	for name, mutate := range map[string]func(*hetznerprovision.Environment){
		"missing plan id":        func(e *hetznerprovision.Environment) { e.PlanID = "" },
		"zero profile revision":  func(e *hetznerprovision.Environment) { e.ProfileRevision = 0 },
		"unpinned toolchain":     func(e *hetznerprovision.Environment) { e.ToolchainRelease = "tofu-latest" },
		"non-https overlay":      func(e *hetznerprovision.Environment) { e.OverlayRepositoryURL = "http://example.com/x.git" },
		"overlay with token":     func(e *hetznerprovision.Environment) { e.OverlayRepositoryURL = "https://t@example.com/x.git" },
		"short overlay commit":   func(e *hetznerprovision.Environment) { e.OverlayCommit = "abc123" },
		"release mismatch":       func(e *hetznerprovision.Environment) { e.OverlayRelease = "v1.2.26" },
		"non-semver release":     func(e *hetznerprovision.Environment) { e.Release = "main" },
		"malformed state digest": func(e *hetznerprovision.Environment) { e.StateDigest = "not-a-digest" },
	} {
		t.Run(name, func(t *testing.T) {
			env := environment()
			mutate(&env)
			if _, err := hetznerprovision.BindPlan(approvedPlan(), env); !errors.Is(err, hetznerprovision.ErrInvalidBinding) {
				t.Fatalf("err = %v, want ErrInvalidBinding", err)
			}
		})
	}
}

func TestBindingRoundTripsAndCarriesNoSecrets(t *testing.T) {
	binding := mustBind(t)
	encoded, err := binding.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, forbidden := range []string{"token", "secret", "password", "privateKey"} {
		if strings.Contains(strings.ToLower(encoded), strings.ToLower(forbidden)) {
			t.Fatalf("encoded binding mentions %q: %s", forbidden, encoded)
		}
	}
	parsed, err := hetznerprovision.ParseBinding(encoded)
	if err != nil {
		t.Fatalf("ParseBinding: %v", err)
	}
	if parsed.PlanDigestFor("ProvisionHetznerInfrastructure") != binding.PlanDigestFor("ProvisionHetznerInfrastructure") {
		t.Fatal("a round-tripped binding must produce the same plan digest")
	}
}

func TestPlanDigestBindsEveryApprovedFact(t *testing.T) {
	base := mustBind(t)
	intent := "ProvisionHetznerInfrastructure"
	for name, mutate := range map[string]func(*hetznerprovision.Binding){
		"overlay commit": func(b *hetznerprovision.Binding) { b.OverlayCommit = strings.Repeat("b", 40) },
		"release":        func(b *hetznerprovision.Binding) { b.Release = "v1.2.28" },
		"toolchain":      func(b *hetznerprovision.Binding) { b.ToolchainRelease = "tofu-1.10.7+hcloud-1.54.0" },
		"adoptions":      func(b *hetznerprovision.Binding) { b.Adoptions = nil },
		"plan digest":    func(b *hetznerprovision.Binding) { b.PlanDigest = strings.Repeat("c", 64) },
		"state digest":   func(b *hetznerprovision.Binding) { b.StateDigest = stateDigest },
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			if changed.PlanDigestFor(intent) == base.PlanDigestFor(intent) {
				t.Fatalf("changing the %s must change the approved plan digest", name)
			}
		})
	}
}

func TestPublicAddressOfReadsThePrimaryIP(t *testing.T) {
	inventory := hetzner.Inventory{Findings: []hetzner.Finding{
		{Expectation: hetzner.Expectation{Kind: hetzner.KindVolume, Name: "smallworlds-data"}, Match: &hetzner.Resource{Detail: "50 GB"}},
		{Expectation: hetzner.Expectation{Kind: hetzner.KindPrimaryIP, Name: "smallworlds-ip"}, Match: &hetzner.Resource{Detail: "203.0.113.9"}},
	}}
	if address := hetznerprovision.PublicAddressOf(inventory); address != "203.0.113.9" {
		t.Fatalf("address = %q, want the Primary IP", address)
	}
	if address := hetznerprovision.PublicAddressOf(hetzner.Inventory{}); address != "" {
		t.Fatalf("address = %q, want empty when no Primary IP exists", address)
	}
}
