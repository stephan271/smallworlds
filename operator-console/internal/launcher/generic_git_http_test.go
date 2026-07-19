package launcher_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/githttps"
	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
)

type genericGitStub struct {
	validateErr     error
	initializeErr   error
	contains        bool
	initializeCalls int
	proposalCalls   int
}

func (stub *genericGitStub) ValidateAccess(context.Context, string, string, string) error {
	return stub.validateErr
}
func (stub *genericGitStub) RemoteContainsCommit(context.Context, string, string, string, string) (bool, error) {
	return stub.contains, nil
}
func (stub *genericGitStub) InitializeEmptyRemote(_ context.Context, remoteURL, _ string, _ string, _ map[string]string) (githttps.Identity, error) {
	stub.initializeCalls++
	if stub.initializeErr != nil {
		return githttps.Identity{}, stub.initializeErr
	}
	return githttps.Identity{RepositoryURL: remoteURL, Commit: "initial-commit"}, nil
}
func (stub *genericGitStub) CreateProposalBranch(_ context.Context, _ string, _ string, _ string, branch string, _ map[string]string) (githttps.Proposal, error) {
	stub.proposalCalls++
	return githttps.Proposal{Branch: branch, Commit: "proposal-commit"}, nil
}

func TestGenericGitRejectsSSHAndNeverReturnsStoredCredentials(t *testing.T) {
	stub := &genericGitStub{}
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "generic-launch", GenericGitClient: stub})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "generic-launch")
	profile := createProfile(t, handler, cookie, csrf, "Generic", "en", "local-lan")
	unlockVaultForRecoveryTest(t, handler, cookie, csrf)
	sshBody, _ := json.Marshal(map[string]string{"profileId": profile.ID, "repositoryUrl": "ssh://git@example/repo.git", "username": "operator", "token": "never-return"})
	response := request(t, handler, http.MethodPost, "/api/v1/generic-git/token/validate", sshBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("SSH status = %d", response.StatusCode)
	}
	response.Body.Close()
	body, _ := json.Marshal(map[string]string{"profileId": profile.ID, "repositoryUrl": "https://git.example/overlay.git", "username": "operator", "token": "never-return"})
	response = request(t, handler, http.MethodPost, "/api/v1/generic-git/token/validate", body, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("validation status = %d", response.StatusCode)
	}
	if bytes.Contains(readAll(t, response), []byte("never-return")) {
		t.Fatal("credential response exposes token")
	}
	response = request(t, handler, http.MethodGet, "/api/v1/profiles/"+profile.ID+"/credentials", nil, cookie, nil)
	if bytes.Contains(readAll(t, response), []byte("never-return")) {
		t.Fatal("credential metadata exposes token")
	}
}

func TestGenericGitEstablishesApprovedPlanAndSafelyResumes(t *testing.T) {
	stub := &genericGitStub{contains: true}
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "generic-establish", GenericGitClient: stub})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "generic-establish")
	profile := createProfile(t, handler, cookie, csrf, "Generic", "en", "local-lan")
	unlockVaultForRecoveryTest(t, handler, cookie, csrf)
	credentialBody, _ := json.Marshal(map[string]string{"profileId": profile.ID, "repositoryUrl": "https://git.example/overlay.git", "username": "operator", "token": "generic-secret"})
	response := request(t, handler, http.MethodPost, "/api/v1/generic-git/token/validate", credentialBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	response.Body.Close()
	planID := genericCapabilityPlan(t, handler, cookie, csrf, profile.ID)
	response = request(t, handler, http.MethodPost, "/api/v1/plans/"+planID+"/approve", nil, cookie, map[string]string{"X-CSRF-Token": csrf})
	response.Body.Close()
	establishBody, _ := json.Marshal(map[string]any{"profileId": profile.ID, "planId": planID, "repositoryUrl": "https://git.example/overlay.git", "mode": "minimal", "communityIds": []string{}, "release": "v1.2.3", "domain": "home.example"})
	response = request(t, handler, http.MethodPost, "/api/v1/generic-git/overlay/establish", establishBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("establish status = %d", response.StatusCode)
	}
	if !bytes.Contains(readAll(t, response), []byte("initial-commit")) {
		t.Fatal("identity does not include commit")
	}
	response = request(t, handler, http.MethodPost, "/api/v1/generic-git/overlay/establish", establishBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("resume status = %d", response.StatusCode)
	}
	response.Body.Close()
	if stub.initializeCalls != 1 {
		t.Fatalf("initialize calls = %d, want 1", stub.initializeCalls)
	}
	response = request(t, handler, http.MethodPost, "/api/v1/generic-git/overlay/propose", establishBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("proposal status = %d", response.StatusCode)
	}
	if !bytes.Contains(readAll(t, response), []byte("smallworlds/proposal-")) {
		t.Fatal("proposal response does not provide a manual-merge branch")
	}
	if stub.proposalCalls != 1 {
		t.Fatalf("proposal calls = %d, want 1", stub.proposalCalls)
	}
}

func TestGenericGitMapsAuthenticationAndRemoteConflicts(t *testing.T) {
	for name, expected := range map[string]error{"authentication": githttps.ErrAuthentication, "non-empty": githttps.ErrRemoteNotEmpty, "concurrent": githttps.ErrConcurrentChange} {
		t.Run(name, func(t *testing.T) {
			stub := &genericGitStub{initializeErr: expected}
			if expected == githttps.ErrAuthentication {
				stub.validateErr = expected
			}
			handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: name, GenericGitClient: stub})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = handler.Close() })
			cookie, csrf := exchange(t, handler, name)
			profile := createProfile(t, handler, cookie, csrf, name, "en", "local-lan")
			unlockVaultForRecoveryTest(t, handler, cookie, csrf)
			body, _ := json.Marshal(map[string]string{"profileId": profile.ID, "repositoryUrl": "https://git.example/overlay.git", "username": "operator", "token": "secret"})
			response := request(t, handler, http.MethodPost, "/api/v1/generic-git/token/validate", body, cookie, map[string]string{"X-CSRF-Token": csrf})
			if expected == githttps.ErrAuthentication {
				if response.StatusCode != http.StatusForbidden {
					t.Fatalf("auth status = %d", response.StatusCode)
				}
				response.Body.Close()
				return
			}
			response.Body.Close()
			planID := genericCapabilityPlan(t, handler, cookie, csrf, profile.ID)
			response = request(t, handler, http.MethodPost, "/api/v1/plans/"+planID+"/approve", nil, cookie, map[string]string{"X-CSRF-Token": csrf})
			response.Body.Close()
			establish, _ := json.Marshal(map[string]any{"profileId": profile.ID, "planId": planID, "repositoryUrl": "https://git.example/overlay.git", "mode": "minimal", "communityIds": []string{}, "release": "v1.2.3", "domain": "home.example"})
			response = request(t, handler, http.MethodPost, "/api/v1/generic-git/overlay/establish", establish, cookie, map[string]string{"X-CSRF-Token": csrf})
			if response.StatusCode != http.StatusConflict {
				t.Fatalf("conflict status = %d", response.StatusCode)
			}
			response.Body.Close()
		})
	}
}

func TestGenericGitTransientFailureDoesNotRecordOrDuplicateAnOverlay(t *testing.T) {
	stub := &genericGitStub{initializeErr: errors.New("temporary upstream outage")}
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "transient", GenericGitClient: stub})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "transient")
	profile := createProfile(t, handler, cookie, csrf, "Transient", "en", "local-lan")
	unlockVaultForRecoveryTest(t, handler, cookie, csrf)
	credentials, _ := json.Marshal(map[string]string{"profileId": profile.ID, "repositoryUrl": "https://git.example/overlay.git", "username": "operator", "token": "secret"})
	response := request(t, handler, http.MethodPost, "/api/v1/generic-git/token/validate", credentials, cookie, map[string]string{"X-CSRF-Token": csrf})
	response.Body.Close()
	planID := genericCapabilityPlan(t, handler, cookie, csrf, profile.ID)
	response = request(t, handler, http.MethodPost, "/api/v1/plans/"+planID+"/approve", nil, cookie, map[string]string{"X-CSRF-Token": csrf})
	response.Body.Close()
	establish, _ := json.Marshal(map[string]any{"profileId": profile.ID, "planId": planID, "repositoryUrl": "https://git.example/overlay.git", "mode": "minimal", "communityIds": []string{}, "release": "v1.2.3", "domain": "home.example"})
	response = request(t, handler, http.MethodPost, "/api/v1/generic-git/overlay/establish", establish, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("transient status = %d", response.StatusCode)
	}
	response.Body.Close()
	if stub.initializeCalls != 1 {
		t.Fatalf("failed initialize calls = %d, want 1", stub.initializeCalls)
	}
	stub.initializeErr = nil
	response = request(t, handler, http.MethodPost, "/api/v1/generic-git/overlay/establish", establish, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("safe retry status = %d", response.StatusCode)
	}
	response.Body.Close()
	if stub.initializeCalls != 2 {
		t.Fatalf("retry initialize calls = %d, want 2", stub.initializeCalls)
	}
}

func genericCapabilityPlan(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, profileID string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"profileId": profileID, "mode": "minimal", "communityIds": []string{}, "release": "v1.2.3", "repositoryUrl": "https://git.example/overlay.git", "domain": "home.example"})
	response := request(t, handler, http.MethodPost, "/api/v1/capabilities/plan", body, cookie, map[string]string{"X-CSRF-Token": csrf})
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("plan status = %d", response.StatusCode)
	}
	var planned struct {
		Plan struct {
			ID string `json:"id"`
		} `json:"plan"`
	}
	if err := json.NewDecoder(response.Body).Decode(&planned); err != nil {
		t.Fatal(err)
	}
	if planned.Plan.ID == "" {
		t.Fatal("missing plan ID")
	}
	return planned.Plan.ID
}

var _ = errors.Is
