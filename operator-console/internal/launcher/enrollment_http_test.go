package launcher_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
)

type enrollmentCredential struct {
	Role      string  `json:"role"`
	SingleUse bool    `json:"singleUse"`
	Stable    bool    `json:"stable"`
	ExpiresAt *string `json:"expiresAt"`
	Used      bool    `json:"used"`
}

type enrollmentResponse struct {
	BaseDomain string               `json:"baseDomain"`
	Launcher   enrollmentCredential `json:"launcher"`
	Gateway    enrollmentCredential `json:"gateway"`
}

func TestEnrollmentIssuesSingleUseLauncherAndStableGatewayIdentity(t *testing.T) {
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "enrollment"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "enrollment")
	profile := createProfile(t, handler, cookie, csrf, "Home", "en", "local-lan")
	headers := map[string]string{"X-CSRF-Token": csrf}
	body, _ := json.Marshal(map[string]string{"profileId": profile.ID})
	unlockVaultForRecoveryTest(t, handler, cookie, csrf)

	// Enrollment requires an established Private Network base domain first.
	response := request(t, handler, http.MethodPost, "/api/v1/enrollment/establish", body, cookie, headers)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("establish without private network status = %d, want 409", response.StatusCode)
	}
	response.Body.Close()

	network, _ := json.Marshal(map[string]string{"profileId": profile.ID, "baseDomain": "smallworlds.internal"})
	response = request(t, handler, http.MethodPost, "/api/v1/private-network/establish", network, cookie, headers)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("private network status = %d", response.StatusCode)
	}
	response.Body.Close()

	response = request(t, handler, http.MethodPost, "/api/v1/enrollment/establish", body, cookie, headers)
	created := readAll(t, response)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("enrollment status = %d: %s", response.StatusCode, created)
	}
	if bytes.Contains(created, []byte("secret")) || bytes.Contains(created, []byte("KEY")) {
		t.Fatalf("enrollment leaked secret material: %s", created)
	}
	var enrollment enrollmentResponse
	if err := json.Unmarshal(created, &enrollment); err != nil {
		t.Fatal(err)
	}
	if !enrollment.Launcher.SingleUse || enrollment.Launcher.Stable || enrollment.Launcher.ExpiresAt == nil {
		t.Fatalf("launcher credential is not short-lived single-use: %s", created)
	}
	if enrollment.Gateway.Stable == false || enrollment.Gateway.SingleUse || enrollment.Gateway.ExpiresAt != nil {
		t.Fatalf("gateway identity is not stable/durable: %s", created)
	}

	// Both custody entries are visible without values.
	response = request(t, handler, http.MethodGet, "/api/v1/profiles/"+profile.ID+"/credentials", nil, cookie, nil)
	credentials := readAll(t, response)
	if !bytes.Contains(credentials, []byte("launcher-enrollment-preauth")) || !bytes.Contains(credentials, []byte("gateway-auth-key")) {
		t.Fatalf("credentials view missing enrollment custody entries: %s", credentials)
	}

	// The Launcher credential is single-use: consuming it once succeeds.
	response = request(t, handler, http.MethodPost, "/api/v1/enrollment/launcher/consume", body, cookie, headers)
	consumed := readAll(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("consume status = %d: %s", response.StatusCode, consumed)
	}
	if !bytes.Contains(consumed, []byte(`"used":true`)) {
		t.Fatalf("consumed credential not marked used: %s", consumed)
	}

	// Consuming again is rejected.
	response = request(t, handler, http.MethodPost, "/api/v1/enrollment/launcher/consume", body, cookie, headers)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("second consume status = %d, want 409", response.StatusCode)
	}
	response.Body.Close()

	// The consumed Launcher custody entry is gone; the stable gateway remains.
	response = request(t, handler, http.MethodGet, "/api/v1/profiles/"+profile.ID+"/credentials", nil, cookie, nil)
	after := readAll(t, response)
	if bytes.Contains(after, []byte("launcher-enrollment-preauth")) {
		t.Fatalf("consumed launcher credential still listed: %s", after)
	}
	if !bytes.Contains(after, []byte("gateway-auth-key")) {
		t.Fatalf("stable gateway identity dropped after launcher consumption: %s", after)
	}
}
