package github_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/github"
)

func TestCreatePrivateRepositoryAndInitialCommitWithoutGitCLI(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls = append(calls, request.Method+" "+request.URL.Path)
		if request.URL.Path == "/user/repos" {
			var body map[string]any
			_ = json.NewDecoder(request.Body).Decode(&body)
			if body["private"] != true || body["name"] != "smallworlds-overlay" {
				t.Fatalf("repository payload = %#v", body)
			}
			_, _ = response.Write([]byte(`{"full_name":"octocat/smallworlds-overlay","html_url":"https://github.com/octocat/smallworlds-overlay","default_branch":"main"}`))
			return
		}
		switch request.URL.Path {
		case "/repos/octocat/smallworlds-overlay/git/blobs":
			_, _ = response.Write([]byte(`{"sha":"blob123"}`))
			return
		case "/repos/octocat/smallworlds-overlay/git/trees":
			_, _ = response.Write([]byte(`{"sha":"tree123"}`))
			return
		case "/repos/octocat/smallworlds-overlay/git/commits":
			_, _ = response.Write([]byte(`{"sha":"abc123"}`))
			return
		case "/repos/octocat/smallworlds-overlay/git/refs":
			_, _ = response.Write([]byte(`{"ref":"refs/heads/main"}`))
			return
		}
		http.NotFound(response, request)
	}))
	defer server.Close()
	client := github.New(server.URL, server.Client())
	repository, err := client.CreatePrivateRepository(t.Context(), "token", "smallworlds-overlay")
	if err != nil {
		t.Fatal(err)
	}
	commit, err := client.WriteInitialFiles(t.Context(), "token", repository, map[string]string{"kustomization.yaml": "apiVersion: kustomize.config.k8s.io/v1beta1\n"})
	if err != nil {
		t.Fatal(err)
	}
	if repository.FullName != "octocat/smallworlds-overlay" || commit != "abc123" || len(calls) != 5 {
		t.Fatalf("repository=%#v commit=%q calls=%v", repository, commit, calls)
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

func TestValidateTokenRejectsRateLimitAndMissingAuthority(t *testing.T) {
	for _, testcase := range []struct {
		name    string
		status  int
		headers map[string]string
		want    error
	}{
		{"rate limit", http.StatusForbidden, map[string]string{"X-RateLimit-Remaining": "0"}, github.ErrRateLimited},
		{"missing authority", http.StatusOK, map[string]string{}, github.ErrInsufficientAuthority},
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
