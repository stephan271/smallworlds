package launcher_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
)

func TestOneTimeLaunchTokenCreatesSession(t *testing.T) {
	handler, err := launcher.New(launcher.Config{
		DataDir:     t.TempDir(),
		LaunchToken: "one-time-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	response := request(t, handler, http.MethodGet, "/api/v1/session", nil, nil, nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated session status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
	response.Body.Close()

	body, err := json.Marshal(map[string]string{"token": "one-time-token"})
	if err != nil {
		t.Fatal(err)
	}
	response = request(t, handler, http.MethodPost, "/api/v1/session/exchange", body, nil, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("exchange status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var exchange struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.NewDecoder(response.Body).Decode(&exchange); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if exchange.CSRFToken == "" {
		t.Fatal("exchange returned an empty CSRF token")
	}
	setCookie := response.Header.Get("Set-Cookie")
	if !containsAll(setCookie, "HttpOnly", "SameSite=Strict") {
		t.Fatalf("session cookie lacks security attributes: %q", setCookie)
	}
	sessionCookie := response.Cookies()[0]
	if sessionCookie.MaxAge <= 0 {
		t.Fatal("session cookie will not survive a browser close while the launcher remains active")
	}

	response = request(t, handler, http.MethodGet, "/api/v1/session", nil, sessionCookie, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("authenticated session status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	response.Body.Close()

	response = request(t, handler, http.MethodPost, "/api/v1/session/exchange", body, nil, nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reused token status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
}

func TestClusterProfilesRemainDistinctAfterLauncherRestart(t *testing.T) {
	dataDir := t.TempDir()
	firstLauncher, err := launcher.New(launcher.Config{DataDir: dataDir, LaunchToken: "first-launch"})
	if err != nil {
		t.Fatal(err)
	}
	firstCookie, firstCSRF := exchange(t, firstLauncher, "first-launch")

	for _, name := range []string{"Production", "Workshop"} {
		body, err := json.Marshal(map[string]string{
			"name":           name,
			"language":       "en",
			"deploymentMode": "local-lan",
		})
		if err != nil {
			t.Fatal(err)
		}
		response := request(t, firstLauncher, http.MethodPost, "/api/v1/profiles", body, firstCookie, map[string]string{"X-CSRF-Token": firstCSRF})
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("create profile %q status = %d, want %d", name, response.StatusCode, http.StatusCreated)
		}
		response.Body.Close()
	}

	secondLauncher, err := launcher.New(launcher.Config{DataDir: dataDir, LaunchToken: "second-launch"})
	if err != nil {
		t.Fatal(err)
	}
	secondCookie, _ := exchange(t, secondLauncher, "second-launch")
	response := request(t, secondLauncher, http.MethodGet, "/api/v1/profiles", nil, secondCookie, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list profiles status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	defer response.Body.Close()
	var profiles []struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		Language       string `json:"language"`
		DeploymentMode string `json:"deploymentMode"`
		Revision       int64  `json:"revision"`
	}
	if err := json.NewDecoder(response.Body).Decode(&profiles); err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 2 {
		t.Fatalf("profile count = %d, want 2", len(profiles))
	}
	if profiles[0].ID == "" || profiles[1].ID == "" || profiles[0].ID == profiles[1].ID {
		t.Fatalf("profiles do not have distinct stable identities: %#v", profiles)
	}
	if profiles[0].Name != "Production" || profiles[1].Name != "Workshop" {
		t.Fatalf("profiles = %#v, want creation order and names preserved", profiles)
	}
}

func TestSetupJourneyRecommendsNextTaskAfterProfileRevision(t *testing.T) {
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "journey-launch"})
	if err != nil {
		t.Fatal(err)
	}
	cookie, csrf := exchange(t, handler, "journey-launch")
	profile := createProfile(t, handler, cookie, csrf, "Workshop", "en", "local-lan")

	updateBody, err := json.Marshal(map[string]string{
		"name":           "Werkstatt",
		"language":       "de",
		"deploymentMode": "local-lan",
	})
	if err != nil {
		t.Fatal(err)
	}
	response := request(t, handler, http.MethodPut, "/api/v1/profiles/"+profile.ID, updateBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("update profile status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var updated profileResponse
	if err := json.NewDecoder(response.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if updated.Name != "Werkstatt" || updated.Language != "de" || updated.Revision != 2 {
		t.Fatalf("updated profile = %#v, want revised German profile at revision 2", updated)
	}

	response = request(t, handler, http.MethodGet, "/api/v1/profiles/"+profile.ID+"/journey", nil, cookie, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("get journey status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	defer response.Body.Close()
	var journey struct {
		ProfileID string `json:"profileId"`
		Tasks     []struct {
			ID          string `json:"id"`
			State       string `json:"state"`
			Recommended bool   `json:"recommended"`
		} `json:"tasks"`
	}
	if err := json.NewDecoder(response.Body).Decode(&journey); err != nil {
		t.Fatal(err)
	}
	if journey.ProfileID != profile.ID || len(journey.Tasks) != 1 {
		t.Fatalf("journey = %#v, want one task for profile %q", journey, profile.ID)
	}
	if journey.Tasks[0].ID != "verify-launcher" || journey.Tasks[0].State != "ready" || !journey.Tasks[0].Recommended {
		t.Fatalf("task = %#v, want recommended ready verify-launcher task", journey.Tasks[0])
	}
}

func TestChangedProfileInvalidatesPlanApproval(t *testing.T) {
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "plan-launch"})
	if err != nil {
		t.Fatal(err)
	}
	cookie, csrf := exchange(t, handler, "plan-launch")
	profile := createProfile(t, handler, cookie, csrf, "Workshop", "en", "local-lan")

	planBody, err := json.Marshal(map[string]string{"profileId": profile.ID, "intent": "VerifyLauncher"})
	if err != nil {
		t.Fatal(err)
	}
	response := request(t, handler, http.MethodPost, "/api/v1/plans", planBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create plan status = %d, want %d", response.StatusCode, http.StatusCreated)
	}
	var plan struct {
		ID     string `json:"id"`
		Digest string `json:"digest"`
		Status string `json:"status"`
		Risks  []struct {
			Code string `json:"code"`
		} `json:"risks"`
		Effects []struct {
			Code string `json:"code"`
		} `json:"effects"`
		Preconditions struct {
			ProfileRevision int64 `json:"profileRevision"`
		} `json:"preconditions"`
	}
	if err := json.NewDecoder(response.Body).Decode(&plan); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if plan.ID == "" || plan.Digest == "" || plan.Status != "planned" || plan.Preconditions.ProfileRevision != 1 {
		t.Fatalf("plan lacks immutable review data: %#v", plan)
	}
	if len(plan.Effects) == 0 || plan.Effects[0].Code != "launcher.verification.recorded" {
		t.Fatalf("plan effects = %#v, want launcher verification effect", plan.Effects)
	}

	updateBody, err := json.Marshal(map[string]string{
		"name":           "Changed workshop",
		"language":       "en",
		"deploymentMode": "local-lan",
	})
	if err != nil {
		t.Fatal(err)
	}
	response = request(t, handler, http.MethodPut, "/api/v1/profiles/"+profile.ID, updateBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("update profile status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	response.Body.Close()

	response = request(t, handler, http.MethodPost, "/api/v1/plans/"+plan.ID+"/approve", nil, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("approve stale plan status = %d, want %d", response.StatusCode, http.StatusConflict)
	}
	defer response.Body.Close()
	var failure struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(response.Body).Decode(&failure); err != nil {
		t.Fatal(err)
	}
	if failure.Code != "plan_precondition_changed" {
		t.Fatalf("stale plan failure code = %q, want plan_precondition_changed", failure.Code)
	}
}

func TestApprovedPlanProducesDurableVerifiedWorkflowRun(t *testing.T) {
	dataDir := t.TempDir()
	firstLauncher, err := launcher.New(launcher.Config{DataDir: dataDir, LaunchToken: "run-launch"})
	if err != nil {
		t.Fatal(err)
	}
	cookie, csrf := exchange(t, firstLauncher, "run-launch")
	profile := createProfile(t, firstLauncher, cookie, csrf, "Workshop", "en", "local-lan")
	planID := createVerificationPlan(t, firstLauncher, cookie, csrf, profile.ID)

	response := request(t, firstLauncher, http.MethodPost, "/api/v1/plans/"+planID+"/approve", nil, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("approve plan status = %d, want %d", response.StatusCode, http.StatusAccepted)
	}
	var approved workflowRunResponse
	if err := json.NewDecoder(response.Body).Decode(&approved); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if approved.ID == "" || approved.PlanID != planID || approved.State != "running" {
		t.Fatalf("approved run = %#v, want running run for plan", approved)
	}

	var verified workflowRunResponse
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		response = request(t, firstLauncher, http.MethodGet, "/api/v1/runs/"+approved.ID, nil, cookie, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("get run status = %d, want %d", response.StatusCode, http.StatusOK)
		}
		if err := json.NewDecoder(response.Body).Decode(&verified); err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if verified.State == "verified" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if verified.State != "verified" || verified.CurrentCheckpoint != "verification-complete" {
		t.Fatalf("run did not verify: %#v", verified)
	}
	if verified.Verification.Code != "launcher.responding" || verified.Verification.ObservedAt.IsZero() {
		t.Fatalf("verification evidence = %#v, want observed launcher evidence", verified.Verification)
	}

	if err := firstLauncher.Close(); err != nil {
		t.Fatal(err)
	}
	secondLauncher, err := launcher.New(launcher.Config{DataDir: dataDir, LaunchToken: "reopened-launch"})
	if err != nil {
		t.Fatal(err)
	}
	secondCookie, _ := exchange(t, secondLauncher, "reopened-launch")
	response = request(t, secondLauncher, http.MethodGet, "/api/v1/runs/"+approved.ID, nil, secondCookie, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("get run after restart status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	defer response.Body.Close()
	var reopened workflowRunResponse
	if err := json.NewDecoder(response.Body).Decode(&reopened); err != nil {
		t.Fatal(err)
	}
	if reopened.State != "verified" || reopened.Verification.Code != "launcher.responding" {
		t.Fatalf("reopened run = %#v, want durable verification", reopened)
	}
}

func TestWorkflowEventsResumeAfterLastEventIDWithoutDuplicates(t *testing.T) {
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "events-launch"})
	if err != nil {
		t.Fatal(err)
	}
	cookie, csrf := exchange(t, handler, "events-launch")
	profile := createProfile(t, handler, cookie, csrf, "Workshop", "en", "local-lan")
	planID := createVerificationPlan(t, handler, cookie, csrf, profile.ID)
	response := request(t, handler, http.MethodPost, "/api/v1/plans/"+planID+"/approve", nil, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("approve plan status = %d, want %d", response.StatusCode, http.StatusAccepted)
	}
	var run workflowRunResponse
	if err := json.NewDecoder(response.Body).Decode(&run); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	waitForVerifiedRun(t, handler, cookie, run.ID)

	response = request(t, handler, http.MethodGet, "/api/v1/events?profileId="+profile.ID+"&cursor=0", nil, cookie, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("events content type = %q, want text/event-stream", contentType)
	}
	eventStream, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	streamText := string(eventStream)
	for _, eventType := range []string{"run.started", "run.checkpoint", "run.verified"} {
		if !strings.Contains(streamText, `"type":"`+eventType+`"`) {
			t.Fatalf("event stream lacks %q event: %s", eventType, streamText)
		}
	}
	lastID := lastSSEID(t, streamText)

	response = request(t, handler, http.MethodGet, "/api/v1/events?profileId="+profile.ID, nil, cookie, map[string]string{"Last-Event-ID": strconv.FormatInt(lastID, 10)})
	resumed, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if strings.Contains(string(resumed), "id:") {
		t.Fatalf("resumed stream repeated an event after cursor %d: %s", lastID, resumed)
	}
}

func TestCancellationStopsWorkflowRunAtSafeCheckpoint(t *testing.T) {
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "cancel-launch"})
	if err != nil {
		t.Fatal(err)
	}
	cookie, csrf := exchange(t, handler, "cancel-launch")
	profile := createProfile(t, handler, cookie, csrf, "Workshop", "en", "local-lan")
	planID := createVerificationPlan(t, handler, cookie, csrf, profile.ID)
	response := request(t, handler, http.MethodPost, "/api/v1/plans/"+planID+"/approve", nil, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("approve plan status = %d, want %d", response.StatusCode, http.StatusAccepted)
	}
	var run workflowRunResponse
	if err := json.NewDecoder(response.Body).Decode(&run); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	response = request(t, handler, http.MethodPost, "/api/v1/runs/"+run.ID+"/cancel", nil, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("cancel run status = %d, want %d", response.StatusCode, http.StatusAccepted)
	}
	response.Body.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		response = request(t, handler, http.MethodGet, "/api/v1/runs/"+run.ID, nil, cookie, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("get cancelled run status = %d, want %d", response.StatusCode, http.StatusOK)
		}
		if err := json.NewDecoder(response.Body).Decode(&run); err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if run.State == "cancelled" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if run.State != "cancelled" || run.CurrentCheckpoint != "execution-complete" || run.CancellationState != "completed" {
		t.Fatalf("cancelled run = %#v, want cancellation at execution-complete checkpoint", run)
	}
	if run.Verification.Code != "" {
		t.Fatalf("cancelled run unexpectedly has verification evidence: %#v", run.Verification)
	}
}

func TestActiveWorkflowRunResumesAfterLauncherRestart(t *testing.T) {
	dataDir := t.TempDir()
	firstLauncher, err := launcher.New(launcher.Config{DataDir: dataDir, LaunchToken: "before-restart"})
	if err != nil {
		t.Fatal(err)
	}
	cookie, csrf := exchange(t, firstLauncher, "before-restart")
	profile := createProfile(t, firstLauncher, cookie, csrf, "Workshop", "en", "local-lan")
	planID := createVerificationPlan(t, firstLauncher, cookie, csrf, profile.ID)
	response := request(t, firstLauncher, http.MethodPost, "/api/v1/plans/"+planID+"/approve", nil, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("approve plan status = %d, want %d", response.StatusCode, http.StatusAccepted)
	}
	var run workflowRunResponse
	if err := json.NewDecoder(response.Body).Decode(&run); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if err := firstLauncher.Close(); err != nil {
		t.Fatal(err)
	}

	restartedLauncher, err := launcher.New(launcher.Config{DataDir: dataDir, LaunchToken: "after-restart"})
	if err != nil {
		t.Fatal(err)
	}
	restartedCookie, _ := exchange(t, restartedLauncher, "after-restart")
	verified := waitForVerifiedRun(t, restartedLauncher, restartedCookie, run.ID)
	if verified.CurrentCheckpoint != "verification-complete" || verified.Verification.Code != "launcher.responding" {
		t.Fatalf("resumed run = %#v, want verified evidence after restart", verified)
	}
}

func TestCompletedJourneyTaskIsRevalidatedAfterProfileChange(t *testing.T) {
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "revalidate-launch"})
	if err != nil {
		t.Fatal(err)
	}
	cookie, csrf := exchange(t, handler, "revalidate-launch")
	profile := createProfile(t, handler, cookie, csrf, "Workshop", "en", "local-lan")
	planID := createVerificationPlan(t, handler, cookie, csrf, profile.ID)
	response := request(t, handler, http.MethodPost, "/api/v1/plans/"+planID+"/approve", nil, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("approve plan status = %d, want %d", response.StatusCode, http.StatusAccepted)
	}
	var run workflowRunResponse
	if err := json.NewDecoder(response.Body).Decode(&run); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	waitForVerifiedRun(t, handler, cookie, run.ID)

	completed := getJourneyTask(t, handler, cookie, profile.ID)
	if completed.State != "completed" || completed.Recommended || completed.EvidenceState != "current" || completed.EvidenceCode != "launcher.responding" {
		t.Fatalf("completed task = %#v, want current verification evidence", completed)
	}

	updateBody, err := json.Marshal(map[string]string{
		"name":           "Changed workshop",
		"language":       "en",
		"deploymentMode": "local-lan",
	})
	if err != nil {
		t.Fatal(err)
	}
	response = request(t, handler, http.MethodPut, "/api/v1/profiles/"+profile.ID, updateBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("update profile status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	response.Body.Close()

	revalidated := getJourneyTask(t, handler, cookie, profile.ID)
	if revalidated.State != "ready" || !revalidated.Recommended || revalidated.EvidenceState != "stale" {
		t.Fatalf("revalidated task = %#v, want stale evidence and a recommended rerun", revalidated)
	}
}

type journeyTaskResponse struct {
	ID            string `json:"id"`
	State         string `json:"state"`
	Recommended   bool   `json:"recommended"`
	EvidenceState string `json:"evidenceState"`
	EvidenceCode  string `json:"evidenceCode"`
}

func getJourneyTask(t *testing.T, handler http.Handler, cookie *http.Cookie, profileID string) journeyTaskResponse {
	t.Helper()
	response := request(t, handler, http.MethodGet, "/api/v1/profiles/"+profileID+"/journey", nil, cookie, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("get journey status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	defer response.Body.Close()
	var journey struct {
		Tasks []journeyTaskResponse `json:"tasks"`
	}
	if err := json.NewDecoder(response.Body).Decode(&journey); err != nil {
		t.Fatal(err)
	}
	if len(journey.Tasks) != 1 {
		t.Fatalf("journey task count = %d, want 1", len(journey.Tasks))
	}
	return journey.Tasks[0]
}

func waitForVerifiedRun(t *testing.T, handler http.Handler, cookie *http.Cookie, runID string) workflowRunResponse {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		response := request(t, handler, http.MethodGet, "/api/v1/runs/"+runID, nil, cookie, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("get run status = %d, want %d", response.StatusCode, http.StatusOK)
		}
		var run workflowRunResponse
		if err := json.NewDecoder(response.Body).Decode(&run); err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if run.State == "verified" {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %s did not verify before deadline", runID)
	return workflowRunResponse{}
}

func lastSSEID(t *testing.T, stream string) int64 {
	t.Helper()
	var last int64
	for _, line := range strings.Split(stream, "\n") {
		if !strings.HasPrefix(line, "id:") {
			continue
		}
		value, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "id:")), 10, 64)
		if err != nil {
			t.Fatalf("invalid SSE id in %q: %v", line, err)
		}
		last = value
	}
	if last == 0 {
		t.Fatalf("event stream has no id: %s", stream)
	}
	return last
}

type workflowRunResponse struct {
	ID                string `json:"id"`
	PlanID            string `json:"planId"`
	ProfileID         string `json:"profileId"`
	State             string `json:"state"`
	CurrentCheckpoint string `json:"currentCheckpoint"`
	CancellationState string `json:"cancellationState"`
	Verification      struct {
		Code       string    `json:"code"`
		ObservedAt time.Time `json:"observedAt"`
	} `json:"verification"`
}

func createVerificationPlan(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, profileID string) string {
	t.Helper()
	body, err := json.Marshal(map[string]string{"profileId": profileID, "intent": "VerifyLauncher"})
	if err != nil {
		t.Fatal(err)
	}
	response := request(t, handler, http.MethodPost, "/api/v1/plans", body, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create plan status = %d, want %d", response.StatusCode, http.StatusCreated)
	}
	defer response.Body.Close()
	var plan struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&plan); err != nil {
		t.Fatal(err)
	}
	return plan.ID
}

type profileResponse struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Language       string `json:"language"`
	DeploymentMode string `json:"deploymentMode"`
	Revision       int64  `json:"revision"`
}

func createProfile(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, name, language, deploymentMode string) profileResponse {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"name":           name,
		"language":       language,
		"deploymentMode": deploymentMode,
	})
	if err != nil {
		t.Fatal(err)
	}
	response := request(t, handler, http.MethodPost, "/api/v1/profiles", body, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create profile status = %d, want %d", response.StatusCode, http.StatusCreated)
	}
	defer response.Body.Close()
	var profile profileResponse
	if err := json.NewDecoder(response.Body).Decode(&profile); err != nil {
		t.Fatal(err)
	}
	return profile
}

func exchange(t *testing.T, handler http.Handler, token string) (*http.Cookie, string) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		t.Fatal(err)
	}
	response := request(t, handler, http.MethodPost, "/api/v1/session/exchange", body, nil, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("exchange status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	defer response.Body.Close()
	var result struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return response.Cookies()[0], result.CSRFToken
}

func request(t *testing.T, handler http.Handler, method, path string, body []byte, cookie *http.Cookie, headers map[string]string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, "http://127.0.0.1"+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Result()
}

func containsAll(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !bytes.Contains([]byte(value), []byte(fragment)) {
			return false
		}
	}
	return true
}
