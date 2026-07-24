package launcher_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
)

func TestGatewayAccessIsHTTPSOnlyAndRejectsForgedHosts(t *testing.T) {
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "gateway-access"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "gateway-access")
	profile := createProfile(t, handler, cookie, csrf, "Home", "en", "local-lan")
	headers := map[string]string{"X-CSRF-Token": csrf}
	unlockVaultForRecoveryTest(t, handler, cookie, csrf)

	// Without a Private Network there is no policy to derive.
	response := request(t, handler, http.MethodGet, "/api/v1/gateway-access?profileId="+profile.ID, nil, cookie, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("gateway access without private network status = %d, want 404", response.StatusCode)
	}
	response.Body.Close()

	network, _ := json.Marshal(map[string]string{"profileId": profile.ID, "baseDomain": "smallworlds.internal"})
	response = request(t, handler, http.MethodPost, "/api/v1/private-network/establish", network, cookie, headers)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("private network status = %d", response.StatusCode)
	}
	response.Body.Close()

	response = request(t, handler, http.MethodGet, "/api/v1/gateway-access?profileId="+profile.ID, nil, cookie, nil)
	body := readAll(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("gateway access status = %d: %s", response.StatusCode, body)
	}
	if !bytes.Contains(body, []byte(`"scheme":"https"`)) || !bytes.Contains(body, []byte(`"entrypoint":"private-gateway"`)) {
		t.Fatalf("policy is not HTTPS-only via the private gateway: %s", body)
	}
	if !bytes.Contains(body, []byte(`"lanIngress":"deny"`)) || !bytes.Contains(body, []byte(`"publicIngress":"deny"`)) {
		t.Fatalf("policy does not deny LAN/public ingress: %s", body)
	}

	check := func(host string) bool {
		payload, _ := json.Marshal(map[string]string{"profileId": profile.ID, "host": host})
		response := request(t, handler, http.MethodPost, "/api/v1/gateway-access/check", payload, cookie, headers)
		result := readAll(t, response)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("check %q status = %d: %s", host, response.StatusCode, result)
		}
		var decoded struct {
			Allowed bool `json:"allowed"`
		}
		if err := json.Unmarshal(result, &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded.Allowed
	}

	if !check("console.smallworlds.internal") || !check("grafana.smallworlds.internal:443") {
		t.Fatal("legitimate operator Host rejected by the gateway policy")
	}
	for _, forged := range []string{"evil.example", "192.168.178.52", "console.smallworlds.internal.evil.example", "smallworlds.internal"} {
		if check(forged) {
			t.Fatalf("forged Host accepted: %q", forged)
		}
	}
}
