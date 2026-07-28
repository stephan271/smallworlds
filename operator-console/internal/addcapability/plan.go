package addcapability

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/stephan271/smallworlds/operator-console/internal/assessment"
	"github.com/stephan271/smallworlds/operator-console/internal/capability"
)

var (
	// ErrNotOffered is returned when the target is not an addable Community
	// Application in the current state (unknown, required, a platform service,
	// already present, or unsupported in the deployment mode).
	ErrNotOffered = errors.New("addcapability: capability is not offered for addition")
	// ErrInvalidOverlayTarget is returned when the overlay release or repository
	// URL is not a pinned tag / credential-free HTTPS URL.
	ErrInvalidOverlayTarget = errors.New("addcapability: invalid overlay target")
)

var pinnedRelease = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)

// OverlayTarget names where the proposal lands: the pinned SmallWorlds release
// the added tenant is referenced at, and the operator's credential-free HTTPS
// overlay repository the ArgoCD Application is repointed to. Both mirror the
// values issue 04/05 rendered into the overlay, so an add-capability proposal is
// consistent with the overlay it amends.
// OverlayTarget is the overlay a proposal is written against. Domain and
// EnvironmentExtension are part of it because the hostnames belong to the
// operator: a proposal that omitted them added applications on the project's
// own addresses, which is not a cosmetic difference but an application nobody
// can reach.
type OverlayTarget struct {
	Release              string `json:"release"`
	RepositoryURL        string `json:"repositoryUrl"`
	Domain               string `json:"domain"`
	EnvironmentExtension string `json:"environmentExtension,omitempty"`
}

func (target OverlayTarget) validate() error {
	if !pinnedRelease.MatchString(target.Release) {
		return fmt.Errorf("%w: release must be an exact pinned tag", ErrInvalidOverlayTarget)
	}
	repository, err := url.Parse(target.RepositoryURL)
	if err != nil || repository.Scheme != "https" || repository.Host == "" || repository.User != nil {
		return fmt.Errorf("%w: repository URL must be credential-free HTTPS", ErrInvalidOverlayTarget)
	}
	// Rejected here rather than left to the renderer: an empty domain would
	// otherwise surface as a rendering error at the moment of proposing, when
	// the operator has already reviewed a plan.
	if strings.TrimSpace(target.Domain) == "" {
		return fmt.Errorf("%w: the overlay's domain is required", ErrInvalidOverlayTarget)
	}
	return nil
}

// Capacity is the live cluster capacity a plan compares the added footprint
// against. Available headroom is allocatable minus already-used; a plan reports
// whether the estimated need fits without ever refusing on the operator's behalf.
type Capacity struct {
	AllocatableMemoryMi  int `json:"allocatableMemoryMi"`
	AllocatableStorageGi int `json:"allocatableStorageGi"`
	UsedMemoryMi         int `json:"usedMemoryMi"`
	UsedStorageGi        int `json:"usedStorageGi"`
}

// ResourceComparison states the estimated additional need against current
// headroom, per resource, so the operator sees whether the cluster has room
// before proposing.
type ResourceComparison struct {
	RequiredMemoryMi   int  `json:"requiredMemoryMi"`
	RequiredStorageGi  int  `json:"requiredStorageGi"`
	AvailableMemoryMi  int  `json:"availableMemoryMi"`
	AvailableStorageGi int  `json:"availableStorageGi"`
	FitsMemory         bool `json:"fitsMemory"`
	FitsStorage        bool `json:"fitsStorage"`
}

// Fits reports whether both memory and storage headroom cover the added need.
func (comparison ResourceComparison) Fits() bool {
	return comparison.FitsMemory && comparison.FitsStorage
}

// Plan is the reviewable Change Plan for adding one Community Application: which
// capabilities it adds (the target plus any disabled dependencies pulled in),
// which dependencies were already present, the resource-vs-capacity comparison,
// the disclosed exposure / persistent-data / protection implications, and the
// exact catalog-derived Git diff the proposal would commit. Files is the
// path→content set the proposal writes; it equals what GitDiff renders.
type Plan struct {
	Target              string             `json:"target"`
	AddedCapabilities   []string           `json:"addedCapabilities"`
	PresentDependencies []string           `json:"presentDependencies"`
	Resources           ResourceComparison `json:"resources"`
	Exposure            []string           `json:"exposure"`
	Protection          []string           `json:"protection"`
	PersistentData      []string           `json:"persistentData"`
	Files               map[string]string  `json:"-"`
	GitDiff             string             `json:"gitDiff"`
}

// BuildPlan assembles the Change Plan for adding targetID. It fails if the target
// is not an addable Community Application in the current state, or the overlay
// target is invalid. Disabled dependencies are pulled into the same plan (and
// listed in AddedCapabilities); already-present ones are recorded separately so
// the operator sees exactly what the proposal introduces.
func BuildPlan(catalog capability.Catalog, targetID string, states map[string]assessment.CapabilityState, capacity Capacity, overlay OverlayTarget, mode capability.DeploymentMode) (Plan, error) {
	if err := catalog.Validate(); err != nil {
		return Plan{}, err
	}
	if err := overlay.validate(); err != nil {
		return Plan{}, err
	}
	target, found := catalog.Entry(targetID)
	if !found || target.Category != capability.CommunityApplication || target.Required || states[targetID] != assessment.StateDisabled || !supportsMode(target, mode) {
		return Plan{}, ErrNotOffered
	}

	// Walk the dependency graph depth-first so dependencies precede dependents in
	// the added order. A dependency that is already present is recorded but not
	// added; a disabled one is pulled into this proposal.
	added := make([]string, 0)
	presentDeps := make([]string, 0)
	seen := map[string]bool{}
	var visit func(id string) error
	visit = func(id string) error {
		if seen[id] {
			return nil
		}
		seen[id] = true
		entry, ok := catalog.Entry(id)
		if !ok {
			return fmt.Errorf("%w: unknown dependency %q", ErrNotOffered, id)
		}
		if id != targetID && present(states[id]) {
			presentDeps = append(presentDeps, id)
			return nil
		}
		if id != targetID && !supportsMode(entry, mode) {
			return fmt.Errorf("%w: dependency %q unsupported in deployment mode", ErrNotOffered, id)
		}
		for _, dependency := range entry.Dependencies {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		added = append(added, id)
		return nil
	}
	if err := visit(targetID); err != nil {
		return Plan{}, err
	}
	sort.Strings(presentDeps)

	plan := Plan{
		Target:              targetID,
		AddedCapabilities:   added,
		PresentDependencies: presentDeps,
		Resources:           compareResources(catalog, added, capacity),
	}
	plan.Exposure, plan.Protection, plan.PersistentData = implications(catalog, added)

	// Rendered by the same code that establishes an overlay, against the
	// selection as it stands and as it would stand. Anything this package
	// rendered on its own would be a second opinion about a file format only
	// one of them can be right about.
	current := enabledCommunity(catalog, states)
	proposed := append(append([]string(nil), current...), added...)
	change, err := catalog.RenderChange(
		overlay.input(mode, current),
		overlay.input(mode, proposed),
	)
	if err != nil {
		return Plan{}, err
	}
	plan.Files = change.Files
	plan.GitDiff = change.Diff
	return plan, nil
}

// input builds the overlay description this target and selection amount to.
func (target OverlayTarget) input(mode capability.DeploymentMode, communityIDs []string) capability.OverlayInput {
	return capability.OverlayInput{
		Selection:            capability.Selection{Mode: capability.Custom, DeploymentMode: mode, CommunityIDs: communityIDs},
		Release:              target.Release,
		RepositoryURL:        target.RepositoryURL,
		Domain:               target.Domain,
		EnvironmentExtension: target.EnvironmentExtension,
	}
}

// enabledCommunity is the overlay's current community selection, read from the
// observed state rather than remembered separately, so a plan can never be
// built against a selection the cluster no longer has.
func enabledCommunity(catalog capability.Catalog, states map[string]assessment.CapabilityState) []string {
	selected := make([]string, 0, len(states))
	for _, entry := range catalog.Capabilities {
		if entry.Category == capability.CommunityApplication && present(states[entry.ID]) {
			selected = append(selected, entry.ID)
		}
	}
	sort.Strings(selected)
	return selected
}

func compareResources(catalog capability.Catalog, added []string, capacity Capacity) ResourceComparison {
	comparison := ResourceComparison{
		AvailableMemoryMi:  capacity.AllocatableMemoryMi - capacity.UsedMemoryMi,
		AvailableStorageGi: capacity.AllocatableStorageGi - capacity.UsedStorageGi,
	}
	for _, id := range added {
		entry, _ := catalog.Entry(id)
		comparison.RequiredMemoryMi += entry.Resources.MemoryMi
		comparison.RequiredStorageGi += entry.Resources.StorageGi
	}
	comparison.FitsMemory = comparison.AvailableMemoryMi >= comparison.RequiredMemoryMi
	comparison.FitsStorage = comparison.AvailableStorageGi >= comparison.RequiredStorageGi
	return comparison
}

// implications gathers the distinct exposure and protection classes across the
// added capabilities, and the capabilities that hold persistent data — the three
// disclosures an operator weighs before proposing.
func implications(catalog capability.Catalog, added []string) (exposure, protection, persistentData []string) {
	exposures, protections := map[string]bool{}, map[string]bool{}
	for _, id := range added {
		entry, _ := catalog.Entry(id)
		exposures[entry.Exposure] = true
		protections[entry.Protection] = true
		if entry.Resources.StorageGi > 0 {
			persistentData = append(persistentData, id)
		}
	}
	for value := range exposures {
		exposure = append(exposure, value)
	}
	for value := range protections {
		protection = append(protection, value)
	}
	sort.Strings(exposure)
	sort.Strings(protection)
	sort.Strings(persistentData)
	return exposure, protection, persistentData
}
