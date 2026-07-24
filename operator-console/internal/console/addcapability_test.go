package console

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/addcapability"
	"github.com/stephan271/smallworlds/operator-console/internal/assessment"
	"github.com/stephan271/smallworlds/operator-console/internal/capability"
	"github.com/stephan271/smallworlds/operator-console/internal/consoleauth"
)

// mapAssessor reports a per-capability state from a mutable map, defaulting to
// disabled. It lets a test simulate the pre-add (disabled) and post-merge
// (installing/healthy) observations of a capability.
type mapAssessor struct {
	states map[string]assessment.CapabilityState
}

func (a *mapAssessor) Assess(_ context.Context, ref assessment.CapabilityRef) assessment.CapabilityAssessment {
	state := a.states[ref.ID]
	if state == "" {
		state = assessment.StateDisabled
	}
	return assessment.CapabilityAssessment{CapabilityID: ref.ID, State: state, ReasonCode: assessment.ReasonHealthy}
}

// healthyPlatform marks every platform service healthy and leaves community apps
// disabled — the state of a freshly bootstrapped cluster.
func healthyPlatform() map[string]assessment.CapabilityState {
	states := map[string]assessment.CapabilityState{}
	for _, entry := range capability.DefaultCatalog().Capabilities {
		if entry.Category == capability.PlatformService {
			states[entry.ID] = assessment.StateHealthy
		}
	}
	return states
}

type fakeCapacity struct {
	capacity addcapability.Capacity
	err      error
}

func (f fakeCapacity) Capacity(context.Context) (addcapability.Capacity, error) {
	return f.capacity, f.err
}

type fakeOpener struct {
	files  map[string]string
	title  string
	body   string
	result OpenedProposal
	err    error
}

func (f *fakeOpener) OpenProposal(_ context.Context, title, body string, files map[string]string) (OpenedProposal, error) {
	f.title, f.body, f.files = title, body, files
	return f.result, f.err
}

func newAdditionServer(t *testing.T, exchanger consoleauth.TokenExchanger, assessor CapabilityAssessor, capacity CapacityReporter, opener ProposalOpener) *Server {
	t.Helper()
	rich := capability.DefaultCatalog()
	refs := make([]assessment.CapabilityRef, 0, len(rich.Capabilities))
	for _, entry := range rich.Capabilities {
		refs = append(refs, assessment.CapabilityRef{ID: entry.ID, Exposure: assessment.ExposurePrivate})
	}
	server, err := New(Config{
		Issuer:                testIssuer,
		ClientID:              testClientID,
		AuthorizationEndpoint: testIssuer + "/protocol/openid-connect/auth",
		RedirectURI:           "https://console.test/api/v1/auth/callback",
		Exchanger:             exchanger,
		Assessor:              assessor,
		Catalog:               refs,
		RichCatalog:           rich,
		DeploymentMode:        capability.Hetzner,
		OverlayTarget:         addcapability.OverlayTarget{Release: "v1.2.20", RepositoryURL: "https://github.com/community/overlay"},
		Capacity:              capacity,
		Proposals:             opener,
		BaseDomain:            "sw.example.internal",
		SessionKey:            []byte("0123456789abcdef0123456789abcdef"),
		Leeway:                30 * time.Second,
		Now:                   func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return server
}

func post(t *testing.T, server *Server, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	for _, cookie := range cookies {
		if cookie != nil {
			request.AddCookie(cookie)
		}
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

func TestAddCapabilityHappyPath(t *testing.T) {
	exchanger := &fakeExchanger{}
	assessor := &mapAssessor{states: healthyPlatform()}
	capacity := fakeCapacity{capacity: addcapability.Capacity{AllocatableMemoryMi: 8192, AllocatableStorageGi: 200, UsedMemoryMi: 1024, UsedStorageGi: 20}}
	opener := &fakeOpener{result: OpenedProposal{Provider: "github", Branch: "add-excalidraw", Commit: "abc123def456", URL: "https://github.com/community/overlay/pull/7"}}
	server := newAdditionServer(t, exchanger, assessor, capacity, opener)
	session := loginSession(t, server, exchanger, "operator")

	// Offers include the disabled optional excalidraw, exclude required keycloak.
	offers := get(t, server, "/api/v1/additions/offers", session)
	if offers.Code != http.StatusOK {
		t.Fatalf("offers status = %d, want 200", offers.Code)
	}
	var offersBody struct {
		Offers []addcapability.Offer `json:"offers"`
	}
	json.Unmarshal(offers.Body.Bytes(), &offersBody)
	ids := map[string]bool{}
	for _, offer := range offersBody.Offers {
		ids[offer.ID] = true
	}
	if !ids["excalidraw"] || ids["keycloak"] {
		t.Fatalf("offers = %v, want excalidraw offered and keycloak not", ids)
	}

	// Plan discloses resources, capacity, and implications and renders the diff.
	planResp := post(t, server, "/api/v1/additions/plan", `{"capabilityId":"excalidraw"}`, session)
	if planResp.Code != http.StatusCreated {
		t.Fatalf("plan status = %d, want 201; body=%s", planResp.Code, planResp.Body)
	}
	var planBody struct {
		PlanID string             `json:"planId"`
		Plan   addcapability.Plan `json:"plan"`
	}
	json.Unmarshal(planResp.Body.Bytes(), &planBody)
	if planBody.PlanID == "" || !planBody.Plan.Resources.Fits() || planBody.Plan.GitDiff == "" {
		t.Fatalf("unexpected plan body: %+v", planBody)
	}

	// Proposing before approval is refused.
	if early := post(t, server, "/api/v1/additions/"+planBody.PlanID+"/propose", "", session); early.Code != http.StatusConflict {
		t.Fatalf("propose before approval = %d, want 409", early.Code)
	}

	// Approve, then propose opens a proposal carrying the catalog-derived files.
	if approve := post(t, server, "/api/v1/additions/"+planBody.PlanID+"/approve", "", session); approve.Code != http.StatusOK {
		t.Fatalf("approve status = %d, want 200", approve.Code)
	}
	propose := post(t, server, "/api/v1/additions/"+planBody.PlanID+"/propose", "", session)
	if propose.Code != http.StatusCreated {
		t.Fatalf("propose status = %d, want 201; body=%s", propose.Code, propose.Body)
	}
	var proposeBody struct {
		Commit        string `json:"commit"`
		MergeObserved bool   `json:"mergeObserved"`
	}
	json.Unmarshal(propose.Body.Bytes(), &proposeBody)
	if proposeBody.Commit != "abc123def456" || !proposeBody.MergeObserved {
		t.Fatalf("propose body = %+v, want observed merge with commit", proposeBody)
	}
	if _, ok := opener.files["applications/excalidraw.yaml"]; !ok {
		t.Fatalf("proposal files = %v, want the catalog-derived enable file", opener.files)
	}

	// The Activity Record shows the plan and the run's remote commit identity.
	proposals := get(t, server, "/api/v1/proposals", session)
	var workspace struct {
		Proposals []additionPlanView `json:"proposals"`
		Runs      []additionRunView  `json:"runs"`
	}
	json.Unmarshal(proposals.Body.Bytes(), &workspace)
	if len(workspace.Proposals) != 1 || !workspace.Proposals[0].Approved {
		t.Fatalf("proposals = %+v, want one approved plan", workspace.Proposals)
	}
	if len(workspace.Runs) != 1 || !strings.Contains(workspace.Runs[0].EvidenceSummary, "abc123def456") {
		t.Fatalf("runs = %+v, want commit identity in the Activity Record", workspace.Runs)
	}
}

func TestAddCapabilityAuthorization(t *testing.T) {
	exchanger := &fakeExchanger{}
	assessor := &mapAssessor{states: healthyPlatform()}
	server := newAdditionServer(t, exchanger, assessor, fakeCapacity{capacity: addcapability.Capacity{AllocatableMemoryMi: 8192, AllocatableStorageGi: 200}}, &fakeOpener{})

	// Observer is rejected on every add-capability route.
	observer := loginSession(t, server, exchanger, "observer")
	if code := get(t, server, "/api/v1/additions/offers", observer).Code; code != http.StatusForbidden {
		t.Errorf("observer offers = %d, want 403", code)
	}
	if code := post(t, server, "/api/v1/additions/plan", `{"capabilityId":"excalidraw"}`, observer).Code; code != http.StatusForbidden {
		t.Errorf("observer plan = %d, want 403", code)
	}
	if code := post(t, server, "/api/v1/additions/x/approve", "", observer).Code; code != http.StatusForbidden {
		t.Errorf("observer approve = %d, want 403", code)
	}

	// A user without any Console Role is rejected before login even establishes.
	anon := post(t, server, "/api/v1/additions/plan", `{"capabilityId":"excalidraw"}`)
	if anon.Code != http.StatusUnauthorized {
		t.Errorf("anonymous plan = %d, want 401", anon.Code)
	}
}

func TestAddCapabilityCapacityUnavailable(t *testing.T) {
	exchanger := &fakeExchanger{}
	assessor := &mapAssessor{states: healthyPlatform()}
	// No capacity reporter wired: planning must refuse honestly.
	server := newAdditionServer(t, exchanger, assessor, nil, &fakeOpener{})
	session := loginSession(t, server, exchanger, "operator")

	resp := post(t, server, "/api/v1/additions/plan", `{"capabilityId":"excalidraw"}`, session)
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("plan status = %d, want 503 when capacity is unavailable", resp.Code)
	}
}

func TestAddCapabilityProposalUnavailable(t *testing.T) {
	exchanger := &fakeExchanger{}
	assessor := &mapAssessor{states: healthyPlatform()}
	// No proposal opener wired: proposing must refuse honestly.
	server := newAdditionServer(t, exchanger, assessor, fakeCapacity{capacity: addcapability.Capacity{AllocatableMemoryMi: 8192, AllocatableStorageGi: 200}}, nil)
	session := loginSession(t, server, exchanger, "operator")

	planResp := post(t, server, "/api/v1/additions/plan", `{"capabilityId":"excalidraw"}`, session)
	var planBody struct {
		PlanID string `json:"planId"`
	}
	json.Unmarshal(planResp.Body.Bytes(), &planBody)
	post(t, server, "/api/v1/additions/"+planBody.PlanID+"/approve", "", session)
	propose := post(t, server, "/api/v1/additions/"+planBody.PlanID+"/propose", "", session)
	if propose.Code != http.StatusServiceUnavailable {
		t.Fatalf("propose status = %d, want 503 when the proposal path is unavailable", propose.Code)
	}
}

func TestAddCapabilityApprovalBindsExactDiff(t *testing.T) {
	exchanger := &fakeExchanger{}
	assessor := &mapAssessor{states: healthyPlatform()}
	opener := &fakeOpener{result: OpenedProposal{Provider: "github", Commit: "c1"}}
	server := newAdditionServer(t, exchanger, assessor, fakeCapacity{capacity: addcapability.Capacity{AllocatableMemoryMi: 16384, AllocatableStorageGi: 500}}, opener)
	session := loginSession(t, server, exchanger, "operator")

	// Plan collabora while nextcloud is disabled: the diff adds both.
	planResp := post(t, server, "/api/v1/additions/plan", `{"capabilityId":"collabora"}`, session)
	var planBody struct {
		PlanID string `json:"planId"`
	}
	json.Unmarshal(planResp.Body.Bytes(), &planBody)
	post(t, server, "/api/v1/additions/"+planBody.PlanID+"/approve", "", session)

	// Between approval and proposal, nextcloud becomes present, shrinking the
	// diff. The proposal must refuse rather than commit a diff nobody approved.
	assessor.states["nextcloud"] = assessment.StateHealthy
	propose := post(t, server, "/api/v1/additions/"+planBody.PlanID+"/propose", "", session)
	if propose.Code != http.StatusConflict {
		t.Fatalf("propose after state drift = %d, want 409 (diff mismatch)", propose.Code)
	}
	if opener.files != nil {
		t.Fatal("no proposal must be opened when the diff no longer matches the approval")
	}
}

func TestPostMergeAssessmentDrivesState(t *testing.T) {
	exchanger := &fakeExchanger{}
	// Simulate the post-merge world: excalidraw is now installing per Argo/runtime
	// evidence, not disabled.
	states := healthyPlatform()
	states["excalidraw"] = assessment.StateInstalling
	server := newAdditionServer(t, exchanger, &mapAssessor{states: states}, fakeCapacity{}, &fakeOpener{})
	session := loginSession(t, server, exchanger, "operator")

	// It is no longer offered (already being added)...
	offers := get(t, server, "/api/v1/additions/offers", session)
	var offersBody struct {
		Offers []addcapability.Offer `json:"offers"`
	}
	json.Unmarshal(offers.Body.Bytes(), &offersBody)
	for _, offer := range offersBody.Offers {
		if offer.ID == "excalidraw" {
			t.Fatal("a capability being installed must not be offered again")
		}
	}
	// ...and its Capability Assessment reflects the delivery evidence.
	detail := get(t, server, "/api/v1/capabilities/excalidraw", session)
	var view struct {
		State string `json:"state"`
	}
	json.Unmarshal(detail.Body.Bytes(), &view)
	if view.State != string(assessment.StateInstalling) {
		t.Fatalf("assessment state = %q, want installing (evidence-driven)", view.State)
	}
}

func TestNoRemovalAction(t *testing.T) {
	exchanger := &fakeExchanger{}
	server := newAdditionServer(t, exchanger, &mapAssessor{states: healthyPlatform()}, fakeCapacity{}, &fakeOpener{})
	session := loginSession(t, server, exchanger, "operator")

	// There is no remove/disable route: a DELETE against a capability is refused.
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/capabilities/nextcloud", nil)
	request.AddCookie(session)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code >= 200 && recorder.Code < 300 {
		t.Fatalf("DELETE capability = %d, want a non-success (no removal in the first release)", recorder.Code)
	}
}
