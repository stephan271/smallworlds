package hetzner

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/capability"
)

// Sizing constants. The capability catalog states what the workloads request;
// these cover what the node itself costs on top (k3s, the OS, container images)
// and the headroom that keeps a node from being sized exactly at its limit.
const (
	// NodeOverheadMemoryMi is k3s plus the operating system.
	NodeOverheadMemoryMi = 2048
	// MemoryHeadroomPercent keeps burst and upgrade room above the workloads.
	MemoryHeadroomPercent = 25
	// DataOverheadGB is the non-capability data footprint on the volume.
	DataOverheadGB = 20
	// DataHeadroomPercent keeps a volume from filling at steady state. A volume
	// can be grown but never shrunk, so over-sizing is a lasting cost while
	// under-sizing is an outage — the headroom is deliberately modest.
	DataHeadroomPercent = 20
	// MinVolumeGB and MaxVolumeGB are the provider's volume bounds.
	MinVolumeGB = 10
	MaxVolumeGB = 10240
)

// ServerOffering is one server type as the provider currently offers it. Price
// and availability are observed, never compiled in — a stale price is a
// financial surprise, which is the thing this is meant to prevent.
type ServerOffering struct {
	Name         string  `json:"name"`
	VCPU         int     `json:"vcpu"`
	MemoryGB     int     `json:"memoryGb"`
	DiskGB       int     `json:"diskGb"`
	Architecture string  `json:"architecture"`
	MonthlyEUR   float64 `json:"monthlyEur"`
	// AvailableLocations are the locations where the type can currently be
	// created. An empty list means available nowhere right now.
	AvailableLocations []string `json:"availableLocations"`
}

// AvailableIn reports whether the type can currently be created in a location.
func (offering ServerOffering) AvailableIn(location string) bool {
	for _, candidate := range offering.AvailableLocations {
		if strings.EqualFold(candidate, location) {
			return true
		}
	}
	return false
}

// PriceCatalog is the observed provider catalog a plan is costed against.
type PriceCatalog struct {
	Offerings             []ServerOffering `json:"offerings"`
	VolumeMonthlyEURPerGB float64          `json:"volumeMonthlyEurPerGb"`
	PrimaryIPMonthlyEUR   float64          `json:"primaryIpMonthlyEur"`
	Locations             []string         `json:"locations"`
	ObservedAt            time.Time        `json:"observedAt"`
}

// Validate enforces a usable catalog: without offerings and prices there is no
// honest plan to show.
func (catalog PriceCatalog) Validate() error {
	if len(catalog.Offerings) == 0 || catalog.VolumeMonthlyEURPerGB <= 0 || catalog.PrimaryIPMonthlyEUR <= 0 || catalog.ObservedAt.IsZero() {
		return fmt.Errorf("%w: incomplete provider catalog", ErrInvalidChoice)
	}
	for _, offering := range catalog.Offerings {
		if offering.Name == "" || offering.MemoryGB <= 0 || offering.VCPU <= 0 || offering.MonthlyEUR <= 0 {
			return fmt.Errorf("%w: offering %q", ErrInvalidChoice, offering.Name)
		}
	}
	return nil
}

// Offering returns a named offering.
func (catalog PriceCatalog) Offering(name string) (ServerOffering, bool) {
	for _, offering := range catalog.Offerings {
		if strings.EqualFold(offering.Name, name) {
			return offering, true
		}
	}
	return ServerOffering{}, false
}

// sortedOfferings returns the offerings ordered by capacity then price, which
// is the order presets step through.
func (catalog PriceCatalog) sortedOfferings() []ServerOffering {
	offerings := append([]ServerOffering(nil), catalog.Offerings...)
	sort.SliceStable(offerings, func(left, right int) bool {
		if offerings[left].MemoryGB != offerings[right].MemoryGB {
			return offerings[left].MemoryGB < offerings[right].MemoryGB
		}
		return offerings[left].MonthlyEUR < offerings[right].MonthlyEUR
	})
	return offerings
}

// Requirement is what the selected Cluster Capabilities need from a node,
// including node overhead and headroom.
type Requirement struct {
	MemoryGB int `json:"memoryGb"`
	VolumeGB int `json:"volumeGb"`
	// WorkloadMemoryMi and WorkloadStorageGi are the raw capability sums, kept
	// so the console can explain where the requirement came from.
	WorkloadMemoryMi  int `json:"workloadMemoryMi"`
	WorkloadStorageGi int `json:"workloadStorageGi"`
}

// Requirements derives the node requirement from a Capability Assessment.
func Requirements(assessment capability.Assessment) Requirement {
	memoryMi := assessment.Resources.MemoryMi*(100+MemoryHeadroomPercent)/100 + NodeOverheadMemoryMi
	volumeGB := roundUpTo(assessment.Resources.StorageGi*(100+DataHeadroomPercent)/100+DataOverheadGB, 10)
	if volumeGB < MinVolumeGB {
		volumeGB = MinVolumeGB
	}
	return Requirement{
		MemoryGB:          int(math.Ceil(float64(memoryMi) / 1024)),
		VolumeGB:          volumeGB,
		WorkloadMemoryMi:  assessment.Resources.MemoryMi,
		WorkloadStorageGi: assessment.Resources.StorageGi,
	}
}

// PresetTier names the three capacity presets.
type PresetTier string

const (
	// PresetSmall is the cheapest step below the recommendation. It may not fit
	// the selection, and says so rather than being hidden.
	PresetSmall PresetTier = "small"
	// PresetRecommended is the smallest offering that satisfies the requirement.
	PresetRecommended PresetTier = "recommended"
	// PresetHigh is one step above, with room to add capabilities later.
	PresetHigh PresetTier = "high"
	// PresetAdvanced is an explicit operator override of type and volume.
	PresetAdvanced PresetTier = "advanced"
)

// Preset is one capacity option with its fit, availability, and cost.
type Preset struct {
	Tier       PresetTier `json:"tier"`
	ServerType string     `json:"serverType"`
	VCPU       int        `json:"vcpu"`
	MemoryGB   int        `json:"memoryGb"`
	VolumeGB   int        `json:"volumeGb"`
	// Fits is false when the type cannot hold the selected capabilities.
	Fits bool `json:"fits"`
	// Available is false when the provider cannot currently create the type in
	// the chosen location.
	Available bool         `json:"available"`
	ReasonKey string       `json:"reasonKey,omitempty"`
	Cost      CostEstimate `json:"cost"`
}

// Presets derives the Small, Recommended, and High options for a selection in a
// location. Every option is costed and availability-checked against the
// observed catalog, so an undersized or unavailable choice is visible before it
// is made rather than after provisioning fails.
func Presets(assessment capability.Assessment, catalog PriceCatalog, location string) ([]Preset, Requirement, error) {
	requirement := Requirements(assessment)
	if err := catalog.Validate(); err != nil {
		return nil, requirement, err
	}
	offerings := catalog.sortedOfferings()
	recommendedIndex := len(offerings) - 1
	for index, offering := range offerings {
		if offering.MemoryGB >= requirement.MemoryGB {
			recommendedIndex = index
			break
		}
	}
	tiers := []struct {
		tier   PresetTier
		index  int
		volume int
	}{
		{PresetSmall, recommendedIndex - 1, requirement.VolumeGB},
		{PresetRecommended, recommendedIndex, requirement.VolumeGB},
		{PresetHigh, recommendedIndex + 1, roundUpTo(requirement.VolumeGB*2, 10)},
	}
	presets := make([]Preset, 0, len(tiers))
	for _, entry := range tiers {
		index := entry.index
		if index < 0 {
			index = 0
		}
		if index > len(offerings)-1 {
			index = len(offerings) - 1
		}
		offering := offerings[index]
		volume := entry.volume
		if volume > MaxVolumeGB {
			volume = MaxVolumeGB
		}
		preset := Preset{
			Tier:       entry.tier,
			ServerType: offering.Name,
			VCPU:       offering.VCPU,
			MemoryGB:   offering.MemoryGB,
			VolumeGB:   volume,
			Fits:       offering.MemoryGB >= requirement.MemoryGB && volume >= requirement.VolumeGB,
			Available:  offering.AvailableIn(location),
			Cost:       EstimateCost(offering, volume, catalog),
		}
		switch {
		case !preset.Fits:
			preset.ReasonKey = "preset-below-selected-capabilities"
		case !preset.Available:
			preset.ReasonKey = "preset-unavailable-in-location"
		}
		presets = append(presets, preset)
	}
	return presets, requirement, nil
}

// Choice is the infrastructure the operator selected. A tier other than
// advanced derives the type and volume from the preset; advanced takes the
// explicit overrides, which are still validated against requirement, provider
// availability, and the volume bounds.
type Choice struct {
	Tier       PresetTier `json:"tier"`
	Location   string     `json:"location"`
	ServerType string     `json:"serverType,omitempty"`
	VolumeGB   int        `json:"volumeGb,omitempty"`
}

// Resolution is a validated choice with its offering, requirement fit, and cost.
type Resolution struct {
	Choice      Choice         `json:"choice"`
	Offering    ServerOffering `json:"offering"`
	Requirement Requirement    `json:"requirement"`
	VolumeGB    int            `json:"volumeGb"`
	Fits        bool           `json:"fits"`
	Available   bool           `json:"available"`
	Cost        CostEstimate   `json:"cost"`
	// WarningKeys are non-blocking consequences the operator should see (an
	// undersized advanced override, for instance).
	WarningKeys []string `json:"warningKeys,omitempty"`
}

// ResolveChoice validates a choice against the observed catalog. It refuses
// unknown types, unavailable locations, and out-of-bounds volumes; an
// undersized advanced override is allowed but warned about, since an
// experienced operator may knowingly run tight.
func ResolveChoice(assessment capability.Assessment, catalog PriceCatalog, choice Choice) (Resolution, error) {
	if err := catalog.Validate(); err != nil {
		return Resolution{}, err
	}
	if strings.TrimSpace(choice.Location) == "" || !containsFold(catalog.Locations, choice.Location) {
		return Resolution{}, fmt.Errorf("%w: unknown location %q", ErrInvalidChoice, choice.Location)
	}
	presets, requirement, err := Presets(assessment, catalog, choice.Location)
	if err != nil {
		return Resolution{}, err
	}
	resolved := choice
	if choice.Tier != PresetAdvanced {
		matched := false
		for _, preset := range presets {
			if preset.Tier == choice.Tier {
				resolved.ServerType, resolved.VolumeGB, matched = preset.ServerType, preset.VolumeGB, true
				break
			}
		}
		if !matched {
			return Resolution{}, fmt.Errorf("%w: unknown preset %q", ErrInvalidChoice, choice.Tier)
		}
	}
	offering, found := catalog.Offering(resolved.ServerType)
	if !found {
		return Resolution{}, fmt.Errorf("%w: unknown server type %q", ErrInvalidChoice, resolved.ServerType)
	}
	resolved.ServerType = offering.Name
	if resolved.VolumeGB < MinVolumeGB || resolved.VolumeGB > MaxVolumeGB {
		return Resolution{}, fmt.Errorf("%w: volume size %d GB is outside the provider bounds", ErrInvalidChoice, resolved.VolumeGB)
	}
	resolution := Resolution{
		Choice:      resolved,
		Offering:    offering,
		Requirement: requirement,
		VolumeGB:    resolved.VolumeGB,
		Fits:        offering.MemoryGB >= requirement.MemoryGB && resolved.VolumeGB >= requirement.VolumeGB,
		Available:   offering.AvailableIn(resolved.Location),
		Cost:        EstimateCost(offering, resolved.VolumeGB, catalog),
	}
	if offering.MemoryGB < requirement.MemoryGB {
		resolution.WarningKeys = append(resolution.WarningKeys, "server-type-below-selected-capabilities")
	}
	if resolved.VolumeGB < requirement.VolumeGB {
		resolution.WarningKeys = append(resolution.WarningKeys, "volume-below-selected-capabilities")
	}
	return resolution, nil
}

// The recurring-cost notes every estimate carries. They state the two ways
// Hetzner keeps billing after an operator believes they have stopped paying,
// and the one-way nature of volume growth.
const (
	NoteVolumeGrowsOnly     = "volume-can-grow-but-never-shrink"
	NoteVolumeBillable      = "volume-remains-billable-until-deleted"
	NotePrimaryIPBillable   = "primary-ip-remains-billable-while-reserved"
	NoteSnapshotsBillable   = "snapshots-and-backups-billed-separately"
	NotePricesExcludeVAT    = "prices-exclude-vat-and-traffic-overage"
	NoteEstimateFromCatalog = "estimate-from-observed-provider-catalog"
)

// CostEstimate is the recurring monthly cost of a choice, in the provider's
// billing currency.
type CostEstimate struct {
	Currency            string    `json:"currency"`
	ServerMonthlyEUR    float64   `json:"serverMonthlyEur"`
	VolumeMonthlyEUR    float64   `json:"volumeMonthlyEur"`
	PrimaryIPMonthlyEUR float64   `json:"primaryIpMonthlyEur"`
	TotalMonthlyEUR     float64   `json:"totalMonthlyEur"`
	ObservedAt          time.Time `json:"observedAt"`
	NoteKeys            []string  `json:"noteKeys"`
}

// EstimateCost prices a server type plus its volume and Primary IP from the
// observed catalog, and attaches the notes about resources that stay billable.
func EstimateCost(offering ServerOffering, volumeGB int, catalog PriceCatalog) CostEstimate {
	estimate := CostEstimate{
		Currency:            "EUR",
		ServerMonthlyEUR:    cents(offering.MonthlyEUR),
		VolumeMonthlyEUR:    cents(float64(volumeGB) * catalog.VolumeMonthlyEURPerGB),
		PrimaryIPMonthlyEUR: cents(catalog.PrimaryIPMonthlyEUR),
		ObservedAt:          catalog.ObservedAt,
		NoteKeys:            []string{NoteVolumeGrowsOnly, NoteVolumeBillable, NotePrimaryIPBillable, NoteSnapshotsBillable, NotePricesExcludeVAT, NoteEstimateFromCatalog},
	}
	estimate.TotalMonthlyEUR = cents(estimate.ServerMonthlyEUR + estimate.VolumeMonthlyEUR + estimate.PrimaryIPMonthlyEUR)
	return estimate
}

func cents(value float64) float64 { return math.Round(value*100) / 100 }

func roundUpTo(value, step int) int {
	if value <= 0 {
		return 0
	}
	return ((value + step - 1) / step) * step
}

func containsFold(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(value, wanted) {
			return true
		}
	}
	return false
}
