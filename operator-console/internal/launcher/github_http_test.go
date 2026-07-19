package launcher_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/github"
	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
)

func TestGitHubTokenValidationStoresOnlySafeMetadata(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-OAuth-Scopes", "repo")
		response.Header().Set("GitHub-Authentication-Token-Expiration", "2032-01-02 03:04:05 UTC")
		_, _ = response.Write([]byte(`{"login":"octocat"}`))
	}))
	defer provider.Close()
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "github-launch", GitHubClient: github.New(provider.URL, provider.Client())})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "github-launch")
	profile := createProfile(t, handler, cookie, csrf, "GitHub", "en", "local-lan")
	unlockVaultForRecoveryTest(t, handler, cookie, csrf)
	secret := "github_pat_never_return_this"
	body, _ := json.Marshal(map[string]string{"profileId": profile.ID, "token": secret, "authority": "creation"})
	response := request(t, handler, http.MethodPost, "/api/v1/github/token/validate", body, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if bytes.Contains(readAll(t, response), []byte(secret)) {
		t.Fatal("token validation response exposes secret")
	}
	ongoingBody, _ := json.Marshal(map[string]string{"profileId": profile.ID, "token": "github_pat_replacement", "authority": "ongoing"})
	response = request(t, handler, http.MethodPost, "/api/v1/github/token/validate", ongoingBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("ongoing validation status = %d", response.StatusCode)
	}
	response.Body.Close()
	response = request(t, handler, http.MethodGet, "/api/v1/profiles/"+profile.ID+"/credentials", nil, cookie, nil)
	metadata := readAll(t, response)
	if bytes.Contains(metadata, []byte("github-creation-token")) || !bytes.Contains(metadata, []byte("github-ongoing-token")) {
		t.Fatalf("token rotation metadata = %s", metadata)
	}
}

func TestApprovedCapabilityPlanEstablishesGitHubOverlayAndRecordsIdentity(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/user":
			response.Header().Set("X-OAuth-Scopes", "repo")
			_, _ = response.Write([]byte(`{"login":"octocat"}`))
		case "/user/repos":
			_, _ = response.Write([]byte(`{"full_name":"octocat/overlay","html_url":"https://github.com/octocat/overlay","default_branch":"main"}`))
		case "/repos/octocat/overlay/git/blobs":
			_, _ = response.Write([]byte(`{"sha":"blob"}`))
		case "/repos/octocat/overlay/git/trees":
			_, _ = response.Write([]byte(`{"sha":"tree"}`))
		case "/repos/octocat/overlay/git/commits":
			_, _ = response.Write([]byte(`{"sha":"commit123"}`))
		case "/repos/octocat/overlay/git/refs":
			_, _ = response.Write([]byte(`{"ref":"refs/heads/main"}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer provider.Close()
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "overlay-launch", GitHubClient: github.New(provider.URL, provider.Client())})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "overlay-launch")
	profile := createProfile(t, handler, cookie, csrf, "Overlay", "en", "local-lan")
	unlockVaultForRecoveryTest(t, handler, cookie, csrf)
	tokenBody, _ := json.Marshal(map[string]string{"profileId": profile.ID, "token": "github_pat_secret", "authority": "creation"})
	response := request(t, handler, http.MethodPost, "/api/v1/github/token/validate", tokenBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	response.Body.Close()
	planBody, _ := json.Marshal(map[string]any{"profileId": profile.ID, "mode": "minimal", "communityIds": []string{}, "release": "v1.2.3", "repositoryUrl": "https://github.com/octocat/overlay.git", "domain": "home.example"})
	response = request(t, handler, http.MethodPost, "/api/v1/capabilities/plan", planBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	var planned struct {
		Plan struct {
			ID string `json:"id"`
		} `json:"plan"`
	}
	if err := json.NewDecoder(response.Body).Decode(&planned); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	response = request(t, handler, http.MethodPost, "/api/v1/plans/"+planned.Plan.ID+"/approve", nil, cookie, map[string]string{"X-CSRF-Token": csrf})
	response.Body.Close()
	establishBody, _ := json.Marshal(map[string]any{"profileId": profile.ID, "planId": planned.Plan.ID, "repositoryName": "overlay", "mode": "minimal", "communityIds": []string{}, "release": "v1.2.3", "domain": "home.example"})
	response = request(t, handler, http.MethodPost, "/api/v1/github/overlay/establish", establishBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("establish status=%d", response.StatusCode)
	}
	body := readAll(t, response)
	if bytes.Contains(body, []byte("github_pat_secret")) {
		t.Fatal("overlay response exposes token")
	}
	if !bytes.Contains(body, []byte("commit123")) {
		t.Fatalf("identity missing commit: %s", body)
	}
}

func readAll(t *testing.T, response *http.Response) []byte {
	t.Helper()
	defer response.Body.Close()
	var data bytes.Buffer
	_, err := data.ReadFrom(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
