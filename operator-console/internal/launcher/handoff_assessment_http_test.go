package launcher_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/handoffverification"
	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
)

func TestHandoffAssessmentCompletesAndProvidesConsoleURL(t *testing.T) {
	verifier := &stubHandoffVerifier{observations: handoffverification.Observations{PrivateReachable: true, DNSResolves: true, TLSChainsToClusterCA: true, GatewayIdentityMatches: true}}
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "assessment", HandoffVerifier: verifier})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "assessment")
	profile := createProfile(t, handler, cookie, csrf, "Home", "en", "local-lan")
	headers := map[string]string{"X-CSRF-Token": csrf}
	body, _ := json.Marshal(map[string]string{"profileId": profile.ID})
	unlockVaultForRecoveryTest(t, handler, cookie, csrf)
	establishHandoffPrerequisites(t, handler, cookie, csrf, profile.ID)

	// Partway through, the assessment is incomplete, withholds the URL, and still
	// states the LAN-only limitations.
	response := request(t, handler, http.MethodGet, "/api/v1/handoff-assessment?profileId="+profile.ID, nil, cookie, nil)
	partial := readAll(t, response)
	if !bytes.Contains(partial, []byte(`"complete":false`)) || bytes.Contains(partial, []byte("consoleHandoffUrl")) {
		t.Fatalf("incomplete assessment should withhold the console URL: %s", partial)
	}
	if !bytes.Contains(partial, []byte("remote administration")) {
		t.Fatalf("assessment does not state the LAN-only limitations: %s", partial)
	}

	// Complete the remaining steps: device trust, launcher consumption, closure,
	// and first-owner registration.
	for _, path := range []string{"/api/v1/cluster-ca/device-trust", "/api/v1/enrollment/launcher/consume", "/api/v1/handoff/close-temporary-access"} {
		response = request(t, handler, http.MethodPost, path, body, cookie, headers)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d: %s", path, response.StatusCode, readAll(t, response))
		}
		response.Body.Close()
	}

	response = request(t, handler, http.MethodPost, "/api/v1/first-owner/claim", body, cookie, headers)
	var claim firstOwnerClaimResponse
	if err := json.Unmarshal(readAll(t, response), &claim); err != nil {
		t.Fatal(err)
	}
	register, _ := json.Marshal(map[string]string{"profileId": profile.ID, "credentialId": "cred-1", "publicKey": "pub-1", "challenge": claim.Claim.Challenge})
	response = request(t, handler, http.MethodPost, "/api/v1/first-owner/register", register, cookie, headers)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("register status = %d: %s", response.StatusCode, readAll(t, response))
	}
	response.Body.Close()

	response = request(t, handler, http.MethodGet, "/api/v1/handoff-assessment?profileId="+profile.ID, nil, cookie, nil)
	final := readAll(t, response)
	if !bytes.Contains(final, []byte(`"complete":true`)) {
		t.Fatalf("assessment not complete after full handoff: %s", final)
	}
	if !bytes.Contains(final, []byte(`"consoleHandoffUrl":"https://console.smallworlds.internal"`)) {
		t.Fatalf("final assessment missing the in-cluster console URL: %s", final)
	}
}
