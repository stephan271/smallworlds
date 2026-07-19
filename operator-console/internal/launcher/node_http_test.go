package launcher_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"runtime"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
)

func TestSameHostInspectionReturnsSafeAssessmentOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("same-host is intentionally Linux-only")
	}
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "same-host"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "same-host")
	profile := createProfile(t, handler, cookie, csrf, "Node", "en", "local-lan")
	body, _ := json.Marshal(map[string]any{"profileId": profile.ID, "target": map[string]any{"kind": "same-host"}, "authentication": map[string]any{"kind": "agent"}})
	response := request(t, handler, http.MethodPost, "/api/v1/nodes/inspect", body, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("same host status = %d", response.StatusCode)
	}
	data := readAll(t, response)
	if !bytes.Contains(data, []byte(`"operatingSystem":"linux"`)) || bytes.Contains(data, []byte("privateKey")) || bytes.Contains(data, []byte("password")) {
		t.Fatalf("unsafe or incomplete node report: %s", data)
	}
}

func TestNodeTrustRejectsFabricatedConfirmation(t *testing.T) {
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "fabricated-trust"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "fabricated-trust")
	profile := createProfile(t, handler, cookie, csrf, "Node", "en", "local-lan")
	body, _ := json.Marshal(map[string]any{"profileId": profile.ID, "target": map[string]any{"kind": "remote", "host": "node.example", "port": 22, "username": "operator"}, "fingerprint": "SHA256:unobserved", "confirmed": true})
	response := request(t, handler, http.MethodPost, "/api/v1/nodes/trust", body, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("fabricated trust status = %d", response.StatusCode)
	}
	response.Body.Close()
}

func TestNodeSSHKeyPlanRequiresPinnedNodeIdentity(t *testing.T) {
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "node-key-plan"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "node-key-plan")
	profile := createProfile(t, handler, cookie, csrf, "Node", "en", "local-lan")
	body, _ := json.Marshal(map[string]string{"profileId": profile.ID})
	response := request(t, handler, http.MethodPost, "/api/v1/nodes/ssh-key/plan", body, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("untrusted node key plan status = %d", response.StatusCode)
	}
	response.Body.Close()
}
