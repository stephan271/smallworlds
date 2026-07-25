package hetznerprovision

import (
	"strings"

	"github.com/stephan271/smallworlds/operator-console/internal/hetzner"
)

// The named checks a revalidation performs. Each corresponds to one fact the
// operator's approval rested on, and each is reported separately so a refusal
// says which fact moved rather than only that something did.
const (
	// InventoryCheck confirms the project still holds exactly the resources,
	// under the same stable provider identities and ownership, that the plan was
	// derived from.
	InventoryCheck = "provider-inventory"
	// PublicAddressCheck confirms the Primary IP the plan expects is still the
	// one the project holds.
	PublicAddressCheck = "public-address"
	// NameserverCheck confirms the domain is still delegated to the zone's
	// nameservers.
	NameserverCheck = "nameserver-delegation"
	// ReleaseCheck confirms the selected SmallWorlds release has not moved.
	ReleaseCheck = "selected-release"
	// OverlayCheck confirms the GitOps Overlay still points at the approved
	// repository and commit.
	OverlayCheck = "overlay-commit"
	// ToolchainCheck confirms the pinned OpenTofu and provider artifacts are the
	// approved release and verified.
	ToolchainCheck = "pinned-toolchain"
	// StateCheck confirms nobody else has written this profile's OpenTofu state
	// since approval.
	StateCheck = "opentofu-state"
	// ProfileCheck confirms the Cluster Profile has not been edited since the
	// plan was approved against it.
	ProfileCheck = "profile-revision"
)

// checkOrder fixes the order results are reported in, so an operator reading two
// revalidations sees the same sequence.
var checkOrder = []string{ProfileCheck, InventoryCheck, PublicAddressCheck, NameserverCheck, ReleaseCheck, OverlayCheck, ToolchainCheck, StateCheck}

// Observed is the freshly gathered evidence a revalidation compares the binding
// against. Every field is an observation made immediately before execution —
// none of it is read back from what was stored at approval.
//
// Inventory and Delegation are the results of re-running the same read-only
// inspection the plan was built from. ToolchainReady and HasState come from the
// launcher's own boundaries.
type Observed struct {
	ProfileRevision      int64
	Inventory            hetzner.Inventory
	Delegation           hetzner.Delegation
	Release              string
	OverlayRepositoryURL string
	OverlayCommit        string
	OverlayRelease       string
	ToolchainRelease     string
	ToolchainReady       bool
	StateDigest          string
	HasState             bool
}

// Check is one named revalidation result. ReasonKey is stable and translatable,
// and states the outcome rather than the raw values — a refusal must be
// explainable without echoing project detail into a log.
type Check struct {
	Name      string `json:"name"`
	Passed    bool   `json:"passed"`
	ReasonKey string `json:"reasonKey"`
}

// Preflight is the aggregate verdict. Execution may proceed only when every
// check passed.
type Preflight struct {
	Checks    []Check `json:"checks"`
	Permitted bool    `json:"permitted"`
	// FirstFailure names the earliest failed check, so a run's checkpoint can
	// record precisely why it stopped.
	FirstFailure string `json:"firstFailure,omitempty"`
}

// Revalidate re-checks every fact the approval rested on against freshly
// observed evidence. It is pure: it performs no provider call and mutates
// nothing, so the same evidence always produces the same verdict.
func Revalidate(binding Binding, observed Observed) Preflight {
	if err := binding.Validate(); err != nil {
		return refuseAll("binding-invalid")
	}
	results := map[string]Check{
		ProfileCheck:       compare(ProfileCheck, observed.ProfileRevision == binding.ProfileRevision, "profile-unchanged", "profile-changed-since-approval"),
		InventoryCheck:     inventoryCheck(binding, observed),
		PublicAddressCheck: publicAddressCheck(binding, observed),
		NameserverCheck:    nameserverCheck(binding, observed),
		ReleaseCheck:       compare(ReleaseCheck, observed.Release == binding.Release, "release-unchanged", "release-changed-since-approval"),
		OverlayCheck:       overlayCheck(binding, observed),
		ToolchainCheck:     toolchainCheck(binding, observed),
		StateCheck:         stateCheck(binding, observed),
	}
	preflight := Preflight{Checks: make([]Check, 0, len(checkOrder)), Permitted: true}
	for _, name := range checkOrder {
		check := results[name]
		if !check.Passed {
			preflight.Permitted = false
			if preflight.FirstFailure == "" {
				preflight.FirstFailure = check.Name
			}
		}
		preflight.Checks = append(preflight.Checks, check)
	}
	return preflight
}

// inventoryCheck refuses an incomplete inspection outright: a listing that did
// not finish cannot prove a resource is absent, and "absent" is exactly what
// authorises creating one.
func inventoryCheck(binding Binding, observed Observed) Check {
	if observed.Inventory.Digest == "" || observed.Inventory.Incomplete {
		return Check{Name: InventoryCheck, ReasonKey: "inventory-reinspection-incomplete"}
	}
	if observed.Inventory.ProjectID != "" && observed.Inventory.ProjectID != binding.ProjectID {
		return Check{Name: InventoryCheck, ReasonKey: "inventory-addresses-different-project"}
	}
	if observed.Inventory.Digest != binding.InventoryDigest {
		return Check{Name: InventoryCheck, ReasonKey: "inventory-changed-since-approval"}
	}
	// The digest covers ownership, so an adoptable resource that is still
	// adoptable is still the one the operator decided about; what must be
	// re-checked is that every resource the plan takes over was in fact chosen.
	for _, finding := range observed.Inventory.Findings {
		if finding.Ownership == hetzner.OwnershipAdoptable && finding.Match != nil && !binding.Adopted(finding.Match.ProviderID) {
			return Check{Name: InventoryCheck, ReasonKey: "inventory-adoption-not-approved"}
		}
	}
	return Check{Name: InventoryCheck, Passed: true, ReasonKey: "inventory-unchanged"}
}

// publicAddressCheck compares the observed Primary IP with the approved one. An
// address that appeared where the plan expected none means something else
// created infrastructure in this project, which is why it refuses rather than
// treating the extra address as harmless.
func publicAddressCheck(binding Binding, observed Observed) Check {
	current := PublicAddressOf(observed.Inventory)
	if current == binding.PublicAddress {
		if current == "" {
			return Check{Name: PublicAddressCheck, Passed: true, ReasonKey: "public-address-will-be-created"}
		}
		return Check{Name: PublicAddressCheck, Passed: true, ReasonKey: "public-address-unchanged"}
	}
	if binding.PublicAddress == "" {
		return Check{Name: PublicAddressCheck, ReasonKey: "public-address-appeared-since-approval"}
	}
	return Check{Name: PublicAddressCheck, ReasonKey: "public-address-changed-since-approval"}
}

// nameserverCheck requires a freshly satisfied delegation for the same domain.
// An inconclusive lookup fails: unknown is not evidence of readiness, and a
// public installation whose names do not resolve cannot obtain certificates.
func nameserverCheck(binding Binding, observed Observed) Check {
	if !strings.EqualFold(observed.Delegation.Domain, binding.Naming.Domain) {
		return Check{Name: NameserverCheck, ReasonKey: "delegation-checked-for-different-domain"}
	}
	if !observed.Delegation.Satisfied() {
		return Check{Name: NameserverCheck, ReasonKey: "delegation-no-longer-satisfied"}
	}
	if observed.Delegation.Status != binding.Delegation {
		return Check{Name: NameserverCheck, ReasonKey: "delegation-changed-since-approval"}
	}
	return Check{Name: NameserverCheck, Passed: true, ReasonKey: "delegation-unchanged"}
}

func overlayCheck(binding Binding, observed Observed) Check {
	switch {
	case observed.OverlayRepositoryURL != binding.OverlayRepositoryURL:
		return Check{Name: OverlayCheck, ReasonKey: "overlay-repository-changed-since-approval"}
	case observed.OverlayCommit != binding.OverlayCommit:
		return Check{Name: OverlayCheck, ReasonKey: "overlay-commit-changed-since-approval"}
	case observed.OverlayRelease != binding.OverlayRelease:
		return Check{Name: OverlayCheck, ReasonKey: "overlay-release-changed-since-approval"}
	}
	return Check{Name: OverlayCheck, Passed: true, ReasonKey: "overlay-unchanged"}
}

// toolchainCheck requires the approved pinned release *and* verified artifacts.
// Reconciling with a different OpenTofu or provider than the one reviewed would
// break the reproducibility the pinning exists for.
func toolchainCheck(binding Binding, observed Observed) Check {
	if observed.ToolchainRelease != binding.ToolchainRelease {
		return Check{Name: ToolchainCheck, ReasonKey: "toolchain-release-changed-since-approval"}
	}
	if !observed.ToolchainReady {
		return Check{Name: ToolchainCheck, ReasonKey: "toolchain-not-verified"}
	}
	return Check{Name: ToolchainCheck, Passed: true, ReasonKey: "toolchain-verified"}
}

// stateCheck detects another actor having reconciled this profile since
// approval. A plan approved against one state applied on top of another is how
// a resource gets created twice or destroyed unseen.
func stateCheck(binding Binding, observed Observed) Check {
	switch {
	case binding.StateDigest == "" && !observed.HasState:
		return Check{Name: StateCheck, Passed: true, ReasonKey: "state-absent-as-approved"}
	case binding.StateDigest == "" && observed.HasState:
		return Check{Name: StateCheck, ReasonKey: "state-appeared-since-approval"}
	case !observed.HasState:
		return Check{Name: StateCheck, ReasonKey: "state-disappeared-since-approval"}
	case observed.StateDigest != binding.StateDigest:
		return Check{Name: StateCheck, ReasonKey: "state-changed-since-approval"}
	}
	return Check{Name: StateCheck, Passed: true, ReasonKey: "state-unchanged"}
}

func compare(name string, passed bool, passedKey, failedKey string) Check {
	if passed {
		return Check{Name: name, Passed: true, ReasonKey: passedKey}
	}
	return Check{Name: name, ReasonKey: failedKey}
}

// refuseAll reports every check as failed under one reason, used when the
// binding itself cannot be trusted to say what to compare.
func refuseAll(reasonKey string) Preflight {
	preflight := Preflight{Checks: make([]Check, 0, len(checkOrder)), FirstFailure: checkOrder[0]}
	for _, name := range checkOrder {
		preflight.Checks = append(preflight.Checks, Check{Name: name, ReasonKey: reasonKey})
	}
	return preflight
}
