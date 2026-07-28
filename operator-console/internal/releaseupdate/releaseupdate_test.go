package releaseupdate

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/capability"

	"github.com/stephan271/smallworlds/operator-console/internal/assessment"
)

func testMetadata() Metadata {
	return Metadata{
		Release: "v1.3.0", BaseTag: "v1.3.0", CatalogVersion: 8,
		Images: map[string]string{"operator-console": "sha256:" + strings.Repeat("a", 64)},
		Tools:  map[string]string{"k3s": "sha256:" + strings.Repeat("b", 64)},
		Compatibility: Compatibility{
			LauncherMin: "v1.2.0", LauncherMax: "v1.3.9",
			ClusterMin: "v1.2.0", ClusterMax: "v1.2.99",
			CatalogMin: 7, CatalogMax: 8,
		},
		ReleaseNotes:      []string{"Updates the operator console."},
		CapabilityChanges: []CapabilityChange{{ID: "nextcloud", Change: "updated", Detail: "New probes."}},
		Risks: Risks{
			Downtime: []string{"Controllers may restart."},
			Data:     []string{"No schema migration."},
			Exposure: []string{"No exposure change."},
		},
		Recovery: Recovery{Expected: "Revert the release proposal.", Steps: []string{"Revert the merge.", "Wait for Argo."}},
	}
}

func signedCatalog(t *testing.T, releases ...Metadata) (Catalog, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	catalog := Catalog{TrustedPublicKey: publicKey}
	for _, release := range releases {
		payload, err := json.Marshal(release)
		if err != nil {
			t.Fatal(err)
		}
		catalog.Releases = append(catalog.Releases, SignedMetadata{
			Payload:   payload,
			Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload)),
		})
	}
	return catalog, privateKey
}

func compatibleProfile() ClusterProfile {
	return ClusterProfile{
		LauncherVersion: "v1.2.5", ClusterVersion: "v1.2.20",
		BaseTag: "v1.2.20", CatalogVersion: 7, DeploymentMode: "hetzner",
		Capabilities: []string{"keycloak", "nextcloud"},
		Images:       map[string]string{"operator-console": "sha256:" + strings.Repeat("c", 64)},
		Tools:        map[string]string{"k3s": "sha256:" + strings.Repeat("d", 64)},
	}
}

func TestCatalogReturnsOnlyVerifiedMetadataAndCompatibility(t *testing.T) {
	catalog, _ := signedCatalog(t, testMetadata())
	available, err := catalog.Available(compatibleProfile())
	if err != nil {
		t.Fatalf("Available: %v", err)
	}
	if !available.SignatureValid || !available.Compatibility.Compatible {
		t.Fatalf("available = %+v", available)
	}
	if available.Metadata.BaseTag != "v1.3.0" ||
		available.Metadata.CatalogVersion != 8 ||
		!strings.HasPrefix(available.Metadata.Images["operator-console"], "sha256:") {
		t.Fatalf("signed release identity incomplete: %+v", available.Metadata)
	}

	catalog.Releases[0].Payload[10] ^= 1
	if _, err := catalog.Available(compatibleProfile()); !errors.Is(err, ErrInvalidMetadata) {
		t.Fatalf("tampered metadata error = %v, want ErrInvalidMetadata", err)
	}
}

func TestIncompatibleLauncherCanInspectButCannotBuildPlan(t *testing.T) {
	catalog, _ := signedCatalog(t, testMetadata())
	profile := compatibleProfile()
	profile.LauncherVersion = "v1.1.9"
	available, err := catalog.Available(profile)
	if err != nil {
		t.Fatal(err)
	}
	if available.Compatibility.Compatible ||
		len(available.Compatibility.Reasons) != 1 ||
		available.Compatibility.Reasons[0] != "launcher-version-out-of-range" {
		t.Fatalf("compatibility = %+v", available.Compatibility)
	}
	if _, err := BuildPlan(profile, available.Metadata, capability.DefaultCatalog(), testOverlay()); !errors.Is(err, ErrIncompatible) {
		t.Fatalf("BuildPlan error = %v, want incompatible", err)
	}
}

// The overlay is what decides which release a cluster runs, so a plan is
// judged by the overlay it would commit.
func testOverlay() Overlay {
	return Overlay{RepositoryURL: "https://github.com/community/overlay.git", Domain: "home.example"}
}

func TestBuildPlanRendersTheOverlayTheUpdateWouldCommit(t *testing.T) {
	metadata := testMetadata()
	plan, err := BuildPlan(compatibleProfile(), metadata, capability.DefaultCatalog(), testOverlay())
	if err != nil {
		t.Fatal(err)
	}
	if plan.FromBaseTag != "v1.2.20" || plan.ToBaseTag != "v1.3.0" ||
		len(plan.ReleaseNotes) == 0 || len(plan.CapabilityChanges) == 0 ||
		len(plan.Risks.Downtime) == 0 || len(plan.Risks.Data) == 0 ||
		len(plan.Risks.Exposure) == 0 || plan.Recovery.Expected == "" {
		t.Fatalf("incomplete plan: %+v", plan)
	}
	// The files are a real overlay, not a pins file nothing reads: the root
	// kustomization, the config map, and one unit per application.
	if plan.Files["kustomization.yaml"] == "" || plan.Files["overlay-config.yaml"] == "" {
		t.Fatalf("plan does not render an overlay: %#v", keys(plan.Files))
	}
	if plan.Files["smallworlds-release.yaml"] != "" {
		t.Error("plan still writes the pins file no reader consumes")
	}
	if err := capability.ValidateOverlay(capability.Overlay{Files: plan.Files, Diff: plan.GitDiff}); err != nil {
		t.Errorf("planned overlay is not valid: %v", err)
	}
	// The diff moves the pinned base and says so where an operator can see it.
	if !strings.Contains(plan.GitDiff, "-  - https://github.com/stephan271/smallworlds.git/infrastructure/kubernetes?ref=v1.2.20") ||
		!strings.Contains(plan.GitDiff, "+  - https://github.com/stephan271/smallworlds.git/infrastructure/kubernetes?ref=v1.3.0") ||
		!strings.Contains(plan.GitDiff, "+  smallworldsRelease: v1.3.0") {
		t.Fatalf("diff does not move the pinned base:\n%s", plan.GitDiff)
	}
	// Unchanged files stay out of the diff entirely.
	if strings.Contains(plan.GitDiff, "diff --git a/overlay-config.yaml") && !strings.Contains(plan.GitDiff, "+  smallworldsRelease: v1.3.0") {
		t.Error("the config map appears in the diff without the change that put it there")
	}
}

func keys(files map[string]string) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func TestAssessAdoptionExposesPartialAndFailedStates(t *testing.T) {
	partial := AssessAdoption(AdoptionEvidence{
		Merged: true, ArgoSynced: true, ArgoHealthy: true,
		Capabilities: []assessment.CapabilityAssessment{
			{CapabilityID: "keycloak", State: assessment.StateHealthy},
			{CapabilityID: "nextcloud", State: assessment.StateInstalling},
		},
	})
	if partial.State != AdoptionPartial || len(partial.Reasons) != 1 {
		t.Fatalf("partial adoption = %+v", partial)
	}
	failed := partial
	failed.ArgoFailed = true
	if got := AssessAdoption(failed); got.State != AdoptionFailed {
		t.Fatalf("failed adoption = %+v", got)
	}
}
