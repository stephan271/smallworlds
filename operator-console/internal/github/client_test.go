package github_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/github"
)

// emptyRepositoryGitHub models the one GitHub behaviour that a permissive fake
// hides: a repository with no commits at all refuses every Git Database write
// with 409 "Git Repository is empty.", so the first file has to arrive through
// the Contents API. Without this rule the overlay initialization passed in tests
// and failed against real GitHub every time.
type emptyRepositoryGitHub struct {
	seeded  bool
	written map[string]string
	calls   []string
}

func (fake *emptyRepositoryGitHub) handler(t *testing.T) http.HandlerFunc {
	const full = "octocat/smallworlds-overlay"
	return func(response http.ResponseWriter, request *http.Request) {
		fake.calls = append(fake.calls, request.Method+" "+request.URL.Path)
		var body map[string]any
		if request.Body != nil {
			_ = json.NewDecoder(request.Body).Decode(&body)
		}
		if request.URL.Path == "/user/repos" {
			if body["private"] != true || body["name"] != "smallworlds-overlay" {
				t.Fatalf("repository payload = %#v", body)
			}
			_, _ = response.Write([]byte(`{"full_name":"` + full + `","html_url":"https://github.com/` + full + `","default_branch":"main"}`))
			return
		}
		if path, found := strings.CutPrefix(request.URL.Path, "/repos/"+full+"/contents/"); found {
			if request.Method != http.MethodPut {
				t.Fatalf("contents method = %s", request.Method)
			}
			decoded, err := base64.StdEncoding.DecodeString(fmt.Sprint(body["content"]))
			if err != nil {
				t.Fatalf("contents payload = %#v", body)
			}
			fake.written[path] = string(decoded)
			fake.seeded = true
			response.WriteHeader(http.StatusCreated)
			_, _ = response.Write([]byte(`{"commit":{"sha":"seed123"}}`))
			return
		}
		if strings.HasPrefix(request.URL.Path, "/repos/"+full+"/git/") && !fake.seeded {
			response.WriteHeader(http.StatusConflict)
			_, _ = response.Write([]byte(`{"message":"Git Repository is empty."}`))
			return
		}
		switch request.URL.Path {
		case "/repos/" + full + "/git/commits/seed123":
			_, _ = response.Write([]byte(`{"tree":{"sha":"seed-tree"}}`))
		case "/repos/" + full + "/git/blobs":
			decoded, err := base64.StdEncoding.DecodeString(fmt.Sprint(body["content"]))
			if err != nil {
				t.Fatalf("blob payload = %#v", body)
			}
			fake.written["blob"+fmt.Sprint(len(fake.written))] = string(decoded)
			_, _ = response.Write([]byte(`{"sha":"blob123"}`))
		case "/repos/" + full + "/git/trees":
			if body["base_tree"] != "seed-tree" {
				t.Fatalf("overlay tree must build on the initial commit, got %#v", body)
			}
			_, _ = response.Write([]byte(`{"sha":"tree123"}`))
		case "/repos/" + full + "/git/commits":
			parents, _ := body["parents"].([]any)
			if len(parents) != 1 || parents[0] != "seed123" {
				t.Fatalf("overlay commit must descend from the initial commit, got %#v", body)
			}
			_, _ = response.Write([]byte(`{"sha":"abc123"}`))
		case "/repos/" + full + "/git/refs/heads/main":
			if request.Method != http.MethodPatch || body["force"] != false {
				t.Fatalf("default branch must advance without a force push: %s %#v", request.Method, body)
			}
			_, _ = response.Write([]byte(`{"object":{"sha":"abc123"}}`))
		default:
			http.NotFound(response, request)
		}
	}
}

func TestCreatePrivateRepositoryAndInitialCommitWithoutGitCLI(t *testing.T) {
	fake := &emptyRepositoryGitHub{written: map[string]string{}}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	client := github.New(server.URL, server.Client())
	repository, err := client.CreatePrivateRepository(t.Context(), "token", "smallworlds-overlay")
	if err != nil {
		t.Fatal(err)
	}
	commit, err := client.WriteInitialFiles(t.Context(), "token", repository, map[string]string{
		"kustomization.yaml":           "apiVersion: kustomize.config.k8s.io/v1beta1\n",
		"dashboard/kustomization.yaml": "kind: Kustomization\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if repository.FullName != "octocat/smallworlds-overlay" || commit != "abc123" {
		t.Fatalf("repository=%#v commit=%q calls=%v", repository, commit, fake.calls)
	}
	if fake.written["dashboard/kustomization.yaml"] != "kind: Kustomization\n" {
		t.Fatalf("first file did not go in through the Contents API: %#v", fake.written)
	}
}

func TestWriteInitialFilesSeedsASingleFileRepositoryWithoutGitDatabaseWrites(t *testing.T) {
	fake := &emptyRepositoryGitHub{written: map[string]string{}}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	commit, err := github.New(server.URL, server.Client()).WriteInitialFiles(t.Context(), "token", github.Repository{FullName: "octocat/smallworlds-overlay", DefaultBranch: "main"}, map[string]string{"kustomization.yaml": "kind: Kustomization\n"})
	if err != nil || commit != "seed123" {
		t.Fatalf("commit=%q err=%v calls=%v", commit, err, fake.calls)
	}
}

func TestCreatePrivateRepositoryAdoptsAnExistingEmptyRepository(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls = append(calls, request.Method+" "+request.URL.Path)
		switch request.URL.Path {
		case "/user/repos":
			response.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = response.Write([]byte(`{"message":"Repository creation failed.","errors":[{"message":"name already exists on this account"}]}`))
		case "/user":
			response.Header().Set("X-OAuth-Scopes", "repo")
			_, _ = response.Write([]byte(`{"login":"octocat"}`))
		case "/repos/octocat/smallworlds-overlay":
			_, _ = response.Write([]byte(`{"full_name":"octocat/smallworlds-overlay","html_url":"https://github.com/octocat/smallworlds-overlay","default_branch":"main","private":true}`))
		case "/repos/octocat/smallworlds-overlay/git/ref/heads/main":
			response.WriteHeader(http.StatusConflict)
			_, _ = response.Write([]byte(`{"message":"Git Repository is empty."}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	repository, err := github.New(server.URL, server.Client()).CreatePrivateRepository(t.Context(), "token", "smallworlds-overlay")
	if err != nil {
		t.Fatal(err)
	}
	if repository.FullName != "octocat/smallworlds-overlay" || repository.DefaultBranch != "main" {
		t.Fatalf("repository=%#v calls=%v", repository, calls)
	}
}

func TestCreatePrivateRepositoryRefusesAnExistingRepositoryItWouldOverwrite(t *testing.T) {
	for _, testcase := range []struct {
		name       string
		repository string
		ref        int
		want       error
	}{
		{"holds commits", `{"full_name":"octocat/overlay","default_branch":"main","private":true}`, http.StatusOK, github.ErrRepositoryNotEmpty},
		{"is public", `{"full_name":"octocat/overlay","default_branch":"main","private":false}`, http.StatusConflict, github.ErrRepositoryNotPrivate},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/user/repos":
					response.WriteHeader(http.StatusUnprocessableEntity)
				case "/user":
					response.Header().Set("X-OAuth-Scopes", "repo")
					_, _ = response.Write([]byte(`{"login":"octocat"}`))
				case "/repos/octocat/overlay":
					_, _ = response.Write([]byte(testcase.repository))
				case "/repos/octocat/overlay/git/ref/heads/main":
					response.WriteHeader(testcase.ref)
					_, _ = response.Write([]byte(`{"object":{"sha":"existing"}}`))
				default:
					http.NotFound(response, request)
				}
			}))
			defer server.Close()
			_, err := github.New(server.URL, server.Client()).CreatePrivateRepository(t.Context(), "token", "overlay")
			if !errors.Is(err, testcase.want) {
				t.Fatalf("error = %v, want %v", err, testcase.want)
			}
		})
	}
}

func TestCreateProposalNeverForcePushesOrMerges(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		methods = append(methods, request.Method+" "+request.URL.Path)
		switch request.URL.Path {
		case "/repos/octocat/overlay/git/ref/heads/main":
			_, _ = response.Write([]byte(`{"object":{"sha":"base123"}}`))
		case "/repos/octocat/overlay/git/refs":
			if request.Method != http.MethodPost {
				t.Fatal("proposal branch must be created, not updated")
			}
			_, _ = response.Write([]byte(`{"ref":"refs/heads/smallworlds/proposal"}`))
		case "/repos/octocat/overlay/pulls":
			_, _ = response.Write([]byte(`{"html_url":"https://github.com/octocat/overlay/pull/7","head":{"sha":"proposal123"}}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	proposal, err := github.New(server.URL, server.Client()).CreateProposal(t.Context(), "token", github.Repository{FullName: "octocat/overlay", DefaultBranch: "main"}, "smallworlds/proposal", "Update capabilities", "Reviewed diff")
	if err != nil {
		t.Fatal(err)
	}
	if proposal.URL != "https://github.com/octocat/overlay/pull/7" || len(methods) != 3 {
		t.Fatalf("proposal=%#v calls=%v", proposal, methods)
	}
}

func TestCreateProposalWithFilesCommitsReviewedContentBeforePullRequest(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		methods = append(methods, request.Method+" "+request.URL.Path)
		switch request.URL.Path {
		case "/repos/octocat/overlay/git/ref/heads/main":
			_, _ = response.Write([]byte(`{"object":{"sha":"base123"}}`))
		case "/repos/octocat/overlay/git/commits/base123":
			_, _ = response.Write([]byte(`{"tree":{"sha":"base-tree"}}`))
		case "/repos/octocat/overlay/git/refs":
			_, _ = response.Write([]byte(`{"ref":"refs/heads/smallworlds/proposal"}`))
		case "/repos/octocat/overlay/git/blobs":
			_, _ = response.Write([]byte(`{"sha":"blob"}`))
		case "/repos/octocat/overlay/git/trees":
			_, _ = response.Write([]byte(`{"sha":"proposal-tree"}`))
		case "/repos/octocat/overlay/git/commits":
			_, _ = response.Write([]byte(`{"sha":"proposal-commit"}`))
		case "/repos/octocat/overlay/git/refs/heads/smallworlds/proposal":
			if request.Method != http.MethodPatch {
				t.Fatal("proposal ref must advance with a normal patch")
			}
			_, _ = response.Write([]byte(`{"object":{"sha":"proposal-commit"}}`))
		case "/repos/octocat/overlay/pulls":
			_, _ = response.Write([]byte(`{"html_url":"https://github.com/octocat/overlay/pull/8","head":{"sha":"proposal-commit"}}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	proposal, err := github.New(server.URL, server.Client()).CreateProposalWithFiles(t.Context(), "token", github.Repository{FullName: "octocat/overlay", DefaultBranch: "main"}, "smallworlds/proposal", "Update", "diff", map[string]string{"overlay-config.yaml": "changed"})
	if err != nil || proposal.Commit != "proposal-commit" || proposal.URL == "" {
		t.Fatalf("proposal=%#v err=%v calls=%v", proposal, err, methods)
	}
}

func TestValidateCreationTokenReportsOwnerPermissionsAndExpiry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/user" {
			t.Fatalf("path = %s", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer temporary-token" {
			t.Fatal("token was not sent as a bearer credential")
		}
		response.Header().Set("GitHub-Authentication-Token-Expiration", "2032-01-02 03:04:05 UTC")
		response.Header().Set("X-OAuth-Scopes", "repo")
		_, _ = response.Write([]byte(`{"login":"octocat","id":1}`))
	}))
	defer server.Close()
	client := github.New(server.URL, server.Client())
	status, err := client.ValidateToken(t.Context(), "temporary-token", github.CreationAuthority)
	if err != nil {
		t.Fatal(err)
	}
	if status.Owner != "octocat" || !status.ExpiresAt.Equal(time.Date(2032, 1, 2, 3, 4, 5, 0, time.UTC)) || !status.CanCreateRepositories {
		t.Fatalf("token status = %#v", status)
	}
}

func TestValidateTokenRejectsRateLimitAndClassicTokensWithoutAuthority(t *testing.T) {
	for _, testcase := range []struct {
		name    string
		status  int
		headers map[string]string
		want    error
	}{
		{"rate limit", http.StatusForbidden, map[string]string{"X-RateLimit-Remaining": "0"}, github.ErrRateLimited},
		{"rejected token", http.StatusUnauthorized, map[string]string{}, github.ErrUnauthorized},
		{"classic token without repository authority", http.StatusOK, map[string]string{"X-OAuth-Scopes": "gist, read:org"}, github.ErrInsufficientAuthority},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				for key, value := range testcase.headers {
					response.Header().Set(key, value)
				}
				response.WriteHeader(testcase.status)
				_, _ = response.Write([]byte(`{"login":"octocat"}`))
			}))
			defer server.Close()
			_, err := github.New(server.URL, server.Client()).ValidateToken(t.Context(), "token", github.CreationAuthority)
			if err != testcase.want {
				t.Fatalf("error = %v, want %v", err, testcase.want)
			}
		})
	}
}

// A fine-grained token is what the console tells the Operator to create, and it
// reports no scopes at all. Refusing it for that would refuse the documented
// path; its authority is reported as unverified and settled by the first real
// call instead.
func TestValidateTokenAcceptsAFineGrainedTokenAsUnverifiedAuthority(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte(`{"login":"octocat"}`))
	}))
	defer server.Close()
	for _, authority := range []github.Authority{github.CreationAuthority, github.OngoingAuthority} {
		status, err := github.New(server.URL, server.Client()).ValidateToken(t.Context(), "github_pat_fine_grained", authority)
		if err != nil {
			t.Fatalf("%s authority: %v", authority, err)
		}
		if status.Owner != "octocat" || status.AuthorityVerified || status.CanCreateRepositories {
			t.Fatalf("%s authority: token status = %#v", authority, status)
		}
	}
}

// The permission a fine-grained token turns out to lack has to reach the
// Operator as a token problem, not as an unexplained provider failure — GitHub
// reports it as 403, or as 404 when it hides the repository altogether.
func TestOverlayWritesReportAMissingPermissionAsInsufficientAuthority(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound} {
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			response.WriteHeader(status)
			_, _ = response.Write([]byte(`{"message":"Resource not accessible by personal access token"}`))
		}))
		_, err := github.New(server.URL, server.Client()).WriteInitialFiles(t.Context(), "token", github.Repository{FullName: "octocat/overlay", DefaultBranch: "main"}, map[string]string{"kustomization.yaml": "kind: Kustomization\n"})
		server.Close()
		if !errors.Is(err, github.ErrInsufficientAuthority) {
			t.Fatalf("status %d: error = %v", status, err)
		}
		if !strings.Contains(err.Error(), "Resource not accessible by personal access token") {
			t.Fatalf("status %d: error drops GitHub's explanation: %v", status, err)
		}
	}
}

// The failure that shipped: GitHub refuses Git Database writes on a repository
// with no commits, and the refusal has to stay readable all the way out.
func TestOverlayWritesKeepGitHubsExplanationOfARefusal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusConflict)
		_, _ = response.Write([]byte(`{"message":"Git Repository is empty."}`))
	}))
	defer server.Close()
	_, err := github.New(server.URL, server.Client()).WriteInitialFiles(t.Context(), "token", github.Repository{FullName: "octocat/overlay", DefaultBranch: "main"}, map[string]string{"kustomization.yaml": "kind: Kustomization\n"})
	if err == nil || !strings.Contains(err.Error(), "409") || !strings.Contains(err.Error(), "Git Repository is empty.") {
		t.Fatalf("error = %v", err)
	}
}
