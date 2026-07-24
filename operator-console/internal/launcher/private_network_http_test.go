package launcher_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
)

type privateNetworkResponse struct {
	Shape                string `json:"shape"`
	CoordinationExposure string `json:"coordinationExposure"`
	CoordinationHost     string `json:"coordinationHost"`
	GatewayHostname      string `json:"gatewayHostname"`
	OperatorEndpoints    []struct {
		Name   string `json:"name"`
		FQDN   string `json:"fqdn"`
		Target string `json:"target"`
	} `json:"operatorEndpoints"`
}

func TestPrivateNetworkEstablishesLANOnlyCoordinationAndOperatorDNS(t *testing.T) {
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "private-network"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "private-network")
	profile := createProfile(t, handler, cookie, csrf, "Home", "en", "local-lan")
	body, _ := json.Marshal(map[string]string{"profileId": profile.ID, "baseDomain": "smallworlds.internal"})
	headers := map[string]string{"X-CSRF-Token": csrf}

	// Establishing before the vault is unlocked cannot custody the coordination secret.
	response := request(t, handler, http.MethodPost, "/api/v1/private-network/establish", body, cookie, headers)
	if response.StatusCode != http.StatusLocked {
		t.Fatalf("establish while locked status = %d, want 423", response.StatusCode)
	}
	response.Body.Close()

	unlockVaultForRecoveryTest(t, handler, cookie, csrf)

	response = request(t, handler, http.MethodPost, "/api/v1/private-network/establish", body, cookie, headers)
	created := readAll(t, response)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("establish status = %d: %s", response.StatusCode, created)
	}
	if bytes.Contains(created, []byte("secret")) || bytes.Contains(created, []byte("KEY")) {
		t.Fatalf("establish leaked coordination secret to the browser: %s", created)
	}
	var network privateNetworkResponse
	if err := json.Unmarshal(created, &network); err != nil {
		t.Fatal(err)
	}
	if network.Shape != "lan-only" || network.CoordinationExposure != "private" {
		t.Fatalf("not a private LAN-only shape: %s", created)
	}
	if network.CoordinationHost != "headscale.smallworlds.internal" || network.GatewayHostname != "gateway.smallworlds.internal" {
		t.Fatalf("unexpected coordination/gateway hosts: %s", created)
	}
	if len(network.OperatorEndpoints) != 3 {
		t.Fatalf("operator endpoints = %d", len(network.OperatorEndpoints))
	}
	for _, endpoint := range network.OperatorEndpoints {
		if endpoint.Target != network.GatewayHostname {
			t.Fatalf("operator hostname %q does not resolve to the gateway: %s", endpoint.FQDN, created)
		}
	}

	// Re-establishing is idempotent.
	response = request(t, handler, http.MethodPost, "/api/v1/private-network/establish", body, cookie, headers)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("re-establish status = %d", response.StatusCode)
	}
	response.Body.Close()

	// The coordination secret is surfaced in the credentials view without its value.
	response = request(t, handler, http.MethodGet, "/api/v1/profiles/"+profile.ID+"/credentials", nil, cookie, nil)
	credentials := readAll(t, response)
	if !bytes.Contains(credentials, []byte("headscale-coordination-secret")) {
		t.Fatalf("credentials view missing coordination custody entry: %s", credentials)
	}

	response = request(t, handler, http.MethodGet, "/api/v1/private-network?profileId="+profile.ID, nil, cookie, nil)
	status := readAll(t, response)
	if !bytes.Contains(status, []byte(`"console.smallworlds.internal"`)) {
		t.Fatalf("status missing operator hostname: %s", status)
	}
}

func TestPrivateNetworkRejectedForNonLANOnlyModes(t *testing.T) {
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "private-network"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "private-network")
	profile := createProfile(t, handler, cookie, csrf, "Cloud", "en", "local-public")
	unlockVaultForRecoveryTest(t, handler, cookie, csrf)
	body, _ := json.Marshal(map[string]string{"profileId": profile.ID, "baseDomain": "smallworlds.internal"})
	response := request(t, handler, http.MethodPost, "/api/v1/private-network/establish", body, cookie, map[string]string{"X-CSRF-Token": csrf})
	defer response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("establish for local-public status = %d, want 409", response.StatusCode)
	}
}
