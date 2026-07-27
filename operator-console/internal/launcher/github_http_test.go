package launcher_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

// emptyOverlayRepository answers the way GitHub does for a repository that holds
// no commits at all: every Git Database write is refused with 409 "Git
// Repository is empty." until the Contents API has laid down the first file.
// Requests it handled return true.
type emptyOverlayRepository struct {
	fullName string
	commit   string
	seeded   bool
}

func (repository *emptyOverlayRepository) serve(response http.ResponseWriter, request *http.Request) bool {
	prefix := "/repos/" + repository.fullName
	if strings.HasPrefix(request.URL.Path, prefix+"/contents/") {
		repository.seeded = true
		response.WriteHeader(http.StatusCreated)
		_, _ = response.Write([]byte(`{"commit":{"sha":"seed"}}`))
		return true
	}
	if !strings.HasPrefix(request.URL.Path, prefix+"/git/") {
		return false
	}
	if !repository.seeded {
		response.WriteHeader(http.StatusConflict)
		_, _ = response.Write([]byte(`{"message":"Git Repository is empty."}`))
		return true
	}
	switch request.URL.Path {
	case prefix + "/git/commits/seed":
		_, _ = response.Write([]byte(`{"tree":{"sha":"seed-tree"}}`))
	case prefix + "/git/blobs":
		_, _ = response.Write([]byte(`{"sha":"blob"}`))
	case prefix + "/git/trees":
		_, _ = response.Write([]byte(`{"sha":"tree"}`))
	case prefix + "/git/commits":
		_, _ = response.Write([]byte(`{"sha":"` + repository.commit + `"}`))
	case prefix + "/git/refs/heads/main":
		_, _ = response.Write([]byte(`{"object":{"sha":"` + repository.commit + `"}}`))
	default:
		return false
	}
	return true
}

func TestApprovedCapabilityPlanEstablishesGitHubOverlayAndRecordsIdentity(t *testing.T) {
	repository := &emptyOverlayRepository{fullName: "octocat/overlay", commit: "commit123"}
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if repository.serve(response, request) {
			return
		}
		switch request.URL.Path {
		case "/user":
			response.Header().Set("X-OAuth-Scopes", "repo")
			_, _ = response.Write([]byte(`{"login":"octocat"}`))
		case "/user/repos":
			_, _ = response.Write([]byte(`{"full_name":"octocat/overlay","html_url":"https://github.com/octocat/overlay","default_branch":"main"}`))
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

func TestEstablishGitHubOverlayAdoptsAnExistingEmptyRepository(t *testing.T) {
	repository := &emptyOverlayRepository{fullName: "octocat/overlay", commit: "adopted123"}
	response := establishOverlayAgainstProvider(t, func(response http.ResponseWriter, request *http.Request) {
		if repository.serve(response, request) {
			return
		}
		switch request.URL.Path {
		case "/user/repos":
			response.WriteHeader(http.StatusUnprocessableEntity)
		case "/user":
			response.Header().Set("X-OAuth-Scopes", "repo")
			_, _ = response.Write([]byte(`{"login":"octocat"}`))
		case "/repos/octocat/overlay":
			_, _ = response.Write([]byte(`{"full_name":"octocat/overlay","html_url":"https://github.com/octocat/overlay","default_branch":"main","private":true}`))
		default:
			http.NotFound(response, request)
		}
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("establish status=%d body=%s", response.StatusCode, readAll(t, response))
	}
	if body := readAll(t, response); !bytes.Contains(body, []byte("adopted123")) {
		t.Fatalf("identity missing commit: %s", body)
	}
}

func TestEstablishGitHubOverlayRefusesARepositoryThatAlreadyHasCommits(t *testing.T) {
	response := establishOverlayAgainstProvider(t, func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/user/repos":
			response.WriteHeader(http.StatusUnprocessableEntity)
		case "/user":
			response.Header().Set("X-OAuth-Scopes", "repo")
			_, _ = response.Write([]byte(`{"login":"octocat"}`))
		case "/repos/octocat/overlay":
			_, _ = response.Write([]byte(`{"full_name":"octocat/overlay","html_url":"https://github.com/octocat/overlay","default_branch":"main","private":true}`))
		case "/repos/octocat/overlay/git/ref/heads/main":
			_, _ = response.Write([]byte(`{"object":{"sha":"existing"}}`))
		default:
			t.Errorf("unexpected mutation of a non-empty repository: %s %s", request.Method, request.URL.Path)
			http.NotFound(response, request)
		}
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("establish status=%d body=%s", response.StatusCode, readAll(t, response))
	}
	if body := readAll(t, response); !bytes.Contains(body, []byte("github_repository_not_empty")) {
		t.Fatalf("unexpected refusal: %s", body)
	}
}

// establishOverlayAgainstProvider walks the journey up to an approved capability
// plan and returns the raw response of the GitHub overlay establishment step.
func establishOverlayAgainstProvider(t *testing.T, handle http.HandlerFunc) *http.Response {
	t.Helper()
	provider := httptest.NewServer(handle)
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
	return request(t, handler, http.MethodPost, "/api/v1/github/overlay/establish", establishBody, cookie, map[string]string{"X-CSRF-Token": csrf})
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
