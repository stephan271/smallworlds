package console

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/addcapability"
	"github.com/stephan271/smallworlds/operator-console/internal/assessment"
	"github.com/stephan271/smallworlds/operator-console/internal/releaseupdate"
)

type fakeProfileReporter struct {
	profile releaseupdate.ClusterProfile
	err     error
}

func (reporter *fakeProfileReporter) Profile(context.Context) (releaseupdate.ClusterProfile, error) {
	return reporter.profile, reporter.err
}

type fakeAdoptionReporter struct {
	evidence releaseupdate.AdoptionEvidence
	err      error
}

func (reporter fakeAdoptionReporter) Adoption(context.Context, string) (releaseupdate.AdoptionEvidence, error) {
	return reporter.evidence, reporter.err
}

func updateMetadata() releaseupdate.Metadata {
	return releaseupdate.Metadata{
		Release: "v1.3.0", BaseTag: "v1.3.0", CatalogVersion: 8,
		Images: map[string]string{"operator-console": "sha256:" + strings.Repeat("a", 64)},
		Tools:  map[string]string{"k3s": "sha256:" + strings.Repeat("b", 64)},
		Compatibility: releaseupdate.Compatibility{
			LauncherMin: "v1.2.0", LauncherMax: "v1.3.9",
			ClusterMin: "v1.2.0", ClusterMax: "v1.2.99",
			CatalogMin: 7, CatalogMax: 8,
		},
		ReleaseNotes:      []string{"Signed release notes."},
		CapabilityChanges: []releaseupdate.CapabilityChange{{ID: "nextcloud", Change: "updated", Detail: "New readiness evidence."}},
		Risks: releaseupdate.Risks{
			Downtime: []string{"Controllers restart."},
			Data:     []string{"No data migration."},
			Exposure: []string{"No exposure change."},
		},
		Recovery: releaseupdate.Recovery{Expected: "Revert the merge.", Steps: []string{"Revert.", "Observe Argo."}},
	}
}

func updateCatalog(t *testing.T, metadata releaseupdate.Metadata) releaseupdate.Catalog {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	return releaseupdate.Catalog{
		TrustedPublicKey: publicKey,
		Releases: []releaseupdate.SignedMetadata{{
			Payload:   payload,
			Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload)),
		}},
	}
}

func updateProfile() releaseupdate.ClusterProfile {
	return releaseupdate.ClusterProfile{
		LauncherVersion: "v1.2.5", ClusterVersion: "v1.2.20",
		BaseTag: "v1.2.20", CatalogVersion: 7, DeploymentMode: "hetzner",
		Capabilities: []string{"keycloak", "nextcloud"},
		Images:       map[string]string{"operator-console": "sha256:" + strings.Repeat("c", 64)},
		Tools:        map[string]string{"k3s": "sha256:" + strings.Repeat("d", 64)},
	}
}

func newUpdateServer(t *testing.T, exchanger *fakeExchanger, profile *fakeProfileReporter, opener *fakeOpener) *Server {
	t.Helper()
	server := newAdditionServer(
		t, exchanger, &mapAssessor{states: healthyPlatform()},
		fakeCapacity{capacity: addcapability.Capacity{AllocatableMemoryMi: 8192, AllocatableStorageGi: 200}},
		opener,
	)
	server.releaseCatalog = updateCatalog(t, updateMetadata())
	server.clusterProfile = profile
	server.releaseAdoption = fakeAdoptionReporter{evidence: releaseupdate.AdoptionEvidence{
		Merged: true, MergedCommit: "merge123", ArgoRevision: "merge123",
		ArgoSynced: true, ArgoHealthy: true,
		Capabilities: []assessment.CapabilityAssessment{
			{CapabilityID: "keycloak", State: assessment.StateHealthy},
			{CapabilityID: "nextcloud", State: assessment.StateInstalling},
		},
	}}
	return server
}

func TestReleaseUpdateHappyPathOpensOnlyManualProposal(t *testing.T) {
	exchanger := &fakeExchanger{}
	profile := &fakeProfileReporter{profile: updateProfile()}
	opener := &fakeOpener{result: OpenedProposal{
		Provider: "github", Branch: "update-v1.3.0", Commit: "proposal123",
		URL: "https://github.com/community/overlay/pull/21",
	}}
	server := newUpdateServer(t, exchanger, profile, opener)
	operator := loginSession(t, server, exchanger, "operator")

	available := get(t, server, "/api/v1/updates/available", operator)
	if available.Code != http.StatusOK || !strings.Contains(available.Body.String(), `"signatureValid":true`) {
		t.Fatalf("available = %d %s", available.Code, available.Body.String())
	}
	planResponse := post(t, server, "/api/v1/updates/plan", `{"release":"v1.3.0"}`, operator)
	if planResponse.Code != http.StatusCreated {
		t.Fatalf("plan = %d %s", planResponse.Code, planResponse.Body.String())
	}
	if opener.files != nil {
		t.Fatal("planning must not open a branch or mutate anything")
	}
	var planned struct {
		PlanID string             `json:"planId"`
		Plan   releaseupdate.Plan `json:"plan"`
	}
	if err := json.Unmarshal(planResponse.Body.Bytes(), &planned); err != nil {
		t.Fatal(err)
	}
	if planned.PlanID == "" || len(planned.Plan.ReleaseNotes) == 0 ||
		len(planned.Plan.CapabilityChanges) == 0 || planned.Plan.GitDiff == "" {
		t.Fatalf("incomplete plan: %+v", planned)
	}
	if early := post(t, server, "/api/v1/updates/"+planned.PlanID+"/propose", "", operator); early.Code != http.StatusConflict {
		t.Fatalf("unapproved proposal = %d", early.Code)
	}
	if approve := post(t, server, "/api/v1/updates/"+planned.PlanID+"/approve", "", operator); approve.Code != http.StatusOK {
		t.Fatalf("approve = %d %s", approve.Code, approve.Body.String())
	}
	proposed := post(t, server, "/api/v1/updates/"+planned.PlanID+"/propose", "", operator)
	if proposed.Code != http.StatusCreated {
		t.Fatalf("propose = %d %s", proposed.Code, proposed.Body.String())
	}
	if !strings.Contains(proposed.Body.String(), `"automaticMerge":false`) ||
		!strings.Contains(proposed.Body.String(), `"liveClusterMutated":false`) {
		t.Fatalf("proposal did not state safety boundary: %s", proposed.Body.String())
	}
	// The proposal commits the overlay, which is what decides the release a
	// cluster runs — not a pins file with no reader.
	if opener.files["kustomization.yaml"] == "" ||
		!strings.Contains(opener.files["kustomization.yaml"], "?ref=v1.3.0") ||
		!strings.Contains(opener.files["overlay-config.yaml"], "smallworldsRelease: v1.3.0") {
		t.Fatalf("proposal files = %#v", opener.files)
	}
}

func TestReleaseUpdateRoleChecksAndIncompatibleProfileExport(t *testing.T) {
	exchanger := &fakeExchanger{}
	incompatible := updateProfile()
	incompatible.LauncherVersion = "v1.1.0"
	profile := &fakeProfileReporter{profile: incompatible}
	opener := &fakeOpener{}
	server := newUpdateServer(t, exchanger, profile, opener)
	observer := loginSession(t, server, exchanger, "observer")

	for _, path := range []string{"/api/v1/updates/profile", "/api/v1/updates/profile/export", "/api/v1/updates/available"} {
		if response := get(t, server, path, observer); response.Code != http.StatusOK {
			t.Fatalf("observer GET %s = %d", path, response.Code)
		}
	}
	if response := get(t, server, "/api/v1/updates/profile/export", observer); response.Header().Get("Content-Disposition") == "" {
		t.Fatal("profile export is not marked as a download")
	}
	if response := post(t, server, "/api/v1/updates/plan", `{"release":"v1.3.0"}`, observer); response.Code != http.StatusForbidden {
		t.Fatalf("observer plan = %d, want 403", response.Code)
	}
	for _, path := range []string{"/api/v1/updates/x/approve", "/api/v1/updates/x/propose"} {
		if response := post(t, server, path, "", observer); response.Code != http.StatusForbidden {
			t.Fatalf("observer POST %s = %d, want 403", path, response.Code)
		}
	}

	operator := loginSession(t, server, exchanger, "operator")
	if response := post(t, server, "/api/v1/updates/plan", `{"release":"v1.3.0"}`, operator); response.Code != http.StatusConflict {
		t.Fatalf("incompatible operator plan = %d %s", response.Code, response.Body.String())
	}
	owner := loginSession(t, server, exchanger, "owner")
	if response := get(t, server, "/api/v1/updates/available", owner); response.Code != http.StatusOK {
		t.Fatalf("owner availability = %d", response.Code)
	}
	if opener.files != nil {
		t.Fatal("incompatible launcher must not open a proposal")
	}
}

func TestReleaseUpdateOwnerMayCreateProposal(t *testing.T) {
	exchanger := &fakeExchanger{}
	profile := &fakeProfileReporter{profile: updateProfile()}
	opener := &fakeOpener{result: OpenedProposal{Provider: "github", Commit: "owner-proposal"}}
	server := newUpdateServer(t, exchanger, profile, opener)
	owner := loginSession(t, server, exchanger, "owner")

	planResponse := post(t, server, "/api/v1/updates/plan", `{"release":"v1.3.0"}`, owner)
	var planned struct {
		PlanID string `json:"planId"`
	}
	json.Unmarshal(planResponse.Body.Bytes(), &planned)
	if planned.PlanID == "" {
		t.Fatalf("owner plan = %d %s", planResponse.Code, planResponse.Body.String())
	}
	if response := post(t, server, "/api/v1/updates/"+planned.PlanID+"/approve", "", owner); response.Code != http.StatusOK {
		t.Fatalf("owner approve = %d", response.Code)
	}
	if response := post(t, server, "/api/v1/updates/"+planned.PlanID+"/propose", "", owner); response.Code != http.StatusCreated {
		t.Fatalf("owner propose = %d %s", response.Code, response.Body.String())
	}
}

func TestReleaseUpdateApprovalDetectsProfileDriftAndAdoptionIsExplicit(t *testing.T) {
	exchanger := &fakeExchanger{}
	profile := &fakeProfileReporter{profile: updateProfile()}
	opener := &fakeOpener{result: OpenedProposal{Provider: "github", Commit: "proposal123"}}
	server := newUpdateServer(t, exchanger, profile, opener)
	operator := loginSession(t, server, exchanger, "operator")

	planResponse := post(t, server, "/api/v1/updates/plan", `{"release":"v1.3.0"}`, operator)
	var planned struct {
		PlanID string `json:"planId"`
	}
	json.Unmarshal(planResponse.Body.Bytes(), &planned)
	post(t, server, "/api/v1/updates/"+planned.PlanID+"/approve", "", operator)
	profile.profile.BaseTag = "v1.2.21"
	if response := post(t, server, "/api/v1/updates/"+planned.PlanID+"/propose", "", operator); response.Code != http.StatusConflict {
		t.Fatalf("proposal after profile drift = %d", response.Code)
	}
	if opener.files != nil {
		t.Fatal("profile drift must prevent proposal creation")
	}

	adoption := get(t, server, "/api/v1/updates/v1.3.0/adoption", operator)
	if adoption.Code != http.StatusOK || !strings.Contains(adoption.Body.String(), `"state":"partial"`) ||
		!strings.Contains(adoption.Body.String(), `"argoRevision":"merge123"`) ||
		!strings.Contains(adoption.Body.String(), `"capabilityId":"nextcloud"`) {
		t.Fatalf("adoption = %d %s", adoption.Code, adoption.Body.String())
	}
}
