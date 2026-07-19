package githttps

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	gitHttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
)

var ErrUnsupportedRemote = errors.New("generic git remote must use HTTPS")
var ErrAuthentication = errors.New("generic git authentication failed")
var ErrRemoteNotEmpty = errors.New("generic git repository is not empty")
var ErrConcurrentChange = errors.New("generic git remote changed concurrently")

type Identity struct {
	RepositoryURL string `json:"repositoryUrl"`
	Commit        string `json:"commit"`
}

type Proposal struct {
	Branch string `json:"branch"`
	Commit string `json:"commit"`
}

// Client uses the embedded Go Git implementation. It deliberately never
// delegates to an installed git executable or persists credentials in a URL.
type Client struct{}

func New() *Client { return &Client{} }

func ValidateRemoteURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, ErrUnsupportedRemote
	}
	return parsed, nil
}

// ValidateAccess performs a read-only advertisement request. It is used before
// storing credentials and before every remote mutation.
func (client *Client) ValidateAccess(ctx context.Context, remoteURL, username, token string) error {
	if _, err := ValidateRemoteURL(remoteURL); err != nil {
		return err
	}
	if username == "" || token == "" {
		return ErrAuthentication
	}
	remote := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{Name: "origin", URLs: []string{remoteURL}})
	_, err := remote.ListContext(ctx, &git.ListOptions{Auth: &gitHttp.BasicAuth{Username: username, Password: token}})
	if err == nil || errors.Is(err, git.NoErrAlreadyUpToDate) {
		return nil
	}
	if looksLikeAuthenticationFailure(err) {
		return fmt.Errorf("%w: %v", ErrAuthentication, err)
	}
	return fmt.Errorf("validate generic git access: %w", err)
}

// RemoteContainsCommit verifies a previously recorded commit without writing.
func (client *Client) RemoteContainsCommit(ctx context.Context, remoteURL, username, token, commit string) (bool, error) {
	if err := client.ValidateAccess(ctx, remoteURL, username, token); err != nil {
		return false, err
	}
	remote := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{Name: "origin", URLs: []string{remoteURL}})
	references, err := remote.ListContext(ctx, &git.ListOptions{Auth: &gitHttp.BasicAuth{Username: username, Password: token}})
	if err != nil {
		if looksLikeAuthenticationFailure(err) {
			return false, fmt.Errorf("%w: %v", ErrAuthentication, err)
		}
		return false, fmt.Errorf("list generic git references: %w", err)
	}
	for _, reference := range references {
		if reference.Hash().String() == commit {
			return true, nil
		}
	}
	return false, nil
}

func (client *Client) InitializeEmptyRemote(ctx context.Context, remoteURL, username, token string, files map[string]string) (Identity, error) {
	if _, err := ValidateRemoteURL(remoteURL); err != nil {
		return Identity{}, err
	}
	if username == "" || token == "" {
		return Identity{}, ErrAuthentication
	}
	if err := client.ValidateAccess(ctx, remoteURL, username, token); err != nil {
		return Identity{}, err
	}
	remote := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{Name: "origin", URLs: []string{remoteURL}})
	references, err := remote.ListContext(ctx, &git.ListOptions{Auth: &gitHttp.BasicAuth{Username: username, Password: token}})
	if err != nil {
		return Identity{}, fmt.Errorf("list generic git references: %w", err)
	}
	if len(references) > 0 {
		return Identity{}, ErrRemoteNotEmpty
	}
	directory, err := os.MkdirTemp("", "smallworlds-git-")
	if err != nil {
		return Identity{}, err
	}
	defer os.RemoveAll(directory)
	repository, err := git.PlainInit(directory, false)
	if err != nil {
		return Identity{}, err
	}
	_, err = repository.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{remoteURL}})
	if err != nil {
		return Identity{}, err
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
			return Identity{}, fmt.Errorf("unsafe overlay path")
		}
		full := filepath.Join(directory, path)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			return Identity{}, err
		}
		if err := os.WriteFile(full, []byte(files[path]), 0600); err != nil {
			return Identity{}, err
		}
	}
	worktree, err := repository.Worktree()
	if err != nil {
		return Identity{}, err
	}
	for _, path := range paths {
		if _, err := worktree.Add(path); err != nil {
			return Identity{}, err
		}
	}
	commit, err := worktree.Commit("Initialize SmallWorlds GitOps Overlay", &git.CommitOptions{Author: &object.Signature{Name: "SmallWorlds Operator Console", Email: "operator@smallworlds.invalid"}})
	if err != nil {
		return Identity{}, err
	}
	main := plumbing.NewBranchReferenceName("main")
	if err := repository.Storer.SetReference(plumbing.NewHashReference(main, commit)); err != nil {
		return Identity{}, err
	}
	err = repository.PushContext(ctx, &git.PushOptions{RemoteName: "origin", Auth: &gitHttp.BasicAuth{Username: username, Password: token}, RefSpecs: []config.RefSpec{config.RefSpec(main.String() + ":" + main.String())}})
	if errors.Is(err, git.NoErrAlreadyUpToDate) {
		return Identity{RepositoryURL: remoteURL, Commit: commit.String()}, nil
	}
	if err != nil {
		if looksLikeAuthenticationFailure(err) {
			return Identity{}, fmt.Errorf("%w: %v", ErrAuthentication, err)
		}
		if looksLikeConcurrentPush(err) {
			return Identity{}, fmt.Errorf("%w: %v", ErrConcurrentChange, err)
		}
		return Identity{}, fmt.Errorf("push initial generic overlay: %w", err)
	}
	return Identity{RepositoryURL: remoteURL, Commit: commit.String()}, nil
}

// CreateProposalBranch writes an exact reviewed overlay only to a named branch.
// Generic providers deliberately receive no fabricated pull-request claim; the
// caller can present the branch as a manual merge instruction instead.
func (client *Client) CreateProposalBranch(ctx context.Context, remoteURL, username, token, branch string, files map[string]string) (Proposal, error) {
	if _, err := ValidateRemoteURL(remoteURL); err != nil {
		return Proposal{}, err
	}
	if username == "" || token == "" {
		return Proposal{}, ErrAuthentication
	}
	if !strings.HasPrefix(branch, "smallworlds/proposal-") {
		return Proposal{}, fmt.Errorf("unsafe proposal branch")
	}
	directory, err := os.MkdirTemp("", "smallworlds-git-")
	if err != nil {
		return Proposal{}, err
	}
	defer os.RemoveAll(directory)
	repository, err := git.PlainCloneContext(ctx, directory, false, &git.CloneOptions{URL: remoteURL, Auth: &gitHttp.BasicAuth{Username: username, Password: token}})
	if err != nil {
		if looksLikeAuthenticationFailure(err) {
			return Proposal{}, fmt.Errorf("%w: %v", ErrAuthentication, err)
		}
		return Proposal{}, fmt.Errorf("clone generic git overlay: %w", err)
	}
	worktree, err := repository.Worktree()
	if err != nil {
		return Proposal{}, err
	}
	branchReference := plumbing.NewBranchReferenceName(branch)
	if err := worktree.Checkout(&git.CheckoutOptions{Branch: branchReference, Create: true}); err != nil {
		return Proposal{}, fmt.Errorf("create proposal branch: %w", err)
	}
	paths, err := writeFiles(directory, files)
	if err != nil {
		return Proposal{}, err
	}
	for _, path := range paths {
		if _, err := worktree.Add(path); err != nil {
			return Proposal{}, err
		}
	}
	commit, err := worktree.Commit("Propose SmallWorlds GitOps Overlay change", &git.CommitOptions{Author: &object.Signature{Name: "SmallWorlds Operator Console", Email: "operator@smallworlds.invalid"}})
	if err != nil {
		return Proposal{}, err
	}
	err = repository.PushContext(ctx, &git.PushOptions{RemoteName: "origin", Auth: &gitHttp.BasicAuth{Username: username, Password: token}, RefSpecs: []config.RefSpec{config.RefSpec(branchReference.String() + ":" + branchReference.String())}})
	if err != nil {
		if looksLikeAuthenticationFailure(err) {
			return Proposal{}, fmt.Errorf("%w: %v", ErrAuthentication, err)
		}
		if looksLikeConcurrentPush(err) {
			return Proposal{}, fmt.Errorf("%w: %v", ErrConcurrentChange, err)
		}
		return Proposal{}, fmt.Errorf("push generic git proposal: %w", err)
	}
	return Proposal{Branch: branch, Commit: commit.String()}, nil
}

func writeFiles(directory string, files map[string]string) ([]string, error) {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
			return nil, fmt.Errorf("unsafe overlay path")
		}
		full := filepath.Join(directory, path)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(full, []byte(files[path]), 0600); err != nil {
			return nil, err
		}
	}
	return paths, nil
}

func looksLikeAuthenticationFailure(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "authentication") || strings.Contains(message, "authorization") || strings.Contains(message, "401") || strings.Contains(message, "403")
}

func looksLikeConcurrentPush(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "non-fast-forward") || strings.Contains(message, "already exists") || strings.Contains(message, "remote contains work")
}

func ProposalBranchName(commit plumbing.Hash) string {
	return "smallworlds/proposal-" + commit.String()[:12]
}

func ProposalBranchForDiff(diff string) string {
	digest := sha256.Sum256([]byte(diff))
	return "smallworlds/proposal-" + fmt.Sprintf("%x", digest[:6])
}
