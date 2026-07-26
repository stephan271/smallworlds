package launcher_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
	"github.com/stephan271/smallworlds/operator-console/internal/state"
)

// A returning operator must be able to see what an earlier session established.
// Before these reads existed, that evidence lived only in the browser tab that
// observed it, so reopening an existing cluster looked like starting over.
func TestRecordedNodeTrustAndOverlayAreReadableWhenReopeningAProfile(t *testing.T) {
	dataDir := t.TempDir()
	handler, err := launcher.New(launcher.Config{DataDir: dataDir, LaunchToken: "reopen"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "reopen")
	profile := createProfile(t, handler, cookie, csrf, "Home", "en", "local-lan")
	trustPath := "/api/v1/profiles/" + profile.ID + "/node-trust"
	overlayPath := "/api/v1/profiles/" + profile.ID + "/overlay"

	// A profile that has not got that far says so, rather than inventing a
	// machine or a repository the operator never confirmed.
	for _, path := range []string{trustPath, overlayPath} {
		response := request(t, handler, http.MethodGet, path, nil, cookie, nil)
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s before any evidence: status=%d body=%s", path, response.StatusCode, readAll(t, response))
		}
		response.Body.Close()
	}

	// Confirming trust and establishing an overlay both need the outside world,
	// which a unit test has no business requiring. Write what those round trips
	// would have left behind, then assert the reads surface it.
	store, err := state.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordNodeTrust(context.Background(), state.NodeTrust{
		ProfileID:   profile.ID,
		Host:        "192.168.178.52",
		Port:        22,
		Username:    "operator",
		Fingerprint: "SHA256:confirmed",
		ConfirmedAt: time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOverlayIdentity(context.Background(), state.OverlayIdentity{
		ProfileID:     profile.ID,
		Provider:      "github",
		Repository:    "octocat/smallworlds-overlay",
		RepositoryURL: "https://github.com/octocat/smallworlds-overlay",
		Release:       "v1.2.20",
		Commit:        "abc123def456",
		Domain:        "home.example",
		RecordedAt:    time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	body := readAll(t, request(t, handler, http.MethodGet, trustPath, nil, cookie, nil))
	for _, want := range []string{"192.168.178.52", "operator", "SHA256:confirmed", "2026-07-20"} {
		if !bytes.Contains(body, []byte(want)) {
			t.Fatalf("recorded machine missing %q: %s", want, body)
		}
	}

	body = readAll(t, request(t, handler, http.MethodGet, overlayPath, nil, cookie, nil))
	for _, want := range []string{"octocat/smallworlds-overlay", "v1.2.20", "abc123def456", "home.example"} {
		if !bytes.Contains(body, []byte(want)) {
			t.Fatalf("recorded overlay missing %q: %s", want, body)
		}
	}
}

func TestRecordedSetupReadsRequireAuthentication(t *testing.T) {
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "reopen"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	for _, path := range []string{"/api/v1/profiles/any/node-trust", "/api/v1/profiles/any/overlay"} {
		response := request(t, handler, http.MethodGet, path, nil, nil, nil)
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s without a session: status=%d", path, response.StatusCode)
		}
		response.Body.Close()
	}
}
