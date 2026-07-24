// Package tailscaleclient owns the Launcher-host boundary for the official
// Tailscale client used to join the LAN-only Private Network. It detects an
// already-installed official client, offers pinned, integrity-verified
// acquisition for supported platforms (always surfacing that installation
// requires explicit elevation), and always retains a manual-install fallback.
// Like bootstrapassets it resolves only descriptors compiled into a trusted
// catalog; callers cannot supply a URL or an ambient executable.
package tailscaleclient

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// ErrInvalidPackage is returned when a catalog descriptor fails validation.
var ErrInvalidPackage = errors.New("tailscale client package is invalid")

// ManualInstructionsURL is the official download page used for the manual
// fallback and surfaced alongside every acquisition offer.
const ManualInstructionsURL = "https://tailscale.com/download"

var sha256Text = regexp.MustCompile(`^[a-f0-9]{64}$`)
var safeVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
var validFormats = map[string]bool{"tarball": true, "pkg": true, "msi": true, "appstore": true, "repo": true}

// trustedDownloadHosts are the only hosts an acquisition descriptor may point
// at: Tailscale's official package host and the official GitHub releases.
var trustedDownloadHosts = map[string]bool{"pkgs.tailscale.com": true, "github.com": true}

// Platform identifies the Launcher Host operating system and architecture using
// Go's GOOS/GOARCH vocabulary.
type Platform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

// Package is a pinned, integrity-verified acquisition descriptor for one
// platform. Installation always requires explicit elevation.
type Package struct {
	OS      string
	Arch    string
	Version string
	URL     string
	SHA256  string
	Format  string
}

// Validate enforces that a descriptor is a pinned HTTPS download from a trusted
// host with a fixed SHA-256 digest.
func (pkg Package) Validate() error {
	if pkg.OS == "" || pkg.Arch == "" || !safeVersion.MatchString(pkg.Version) || !validFormats[pkg.Format] {
		return fmt.Errorf("%w: identity", ErrInvalidPackage)
	}
	parsed, err := url.Parse(pkg.URL)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || !trustedDownloadHosts[parsed.Hostname()] {
		return fmt.Errorf("%w: url", ErrInvalidPackage)
	}
	if !sha256Text.MatchString(pkg.SHA256) {
		return fmt.Errorf("%w: digest", ErrInvalidPackage)
	}
	return nil
}

// Catalog is a compiled set of reviewed, pinned Tailscale client descriptors.
type Catalog struct {
	Packages []Package
}

// Resolve returns the validated pinned descriptor for a platform, if any.
func (catalog Catalog) Resolve(platform Platform) (Package, bool, error) {
	for _, pkg := range catalog.Packages {
		if pkg.OS == platform.OS && pkg.Arch == platform.Arch {
			if err := pkg.Validate(); err != nil {
				return Package{}, false, err
			}
			return pkg, true, nil
		}
	}
	return Package{}, false, nil
}

// DefaultCatalog is intentionally empty until release engineering pins reviewed
// SHA-256 digests for official Tailscale downloads, mirroring how bootstrap
// asset digests are provided by the release process. Until an entry exists,
// every platform falls back to the manual install path rather than presenting
// unverified acquisition. Reviewed entries take the shape:
//
//	{OS: "linux", Arch: "amd64", Version: "1.80.0", Format: "tarball",
//	 URL: "https://pkgs.tailscale.com/stable/tailscale_1.80.0_amd64.tgz",
//	 SHA256: "<reviewed digest>"}
func DefaultCatalog() Catalog {
	return Catalog{}
}

// Detection reports only whether an official client is present. It deliberately
// omits the discovered path so the Launcher API never leaks host filesystem
// layout.
type Detection struct {
	Installed bool `json:"installed"`
}

var detectionCandidates = []string{
	"tailscale",
	"/usr/bin/tailscale",
	"/usr/local/bin/tailscale",
	"/Applications/Tailscale.app",
	`C:\Program Files\Tailscale\tailscale.exe`,
}

// Detect probes for an installed official client. The presence check is injected
// so detection is deterministic under test.
func Detect(exists func(candidate string) bool) Detection {
	for _, candidate := range detectionCandidates {
		if exists(candidate) {
			return Detection{Installed: true}
		}
	}
	return Detection{Installed: false}
}

// DetectInstalled performs production detection using PATH lookup for the binary
// and filesystem checks for known GUI install locations.
func DetectInstalled() Detection {
	return Detect(func(candidate string) bool {
		if !strings.ContainsAny(candidate, `/\`) {
			_, err := exec.LookPath(candidate)
			return err == nil
		}
		_, err := os.Stat(candidate)
		return err == nil
	})
}

// Acquisition is the offer surfaced for a platform. When Available, it carries a
// pinned verified download that requires explicit elevation to install; a manual
// fallback is always available.
type Acquisition struct {
	Available             bool   `json:"available"`
	Version               string `json:"version,omitempty"`
	URL                   string `json:"url,omitempty"`
	SHA256                string `json:"sha256,omitempty"`
	Format                string `json:"format,omitempty"`
	ElevationRequired     bool   `json:"elevationRequired"`
	ManualInstructionsURL string `json:"manualInstructionsUrl"`
}

// Offer is the complete, API-safe response describing detection state and how to
// obtain the official client on this host.
type Offer struct {
	Platform       Platform    `json:"platform"`
	Detected       bool        `json:"detected"`
	Acquisition    Acquisition `json:"acquisition"`
	ManualFallback bool        `json:"manualFallback"`
}

// Plan builds the acquisition offer for a platform from detection state and the
// pinned catalog. Installation always requires explicit elevation, and the
// manual fallback is always retained.
func Plan(platform Platform, detection Detection, catalog Catalog) (Offer, error) {
	offer := Offer{Platform: platform, Detected: detection.Installed, ManualFallback: true}
	pkg, found, err := catalog.Resolve(platform)
	if err != nil {
		return Offer{}, err
	}
	if found {
		offer.Acquisition = Acquisition{
			Available:             true,
			Version:               pkg.Version,
			URL:                   pkg.URL,
			SHA256:                pkg.SHA256,
			Format:                pkg.Format,
			ElevationRequired:     true,
			ManualInstructionsURL: ManualInstructionsURL,
		}
		return offer, nil
	}
	offer.Acquisition = Acquisition{Available: false, ElevationRequired: false, ManualInstructionsURL: ManualInstructionsURL}
	return offer, nil
}
