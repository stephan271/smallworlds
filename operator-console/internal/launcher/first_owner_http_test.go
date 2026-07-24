package launcher_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/firstowner"
	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
	"github.com/stephan271/smallworlds/operator-console/internal/webauthntest"
)

type firstOwnerClaimResponse struct {
	Claim struct {
		Challenge string `json:"challenge"`
		Used      bool   `json:"used"`
	} `json:"claim"`
	OwnerRegistered        bool `json:"ownerRegistered"`
	BootstrapGrantDisabled bool `json:"bootstrapGrantDisabled"`
}

func firstOwnerRegistrationBody(t *testing.T, profileID string, registration firstowner.Registration) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"profileId":         profileID,
		"credentialId":      registration.CredentialID,
		"clientDataJson":    registration.ClientDataJSON,
		"attestationObject": registration.AttestationObject,
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestFirstOwnerPasskeyRegistrationPermanentlyDisablesBootstrapGrant(t *testing.T) {
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "first-owner"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "first-owner")
	profile := createProfile(t, handler, cookie, csrf, "Home", "en", "local-lan")
	headers := map[string]string{"X-CSRF-Token": csrf}
	body, _ := json.Marshal(map[string]string{"profileId": profile.ID})

	// Registering before a claim exists is rejected.
	early := firstOwnerRegistrationBody(t, profile.ID, firstowner.Registration{CredentialID: "c", ClientDataJSON: "x", AttestationObject: "y"})
	response := request(t, handler, http.MethodPost, "/api/v1/first-owner/register", early, cookie, headers)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("register without claim status = %d, want 409", response.StatusCode)
	}
	response.Body.Close()

	response = request(t, handler, http.MethodPost, "/api/v1/first-owner/claim", body, cookie, headers)
	claimBody := readAll(t, response)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("claim status = %d: %s", response.StatusCode, claimBody)
	}
	var claim firstOwnerClaimResponse
	if err := json.Unmarshal(claimBody, &claim); err != nil {
		t.Fatal(err)
	}
	if claim.Claim.Challenge == "" || claim.BootstrapGrantDisabled {
		t.Fatalf("unexpected claim: %s", claimBody)
	}

	// A real WebAuthn registration that does not echo the issued challenge is rejected.
	mismatch, err := webauthntest.Registration(webauthntest.Options{Challenge: claim.Claim.Challenge, ChallengeForClientData: base64.RawURLEncoding.EncodeToString([]byte("different"))})
	if err != nil {
		t.Fatal(err)
	}
	response = request(t, handler, http.MethodPost, "/api/v1/first-owner/register", firstOwnerRegistrationBody(t, profile.ID, mismatch), cookie, headers)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("challenge-mismatch register status = %d, want 409", response.StatusCode)
	}
	response.Body.Close()

	// A correct passkey registration disables the bootstrap grant.
	registration, err := webauthntest.Registration(webauthntest.Options{Challenge: claim.Claim.Challenge, Format: "packed"})
	if err != nil {
		t.Fatal(err)
	}
	correct := firstOwnerRegistrationBody(t, profile.ID, registration)
	response = request(t, handler, http.MethodPost, "/api/v1/first-owner/register", correct, cookie, headers)
	registered := readAll(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("register status = %d: %s", response.StatusCode, registered)
	}
	if !bytes.Contains(registered, []byte(`"ownerRegistered":true`)) || !bytes.Contains(registered, []byte(`"bootstrapGrantDisabled":true`)) {
		t.Fatalf("registration did not disable the grant: %s", registered)
	}

	// Irreversible: neither re-registration nor a new claim is allowed.
	response = request(t, handler, http.MethodPost, "/api/v1/first-owner/register", correct, cookie, headers)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("second register status = %d, want 409", response.StatusCode)
	}
	response.Body.Close()
	response = request(t, handler, http.MethodPost, "/api/v1/first-owner/claim", body, cookie, headers)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("re-claim after disable status = %d, want 409", response.StatusCode)
	}
	response.Body.Close()

	response = request(t, handler, http.MethodGet, "/api/v1/first-owner?profileId="+profile.ID, nil, cookie, nil)
	if status := readAll(t, response); !bytes.Contains(status, []byte(`"bootstrapGrantDisabled":true`)) {
		t.Fatalf("status does not reflect the disabled grant: %s", status)
	}
}
