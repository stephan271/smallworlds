package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

var ErrRateLimited = errors.New("github rate limit exceeded")
var ErrInsufficientAuthority = errors.New("github token lacks required authority")
var ErrUnauthorized = errors.New("github token was rejected")
var ErrRepositoryNotEmpty = errors.New("github repository already contains commits")
var ErrRepositoryNotPrivate = errors.New("github repository is not private")

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
	// AuthorityVerified reports whether the token's permissions could actually be
	// read here. Only classic tokens publish them, as scopes in a response
	// header. A fine-grained token — the kind the console asks the Operator to
	// create — publishes nothing, so its authority stays a claim until the first
	// real call either accepts or refuses it.
	AuthorityVerified bool `json:"authorityVerified"`
}
type Client struct {
	baseURL    string
	httpClient *http.Client
}

type Repository struct {
	FullName      string `json:"full_name"`
	HTMLURL       string `json:"html_url"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
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
	// Scopes are a classic-token mechanism. A fine-grained personal access token
	// authenticates perfectly well and reports no scopes at all, so demanding
	// them here refused exactly the tokens this console tells the Operator to
	// create. Its permissions are left unverified instead, and the call that
	// needs them reports the truth — refusing a working token up front is worse
	// than accepting one that turns out to be too narrow one step later.
	status.AuthorityVerified = len(status.Scopes) > 0
	if !status.AuthorityVerified {
		return status, nil
	}
	status.CanCreateRepositories = hasScope(status.Scopes, "repo") || hasScope(status.Scopes, "administration:write")
	if authority == CreationAuthority && !status.CanCreateRepositories {
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
	// A name that is already taken is not a dead end: an empty repository the
	// Operator created ahead of time — or one left behind by a run that failed
	// between creation and the initial commit — is adopted instead, so the
	// journey stays resumable without a second repository name.
	if response.StatusCode == http.StatusUnprocessableEntity {
		return client.adoptEmptyRepository(ctx, token, name)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Repository{}, refusal("create github repository", response)
	}
	var repository Repository
	if err := json.NewDecoder(response.Body).Decode(&repository); err != nil || repository.FullName == "" || repository.DefaultBranch == "" {
		return Repository{}, fmt.Errorf("decode github repository")
	}
	return repository, nil
}

// adoptEmptyRepository takes over a repository that already exists under the
// token's owner, but only when nothing would be overwritten and it is private.
// Anything else is refused rather than silently reconfigured — a repository with
// commits may already be the Desired Configuration of a running cluster.
func (client *Client) adoptEmptyRepository(ctx context.Context, token, name string) (Repository, error) {
	status, err := client.ValidateToken(ctx, token, CreationAuthority)
	if err != nil {
		return Repository{}, err
	}
	response, err := client.doJSON(ctx, token, http.MethodGet, "/repos/"+status.Owner+"/"+name, nil)
	if err != nil {
		return Repository{}, err
	}
	var repository Repository
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		err = json.NewDecoder(response.Body).Decode(&repository)
	} else {
		err = fmt.Errorf("status %s", response.Status)
	}
	response.Body.Close()
	if err != nil || repository.FullName == "" || repository.DefaultBranch == "" {
		return Repository{}, fmt.Errorf("inspect existing github repository: %w", err)
	}
	if !repository.Private {
		return Repository{}, ErrRepositoryNotPrivate
	}
	empty, err := client.repositoryIsEmpty(ctx, token, repository)
	if err != nil {
		return Repository{}, err
	}
	if !empty {
		return Repository{}, ErrRepositoryNotEmpty
	}
	return repository, nil
}

func (client *Client) repositoryIsEmpty(ctx context.Context, token string, repository Repository) (bool, error) {
	response, err := client.doJSON(ctx, token, http.MethodGet, "/repos/"+repository.FullName+"/git/ref/heads/"+repository.DefaultBranch, nil)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	// GitHub answers 404 when the default branch has no ref yet and 409 while
	// the repository holds no commits at all; both mean adopting it overwrites
	// nothing.
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusConflict {
		return true, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false, fmt.Errorf("inspect github default branch: %s", response.Status)
	}
	return false, nil
}

// WriteInitialFiles lays the rendered Overlay into a repository that holds no
// commits at all. GitHub refuses every Git Database write against such a
// repository — blobs, trees and commits all answer 409 "Git Repository is
// empty." — so the first file cannot go in as a blob. The Contents API is the
// one write endpoint an empty repository accepts, and it produces a real
// initial commit; the remaining files then go in through the normal
// blob/tree/commit path on top of it, which keeps the written file set exactly
// the one the Operator approved and never force-pushes.
func (client *Client) WriteInitialFiles(ctx context.Context, token string, repository Repository, files map[string]string) (string, error) {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return "", fmt.Errorf("no github overlay files to write")
	}
	seed, err := client.createFirstCommit(ctx, token, repository, paths[0], files[paths[0]])
	if err != nil {
		return "", err
	}
	if len(paths) == 1 {
		return seed, nil
	}
	baseTree, err := client.readCommitTree(ctx, token, repository, seed)
	if err != nil {
		return "", fmt.Errorf("inspect github overlay initial commit: %w", err)
	}
	treeEntries := make([]map[string]string, 0, len(paths)-1)
	for _, path := range paths[1:] {
		contents := files[path]
		payload, _ := json.Marshal(map[string]string{"content": base64.StdEncoding.EncodeToString([]byte(contents)), "encoding": "base64"})
		response, err := client.doJSON(ctx, token, http.MethodPost, "/repos/"+repository.FullName+"/git/blobs", payload)
		if err != nil {
			return "", err
		}
		var blob struct {
			SHA string `json:"sha"`
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			err = refusal("create github overlay blob", response)
		} else if err = json.NewDecoder(response.Body).Decode(&blob); err == nil && blob.SHA == "" {
			err = fmt.Errorf("create github overlay blob: no sha returned")
		}
		response.Body.Close()
		if err != nil {
			return "", err
		}
		treeEntries = append(treeEntries, map[string]string{"path": path, "mode": "100644", "type": "blob", "sha": blob.SHA})
	}
	treePayload, _ := json.Marshal(map[string]any{"base_tree": baseTree, "tree": treeEntries})
	treeResponse, err := client.doJSON(ctx, token, http.MethodPost, "/repos/"+repository.FullName+"/git/trees", treePayload)
	if err != nil {
		return "", err
	}
	var tree struct {
		SHA string `json:"sha"`
	}
	if treeResponse.StatusCode < 200 || treeResponse.StatusCode >= 300 {
		err = refusal("create github overlay tree", treeResponse)
	} else if err = json.NewDecoder(treeResponse.Body).Decode(&tree); err == nil && tree.SHA == "" {
		err = fmt.Errorf("create github overlay tree: no sha returned")
	}
	treeResponse.Body.Close()
	if err != nil {
		return "", err
	}
	commitPayload, _ := json.Marshal(map[string]any{"message": "Initialize SmallWorlds GitOps Overlay", "tree": tree.SHA, "parents": []string{seed}})
	commitResponse, err := client.doJSON(ctx, token, http.MethodPost, "/repos/"+repository.FullName+"/git/commits", commitPayload)
	if err != nil {
		return "", err
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if commitResponse.StatusCode < 200 || commitResponse.StatusCode >= 300 {
		err = refusal("create github overlay commit", commitResponse)
	} else if err = json.NewDecoder(commitResponse.Body).Decode(&commit); err == nil && commit.SHA == "" {
		err = fmt.Errorf("create github overlay commit: no sha returned")
	}
	commitResponse.Body.Close()
	if err != nil {
		return "", err
	}
	// A fast-forward from the initial commit this call just made, so the default
	// branch is never rewritten even here.
	advancePayload, _ := json.Marshal(map[string]any{"sha": commit.SHA, "force": false})
	advanced, err := client.doJSON(ctx, token, http.MethodPatch, "/repos/"+repository.FullName+"/git/refs/heads/"+repository.DefaultBranch, advancePayload)
	if err != nil {
		return "", err
	}
	if advanced.StatusCode < 200 || advanced.StatusCode >= 300 {
		err = refusal("advance github overlay branch", advanced)
		advanced.Body.Close()
		return "", err
	}
	advanced.Body.Close()
	return commit.SHA, nil
}

// createFirstCommit writes a single file through the Contents API, which is the
// only way to give an empty repository its first commit without a Git client.
func (client *Client) createFirstCommit(ctx context.Context, token string, repository Repository, path, contents string) (string, error) {
	payload, _ := json.Marshal(map[string]string{"message": "Begin SmallWorlds GitOps Overlay", "content": base64.StdEncoding.EncodeToString([]byte(contents)), "branch": repository.DefaultBranch})
	response, err := client.doJSON(ctx, token, http.MethodPut, "/repos/"+repository.FullName+"/contents/"+path, payload)
	if err != nil {
		return "", err
	}
	var created struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		err = refusal("create github overlay initial commit", response)
	} else if err = json.NewDecoder(response.Body).Decode(&created); err == nil && created.Commit.SHA == "" {
		err = fmt.Errorf("create github overlay initial commit: no sha returned")
	}
	response.Body.Close()
	if err != nil {
		return "", err
	}
	return created.Commit.SHA, nil
}

// refusal describes a call GitHub turned down. It keeps GitHub's own
// explanation — "Git Repository is empty.", "Resource not accessible by
// personal access token" — because a bare code tells the Operator to retry
// something that will never succeed, and tells whoever reads the launcher's
// output nothing at all. Only the message field is read, never the whole body.
// 403 and 404 are both how GitHub says a token may not do this: a fine-grained
// token without the needed permission is told the resource does not exist.
func refusal(operation string, response *http.Response) error {
	var payload struct {
		Message string `json:"message"`
	}
	_ = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&payload)
	detail := response.Status
	if payload.Message != "" {
		detail += ": " + payload.Message
	}
	switch response.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("%s: %s: %w", operation, detail, ErrUnauthorized)
	case http.StatusForbidden, http.StatusNotFound:
		return fmt.Errorf("%s: %s: %w", operation, detail, ErrInsufficientAuthority)
	}
	return fmt.Errorf("%s: %s", operation, detail)
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
