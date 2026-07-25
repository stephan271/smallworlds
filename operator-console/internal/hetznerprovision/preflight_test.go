package hetznerprovision_test

import (
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/hetzner"
	"github.com/stephan271/smallworlds/operator-console/internal/hetznerprovision"
)

// currentInventory is the inspection the approved plan was derived from, re-run
// immediately before execution and observed unchanged.
func currentInventory() hetzner.Inventory {
	return hetzner.Inventory{
		ProjectID: "project-9",
		Findings: []hetzner.Finding{
			{Expectation: hetzner.Expectation{Kind: hetzner.KindPrimaryIP, Name: "smallworlds-ip"}, Ownership: hetzner.OwnershipAbsent},
			{Expectation: hetzner.Expectation{Kind: hetzner.KindVolume, Name: "smallworlds-data"}, Ownership: hetzner.OwnershipAdoptable, Match: &hetzner.Resource{ProviderID: "vol-77"}},
		},
		Digest: inventoryDigest,
	}
}

func healthyObservation() hetznerprovision.Observed {
	return hetznerprovision.Observed{
		ProfileRevision:      4,
		Inventory:            currentInventory(),
		Delegation:           hetzner.Delegation{Domain: "example.org", Status: hetzner.DelegationConfirmed},
		Release:              "v1.2.27",
		OverlayRepositoryURL: "https://github.com/example/my-community-config.git",
		OverlayCommit:        overlayCommit,
		OverlayRelease:       "v1.2.27",
		ToolchainRelease:     "tofu-1.10.6+hcloud-1.54.0",
		ToolchainReady:       true,
		StateDigest:          "",
		HasState:             false,
	}
}

func TestRevalidatePermitsAnUnchangedProject(t *testing.T) {
	preflight := hetznerprovision.Revalidate(mustBind(t), healthyObservation())
	if !preflight.Permitted {
		t.Fatalf("preflight refused an unchanged project: %+v", preflight.Checks)
	}
	if preflight.FirstFailure != "" {
		t.Fatalf("firstFailure = %q, want none", preflight.FirstFailure)
	}
	// Every fact the approval rested on must be reported, so an operator can see
	// what was re-checked and not only that it passed.
	want := []string{
		hetznerprovision.ProfileCheck, hetznerprovision.InventoryCheck, hetznerprovision.PublicAddressCheck,
		hetznerprovision.NameserverCheck, hetznerprovision.ReleaseCheck, hetznerprovision.OverlayCheck,
		hetznerprovision.ToolchainCheck, hetznerprovision.StateCheck,
	}
	if len(preflight.Checks) != len(want) {
		t.Fatalf("got %d checks, want %d", len(preflight.Checks), len(want))
	}
	for index, name := range want {
		if preflight.Checks[index].Name != name {
			t.Fatalf("check %d = %q, want %q", index, preflight.Checks[index].Name, name)
		}
		if preflight.Checks[index].ReasonKey == "" {
			t.Fatalf("check %q has no reason key", name)
		}
	}
}

// Each case moves exactly one approved fact and asserts execution is refused for
// that reason. A single moved fact is precisely the situation the gate exists
// for: the operator approved paid infrastructure against evidence that no longer
// holds.
func TestRevalidateRefusesEachChangedFact(t *testing.T) {
	cases := map[string]struct {
		mutate func(*hetznerprovision.Observed)
		check  string
		reason string
	}{
		"profile edited": {
			func(o *hetznerprovision.Observed) { o.ProfileRevision = 5 },
			hetznerprovision.ProfileCheck, "profile-changed-since-approval",
		},
		"a resource appeared": {
			func(o *hetznerprovision.Observed) { o.Inventory.Digest = "deadbeef" },
			hetznerprovision.InventoryCheck, "inventory-changed-since-approval",
		},
		"re-inspection incomplete": {
			func(o *hetznerprovision.Observed) { o.Inventory.Incomplete = true },
			hetznerprovision.InventoryCheck, "inventory-reinspection-incomplete",
		},
		"token now addresses another project": {
			func(o *hetznerprovision.Observed) { o.Inventory.ProjectID = "project-other" },
			hetznerprovision.InventoryCheck, "inventory-addresses-different-project",
		},
		"public address appeared": {
			func(o *hetznerprovision.Observed) {
				o.Inventory.Findings[0].Match = &hetzner.Resource{ProviderID: "ip-1", Detail: "203.0.113.9"}
			},
			hetznerprovision.PublicAddressCheck, "public-address-appeared-since-approval",
		},
		"delegation withdrawn": {
			func(o *hetznerprovision.Observed) { o.Delegation.Status = hetzner.DelegationMissing },
			hetznerprovision.NameserverCheck, "delegation-no-longer-satisfied",
		},
		"delegation lookup inconclusive": {
			func(o *hetznerprovision.Observed) { o.Delegation.Status = hetzner.DelegationUnknown },
			hetznerprovision.NameserverCheck, "delegation-no-longer-satisfied",
		},
		"delegation checked for another domain": {
			func(o *hetznerprovision.Observed) { o.Delegation.Domain = "elsewhere.test" },
			hetznerprovision.NameserverCheck, "delegation-checked-for-different-domain",
		},
		"release moved": {
			func(o *hetznerprovision.Observed) { o.Release = "v1.2.28" },
			hetznerprovision.ReleaseCheck, "release-changed-since-approval",
		},
		"overlay commit moved": {
			func(o *hetznerprovision.Observed) { o.OverlayCommit = "5555555555555555555555555555555555555555" },
			hetznerprovision.OverlayCheck, "overlay-commit-changed-since-approval",
		},
		"overlay repository moved": {
			func(o *hetznerprovision.Observed) { o.OverlayRepositoryURL = "https://example.com/other.git" },
			hetznerprovision.OverlayCheck, "overlay-repository-changed-since-approval",
		},
		"toolchain version moved": {
			func(o *hetznerprovision.Observed) { o.ToolchainRelease = "tofu-1.11.0+hcloud-1.54.0" },
			hetznerprovision.ToolchainCheck, "toolchain-release-changed-since-approval",
		},
		"toolchain no longer verified": {
			func(o *hetznerprovision.Observed) { o.ToolchainReady = false },
			hetznerprovision.ToolchainCheck, "toolchain-not-verified",
		},
		"another actor wrote state": {
			func(o *hetznerprovision.Observed) { o.HasState, o.StateDigest = true, stateDigest },
			hetznerprovision.StateCheck, "state-appeared-since-approval",
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			observed := healthyObservation()
			testCase.mutate(&observed)
			preflight := hetznerprovision.Revalidate(mustBind(t), observed)
			if preflight.Permitted {
				t.Fatal("preflight permitted execution after an approved fact changed")
			}
			if preflight.FirstFailure != testCase.check {
				t.Fatalf("firstFailure = %q, want %q", preflight.FirstFailure, testCase.check)
			}
			for _, check := range preflight.Checks {
				if check.Name == testCase.check {
					if check.Passed {
						t.Fatalf("check %q passed", check.Name)
					}
					if check.ReasonKey != testCase.reason {
						t.Fatalf("reasonKey = %q, want %q", check.ReasonKey, testCase.reason)
					}
					return
				}
			}
			t.Fatalf("check %q not reported", testCase.check)
		})
	}
}

// An adoptable resource the operator never chose must block execution even when
// the inventory is otherwise identical — the whole point of adoption being an
// explicit decision is that it cannot be inherited by a later run.
func TestRevalidateRefusesAnUnapprovedAdoption(t *testing.T) {
	binding := mustBind(t)
	binding.Adoptions = nil
	observed := healthyObservation()
	preflight := hetznerprovision.Revalidate(binding, observed)
	if preflight.Permitted {
		t.Fatal("preflight permitted taking over a resource that was never approved for adoption")
	}
	if preflight.FirstFailure != hetznerprovision.InventoryCheck {
		t.Fatalf("firstFailure = %q, want the inventory check", preflight.FirstFailure)
	}
}

// State that existed at approval and has since been written by someone else, or
// vanished, both mean this plan is no longer the one to apply.
func TestRevalidateComparesExistingState(t *testing.T) {
	binding := mustBind(t)
	binding.StateDigest = stateDigest

	unchanged := healthyObservation()
	unchanged.HasState, unchanged.StateDigest = true, stateDigest
	if preflight := hetznerprovision.Revalidate(binding, unchanged); !preflight.Permitted {
		t.Fatalf("preflight refused unchanged state: %+v", preflight.Checks)
	}

	rewritten := healthyObservation()
	rewritten.HasState, rewritten.StateDigest = true, "9999999999999999999999999999999999999999999999999999999999999999"
	if preflight := hetznerprovision.Revalidate(binding, rewritten); preflight.Permitted || preflight.FirstFailure != hetznerprovision.StateCheck {
		t.Fatalf("preflight permitted applying onto state written by someone else: %+v", preflight)
	}

	vanished := healthyObservation()
	if preflight := hetznerprovision.Revalidate(binding, vanished); preflight.Permitted || preflight.FirstFailure != hetznerprovision.StateCheck {
		t.Fatalf("preflight permitted applying with the approved state gone: %+v", preflight)
	}
}

// A binding that cannot be trusted to say what to compare must refuse
// everything, rather than silently comparing zero values that happen to match.
func TestRevalidateRefusesAnInvalidBinding(t *testing.T) {
	preflight := hetznerprovision.Revalidate(hetznerprovision.Binding{}, healthyObservation())
	if preflight.Permitted {
		t.Fatal("an invalid binding must never permit execution")
	}
	for _, check := range preflight.Checks {
		if check.Passed || check.ReasonKey != "binding-invalid" {
			t.Fatalf("check %+v, want every check refused as binding-invalid", check)
		}
	}
}
