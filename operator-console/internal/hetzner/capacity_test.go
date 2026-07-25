package hetzner

import (
	"errors"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/capability"
)

func testCatalog() PriceCatalog {
	return PriceCatalog{
		Offerings: []ServerOffering{
			{Name: "cx23", VCPU: 2, MemoryGB: 4, DiskGB: 40, Architecture: "x86", MonthlyEUR: 4.51, AvailableLocations: []string{"nbg1", "fsn1", "hel1"}},
			{Name: "cx33", VCPU: 4, MemoryGB: 8, DiskGB: 80, Architecture: "x86", MonthlyEUR: 8.49, AvailableLocations: []string{"nbg1", "fsn1"}},
			{Name: "cx43", VCPU: 8, MemoryGB: 16, DiskGB: 160, Architecture: "x86", MonthlyEUR: 16.40, AvailableLocations: []string{"nbg1", "fsn1"}},
			{Name: "cx53", VCPU: 16, MemoryGB: 32, DiskGB: 320, Architecture: "x86", MonthlyEUR: 31.90, AvailableLocations: []string{"nbg1"}},
		},
		VolumeMonthlyEURPerGB: 0.044,
		PrimaryIPMonthlyEUR:   0.60,
		Locations:             []string{"nbg1", "fsn1", "hel1"},
		ObservedAt:            time.Date(2026, 7, 25, 9, 0, 0, 0, time.UTC),
	}
}

func assessment(t *testing.T, mode capability.SelectionMode) capability.Assessment {
	t.Helper()
	assessed, err := capability.DefaultCatalog().Assess(capability.Selection{Mode: mode, DeploymentMode: capability.Hetzner})
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	return assessed
}

func TestRequirementsIncludeNodeOverheadAndHeadroom(t *testing.T) {
	requirement := Requirements(capability.Assessment{Resources: capability.Resources{MemoryMi: 4096, StorageGi: 100}})
	// 4096 Mi + 25% = 5120 Mi, plus 2048 Mi node overhead = 7168 Mi -> 7 GB.
	if requirement.MemoryGB != 7 {
		t.Fatalf("memory requirement %d GB", requirement.MemoryGB)
	}
	// 100 Gi + 20% = 120, plus 20 GB overhead = 140 GB.
	if requirement.VolumeGB != 140 {
		t.Fatalf("volume requirement %d GB", requirement.VolumeGB)
	}
	if requirement.WorkloadMemoryMi != 4096 || requirement.WorkloadStorageGi != 100 {
		t.Fatal("the raw capability sums must stay visible")
	}
	if tiny := Requirements(capability.Assessment{}); tiny.VolumeGB < MinVolumeGB {
		t.Fatalf("volume requirement %d below provider minimum", tiny.VolumeGB)
	}
}

func TestPresetsDeriveFromSelectedCapabilities(t *testing.T) {
	minimal, full := assessment(t, capability.Minimal), assessment(t, capability.Full)
	catalog := testCatalog()
	minimalPresets, minimalRequirement, err := Presets(minimal, catalog, "nbg1")
	if err != nil {
		t.Fatalf("presets: %v", err)
	}
	fullPresets, fullRequirement, err := Presets(full, catalog, "nbg1")
	if err != nil {
		t.Fatalf("presets: %v", err)
	}
	if fullRequirement.MemoryGB <= minimalRequirement.MemoryGB || fullRequirement.VolumeGB <= minimalRequirement.VolumeGB {
		t.Fatal("a larger selection must require more capacity")
	}
	if len(minimalPresets) != 3 {
		t.Fatalf("expected three presets, got %d", len(minimalPresets))
	}
	for index, preset := range minimalPresets {
		if preset.Tier != []PresetTier{PresetSmall, PresetRecommended, PresetHigh}[index] {
			t.Fatalf("preset %d is %s", index, preset.Tier)
		}
	}
	recommended := presetFor(t, fullPresets, PresetRecommended)
	if !recommended.Fits || recommended.MemoryGB < fullRequirement.MemoryGB {
		t.Fatalf("recommended preset %+v does not fit the selection", recommended)
	}
	high := presetFor(t, fullPresets, PresetHigh)
	if high.MemoryGB < recommended.MemoryGB || high.VolumeGB <= recommended.VolumeGB {
		t.Fatal("the high preset must offer more room than the recommendation")
	}
}

func TestPresetsExplainMisfitAndUnavailability(t *testing.T) {
	presets, _, err := Presets(assessment(t, capability.Minimal), testCatalog(), "hel1")
	if err != nil {
		t.Fatalf("presets: %v", err)
	}
	// The minimal selection recommends cx43, which this catalog does not offer
	// in hel1 — the operator must see that before choosing it.
	recommended := presetFor(t, presets, PresetRecommended)
	if recommended.ServerType != "cx43" || recommended.Available || recommended.ReasonKey != "preset-unavailable-in-location" {
		t.Fatalf("recommended preset %+v", recommended)
	}
	small := presetFor(t, presets, PresetSmall)
	if small.Fits || small.ReasonKey != "preset-below-selected-capabilities" {
		t.Fatalf("small preset %+v must be labelled as undersized", small)
	}
}

func presetFor(t *testing.T, presets []Preset, tier PresetTier) Preset {
	t.Helper()
	for _, preset := range presets {
		if preset.Tier == tier {
			return preset
		}
	}
	t.Fatalf("no %s preset", tier)
	return Preset{}
}

func TestEstimateCostCoversRecurringResources(t *testing.T) {
	catalog := testCatalog()
	offering, _ := catalog.Offering("cx43")
	estimate := EstimateCost(offering, 200, catalog)
	if estimate.ServerMonthlyEUR != 16.40 || estimate.VolumeMonthlyEUR != 8.80 || estimate.PrimaryIPMonthlyEUR != 0.60 {
		t.Fatalf("estimate %+v", estimate)
	}
	if estimate.TotalMonthlyEUR != 25.80 || estimate.Currency != "EUR" || !estimate.ObservedAt.Equal(catalog.ObservedAt) {
		t.Fatalf("total %+v", estimate)
	}
	for _, wanted := range []string{NoteVolumeGrowsOnly, NoteVolumeBillable, NotePrimaryIPBillable} {
		if !containsFold(estimate.NoteKeys, wanted) {
			t.Fatalf("missing cost note %q in %v", wanted, estimate.NoteKeys)
		}
	}
}

func TestResolveChoice(t *testing.T) {
	catalog, selected := testCatalog(), assessment(t, capability.Minimal)
	t.Run("preset resolves to its offering", func(t *testing.T) {
		resolution, err := ResolveChoice(selected, catalog, Choice{Tier: PresetRecommended, Location: "nbg1"})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if resolution.Choice.ServerType == "" || resolution.VolumeGB < resolution.Requirement.VolumeGB || !resolution.Fits || !resolution.Available {
			t.Fatalf("resolution %+v", resolution)
		}
		if resolution.Cost.TotalMonthlyEUR <= 0 {
			t.Fatal("a resolved choice must carry a cost")
		}
	})
	t.Run("advanced override is honoured with a warning", func(t *testing.T) {
		resolution, err := ResolveChoice(selected, catalog, Choice{Tier: PresetAdvanced, Location: "hel1", ServerType: "cx23", VolumeGB: 40})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if resolution.Fits {
			t.Fatal("an undersized override must not report as fitting")
		}
		if len(resolution.WarningKeys) == 0 {
			t.Fatal("an undersized override must warn")
		}
	})
	for _, testCase := range []struct {
		name   string
		choice Choice
	}{
		{name: "unknown location", choice: Choice{Tier: PresetRecommended, Location: "ash"}},
		{name: "unknown server type", choice: Choice{Tier: PresetAdvanced, Location: "nbg1", ServerType: "cx99", VolumeGB: 100}},
		{name: "volume below provider minimum", choice: Choice{Tier: PresetAdvanced, Location: "nbg1", ServerType: "cx43", VolumeGB: 5}},
		{name: "volume above provider maximum", choice: Choice{Tier: PresetAdvanced, Location: "nbg1", ServerType: "cx43", VolumeGB: MaxVolumeGB + 10}},
		{name: "unknown preset", choice: Choice{Tier: "enormous", Location: "nbg1"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := ResolveChoice(selected, catalog, testCase.choice); !errors.Is(err, ErrInvalidChoice) {
				t.Fatalf("resolve returned %v, want ErrInvalidChoice", err)
			}
		})
	}
	t.Run("incomplete catalog is refused", func(t *testing.T) {
		if _, err := ResolveChoice(selected, PriceCatalog{}, Choice{Tier: PresetRecommended, Location: "nbg1"}); !errors.Is(err, ErrInvalidChoice) {
			t.Fatalf("resolve returned %v, want ErrInvalidChoice", err)
		}
	})
}
