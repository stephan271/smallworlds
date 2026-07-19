package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

var ErrRateLimited = errors.New("github rate limit exceeded")
var ErrInsufficientAuthority = errors.New("github token lacks required authority")
var ErrUnauthorized = errors.New("github token was rejected")

type Authority string

const (
	CreationAuthority Authority = "creation"
	OngoingAuthority  Authority = "ongoing"
)

type TokenStatus struct {
	Owner                 string    `json:"owner"`
	ExpiresAt             time.Time `json:"expiresAt"`
	CanCreateRepositories bool      `json:"canCreateRepositories"`
	Scopes                []string  `json:"scopes"`
}
type Client struct {
	baseURL    string
	httpClient *http.Client
}

type Repository struct {
	FullName      string `json:"full_name"`
	HTMLURL       string `json:"html_url"`
	DefaultBranch string `json:"default_branch"`
}
type Proposal struct {
	URL    string `json:"url"`
	Commit string `json:"commit"`
}

func New(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), httpClient: httpClient}
}

func (client *Client) ValidateToken(ctx context.Context, token string, authority Authority) (TokenStatus, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+"/user", nil)
	if err != nil {
		return TokenStatus{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return TokenStatus{}, fmt.Errorf("inspect github token: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized {
		return TokenStatus{}, ErrUnauthorized
	}
	if response.Header.Get("X-RateLimit-Remaining") == "0" {
		return TokenStatus{}, ErrRateLimited
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return TokenStatus{}, fmt.Errorf("github token inspection failed: %s", response.Status)
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(response.Body).Decode(&user); err != nil || user.Login == "" {
		return TokenStatus{}, fmt.Errorf("decode github owner")
	}
	status := TokenStatus{Owner: user.Login, Scopes: splitScopes(response.Header.Get("X-OAuth-Scopes"))}
	if expires := response.Header.Get("GitHub-Authentication-Token-Expiration"); expires != "" {
		status.ExpiresAt, _ = time.Parse("2006-01-02 15:04:05 MST", expires)
	}
	status.CanCreateRepositories = hasScope(status.Scopes, "repo") || hasScope(status.Scopes, "administration:write")
	if authority == CreationAuthority && !status.CanCreateRepositories {
		return TokenStatus{}, ErrInsufficientAuthority
	}
	if authority == OngoingAuthority && len(status.Scopes) == 0 {
		return TokenStatus{}, ErrInsufficientAuthority
	}
	return status, nil
}
func splitScopes(raw string) []string {
	var result []string
	for _, scope := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(scope); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
func hasScope(scopes []string, want string) bool {
	for _, scope := range scopes {
		if scope == want {
			return true
		}
	}
	return false
}

func (client *Client) CreatePrivateRepository(ctx context.Context, token, name string) (Repository, error) {
	payload, _ := json.Marshal(map[string]any{"name": name, "private": true, "auto_init": false})
	response, err := client.doJSON(ctx, token, http.MethodPost, "/user/repos", payload)
	if err != nil {
		return Repository{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnprocessableEntity {
		return Repository{}, fmt.Errorf("github repository conflict")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Repository{}, fmt.Errorf("github repository creation failed: %s", response.Status)
	}
	var repository Repository
	if err := json.NewDecoder(response.Body).Decode(&repository); err != nil || repository.FullName == "" || repository.DefaultBranch == "" {
		return Repository{}, fmt.Errorf("decode github repository")
	}
	return repository, nil
}

func (client *Client) WriteInitialFiles(ctx context.Context, token string, repository Repository, files map[string]string) (string, error) {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	treeEntries := make([]map[string]string, 0, len(paths))
	for _, path := range paths {
		contents := files[path]
		payload, _ := json.Marshal(map[string]string{"content": base64.StdEncoding.EncodeToString([]byte(contents)), "encoding": "base64"})
		response, err := client.doJSON(ctx, token, http.MethodPost, "/repos/"+repository.FullName+"/git/blobs", payload)
		if err != nil {
			return "", err
		}
		var blob struct {
			SHA string `json:"sha"`
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			err = json.NewDecoder(response.Body).Decode(&blob)
		}
		response.Body.Close()
		if err != nil || blob.SHA == "" {
			return "", fmt.Errorf("create github overlay blob")
		}
		treeEntries = append(treeEntries, map[string]string{"path": path, "mode": "100644", "type": "blob", "sha": blob.SHA})
	}
	treePayload, _ := json.Marshal(map[string]any{"tree": treeEntries})
	treeResponse, err := client.doJSON(ctx, token, http.MethodPost, "/repos/"+repository.FullName+"/git/trees", treePayload)
	if err != nil {
		return "", err
	}
	var tree struct {
		SHA string `json:"sha"`
	}
	if treeResponse.StatusCode >= 200 && treeResponse.StatusCode < 300 {
		err = json.NewDecoder(treeResponse.Body).Decode(&tree)
	}
	treeResponse.Body.Close()
	if err != nil || tree.SHA == "" {
		return "", fmt.Errorf("create github overlay tree")
	}
	commitPayload, _ := json.Marshal(map[string]string{"message": "Initialize SmallWorlds GitOps Overlay", "tree": tree.SHA})
	commitResponse, err := client.doJSON(ctx, token, http.MethodPost, "/repos/"+repository.FullName+"/git/commits", commitPayload)
	if err != nil {
		return "", err
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if commitResponse.StatusCode >= 200 && commitResponse.StatusCode < 300 {
		err = json.NewDecoder(commitResponse.Body).Decode(&commit)
	}
	commitResponse.Body.Close()
	if err != nil || commit.SHA == "" {
		return "", fmt.Errorf("create github overlay commit")
	}
	refPayload, _ := json.Marshal(map[string]string{"ref": "refs/heads/" + repository.DefaultBranch, "sha": commit.SHA})
	refResponse, err := client.doJSON(ctx, token, http.MethodPost, "/repos/"+repository.FullName+"/git/refs", refPayload)
	if err != nil {
		return "", err
	}
	if refResponse.StatusCode < 200 || refResponse.StatusCode >= 300 {
		refResponse.Body.Close()
		return "", fmt.Errorf("create github overlay branch")
	}
	refResponse.Body.Close()
	return commit.SHA, nil
}

func (client *Client) doJSON(ctx context.Context, token, method, path string, payload []byte) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	if response.Header.Get("X-RateLimit-Remaining") == "0" {
		response.Body.Close()
		return nil, ErrRateLimited
	}
	return response, nil
}

func (client *Client) CreateProposal(ctx context.Context, token string, repository Repository, branch, title, body string) (Proposal, error) {
	base, err := client.doJSON(ctx, token, http.MethodGet, "/repos/"+repository.FullName+"/git/ref/heads/"+repository.DefaultBranch, nil)
	if err != nil {
		return Proposal{}, err
	}
	var reference struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if base.StatusCode >= 200 && base.StatusCode < 300 {
		err = json.NewDecoder(base.Body).Decode(&reference)
	}
	base.Body.Close()
	if err != nil || reference.Object.SHA == "" {
		return Proposal{}, fmt.Errorf("inspect github default branch")
	}
	branchPayload, _ := json.Marshal(map[string]string{"ref": "refs/heads/" + branch, "sha": reference.Object.SHA})
	created, err := client.doJSON(ctx, token, http.MethodPost, "/repos/"+repository.FullName+"/git/refs", branchPayload)
	if err != nil {
		return Proposal{}, err
	}
	if created.StatusCode < 200 || created.StatusCode >= 300 {
		created.Body.Close()
		return Proposal{}, fmt.Errorf("create github proposal branch failed")
	}
	created.Body.Close()
	pullPayload, _ := json.Marshal(map[string]string{"title": title, "head": branch, "base": repository.DefaultBranch, "body": body})
	pull, err := client.doJSON(ctx, token, http.MethodPost, "/repos/"+repository.FullName+"/pulls", pullPayload)
	if err != nil {
		return Proposal{}, err
	}
	defer pull.Body.Close()
	if pull.StatusCode < 200 || pull.StatusCode >= 300 {
		return Proposal{}, fmt.Errorf("create github pull request failed")
	}
	var result struct {
		HTMLURL string `json:"html_url"`
		Head    struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := json.NewDecoder(pull.Body).Decode(&result); err != nil || result.HTMLURL == "" {
		return Proposal{}, fmt.Errorf("decode github pull request")
	}
	return Proposal{URL: result.HTMLURL, Commit: result.Head.SHA}, nil
}

func (client *Client) CreateProposalWithFiles(ctx context.Context, token string, repository Repository, branch, title, body string, files map[string]string) (Proposal, error) {
	base, err := client.readRef(ctx, token, repository, repository.DefaultBranch)
	if err != nil {
		return Proposal{}, err
	}
	baseTree, err := client.readCommitTree(ctx, token, repository, base)
	if err != nil {
		return Proposal{}, err
	}
	branchPayload, _ := json.Marshal(map[string]string{"ref": "refs/heads/" + branch, "sha": base})
	created, err := client.doJSON(ctx, token, http.MethodPost, "/repos/"+repository.FullName+"/git/refs", branchPayload)
	if err != nil {
		return Proposal{}, err
	}
	if created.StatusCode < 200 || created.StatusCode >= 300 {
		created.Body.Close()
		return Proposal{}, fmt.Errorf("create github proposal branch failed")
	}
	created.Body.Close()
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	entries := make([]map[string]string, 0, len(paths))
	for _, path := range paths {
		payload, _ := json.Marshal(map[string]string{"content": base64.StdEncoding.EncodeToString([]byte(files[path])), "encoding": "base64"})
		response, err := client.doJSON(ctx, token, http.MethodPost, "/repos/"+repository.FullName+"/git/blobs", payload)
		if err != nil {
			return Proposal{}, err
		}
		var blob struct {
			SHA string `json:"sha"`
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			err = json.NewDecoder(response.Body).Decode(&blob)
		}
		response.Body.Close()
		if err != nil || blob.SHA == "" {
			return Proposal{}, fmt.Errorf("create github proposal blob")
		}
		entries = append(entries, map[string]string{"path": path, "mode": "100644", "type": "blob", "sha": blob.SHA})
	}
	treePayload, _ := json.Marshal(map[string]any{"base_tree": baseTree, "tree": entries})
	treeResponse, err := client.doJSON(ctx, token, http.MethodPost, "/repos/"+repository.FullName+"/git/trees", treePayload)
	if err != nil {
		return Proposal{}, err
	}
	var tree struct {
		SHA string `json:"sha"`
	}
	if treeResponse.StatusCode >= 200 && treeResponse.StatusCode < 300 {
		err = json.NewDecoder(treeResponse.Body).Decode(&tree)
	}
	treeResponse.Body.Close()
	if err != nil || tree.SHA == "" {
		return Proposal{}, fmt.Errorf("create github proposal tree")
	}
	commitPayload, _ := json.Marshal(map[string]any{"message": title, "tree": tree.SHA, "parents": []string{base}})
	commitResponse, err := client.doJSON(ctx, token, http.MethodPost, "/repos/"+repository.FullName+"/git/commits", commitPayload)
	if err != nil {
		return Proposal{}, err
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if commitResponse.StatusCode >= 200 && commitResponse.StatusCode < 300 {
		err = json.NewDecoder(commitResponse.Body).Decode(&commit)
	}
	commitResponse.Body.Close()
	if err != nil || commit.SHA == "" {
		return Proposal{}, fmt.Errorf("create github proposal commit")
	}
	advancePayload, _ := json.Marshal(map[string]any{"sha": commit.SHA, "force": false})
	advanced, err := client.doJSON(ctx, token, http.MethodPatch, "/repos/"+repository.FullName+"/git/refs/heads/"+branch, advancePayload)
	if err != nil {
		return Proposal{}, err
	}
	if advanced.StatusCode < 200 || advanced.StatusCode >= 300 {
		advanced.Body.Close()
		return Proposal{}, fmt.Errorf("advance github proposal branch failed")
	}
	advanced.Body.Close()
	pullPayload, _ := json.Marshal(map[string]string{"title": title, "head": branch, "base": repository.DefaultBranch, "body": body})
	pull, err := client.doJSON(ctx, token, http.MethodPost, "/repos/"+repository.FullName+"/pulls", pullPayload)
	if err != nil {
		return Proposal{}, err
	}
	defer pull.Body.Close()
	if pull.StatusCode < 200 || pull.StatusCode >= 300 {
		return Proposal{}, fmt.Errorf("create github pull request failed")
	}
	var result struct {
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(pull.Body).Decode(&result); err != nil || result.HTMLURL == "" {
		return Proposal{}, fmt.Errorf("decode github pull request")
	}
	return Proposal{URL: result.HTMLURL, Commit: commit.SHA}, nil
}

func (client *Client) readRef(ctx context.Context, token string, repository Repository, branch string) (string, error) {
	response, err := client.doJSON(ctx, token, http.MethodGet, "/repos/"+repository.FullName+"/git/ref/heads/"+branch, nil)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		err = json.NewDecoder(response.Body).Decode(&ref)
	}
	if err != nil || ref.Object.SHA == "" {
		return "", fmt.Errorf("inspect github branch")
	}
	return ref.Object.SHA, nil
}
func (client *Client) readCommitTree(ctx context.Context, token string, repository Repository, commit string) (string, error) {
	response, err := client.doJSON(ctx, token, http.MethodGet, "/repos/"+repository.FullName+"/git/commits/"+commit, nil)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	var value struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		err = json.NewDecoder(response.Body).Decode(&value)
	}
	if err != nil || value.Tree.SHA == "" {
		return "", fmt.Errorf("inspect github commit tree")
	}
	return value.Tree.SHA, nil
}
