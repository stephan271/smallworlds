// Package tofu owns the launcher's OpenTofu boundary: the pinned toolchain it
// is allowed to obtain, and the per-Cluster-Profile state workspace it runs in.
//
// Two rules shape it. First, "no globally installed prerequisites" must not
// become "whatever tofu binary is on PATH" — the launcher resolves only pinned
// descriptors that were verified by digest and signature, exactly like every
// other bootstrap asset, and refuses to fall back to an ambient executable.
// Second, OpenTofu state is both authoritative and sensitive: it decides what
// infrastructure exists and it contains credential material, so each profile
// gets an isolated workspace with an exclusive lock and a backup of the
// previous state on every write.
package tofu

import (
	"context"
	"errors"
	"fmt"
	"runtime"

	"github.com/stephan271/smallworlds/operator-console/internal/bootstrapassets"
)

// The pinned versions. They are part of the product's reproducibility promise:
// the same SmallWorlds release always reconciles with the same OpenTofu and the
// same provider.
const (
	// OpenTofuVersion is the pinned OpenTofu CLI version.
	OpenTofuVersion = "1.10.6"
	// HcloudProviderVersion is the pinned hetznercloud/hcloud provider version,
	// matching the constraint in infrastructure/terraform/main.tf (~> 1.54).
	HcloudProviderVersion = "1.54.0"
)

// ErrToolchainUnavailable is returned when no verified descriptor exists for
// this platform's pinned toolchain. The launcher refuses honestly rather than
// reaching for an ambient binary.
var ErrToolchainUnavailable = errors.New("tofu: pinned toolchain artifacts unavailable")

// Release is the asset-catalog release identifier for the pinned toolchain. It
// is derived from the pinned versions, so bumping a version cannot silently
// keep using previously cached artifacts.
func Release() string {
	return fmt.Sprintf("tofu-%s+hcloud-%s", OpenTofuVersion, HcloudProviderVersion)
}

// ArtifactIDs are the descriptor ids the toolchain needs on the current
// platform: the OpenTofu CLI and the Hetzner provider plugin.
func ArtifactIDs() []string {
	platform := runtime.GOOS + "-" + runtime.GOARCH
	return []string{"opentofu-" + platform, "hcloud-provider-" + platform}
}

// AssetSource is the verified-artifact boundary. *bootstrapassets.Manager
// satisfies it directly: descriptors are compiled in, digests and signatures
// are checked before an artifact is usable, and no caller can supply a URL.
type AssetSource interface {
	Requirements(ctx context.Context, release string) ([]bootstrapassets.Status, error)
	Acquire(ctx context.Context, release string) ([]bootstrapassets.Status, error)
}

// Toolchain reports the pinned versions and the verification state of their
// artifacts. It is safe to return over the API: identity and integrity
// evidence only, never cache paths.
type Toolchain struct {
	OpenTofuVersion       string                   `json:"openTofuVersion"`
	HcloudProviderVersion string                   `json:"hcloudProviderVersion"`
	Release               string                   `json:"release"`
	Artifacts             []bootstrapassets.Status `json:"artifacts"`
	Ready                 bool                     `json:"ready"`
	ReasonKey             string                   `json:"reasonKey"`
}

// Inspect reports the current state of the pinned toolchain without
// downloading anything.
func Inspect(ctx context.Context, source AssetSource) (Toolchain, error) {
	statuses, err := source.Requirements(ctx, Release())
	if err != nil {
		return unavailable(), toolchainError(err)
	}
	return summarize(statuses), nil
}

// Acquire obtains and verifies the pinned artifacts, resuming an interrupted
// transfer. A digest or signature mismatch fails the acquisition — a partially
// trusted toolchain is never usable.
func Acquire(ctx context.Context, source AssetSource) (Toolchain, error) {
	statuses, err := source.Acquire(ctx, Release())
	if err != nil {
		return unavailable(), toolchainError(err)
	}
	return summarize(statuses), nil
}

func summarize(statuses []bootstrapassets.Status) Toolchain {
	toolchain := unavailable()
	toolchain.Artifacts = statuses
	present := map[string]bootstrapassets.State{}
	for _, status := range statuses {
		present[status.ID] = status.State
	}
	toolchain.Ready = true
	for _, id := range ArtifactIDs() {
		if present[id] != bootstrapassets.StateReady {
			toolchain.Ready = false
		}
	}
	if toolchain.Ready {
		toolchain.ReasonKey = "toolchain-verified"
	} else {
		toolchain.ReasonKey = "toolchain-not-yet-acquired"
	}
	return toolchain
}

func unavailable() Toolchain {
	return Toolchain{
		OpenTofuVersion:       OpenTofuVersion,
		HcloudProviderVersion: HcloudProviderVersion,
		Release:               Release(),
		Artifacts:             []bootstrapassets.Status{},
		ReasonKey:             "toolchain-artifacts-unavailable",
	}
}

// toolchainError keeps an unresolvable release distinguishable from a transport
// or integrity failure, so the launcher can answer "not published for this
// platform" differently from "download failed".
func toolchainError(err error) error {
	if errors.Is(err, bootstrapassets.ErrUnknownRelease) {
		return fmt.Errorf("%w: no descriptor for release %s", ErrToolchainUnavailable, Release())
	}
	return err
}
