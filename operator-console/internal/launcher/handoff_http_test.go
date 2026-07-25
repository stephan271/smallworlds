package launcher_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/handoffverification"
	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
)

type stubHandoffVerifier struct {
	observations handoffverification.Observations
	err          error
	target       handoffverification.Target
}

func (verifier *stubHandoffVerifier) Observe(_ context.Context, target handoffverification.Target) (handoffverification.Observations, error) {
	verifier.target = target
	return verifier.observations, verifier.err
}

// establishHandoffOverlay records the GitOps Overlay the handoff needs: its
// domain is where the cluster's identity provider lives, and verification now
// probes OIDC discovery there before the temporary administration path may be
// closed.
func establishHandoffOverlay(t *testing.T, handler *launcher.Server, cookie *http.Cookie, csrf, profileID string) {
	t.Helper()
	headers := map[string]string{"X-CSRF-Token": csrf}
	release := "v1.2.27"
	credentials, _ := json.Marshal(map[string]string{"profileId": profileID, "repositoryUrl": "https://git.example/overlay.git", "username": "operator", "token": "git-secret"})
	credentialResponse := request(t, handler, http.MethodPost, "/api/v1/generic-git/token/validate", credentials, cookie, headers)
	if credentialResponse.StatusCode != http.StatusOK {
		t.Fatalf("git credential status = %d: %s", credentialResponse.StatusCode, readAll(t, credentialResponse))
	}
	credentialResponse.Body.Close()
	planBody, _ := json.Marshal(map[string]any{"profileId": profileID, "mode": "minimal", "communityIds": []string{}, "release": release, "repositoryUrl": "https://git.example/overlay.git", "domain": "home.example"})
	response := request(t, handler, http.MethodPost, "/api/v1/capabilities/plan", planBody, cookie, headers)
	var capabilityPlan struct {
		Plan struct {
			ID string `json:"id"`
		} `json:"plan"`
	}
	if err := json.NewDecoder(response.Body).Decode(&capabilityPlan); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	response = request(t, handler, http.MethodPost, "/api/v1/plans/"+capabilityPlan.Plan.ID+"/approve", nil, cookie, headers)
	response.Body.Close()
	establishBody, _ := json.Marshal(map[string]any{"profileId": profileID, "planId": capabilityPlan.Plan.ID, "repositoryUrl": "https://git.example/overlay.git", "mode": "minimal", "communityIds": []string{}, "release": release, "domain": "home.example"})
	response = request(t, handler, http.MethodPost, "/api/v1/generic-git/overlay/establish", establishBody, cookie, headers)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("overlay status = %d: %s", response.StatusCode, readAll(t, response))
	}
	response.Body.Close()
}

func establishHandoffPrerequisites(t *testing.T, handler *launcher.Server, cookie *http.Cookie, csrf, profileID string) {
	t.Helper()
	establishHandoffOverlay(t, handler, cookie, csrf, profileID)
	headers := map[string]string{"X-CSRF-Token": csrf}
	body, _ := json.Marshal(map[string]string{"profileId": profileID})
	for _, path := range []string{"/api/v1/cluster-ca/establish", "/api/v1/private-network/establish", "/api/v1/enrollment/establish"} {
		payload := body
		if path == "/api/v1/private-network/establish" {
			payload, _ = json.Marshal(map[string]string{"profileId": profileID, "baseDomain": "smallworlds.internal"})
		}
		response := request(t, handler, http.MethodPost, path, payload, cookie, headers)
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("prerequisite %s status = %d: %s", path, response.StatusCode, readAll(t, response))
		}
		response.Body.Close()
	}
}

func TestHandoffClosesTemporaryAccessOnlyAfterFullVerification(t *testing.T) {
	verifier := &stubHandoffVerifier{observations: handoffverification.Observations{PrivateReachable: true, DNSResolves: true, TLSTrusted: false, GatewayIdentityMatches: true, OIDCReachable: true}}
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "handoff", HandoffVerifier: verifier, GenericGitClient: &genericGitStub{commit: strings.Repeat("c", 40)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "handoff")
	profile := createProfile(t, handler, cookie, csrf, "Home", "en", "local-lan")
	headers := map[string]string{"X-CSRF-Token": csrf}
	body, _ := json.Marshal(map[string]string{"profileId": profile.ID})
	unlockVaultForRecoveryTest(t, handler, cookie, csrf)

	// Before prerequisites exist, verification reports the missing dependency.
	response := request(t, handler, http.MethodPost, "/api/v1/handoff/verify", body, cookie, headers)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("verify without prerequisites status = %d, want 409", response.StatusCode)
	}
	response.Body.Close()

	establishHandoffPrerequisites(t, handler, cookie, csrf, profile.ID)

	// One failing check must block closure.
	response = request(t, handler, http.MethodPost, "/api/v1/handoff/close-temporary-access", body, cookie, headers)
	blocked := readAll(t, response)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("close with a failing check status = %d: %s", response.StatusCode, blocked)
	}
	if !bytes.Contains(blocked, []byte("handoff_verification_incomplete")) {
		t.Fatalf("unexpected close rejection: %s", blocked)
	}
	// The verifier received the composed target (Cluster CA root fingerprint + gateway).
	if verifier.target.RootFingerprint == "" || verifier.target.GatewayHostname != "gateway.smallworlds.internal" {
		t.Fatalf("verifier target not composed from prior tracers: %#v", verifier.target)
	}

	response = request(t, handler, http.MethodGet, "/api/v1/handoff?profileId="+profile.ID, nil, cookie, nil)
	status := readAll(t, response)
	if !bytes.Contains(status, []byte(`"closed":false`)) {
		t.Fatalf("temporary access reported closed before verification: %s", status)
	}

	// Once every check passes, closure is permitted.
	verifier.observations.TLSTrusted = true
	response = request(t, handler, http.MethodPost, "/api/v1/handoff/close-temporary-access", body, cookie, headers)
	closed := readAll(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("close after full verification status = %d: %s", response.StatusCode, closed)
	}
	if !bytes.Contains(closed, []byte(`"closed":true`)) {
		t.Fatalf("closure not confirmed: %s", closed)
	}

	response = request(t, handler, http.MethodGet, "/api/v1/handoff?profileId="+profile.ID, nil, cookie, nil)
	if final := readAll(t, response); !bytes.Contains(final, []byte(`"closed":true`)) || !bytes.Contains(final, []byte(`"verified":true`)) {
		t.Fatalf("handoff status not reflected: %s", final)
	}
}

func TestHandoffVerificationWithLiveDefaultReportsUnverifiedForUnreachableCluster(t *testing.T) {
	// No injected verifier: the production LiveVerifier probes the (non-existent)
	// private cluster and must honestly report the handoff as unverified, which
	// blocks closing the temporary administration path.
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "handoff", GenericGitClient: &genericGitStub{commit: strings.Repeat("c", 40)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "handoff")
	profile := createProfile(t, handler, cookie, csrf, "Home", "en", "local-lan")
	headers := map[string]string{"X-CSRF-Token": csrf}
	unlockVaultForRecoveryTest(t, handler, cookie, csrf)
	establishHandoffPrerequisites(t, handler, cookie, csrf, profile.ID)
	body, _ := json.Marshal(map[string]string{"profileId": profile.ID})

	response := request(t, handler, http.MethodPost, "/api/v1/handoff/verify", body, cookie, headers)
	verifyBody := readAll(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("live verify status = %d: %s", response.StatusCode, verifyBody)
	}
	if !bytes.Contains(verifyBody, []byte(`"verified":false`)) {
		t.Fatalf("unreachable cluster should not verify: %s", verifyBody)
	}

	response = request(t, handler, http.MethodPost, "/api/v1/handoff/close-temporary-access", body, cookie, headers)
	defer response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("closure with an unverified live check status = %d, want 409", response.StatusCode)
	}
}
