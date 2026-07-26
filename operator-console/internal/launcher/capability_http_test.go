package launcher_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
)

func TestCapabilityPlanRendersPinnedSecretFreeOverlay(t *testing.T) {
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "capability-launch"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "capability-launch")
	profile := createProfile(t, handler, cookie, csrf, "Capabilities", "en", "local-lan")
	body, _ := json.Marshal(map[string]any{"profileId": profile.ID, "mode": "custom", "communityIds": []string{"collabora"}, "release": "v1.2.3", "repositoryUrl": "https://github.com/example/private-overlay.git", "domain": "home.example"})
	response := request(t, handler, http.MethodPost, "/api/v1/capabilities/plan", body, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("capability plan status = %d, want %d", response.StatusCode, http.StatusCreated)
	}
	result, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	for _, wanted := range []string{"ApplyCapabilities", "v1.2.3", "collabora", "nextcloud"} {
		if !bytes.Contains(result, []byte(wanted)) {
			t.Errorf("plan response lacks %q: %s", wanted, result)
		}
	}
	// A secret's VALUE must never appear. Its NAME may and must: the overlay
	// points Grafana at an existing Secret and names the one cert-manager writes
	// a certificate into, which is the safe way to reference either. So look for a
	// key that actually carries a value, not for the words themselves.
	if secretValuePattern.Match(result) {
		t.Fatalf("plan response contains secret-like value: %s", result)
	}
}

// A key literally named password, token or secret carrying a value — as it would
// appear inside the JSON-encoded overlay files.
var secretValuePattern = regexp.MustCompile(`(?i)(^|\\n)[\\t -]*(password|token|secret)[\\t ]*:[\\t ]*[^\\s\\\\]`)
