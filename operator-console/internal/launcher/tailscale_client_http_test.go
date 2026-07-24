package launcher_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"runtime"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
)

func TestTailscaleClientOfferReportsPlatformAndRetainsManualFallback(t *testing.T) {
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "tailscale"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })

	// Unauthenticated access is denied.
	response := request(t, handler, http.MethodGet, "/api/v1/tailscale-client", nil, nil, nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", response.StatusCode)
	}
	response.Body.Close()

	cookie, _ := exchange(t, handler, "tailscale")
	response = request(t, handler, http.MethodGet, "/api/v1/tailscale-client", nil, cookie, nil)
	body := readAll(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", response.StatusCode, body)
	}
	var offer struct {
		Platform struct {
			OS   string `json:"os"`
			Arch string `json:"arch"`
		} `json:"platform"`
		Detected       bool `json:"detected"`
		ManualFallback bool `json:"manualFallback"`
		Acquisition    struct {
			Available             bool   `json:"available"`
			ElevationRequired     bool   `json:"elevationRequired"`
			ManualInstructionsURL string `json:"manualInstructionsUrl"`
		} `json:"acquisition"`
	}
	if err := json.Unmarshal(body, &offer); err != nil {
		t.Fatal(err)
	}
	if offer.Platform.OS != runtime.GOOS || offer.Platform.Arch != runtime.GOARCH {
		t.Fatalf("offer platform = %+v, want %s/%s", offer.Platform, runtime.GOOS, runtime.GOARCH)
	}
	if !offer.ManualFallback || offer.Acquisition.ManualInstructionsURL == "" {
		t.Fatalf("manual fallback not retained: %s", body)
	}
	// The shipped default catalog carries no verified pins yet, so no automated
	// acquisition (and therefore no elevation prompt) is offered.
	if offer.Acquisition.Available || offer.Acquisition.ElevationRequired {
		t.Fatalf("unexpected automated acquisition offered: %s", body)
	}
	if bytes.Contains(body, []byte("/usr/")) || bytes.Contains(body, []byte("Program Files")) {
		t.Fatalf("offer leaked a host filesystem path: %s", body)
	}
}
