package launcher_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/bootstrapassets"
	"github.com/stephan271/smallworlds/operator-console/internal/hetzner"
	"github.com/stephan271/smallworlds/operator-console/internal/hetznerprovision"
	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
	"github.com/stephan271/smallworlds/operator-console/internal/tofu"
)

const hetznerTestToken = "abcdefghij0123456789ABCDEFGHIJabcdefghij0123456789ABCDEFGHIJ0123"

// fakeHetznerProvider is a deterministic stand-in for the read-only provider
// boundary. It records the token it was handed so a test can prove the value
// only ever travels into a provider call.
type fakeHetznerProvider struct {
	probe        hetzner.TokenProbe
	probeErr     error
	resources    []hetzner.Resource
	inventoryErr error
	catalog      hetzner.PriceCatalog
	catalogErr   error
	nameservers  []string
	seenToken    string
	inventories  int
}

func (provider *fakeHetznerProvider) Probe(_ context.Context, token string) (hetzner.TokenProbe, error) {
	provider.seenToken = token
	return provider.probe, provider.probeErr
}

func (provider *fakeHetznerProvider) Inventory(_ context.Context, token, _ string) ([]hetzner.Resource, error) {
	provider.seenToken, provider.inventories = token, provider.inventories+1
	return provider.resources, provider.inventoryErr
}

func (provider *fakeHetznerProvider) Catalog(_ context.Context, token string) (hetzner.PriceCatalog, error) {
	provider.seenToken = token
	return provider.catalog, provider.catalogErr
}

func (provider *fakeHetznerProvider) Nameservers(_ context.Context, token, _ string) ([]string, error) {
	provider.seenToken = token
	return provider.nameservers, nil
}

// recordingReconciler proves the provider is only ever changed through an
// approved, still-current plan, and captures the configuration that would be
// applied so a test can assert on what the plan actually renders.
type recordingReconciler struct {
	calls  int
	module hetznerprovision.Module
	files  map[string]string
	err    error
}

func (reconciler *recordingReconciler) Apply(_ context.Context, request hetznerprovision.ReconcileRequest) (hetznerprovision.Outcome, error) {
	reconciler.calls, reconciler.module, reconciler.files = reconciler.calls+1, request.Module, request.Module.Files
	if reconciler.err != nil {
		return hetznerprovision.Outcome{}, reconciler.err
	}
	return hetznerprovision.Outcome{Applied: true, ServerAddress: "203.0.113.9", ServerID: "srv-1", ObservedAt: time.Now().UTC()}, nil
}

// convergedObserver stands in for watching the node come up.
type convergedObserver struct{ calls int }

func (observer *convergedObserver) Observe(context.Context, hetznerprovision.Binding, string) (hetznerprovision.Convergence, error) {
	observer.calls++
	return hetznerprovision.Convergence{K3SReady: true, ArgoCDReady: true, OverlaySynced: true, ObservedAt: time.Now().UTC()}, nil
}

// hetznerToolchainAssets publishes fake but properly signed descriptors for the
// pinned OpenTofu release, so a test can reach the states that require a
// verified toolchain. Production ships no such descriptors, which is why the
// launcher refuses there.
func hetznerToolchainAssets(t *testing.T) *bootstrapassets.Manager {
	t.Helper()
	contents := []byte("pinned-toolchain-artifact")
	digest := sha256.Sum256(contents)
	digestText := fmt.Sprintf("%x", digest[:])
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(digestText)))
	descriptors := make([]bootstrapassets.Descriptor, 0, 2)
	for _, id := range tofu.ArtifactIDs() {
		descriptors = append(descriptors, bootstrapassets.Descriptor{
			ID: id, Release: tofu.Release(), URL: "https://assets.example.invalid/" + id + ".tar.gz",
			SHA256: digestText, Signature: signature, PublicKey: publicKey, Destination: "assets.example.invalid",
		})
	}
	manager, err := bootstrapassets.NewManager(t.TempDir(), bootstrapassets.Catalog{Descriptors: descriptors}, assetFetcherStub{contents: contents})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Acquire(context.Background(), tofu.Release()); err != nil {
		t.Fatal(err)
	}
	return manager
}

// hetznerProjectPrerequisites are the two resources the whole project shares.
// An installation reuses them and never owns them, so a plan is blocked until
// they exist.
func hetznerProjectPrerequisites() []hetzner.Resource {
	return []hetzner.Resource{
		{Kind: hetzner.KindDNSZone, ProviderID: "zone-1", Name: "example.org"},
		{Kind: hetzner.KindSSHKey, ProviderID: "key-1", Name: hetzner.SharedAdminSSHKeyName},
	}
}

func hetznerCatalogFixture() hetzner.PriceCatalog {
	return hetzner.PriceCatalog{
		Offerings: []hetzner.ServerOffering{
			{Name: "cx23", VCPU: 2, MemoryGB: 4, DiskGB: 40, Architecture: "x86", MonthlyEUR: 4.51, AvailableLocations: []string{"nbg1", "fsn1"}},
			{Name: "cx43", VCPU: 8, MemoryGB: 16, DiskGB: 160, Architecture: "x86", MonthlyEUR: 16.40, AvailableLocations: []string{"nbg1", "fsn1"}},
			{Name: "cx53", VCPU: 16, MemoryGB: 32, DiskGB: 320, Architecture: "x86", MonthlyEUR: 31.90, AvailableLocations: []string{"nbg1"}},
		},
		VolumeMonthlyEURPerGB: 0.044,
		PrimaryIPMonthlyEUR:   0.60,
		Locations:             []string{"nbg1", "fsn1"},
		ObservedAt:            time.Now().UTC(),
	}
}

// newHetznerLauncher builds a launcher whose Hetzner journey can be driven to
// an approvable plan. It deliberately does *not* publish a toolchain: tests that
// need one call newProvisionableHetznerLauncher, so the difference between "can
// plan" and "can provision" stays visible.
func newHetznerLauncher(t *testing.T, provider *fakeHetznerProvider, reconciler hetznerprovision.Reconciler) (*launcher.Server, *http.Cookie, string, string) {
	t.Helper()
	return newHetznerLauncherWithAssets(t, provider, reconciler, nil, nil)
}

// newProvisionableHetznerLauncher adds a verified pinned toolchain and an
// established GitOps Overlay — the two facts an approved plan is bound to
// beyond the project itself.
func newProvisionableHetznerLauncher(t *testing.T, provider *fakeHetznerProvider, reconciler hetznerprovision.Reconciler) (*launcher.Server, *http.Cookie, string, string) {
	t.Helper()
	handler, cookie, csrf, profileID := newHetznerLauncherWithAssets(t, provider, reconciler, hetznerToolchainAssets(t), &convergedObserver{})
	establishHetznerOverlay(t, handler, cookie, csrf, profileID)
	return handler, cookie, csrf, profileID
}

func newHetznerLauncherWithAssets(t *testing.T, provider *fakeHetznerProvider, reconciler hetznerprovision.Reconciler, assets *bootstrapassets.Manager, observer hetznerprovision.ConvergenceObserver) (*launcher.Server, *http.Cookie, string, string) {
	t.Helper()
	config := launcher.Config{
		DataDir: t.TempDir(), LaunchToken: "hetzner",
		HetznerProvider: provider, HetznerReconciler: reconciler, HetznerConvergence: observer,
		GenericGitClient: &genericGitStub{commit: strings.Repeat("c", 40)},
	}
	if assets != nil {
		config.BootstrapAssets = assets
	}
	handler, err := launcher.New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "hetzner")
	profile := createProfile(t, handler, cookie, csrf, "Community", "en", "hetzner")
	unlockVaultForRecoveryTest(t, handler, cookie, csrf)
	return handler, cookie, csrf, profile.ID
}

// establishHetznerOverlay records the pinned GitOps Overlay commit an approved
// infrastructure plan is bound to.
func establishHetznerOverlay(t *testing.T, handler *launcher.Server, cookie *http.Cookie, csrf, profileID string) {
	t.Helper()
	headers := map[string]string{"X-CSRF-Token": csrf}
	credentials, _ := json.Marshal(map[string]string{"profileId": profileID, "repositoryUrl": "https://git.example/overlay.git", "username": "operator", "token": "git-secret"})
	response := request(t, handler, http.MethodPost, "/api/v1/generic-git/token/validate", credentials, cookie, headers)
	response.Body.Close()
	planBody, _ := json.Marshal(map[string]any{"profileId": profileID, "mode": "minimal", "communityIds": []string{}, "release": "v1.2.27", "repositoryUrl": "https://git.example/overlay.git", "domain": "example.org"})
	response = request(t, handler, http.MethodPost, "/api/v1/capabilities/plan", planBody, cookie, headers)
	var capabilityPlan struct {
		Plan struct {
			ID string `json:"id"`
		} `json:"plan"`
	}
	if err := json.NewDecoder(response.Body).Decode(&capabilityPlan); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	response = request(t, handler, http.MethodPost, "/api/v1/plans/"+capabilityPlan.Plan.ID+"/approve", nil, cookie, headers)
	response.Body.Close()
	establishBody, _ := json.Marshal(map[string]any{"profileId": profileID, "planId": capabilityPlan.Plan.ID, "repositoryUrl": "https://git.example/overlay.git", "mode": "minimal", "communityIds": []string{}, "release": "v1.2.27", "domain": "example.org"})
	response = request(t, handler, http.MethodPost, "/api/v1/generic-git/overlay/establish", establishBody, cookie, headers)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("overlay status = %d: %s", response.StatusCode, readAll(t, response))
	}
	response.Body.Close()
}

func postHetzner(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, path string, payload any) (int, string) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	response := request(t, handler, http.MethodPost, path, body, cookie, map[string]string{"X-CSRF-Token": csrf})
	return response.StatusCode, string(readAll(t, response))
}

// validateHetznerToken drives token validation and returns the verdict body.
func validateHetznerToken(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, profileID, token string) string {
	t.Helper()
	status, body := postHetzner(t, handler, cookie, csrf, "/api/v1/hetzner/token/validate", map[string]string{"profileId": profileID, "token": token})
	if status != http.StatusOK {
		t.Fatalf("token validate status = %d: %s", status, body)
	}
	return body
}

func TestHetznerTokenValidationCustodiesAndNeverEchoesTheToken(t *testing.T) {
	provider := &fakeHetznerProvider{probe: hetzner.TokenProbe{ProjectID: "ssh_keys:41", ReadAuthority: true, WriteAuthority: true}}
	handler, cookie, csrf, profileID := newHetznerLauncher(t, provider, nil)

	body := validateHetznerToken(t, handler, cookie, csrf, profileID, hetznerTestToken)
	if strings.Contains(body, hetznerTestToken) {
		t.Fatalf("token validation echoed the token: %s", body)
	}
	var assessment hetzner.TokenAssessment
	if err := json.Unmarshal([]byte(body), &assessment); err != nil {
		t.Fatal(err)
	}
	if assessment.State != hetzner.TokenValid || assessment.Fingerprint == "" || assessment.ProjectID != "ssh_keys:41" {
		t.Fatalf("assessment %+v", assessment)
	}
	if provider.seenToken != hetznerTestToken {
		t.Fatal("the provider probe did not receive the token")
	}

	// The status view stays secret-free too.
	response := request(t, handler, http.MethodGet, "/api/v1/hetzner?profileId="+profileID, nil, cookie, nil)
	status := string(readAll(t, response))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", response.StatusCode, status)
	}
	if strings.Contains(status, hetznerTestToken) {
		t.Fatalf("status leaked the token: %s", status)
	}
	if !strings.Contains(status, `"toolchain"`) || !strings.Contains(status, `"workspace"`) {
		t.Fatalf("status lacks toolchain/workspace evidence: %s", status)
	}
}

func TestHetznerTokenVerdicts(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		token     string
		probe     hetzner.TokenProbe
		wantState hetzner.TokenState
	}{
		{name: "malformed token is refused locally", token: "short", wantState: hetzner.TokenMalformed},
		{name: "rejected token", token: hetznerTestToken, probe: hetzner.TokenProbe{Unauthorized: true}, wantState: hetzner.TokenUnauthorized},
		{name: "read-only token cannot provision", token: hetznerTestToken, probe: hetzner.TokenProbe{ProjectID: "p", ReadAuthority: true}, wantState: hetzner.TokenReadOnly},
		{name: "throttled probe is inconclusive", token: hetznerTestToken, probe: hetzner.TokenProbe{RateLimited: true}, wantState: hetzner.TokenInconclusive},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			provider := &fakeHetznerProvider{probe: testCase.probe}
			handler, cookie, csrf, profileID := newHetznerLauncher(t, provider, nil)
			var assessment hetzner.TokenAssessment
			if err := json.Unmarshal([]byte(validateHetznerToken(t, handler, cookie, csrf, profileID, testCase.token)), &assessment); err != nil {
				t.Fatal(err)
			}
			if assessment.State != testCase.wantState {
				t.Fatalf("state %s, want %s", assessment.State, testCase.wantState)
			}
			// A token that is not usable is never custodied, so inspection is
			// still blocked on the token step.
			status, body := postHetzner(t, handler, cookie, csrf, "/api/v1/hetzner/inspect", map[string]string{"profileId": profileID, "domain": "example.org"})
			if status != http.StatusConflict {
				t.Fatalf("inspect after unusable token = %d: %s", status, body)
			}
		})
	}
	t.Run("malformed token never reaches the provider", func(t *testing.T) {
		provider := &fakeHetznerProvider{}
		handler, cookie, csrf, profileID := newHetznerLauncher(t, provider, nil)
		validateHetznerToken(t, handler, cookie, csrf, profileID, "not-a-token")
		if provider.seenToken != "" {
			t.Fatal("a malformed token was sent to the provider")
		}
	})
}

func TestHetznerInspectionClassifiesOwnershipAndDelegation(t *testing.T) {
	provider := &fakeHetznerProvider{
		probe: hetzner.TokenProbe{ProjectID: "ssh_keys:41", ReadAuthority: true, WriteAuthority: true},
		resources: []hetzner.Resource{
			{Kind: hetzner.KindDNSZone, ProviderID: "zone-1", Name: "example.org"},
			{Kind: hetzner.KindFirewall, ProviderID: "fw-1", Name: "smallworlds-firewall"},
			{Kind: hetzner.KindVolume, ProviderID: "vol-1", Name: "smallworlds-data", Labels: map[string]string{hetzner.LabelProfile: "another-profile"}},
			{Kind: hetzner.KindServer, ProviderID: "srv-9", Name: "CC_Pilot_Node_01"},
		},
		nameservers: hetzner.HetznerNameservers,
	}
	handler, cookie, csrf, profileID := newHetznerLauncher(t, provider, nil)
	validateHetznerToken(t, handler, cookie, csrf, profileID, hetznerTestToken)

	status, body := postHetzner(t, handler, cookie, csrf, "/api/v1/hetzner/inspect", map[string]string{"profileId": profileID, "domain": "example.org"})
	if status != http.StatusOK {
		t.Fatalf("inspect status = %d: %s", status, body)
	}
	var view struct {
		Inventory  hetzner.Inventory  `json:"inventory"`
		Delegation hetzner.Delegation `json:"delegation"`
	}
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatal(err)
	}
	ownership := map[hetzner.ResourceKind]hetzner.Ownership{}
	for _, finding := range view.Inventory.Findings {
		if finding.Ownership != hetzner.OwnershipAbsent {
			ownership[finding.Expectation.Kind] = finding.Ownership
		}
	}
	if ownership[hetzner.KindDNSZone] != hetzner.OwnershipShared || ownership[hetzner.KindFirewall] != hetzner.OwnershipAdoptable ||
		ownership[hetzner.KindVolume] != hetzner.OwnershipConflicting || ownership[hetzner.KindServer] != hetzner.OwnershipUnknown {
		t.Fatalf("ownership %+v", ownership)
	}
	if view.Delegation.Status != hetzner.DelegationConfirmed || view.Inventory.Digest == "" {
		t.Fatalf("delegation/digest %+v", view)
	}

	// A bad domain is refused before any provider call.
	before := provider.inventories
	if status, body := postHetzner(t, handler, cookie, csrf, "/api/v1/hetzner/inspect", map[string]string{"profileId": profileID, "domain": "not-a-domain"}); status != http.StatusBadRequest {
		t.Fatalf("invalid naming status = %d: %s", status, body)
	}
	if provider.inventories != before {
		t.Fatal("an invalid domain reached the provider")
	}
}

func TestHetznerInspectionSurfacesProviderFailuresDistinctly(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "rate limited", err: hetzner.ErrRateLimited, wantStatus: http.StatusTooManyRequests},
		{name: "token rejected", err: hetzner.ErrUnauthorized, wantStatus: http.StatusForbidden},
		{name: "provider down", err: hetzner.ErrProvider, wantStatus: http.StatusBadGateway},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			provider := &fakeHetznerProvider{probe: hetzner.TokenProbe{ProjectID: "p", ReadAuthority: true, WriteAuthority: true}, inventoryErr: testCase.err}
			handler, cookie, csrf, profileID := newHetznerLauncher(t, provider, nil)
			validateHetznerToken(t, handler, cookie, csrf, profileID, hetznerTestToken)
			status, body := postHetzner(t, handler, cookie, csrf, "/api/v1/hetzner/inspect", map[string]string{"profileId": profileID, "domain": "example.org"})
			if status != testCase.wantStatus {
				t.Fatalf("inspect status = %d, want %d: %s", status, testCase.wantStatus, body)
			}
		})
	}
}

func TestHetznerPresetsDeriveFromCapabilitiesWithLiveCostAndAvailability(t *testing.T) {
	provider := &fakeHetznerProvider{probe: hetzner.TokenProbe{ProjectID: "p", ReadAuthority: true, WriteAuthority: true}, catalog: hetznerCatalogFixture()}
	handler, cookie, csrf, profileID := newHetznerLauncher(t, provider, nil)
	validateHetznerToken(t, handler, cookie, csrf, profileID, hetznerTestToken)

	status, body := postHetzner(t, handler, cookie, csrf, "/api/v1/hetzner/presets", map[string]any{"profileId": profileID, "mode": "minimal", "location": "nbg1"})
	if status != http.StatusOK {
		t.Fatalf("presets status = %d: %s", status, body)
	}
	var view struct {
		Presets     []hetzner.Preset         `json:"presets"`
		Requirement hetzner.Requirement      `json:"requirement"`
		Locations   []string                 `json:"locations"`
		Offerings   []hetzner.ServerOffering `json:"offerings"`
	}
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Presets) != 3 || len(view.Locations) == 0 || len(view.Offerings) == 0 {
		t.Fatalf("presets view %+v", view)
	}
	for _, preset := range view.Presets {
		if preset.Cost.TotalMonthlyEUR <= 0 || len(preset.Cost.NoteKeys) == 0 {
			t.Fatalf("preset %s carries no cost estimate", preset.Tier)
		}
	}
	if view.Requirement.MemoryGB <= 0 || view.Requirement.VolumeGB <= 0 {
		t.Fatalf("requirement %+v", view.Requirement)
	}

	// A bigger selection needs at least as much capacity as a minimal one.
	_, fullBody := postHetzner(t, handler, cookie, csrf, "/api/v1/hetzner/presets", map[string]any{"profileId": profileID, "mode": "full", "location": "nbg1"})
	var full struct {
		Requirement hetzner.Requirement `json:"requirement"`
	}
	if err := json.Unmarshal([]byte(fullBody), &full); err != nil {
		t.Fatal(err)
	}
	if full.Requirement.MemoryGB <= view.Requirement.MemoryGB {
		t.Fatalf("full selection requirement %+v did not exceed minimal %+v", full.Requirement, view.Requirement)
	}
}

// planHetzner drives token → inspect → plan and returns the plan response body.
func planHetzner(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, profileID string, payload map[string]any) (int, string) {
	t.Helper()
	validateHetznerToken(t, handler, cookie, csrf, profileID, hetznerTestToken)
	if status, body := postHetzner(t, handler, cookie, csrf, "/api/v1/hetzner/inspect", map[string]string{"profileId": profileID, "domain": "example.org"}); status != http.StatusOK {
		t.Fatalf("inspect status = %d: %s", status, body)
	}
	request := map[string]any{"profileId": profileID, "mode": "minimal", "tier": "recommended", "location": "nbg1", "acmeEmail": "operator@example.org"}
	for key, value := range payload {
		request[key] = value
	}
	return postHetzner(t, handler, cookie, csrf, "/api/v1/hetzner/plan", request)
}

func TestHetznerPlanIsCostBearingAndChangesNothing(t *testing.T) {
	provider := &fakeHetznerProvider{
		probe:       hetzner.TokenProbe{ProjectID: "p", ReadAuthority: true, WriteAuthority: true},
		catalog:     hetznerCatalogFixture(),
		nameservers: hetzner.HetznerNameservers,
		resources:   hetznerProjectPrerequisites(),
	}
	reconciler := &recordingReconciler{}
	handler, cookie, csrf, profileID := newProvisionableHetznerLauncher(t, provider, reconciler)

	status, body := planHetzner(t, handler, cookie, csrf, profileID, nil)
	if status != http.StatusCreated {
		t.Fatalf("plan status = %d: %s", status, body)
	}
	var view struct {
		Plan struct {
			ID    string `json:"id"`
			Risks []struct {
				Code string `json:"code"`
			} `json:"risks"`
		} `json:"plan"`
		ChangePlan hetzner.ChangePlan `json:"changePlan"`
		Approvable bool               `json:"approvable"`
	}
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatal(err)
	}
	if !view.Approvable || len(view.ChangePlan.Blockers) != 0 {
		t.Fatalf("plan is not approvable: %s", body)
	}
	if view.ChangePlan.Cost.TotalMonthlyEUR <= 0 || view.ChangePlan.Digest == "" || view.ChangePlan.InventoryDigest == "" {
		t.Fatalf("change plan %+v", view.ChangePlan)
	}
	recurring := false
	for _, risk := range view.Plan.Risks {
		recurring = recurring || risk.Code == "hetzner.cost.recurring"
	}
	if !recurring {
		t.Fatalf("plan does not declare the recurring cost risk: %s", body)
	}
	if reconciler.calls != 0 {
		t.Fatal("planning must not touch the project")
	}

	// Only approval hands the plan to the reconciler, and only while it is
	// still current.
	response := request(t, handler, http.MethodPost, "/api/v1/plans/"+view.Plan.ID+"/approve", nil, cookie, map[string]string{"X-CSRF-Token": csrf})
	approved := string(readAll(t, response))
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("approve status = %d: %s", response.StatusCode, approved)
	}
	var run struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(approved), &run); err != nil {
		t.Fatal(err)
	}
	settled := waitForOffsiteRun(t, handler, cookie, run.ID)
	if settled.State != "verified" {
		t.Fatalf("approved run state = %s, checkpoint = %s", settled.State, settled.CurrentCheckpoint)
	}
	if reconciler.calls != 1 {
		t.Fatalf("reconciler calls = %d, want exactly one", reconciler.calls)
	}
	// What reached the reconciler is a configuration rendered from the reviewed
	// plan, carrying this profile's ownership labels and the approved overlay
	// commit in its bootstrap payload.
	configuration := reconciler.files["smallworlds.tf"]
	if !strings.Contains(configuration, `"smallworlds-profile" = "`+profileID+`"`) {
		t.Fatalf("rendered configuration does not claim ownership for this profile:\n%s", configuration)
	}
	if bootstrap := reconciler.files["cloud-init.yaml"]; !strings.Contains(bootstrap, "targetRevision: '"+strings.Repeat("c", 40)+"'") {
		t.Fatalf("bootstrap payload is not pinned to the approved overlay commit:\n%s", bootstrap)
	}
}

func TestHetznerPlanBlocksOnUnresolvedOwnershipAndDelegation(t *testing.T) {
	provider := &fakeHetznerProvider{
		probe:     hetzner.TokenProbe{ProjectID: "p", ReadAuthority: true, WriteAuthority: true},
		catalog:   hetznerCatalogFixture(),
		resources: append(hetznerProjectPrerequisites(), hetzner.Resource{Kind: hetzner.KindFirewall, ProviderID: "fw-1", Name: "smallworlds-firewall"}),
		// The registrar still points elsewhere.
		nameservers: []string{"ns1.registrar.example"},
	}
	handler, cookie, csrf, profileID := newHetznerLauncher(t, provider, nil)

	status, body := planHetzner(t, handler, cookie, csrf, profileID, nil)
	if status != http.StatusCreated {
		t.Fatalf("plan status = %d: %s", status, body)
	}
	var view struct {
		ChangePlan hetzner.ChangePlan `json:"changePlan"`
		Approvable bool               `json:"approvable"`
	}
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, blocker := range view.ChangePlan.Blockers {
		codes[blocker.Code] = true
	}
	if view.Approvable || !codes["adoption-decision-required"] || !codes["nameserver-delegation-required"] {
		t.Fatalf("plan should be blocked: %s", body)
	}

	// Adopting explicitly clears only the adoption blocker — a similarly named
	// resource is never taken over implicitly.
	_, adoptedBody := planHetzner(t, handler, cookie, csrf, profileID, map[string]any{"adoptions": []string{"fw-1"}})
	var adopted struct {
		ChangePlan hetzner.ChangePlan `json:"changePlan"`
	}
	if err := json.Unmarshal([]byte(adoptedBody), &adopted); err != nil {
		t.Fatal(err)
	}
	for _, blocker := range adopted.ChangePlan.Blockers {
		if blocker.Code == "adoption-decision-required" {
			t.Fatal("explicit adoption did not clear the adoption blocker")
		}
	}
	adoptedFirewall := false
	for _, item := range adopted.ChangePlan.Items {
		if item.Kind == hetzner.KindFirewall {
			adoptedFirewall = item.Action == "adopt"
		}
	}
	if !adoptedFirewall {
		t.Fatalf("firewall was not adopted: %s", adoptedBody)
	}
}

func TestHetznerPlanRequiresTokenAndInspectionFirst(t *testing.T) {
	provider := &fakeHetznerProvider{probe: hetzner.TokenProbe{ProjectID: "p", ReadAuthority: true, WriteAuthority: true}, catalog: hetznerCatalogFixture()}
	handler, cookie, csrf, profileID := newHetznerLauncher(t, provider, nil)

	planRequest := map[string]any{"profileId": profileID, "mode": "minimal", "tier": "recommended", "location": "nbg1"}
	if status, body := postHetzner(t, handler, cookie, csrf, "/api/v1/hetzner/plan", planRequest); status != http.StatusConflict {
		t.Fatalf("plan without a token = %d: %s", status, body)
	}
	validateHetznerToken(t, handler, cookie, csrf, profileID, hetznerTestToken)
	if status, body := postHetzner(t, handler, cookie, csrf, "/api/v1/hetzner/plan", planRequest); status != http.StatusConflict {
		t.Fatalf("plan without an inspection = %d: %s", status, body)
	}
	if status, body := postHetzner(t, handler, cookie, csrf, "/api/v1/hetzner/inspect", map[string]string{"profileId": profileID, "domain": "example.org"}); status != http.StatusOK {
		t.Fatalf("inspect status = %d: %s", status, body)
	}
	// An infrastructure choice the provider cannot satisfy is refused.
	planRequest["tier"], planRequest["serverType"], planRequest["volumeGb"] = "advanced", "cx99", 200
	if status, body := postHetzner(t, handler, cookie, csrf, "/api/v1/hetzner/plan", planRequest); status != http.StatusBadRequest {
		t.Fatalf("unknown server type = %d: %s", status, body)
	}
}

func TestHetznerApprovedPlanIsRefusedWhenTheProjectDrifted(t *testing.T) {
	provider := &fakeHetznerProvider{
		probe:       hetzner.TokenProbe{ProjectID: "p", ReadAuthority: true, WriteAuthority: true},
		catalog:     hetznerCatalogFixture(),
		nameservers: hetzner.HetznerNameservers,
		resources:   hetznerProjectPrerequisites(),
	}
	reconciler := &recordingReconciler{}
	handler, cookie, csrf, profileID := newProvisionableHetznerLauncher(t, provider, reconciler)

	status, body := planHetzner(t, handler, cookie, csrf, profileID, nil)
	if status != http.StatusCreated {
		t.Fatalf("plan status = %d: %s", status, body)
	}
	var view struct {
		Plan struct {
			ID string `json:"id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatal(err)
	}

	// Someone creates the server in the project between planning and approval.
	provider.resources = append(provider.resources, hetzner.Resource{Kind: hetzner.KindServer, ProviderID: "srv-1", Name: "cc-pilot-node-01"})

	response := request(t, handler, http.MethodPost, "/api/v1/plans/"+view.Plan.ID+"/approve", nil, cookie, map[string]string{"X-CSRF-Token": csrf})
	approved := string(readAll(t, response))
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("approve status = %d: %s", response.StatusCode, approved)
	}
	var run struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(approved), &run); err != nil {
		t.Fatal(err)
	}
	settled := waitForOffsiteRun(t, handler, cookie, run.ID)
	// The refusal names the fact that moved, so an Operator can see what changed
	// rather than only that something did.
	if settled.State != "failed" || !strings.Contains(settled.CurrentCheckpoint, hetznerprovision.InventoryCheck) {
		t.Fatalf("stale plan run = %+v", settled)
	}
	if reconciler.calls != 0 {
		t.Fatal("a stale plan reached the provider")
	}
}

// Without a published pinned toolchain there is nothing verified to reconcile
// with, and the launcher will not fall back to whatever OpenTofu is installed.
// So an approvable plan cannot even be bound: the refusal happens at planning,
// before an Operator can approve something that could never be applied.
func TestHetznerPlanningRefusesHonestlyWithoutAVerifiedToolchain(t *testing.T) {
	provider := &fakeHetznerProvider{
		probe:       hetzner.TokenProbe{ProjectID: "p", ReadAuthority: true, WriteAuthority: true},
		catalog:     hetznerCatalogFixture(),
		nameservers: hetzner.HetznerNameservers,
	}
	// The project itself is ready — only the launcher's own prerequisites are
	// missing, so the refusal is unambiguously about them.
	provider.resources = hetznerProjectPrerequisites()
	// No toolchain assets and no overlay: the launcher default publishes none.
	handler, cookie, csrf, profileID := newHetznerLauncher(t, provider, nil)
	status, body := planHetzner(t, handler, cookie, csrf, profileID, nil)
	if status != http.StatusConflict {
		t.Fatalf("plan status = %d: %s", status, body)
	}
	if !strings.Contains(body, "gitops_overlay_required") && !strings.Contains(body, "hetzner_toolchain_unavailable") {
		t.Fatalf("refusal does not name the missing prerequisite: %s", body)
	}
}

// A blocked plan is still recorded and shown — the Operator must be able to see
// what it would have cost and why it cannot proceed — but it is never bound, so
// approving it can never reach the provider.
func TestHetznerBlockedPlanIsShownButNeverBound(t *testing.T) {
	provider := &fakeHetznerProvider{
		probe:   hetzner.TokenProbe{ProjectID: "p", ReadAuthority: true, WriteAuthority: true},
		catalog: hetznerCatalogFixture(),
		// The registrar still points elsewhere, so the plan is blocked.
		nameservers: []string{"ns1.registrar.example"},
	}
	reconciler := &recordingReconciler{}
	handler, cookie, csrf, profileID := newProvisionableHetznerLauncher(t, provider, reconciler)

	status, body := planHetzner(t, handler, cookie, csrf, profileID, nil)
	if status != http.StatusCreated {
		t.Fatalf("plan status = %d: %s", status, body)
	}
	var view struct {
		Plan struct {
			ID string `json:"id"`
		} `json:"plan"`
		ChangePlan hetzner.ChangePlan `json:"changePlan"`
		Approvable bool               `json:"approvable"`
	}
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatal(err)
	}
	if view.Approvable || view.ChangePlan.Cost.TotalMonthlyEUR <= 0 {
		t.Fatalf("a blocked plan must still be shown with its cost: %s", body)
	}

	response := request(t, handler, http.MethodPost, "/api/v1/plans/"+view.Plan.ID+"/approve", nil, cookie, map[string]string{"X-CSRF-Token": csrf})
	approved := string(readAll(t, response))
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("approve status = %d: %s", response.StatusCode, approved)
	}
	var run struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(approved), &run); err != nil {
		t.Fatal(err)
	}
	settled := waitForOffsiteRun(t, handler, cookie, run.ID)
	if settled.State != "failed" || settled.CurrentCheckpoint != "binding-missing" {
		t.Fatalf("blocked plan run = %+v, want it refused for lack of a binding", settled)
	}
	if reconciler.calls != 0 {
		t.Fatal("a blocked plan reached the provider")
	}
}

// The temporary administration path opens with the plan that creates the node —
// the Operator needs it to watch the cluster converge — and it is scoped only
// when their address can actually be observed.
func TestHetznerTemporaryAccessOpensUnscopedAndNarrowsOnRequest(t *testing.T) {
	provider := &fakeHetznerProvider{
		probe:       hetzner.TokenProbe{ProjectID: "p", ReadAuthority: true, WriteAuthority: true},
		catalog:     hetznerCatalogFixture(),
		nameservers: hetzner.HetznerNameservers,
		resources:   hetznerProjectPrerequisites(),
	}
	reconciler := &recordingReconciler{}
	handler, cookie, csrf, profileID := newProvisionableHetznerLauncher(t, provider, reconciler)
	if status, body := planHetzner(t, handler, cookie, csrf, profileID, nil); status != http.StatusCreated {
		t.Fatalf("plan status = %d: %s", status, body)
	}

	response := request(t, handler, http.MethodGet, "/api/v1/hetzner?profileId="+profileID, nil, cookie, nil)
	opened := string(readAll(t, response))
	if !strings.Contains(opened, `"open":true`) || !strings.Contains(opened, "operator-address-not-observed") {
		t.Fatalf("temporary access did not open unscoped with a stated reason: %s", opened)
	}

	status, narrowed := postHetzner(t, handler, cookie, csrf, "/api/v1/hetzner/temporary-access/narrow", map[string]string{"profileId": profileID, "operatorAddress": "198.51.100.7"})
	if status != http.StatusOK {
		t.Fatalf("narrow status = %d: %s", status, narrowed)
	}
	if !strings.Contains(narrowed, `"198.51.100.7/32"`) || !strings.Contains(narrowed, "scoped-to-operator-address") {
		t.Fatalf("path was not narrowed to the Operator: %s", narrowed)
	}

	// An address that cannot serve as a scope leaves the path open and says so,
	// rather than producing a rule that admits nobody.
	status, unscoped := postHetzner(t, handler, cookie, csrf, "/api/v1/hetzner/temporary-access/narrow", map[string]string{"profileId": profileID, "operatorAddress": "192.168.178.52"})
	if status != http.StatusOK || !strings.Contains(unscoped, "operator-address-not-publicly-routable") {
		t.Fatalf("a private address should leave the path open with a reason: %d %s", status, unscoped)
	}
	if strings.Contains(unscoped, `"scoped":true`) {
		t.Fatalf("a private address must not scope the path: %s", unscoped)
	}
}

func TestHetznerToolchainRefusesAmbientFallbackButPreparesTheWorkspace(t *testing.T) {
	provider := &fakeHetznerProvider{probe: hetzner.TokenProbe{ProjectID: "p", ReadAuthority: true, WriteAuthority: true}}
	handler, cookie, csrf, profileID := newHetznerLauncher(t, provider, nil)

	// No signed toolchain descriptor is published yet, so the launcher refuses
	// rather than reaching for whatever tofu is on PATH.
	status, body := postHetzner(t, handler, cookie, csrf, "/api/v1/hetzner/toolchain/acquire", map[string]string{"profileId": profileID})
	if status != http.StatusServiceUnavailable {
		t.Fatalf("toolchain status = %d: %s", status, body)
	}
	var view struct {
		Error     string `json:"error"`
		Toolchain struct {
			Ready                 bool   `json:"ready"`
			OpenTofuVersion       string `json:"openTofuVersion"`
			HcloudProviderVersion string `json:"hcloudProviderVersion"`
		} `json:"toolchain"`
		Workspace struct {
			ProfileID string `json:"profileId"`
			Isolated  bool   `json:"isolated"`
			Locked    bool   `json:"locked"`
		} `json:"workspace"`
	}
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatal(err)
	}
	if view.Error != "hetzner_toolchain_unavailable" || view.Toolchain.Ready {
		t.Fatalf("toolchain view %+v", view)
	}
	if view.Toolchain.OpenTofuVersion == "" || view.Toolchain.HcloudProviderVersion == "" {
		t.Fatal("the refusal must still state the pinned versions")
	}
	// The per-profile workspace is prepared and isolated regardless.
	if !view.Workspace.Isolated || view.Workspace.ProfileID != profileID || view.Workspace.Locked {
		t.Fatalf("workspace %+v", view.Workspace)
	}
}

func TestHetznerEndpointsRequireSessionAndCSRF(t *testing.T) {
	provider := &fakeHetznerProvider{probe: hetzner.TokenProbe{ProjectID: "p", ReadAuthority: true, WriteAuthority: true}}
	handler, cookie, csrf, profileID := newHetznerLauncher(t, provider, nil)
	body, _ := json.Marshal(map[string]string{"profileId": profileID, "token": hetznerTestToken})
	for _, path := range []string{"/api/v1/hetzner/token/validate", "/api/v1/hetzner/inspect", "/api/v1/hetzner/presets", "/api/v1/hetzner/plan", "/api/v1/hetzner/toolchain/acquire"} {
		if response := request(t, handler, http.MethodPost, path, body, nil, nil); response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s without a session = %d", path, response.StatusCode)
		}
		if response := request(t, handler, http.MethodPost, path, body, cookie, nil); response.StatusCode != http.StatusForbidden {
			t.Fatalf("%s without a CSRF token = %d", path, response.StatusCode)
		}
		if response := request(t, handler, http.MethodGet, path, nil, cookie, map[string]string{"X-CSRF-Token": csrf}); response.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s as GET = %d", path, response.StatusCode)
		}
	}
	if response := request(t, handler, http.MethodGet, "/api/v1/hetzner?profileId="+profileID, nil, nil, nil); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status without a session = %d", response.StatusCode)
	}
}
