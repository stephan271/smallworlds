package launcher_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
	"github.com/stephan271/smallworlds/operator-console/internal/state"
)

type setupSettingsResponse struct {
	ProfileID      string   `json:"profileId"`
	Domain         string   `json:"domain"`
	Release        string   `json:"release"`
	CapabilityApps []string `json:"capabilityApps"`
	NodeHost       string   `json:"nodeHost"`
	NodePort       int      `json:"nodePort"`
	NodeUsername   string   `json:"nodeUsername"`
	ManageDNS      bool     `json:"manageDns"`
	RecordedAt     string   `json:"recordedAt"`
}

func TestSetupSettingsAreSavedOncePerProfileAndReturnedOnReopen(t *testing.T) {
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "settings"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "settings")
	profile := createProfile(t, handler, cookie, csrf, "Home", "en", "local-lan")
	headers := map[string]string{"X-CSRF-Token": csrf}
	path := "/api/v1/profiles/" + profile.ID + "/settings"

	// A profile that has answered nothing yet reads as empty rather than 404,
	// so the console can open the journey without special-casing first use.
	response := request(t, handler, http.MethodGet, path, nil, cookie, nil)
	body := readAll(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("first read status = %d: %s", response.StatusCode, body)
	}
	var empty setupSettingsResponse
	if err := json.Unmarshal(body, &empty); err != nil {
		t.Fatal(err)
	}
	if empty.Domain != "" || empty.NodeHost != "" {
		t.Fatalf("unanswered settings = %s", body)
	}

	saved, _ := json.Marshal(map[string]any{
		"domain":         "home.example",
		"release":        "v1.2.27",
		"capabilityApps": []string{"nextcloud"},
		"nodeHost":       "node.example",
		"nodePort":       22,
		"nodeUsername":   "operator",
		"manageDns":      true,
	})
	response = request(t, handler, http.MethodPut, path, saved, cookie, headers)
	body = readAll(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("save status = %d: %s", response.StatusCode, body)
	}

	response = request(t, handler, http.MethodGet, path, nil, cookie, nil)
	body = readAll(t, response)
	var loaded setupSettingsResponse
	if err := json.Unmarshal(body, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.Domain != "home.example" || loaded.NodeHost != "node.example" || loaded.NodePort != 22 || !loaded.ManageDNS {
		t.Fatalf("reopened settings = %s", body)
	}
	if len(loaded.CapabilityApps) != 1 || loaded.CapabilityApps[0] != "nextcloud" {
		t.Fatalf("reopened apps = %s", body)
	}
	if loaded.ProfileID != profile.ID || loaded.RecordedAt == "" {
		t.Fatalf("settings identity = %s", body)
	}
}

func TestSetupSettingsRejectFieldsThatCouldCarrySecrets(t *testing.T) {
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "settings"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "settings")
	profile := createProfile(t, handler, cookie, csrf, "Home", "en", "local-lan")
	path := "/api/v1/profiles/" + profile.ID + "/settings"

	// The settings table is not a general key-value store: a browser must not be
	// able to park a token in it under a name the launcher does not know.
	smuggled := []byte(`{"domain":"home.example","nodePassword":"hunter2"}`)
	response := request(t, handler, http.MethodPut, path, smuggled, cookie, map[string]string{"X-CSRF-Token": csrf})
	body := readAll(t, response)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("smuggled secret status = %d: %s", response.StatusCode, body)
	}

	// The rejected write must not have partially landed.
	response = request(t, handler, http.MethodGet, path, nil, cookie, nil)
	body = readAll(t, response)
	var loaded setupSettingsResponse
	if err := json.Unmarshal(body, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.Domain != "" {
		t.Fatalf("rejected write landed = %s", body)
	}
}

func TestSetupSettingsWritesRequireCSRFAndAKnownProfile(t *testing.T) {
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "settings"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "settings")
	profile := createProfile(t, handler, cookie, csrf, "Home", "en", "local-lan")
	saved := []byte(`{"domain":"home.example"}`)

	response := request(t, handler, http.MethodPut, "/api/v1/profiles/"+profile.ID+"/settings", saved, cookie, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("save without CSRF status = %d", response.StatusCode)
	}
	response.Body.Close()

	response = request(t, handler, http.MethodGet, "/api/v1/profiles/unknown/settings", nil, cookie, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown profile status = %d", response.StatusCode)
	}
	response.Body.Close()
}

func TestSetupSettingsFallBackToConfirmedNodeTrustSoTheHostIsNeverRetyped(t *testing.T) {
	dataDir := t.TempDir()
	handler, err := launcher.New(launcher.Config{DataDir: dataDir, LaunchToken: "settings"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "settings")
	profile := createProfile(t, handler, cookie, csrf, "Home", "en", "local-lan")

	// Confirming node trust needs a reachable machine, which a unit test has no
	// business requiring. Write the trust the probe/trust round trip would have
	// left behind, then assert the settings read picks it up.
	store, err := state.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordNodeTrust(context.Background(), state.NodeTrust{
		ProfileID:   profile.ID,
		Host:        "node.example",
		Port:        2222,
		Username:    "operator",
		Fingerprint: "SHA256:confirmed",
		ConfirmedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	response := request(t, handler, http.MethodGet, "/api/v1/profiles/"+profile.ID+"/settings", nil, cookie, nil)
	body := readAll(t, response)
	var loaded setupSettingsResponse
	if err := json.Unmarshal(body, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.NodeHost != "node.example" || loaded.NodeUsername != "operator" || loaded.NodePort != 2222 {
		t.Fatalf("settings backfilled from node trust = %s", body)
	}
}
