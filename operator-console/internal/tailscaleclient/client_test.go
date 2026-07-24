package tailscaleclient_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/tailscaleclient"
)

func pinnedCatalog() tailscaleclient.Catalog {
	return tailscaleclient.Catalog{Packages: []tailscaleclient.Package{{
		OS:      "linux",
		Arch:    "amd64",
		Version: "1.80.0",
		Format:  "tarball",
		URL:     "https://pkgs.tailscale.com/stable/tailscale_1.80.0_amd64.tgz",
		SHA256:  strings.Repeat("a", 64),
	}}}
}

func TestPlanOffersPinnedVerifiedAcquisitionWithElevation(t *testing.T) {
	platform := tailscaleclient.Platform{OS: "linux", Arch: "amd64"}
	offer, err := tailscaleclient.Plan(platform, tailscaleclient.Detection{Installed: false}, pinnedCatalog())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if offer.Detected {
		t.Fatal("client reported detected when absent")
	}
	if !offer.Acquisition.Available {
		t.Fatal("pinned acquisition not offered for a supported platform")
	}
	if offer.Acquisition.SHA256 == "" || !strings.HasPrefix(offer.Acquisition.URL, "https://pkgs.tailscale.com/") {
		t.Fatalf("acquisition is not a pinned trusted download: %#v", offer.Acquisition)
	}
	if !offer.Acquisition.ElevationRequired {
		t.Fatal("acquisition did not surface the explicit elevation requirement")
	}
	if !offer.ManualFallback || offer.Acquisition.ManualInstructionsURL != tailscaleclient.ManualInstructionsURL {
		t.Fatalf("manual fallback not retained: %#v", offer)
	}
}

func TestPlanFallsBackToManualWhenUnsupported(t *testing.T) {
	platform := tailscaleclient.Platform{OS: "plan9", Arch: "mips"}
	offer, err := tailscaleclient.Plan(platform, tailscaleclient.Detection{Installed: false}, pinnedCatalog())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if offer.Acquisition.Available || offer.Acquisition.ElevationRequired {
		t.Fatalf("unsupported platform offered automated acquisition: %#v", offer.Acquisition)
	}
	if !offer.ManualFallback || offer.Acquisition.ManualInstructionsURL == "" {
		t.Fatal("manual fallback missing for unsupported platform")
	}
}

func TestDefaultCatalogShipsNoUnverifiedPins(t *testing.T) {
	// Until release engineering pins reviewed digests, the shipped catalog must
	// not present any acquisition, only the manual fallback.
	offer, err := tailscaleclient.Plan(tailscaleclient.Platform{OS: "linux", Arch: "amd64"}, tailscaleclient.Detection{Installed: false}, tailscaleclient.DefaultCatalog())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if offer.Acquisition.Available {
		t.Fatal("default catalog shipped an unverified acquisition")
	}
	if !offer.ManualFallback {
		t.Fatal("default catalog dropped the manual fallback")
	}
}

func TestResolveRejectsUntrustedOrUnpinnedDescriptors(t *testing.T) {
	for name, pkg := range map[string]tailscaleclient.Package{
		"untrusted host": {OS: "linux", Arch: "amd64", Version: "1.80.0", Format: "tarball", URL: "https://evil.example/ts.tgz", SHA256: strings.Repeat("a", 64)},
		"http not https": {OS: "linux", Arch: "amd64", Version: "1.80.0", Format: "tarball", URL: "http://pkgs.tailscale.com/ts.tgz", SHA256: strings.Repeat("a", 64)},
		"missing digest": {OS: "linux", Arch: "amd64", Version: "1.80.0", Format: "tarball", URL: "https://pkgs.tailscale.com/ts.tgz", SHA256: ""},
		"bad version":    {OS: "linux", Arch: "amd64", Version: "latest", Format: "tarball", URL: "https://pkgs.tailscale.com/ts.tgz", SHA256: strings.Repeat("a", 64)},
		"unknown format": {OS: "linux", Arch: "amd64", Version: "1.80.0", Format: "snap", URL: "https://pkgs.tailscale.com/ts.tgz", SHA256: strings.Repeat("a", 64)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := (tailscaleclient.Catalog{Packages: []tailscaleclient.Package{pkg}}).Resolve(tailscaleclient.Platform{OS: "linux", Arch: "amd64"}); err == nil {
				t.Fatal("invalid descriptor accepted")
			}
		})
	}
}

func TestDetectUsesInjectedProbeAndOmitsPath(t *testing.T) {
	present := tailscaleclient.Detect(func(candidate string) bool { return candidate == "tailscale" })
	if !present.Installed {
		t.Fatal("did not detect an installed client")
	}
	encoded, _ := json.Marshal(present)
	if strings.Contains(string(encoded), "/") || strings.Contains(string(encoded), "path") {
		t.Fatalf("detection leaked a host path: %s", encoded)
	}
	absent := tailscaleclient.Detect(func(string) bool { return false })
	if absent.Installed {
		t.Fatal("detected a client that is absent")
	}
}
