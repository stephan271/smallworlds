package launcher_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
)

type clusterCAReferenceResponse struct {
	Reference struct {
		RootSubject         string `json:"rootSubject"`
		RootFingerprint     string `json:"rootFingerprint"`
		IntermediateSubject string `json:"intermediateSubject"`
	} `json:"reference"`
	RootFingerprint      string `json:"rootFingerprint"`
	DeviceTrustInstalled bool   `json:"deviceTrustInstalled"`
}

func TestClusterCAEstablishesProtectedRootAndInstallsDeviceTrust(t *testing.T) {
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "cluster-ca"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "cluster-ca")
	profile := createProfile(t, handler, cookie, csrf, "Home", "en", "local-lan")
	body, _ := json.Marshal(map[string]string{"profileId": profile.ID})
	headers := map[string]string{"X-CSRF-Token": csrf}

	// Establishing before the vault is unlocked cannot custody the private keys.
	response := request(t, handler, http.MethodPost, "/api/v1/cluster-ca/establish", body, cookie, headers)
	if response.StatusCode != http.StatusLocked {
		t.Fatalf("establish while locked status = %d, want 423", response.StatusCode)
	}
	response.Body.Close()

	unlockVaultForRecoveryTest(t, handler, cookie, csrf)

	response = request(t, handler, http.MethodPost, "/api/v1/cluster-ca/establish", body, cookie, headers)
	created := readAll(t, response)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("establish status = %d: %s", response.StatusCode, created)
	}
	if bytes.Contains(created, []byte("PRIVATE KEY")) || bytes.Contains(created, []byte("rootCertificatePem")) {
		t.Fatalf("establish leaked secret or certificate material to the browser: %s", created)
	}
	var establish clusterCAReferenceResponse
	if err := json.Unmarshal(created, &establish); err != nil {
		t.Fatal(err)
	}
	if establish.RootFingerprint == "" || establish.DeviceTrustInstalled {
		t.Fatalf("unexpected establish response: %s", created)
	}

	// Re-establishing is idempotent and keeps the same root identity.
	response = request(t, handler, http.MethodPost, "/api/v1/cluster-ca/establish", body, cookie, headers)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("re-establish status = %d", response.StatusCode)
	}
	var reestablish clusterCAReferenceResponse
	if err := json.NewDecoder(response.Body).Decode(&reestablish); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if reestablish.RootFingerprint != establish.RootFingerprint {
		t.Fatalf("root identity changed on re-establish: %q vs %q", reestablish.RootFingerprint, establish.RootFingerprint)
	}

	// The credentials view surfaces the vault-custodied keys without values.
	response = request(t, handler, http.MethodGet, "/api/v1/profiles/"+profile.ID+"/credentials", nil, cookie, nil)
	credentials := readAll(t, response)
	if !bytes.Contains(credentials, []byte("cluster-ca-root-key")) || !bytes.Contains(credentials, []byte("cluster-ca-intermediate-key")) {
		t.Fatalf("credentials view missing Cluster CA custody entries: %s", credentials)
	}
	if bytes.Contains(credentials, []byte("PRIVATE KEY")) {
		t.Fatalf("credentials view leaked key material: %s", credentials)
	}

	// Installing device trust returns only the public root certificate.
	response = request(t, handler, http.MethodPost, "/api/v1/cluster-ca/device-trust", body, cookie, headers)
	trust := readAll(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("device-trust status = %d: %s", response.StatusCode, trust)
	}
	if !bytes.Contains(trust, []byte("BEGIN CERTIFICATE")) || bytes.Contains(trust, []byte("PRIVATE KEY")) {
		t.Fatalf("device trust material is not a key-free certificate: %s", trust)
	}
	if !bytes.Contains(trust, []byte(`"deviceTrustInstalled":true`)) {
		t.Fatalf("device trust not marked installed: %s", trust)
	}

	response = request(t, handler, http.MethodGet, "/api/v1/cluster-ca?profileId="+profile.ID, nil, cookie, nil)
	status := readAll(t, response)
	if !bytes.Contains(status, []byte(`"deviceTrustInstalled":true`)) {
		t.Fatalf("status does not reflect installed device trust: %s", status)
	}
}

func TestClusterCARejectedForNonLANOnlyModes(t *testing.T) {
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "cluster-ca"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "cluster-ca")
	profile := createProfile(t, handler, cookie, csrf, "Cloud", "en", "hetzner")
	unlockVaultForRecoveryTest(t, handler, cookie, csrf)
	body, _ := json.Marshal(map[string]string{"profileId": profile.ID})
	response := request(t, handler, http.MethodPost, "/api/v1/cluster-ca/establish", body, cookie, map[string]string{"X-CSRF-Token": csrf})
	defer response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("establish for hetzner status = %d, want 409", response.StatusCode)
	}
}
