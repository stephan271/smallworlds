package consoleserve

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/assessment"
	"github.com/stephan271/smallworlds/operator-console/internal/capability"
	"github.com/stephan271/smallworlds/operator-console/internal/kubeclient"
)

var errNoSuchHost = errors.New("no such host")

// --- a stand-in Keycloak realm ---

type fakeKeycloak struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	roles  []string
	nonce  string
}

func newFakeKeycloak(t *testing.T, roles ...string) *fakeKeycloak {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	realm := &fakeKeycloak{key: key, roles: roles}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(response http.ResponseWriter, _ *http.Request) {
		base := realm.server.URL
		_ = json.NewEncoder(response).Encode(map[string]string{
			"issuer":                 base,
			"authorization_endpoint": base + "/protocol/openid-connect/auth",
			"token_endpoint":         base + "/protocol/openid-connect/token",
			"jwks_uri":               base + "/protocol/openid-connect/certs",
		})
	})
	// The authorize endpoint records the nonce the console issued, so the ID
	// token it later mints is bound to that exact login.
	mux.HandleFunc("/protocol/openid-connect/auth", func(response http.ResponseWriter, request *http.Request) {
		realm.nonce = request.URL.Query().Get("nonce")
		response.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/protocol/openid-connect/token", func(response http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(response).Encode(map[string]string{"id_token": realm.idToken(t, "smallworlds-console")})
	})
	mux.HandleFunc("/protocol/openid-connect/certs", func(response http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(response).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "test-key", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	realm.server = httptest.NewServer(mux)
	t.Cleanup(realm.server.Close)
	return realm
}

func (realm *fakeKeycloak) idToken(t *testing.T, clientID string) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "test-key", "typ": "JWT"})
	claims := map[string]any{
		"iss": realm.server.URL, "aud": clientID, "azp": clientID, "nonce": realm.nonce,
		"sub": "operator-1", "preferred_username": "ada",
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
		"resource_access": map[string]any{clientID: map[string]any{"roles": realm.roles}},
	}
	payload, _ := json.Marshal(claims)
	signed := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signed))
	signature, err := rsa.SignPKCS1v15(rand.Reader, realm.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// --- a stand-in Kubernetes API server ---

func newFakeAPIServer(t *testing.T, objects map[string]any) *kubeclient.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		object, ok := objects[request.URL.Path]
		if !ok {
			response.WriteHeader(http.StatusNotFound)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(object)
	}))
	t.Cleanup(server.Close)
	return &kubeclient.Client{
		BaseURL:     server.URL,
		Namespace:   "operator-console",
		HTTPClient:  server.Client(),
		TokenSource: func() (string, error) { return "test-token", nil },
	}
}

func convergedCluster() map[string]any {
	return map[string]any{
		kubeclient.NamespacedPath(kubeclient.ArgoAPI, "argocd", "applications", "smallworlds-root"): map[string]any{
			"status": map[string]any{"resources": []map[string]any{
				{"kind": "Application", "name": "keycloak"},
				{"kind": "Application", "name": "excalidraw"},
				{"kind": "Application", "name": "operator-console"},
			}},
		},
		// Excalidraw's declared dependency on Keycloak has to be satisfied by a
		// Keycloak that is itself delivered, or the capability reads blocked.
		kubeclient.NamespacedPath(kubeclient.ArgoAPI, "argocd", "applications", "keycloak"): map[string]any{
			"status": map[string]any{
				"sync":   map[string]any{"status": "Synced"},
				"health": map[string]any{"status": "Healthy"},
			},
		},
		kubeclient.NamespacedPath(kubeclient.ArgoAPI, "argocd", "applications", "excalidraw"): map[string]any{
			"status": map[string]any{
				"sync":   map[string]any{"status": "Synced"},
				"health": map[string]any{"status": "Healthy"},
				"resources": []map[string]any{
					{"kind": "Deployment", "namespace": "excalidraw", "name": "excalidraw"},
					{"kind": "Ingress", "namespace": "excalidraw", "name": "excalidraw"},
				},
			},
		},
		kubeclient.NamespacedPath(kubeclient.NetworkingAPI, "excalidraw", "ingresses", "excalidraw"): map[string]any{
			"metadata": map[string]any{"annotations": map[string]string{"traefik.ingress.kubernetes.io/router.entrypoints": "websecure"}},
			"spec": map[string]any{
				"rules": []map[string]any{{"host": "draw.example.test"}},
				"tls":   []map[string]any{{"secretName": "excalidraw-tls"}},
			},
		},
		kubeclient.NamespacedPath(kubeclient.CertManagerAPI, "excalidraw", "certificates", ""): map[string]any{
			"items": []map[string]any{{
				"spec":   map[string]any{"secretName": "excalidraw-tls"},
				"status": map[string]any{"conditions": []map[string]any{{"type": "Ready", "status": "True"}}},
			}},
		},
		kubeclient.NamespacedPath(kubeclient.AppsAPI, "excalidraw", "deployments", "excalidraw"): map[string]any{
			"metadata": map[string]any{"generation": 1},
			"spec":     map[string]any{"replicas": 1},
			"status":   map[string]any{"observedGeneration": 1, "readyReplicas": 1, "updatedReplicas": 1},
		},
	}
}

func startConsole(t *testing.T, realm *fakeKeycloak, objects map[string]any) *httptest.Server {
	t.Helper()
	client := newFakeAPIServer(t, objects)
	settings := Settings{
		Issuer:         realm.server.URL,
		ClientID:       "smallworlds-console",
		ExternalURL:    "https://console.example.test",
		BaseDomain:     "example.test",
		SessionKey:     []byte("a-fixed-session-key-for-the-test"),
		Namespace:      "operator-console",
		ArgoNamespace:  "argocd",
		DeploymentMode: capability.Hetzner,
		EvidenceMaxAge: time.Hour,
	}
	server, err := newWithClient(context.Background(), settings, client, adapters{
		lookupHost: func(_ context.Context, host string) ([]string, error) {
			if host == "draw.example.test" {
				return []string{"203.0.113.7"}, nil
			}
			return nil, errNoSuchHost
		},
	})
	if err != nil {
		t.Fatalf("build console: %v", err)
	}
	// TLS, not plain HTTP: the console's session and login cookies are Secure,
	// which is what a browser needs behind the gateway — and what makes a
	// cleartext test silently drop them.
	consoleServer := httptest.NewTLSServer(server.Handler)
	t.Cleanup(consoleServer.Close)
	return consoleServer
}

// signIn drives the real OIDC flow against the fake realm and returns a client
// holding the console's session cookie.
func signIn(t *testing.T, console *httptest.Server, realm *fakeKeycloak) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := console.Client()
	client.Jar = jar
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	response, err := client.Get(console.URL + "/api/v1/auth/login")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("login status = %d, want a redirect to Keycloak", response.StatusCode)
	}
	authorizeURL, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatalf("authorization url: %v", err)
	}
	// Visiting the authorize endpoint is what hands Keycloak the nonce.
	if _, err := realm.server.Client().Get(authorizeURL.String()); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	state := authorizeURL.Query().Get("state")

	callback, err := client.Get(console.URL + "/api/v1/auth/callback?code=the-code&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	_ = callback.Body.Close()
	if location := callback.Header.Get("Location"); strings.Contains(location, "auth_error") {
		t.Fatalf("login failed: %s", location)
	}
	return client
}

// The console was already complete as code; what was missing was a process that
// serves it. This is that end to end: a real OIDC login into a real HTTP surface
// whose answers come from live cluster evidence.
func TestServedConsoleCompletesLoginAndReportsLiveEvidence(t *testing.T) {
	realm := newFakeKeycloak(t, "operator")
	console := startConsole(t, realm, convergedCluster())
	client := signIn(t, console, realm)

	var session struct {
		Authenticated bool     `json:"authenticated"`
		Username      string   `json:"username"`
		Role          string   `json:"role"`
		Permissions   []string `json:"permissions"`
	}
	getJSON(t, client, console.URL+"/api/v1/session", &session)
	if !session.Authenticated || session.Username != "ada" || session.Role != "operator" {
		t.Fatalf("session = %+v", session)
	}

	var overview struct {
		Total        int `json:"total"`
		Capabilities []struct {
			ID         string `json:"id"`
			State      string `json:"state"`
			ReasonCode string `json:"reasonCode"`
		} `json:"capabilities"`
	}
	getJSON(t, client, console.URL+"/api/v1/overview", &overview)
	if overview.Total != len(capability.DefaultCatalog().Capabilities) {
		t.Fatalf("overview covers %d capabilities, want the whole catalog", overview.Total)
	}
	states := map[string]string{}
	for _, item := range overview.Capabilities {
		states[item.ID] = item.State
	}
	// excalidraw is declared, synced, healthy and its Deployment is ready.
	if states["excalidraw"] != string(assessment.StateHealthy) {
		t.Fatalf("excalidraw = %q, want healthy", states["excalidraw"])
	}
	// nextcloud is not declared by the root Application at all.
	if states["nextcloud"] == string(assessment.StateHealthy) {
		t.Fatal("a capability the overlay does not declare must not read healthy")
	}
}

// Server-side authorization, not the UI, is what stops an Observer mutating.
func TestServedConsoleEnforcesConsoleRoles(t *testing.T) {
	realm := newFakeKeycloak(t, "observer")
	console := startConsole(t, realm, convergedCluster())
	client := signIn(t, console, realm)

	if status := statusOf(t, client, console.URL+"/api/v1/overview"); status != http.StatusOK {
		t.Fatalf("observe = %d, want 200", status)
	}
	if status := statusOf(t, client, console.URL+"/api/v1/proposals"); status != http.StatusForbidden {
		t.Fatalf("propose as observer = %d, want 403", status)
	}
	if status := statusOf(t, client, console.URL+"/api/v1/administration/access"); status != http.StatusForbidden {
		t.Fatalf("administer as observer = %d, want 403", status)
	}
}

// A user Keycloak authenticates but who holds no Console Role is denied by
// default and never receives a session.
func TestServedConsoleDeniesUserWithoutConsoleRole(t *testing.T) {
	realm := newFakeKeycloak(t)
	console := startConsole(t, realm, convergedCluster())
	jar, _ := cookiejar.New(nil)
	client := console.Client()
	client.Jar = jar
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	response, err := client.Get(console.URL + "/api/v1/auth/login")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = response.Body.Close()
	authorizeURL, _ := url.Parse(response.Header.Get("Location"))
	if _, err := realm.server.Client().Get(authorizeURL.String()); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	callback, err := client.Get(console.URL + "/api/v1/auth/callback?code=c&state=" + url.QueryEscape(authorizeURL.Query().Get("state")))
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	_ = callback.Body.Close()
	if !strings.Contains(callback.Header.Get("Location"), "no_console_role") {
		t.Fatalf("expected a no_console_role redirect, got %q", callback.Header.Get("Location"))
	}
	if status := statusOf(t, client, console.URL+"/api/v1/overview"); status != http.StatusUnauthorized {
		t.Fatalf("overview without a session = %d, want 401", status)
	}
}

// The kubelet holds no session, so the probes must answer outside the role gate
// — otherwise a healthy pod would be restarted forever.
func TestServedConsoleAnswersProbesWithoutASession(t *testing.T) {
	realm := newFakeKeycloak(t, "operator")
	console := startConsole(t, realm, convergedCluster())
	for _, path := range []string{"/healthz", "/readyz"} {
		response, err := console.Client().Get(console.URL + path)
		if err != nil {
			t.Fatalf("probe %s: %v", path, err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s = %d, want 200", path, response.StatusCode)
		}
	}
	if status := statusOf(t, console.Client(), console.URL+"/api/v1/overview"); status != http.StatusUnauthorized {
		t.Fatalf("anonymous overview = %d, want 401", status)
	}
}

// An Operator opening the console hostname must land on the console, not on a
// setup wizard for a cluster that already exists.
func TestServedConsoleSendsTheRootToTheConsoleScreen(t *testing.T) {
	realm := newFakeKeycloak(t, "operator")
	console := startConsole(t, realm, convergedCluster())
	client := console.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	response, err := client.Get(console.URL + "/")
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusFound || response.Header.Get("Location") != "/console" {
		t.Fatalf("root = %d %q, want a redirect to /console", response.StatusCode, response.Header.Get("Location"))
	}

	// The login failure code must survive that redirect, or a refused login
	// would look like a silent one.
	response, err = client.Get(console.URL + "/?auth_error=no_console_role")
	if err != nil {
		t.Fatalf("root with an error: %v", err)
	}
	_ = response.Body.Close()
	if !strings.Contains(response.Header.Get("Location"), "auth_error=no_console_role") {
		t.Fatalf("redirect dropped the login error: %q", response.Header.Get("Location"))
	}
}

func TestSettingsRequireTheValuesALoginNeeds(t *testing.T) {
	t.Setenv("SMALLWORLDS_OIDC_ISSUER", "")
	t.Setenv("SMALLWORLDS_OIDC_CLIENT_ID", "")
	t.Setenv("SMALLWORLDS_CONSOLE_URL", "")
	if _, err := SettingsFromEnvironment(); err == nil {
		t.Fatal("expected missing configuration to fail startup")
	}

	t.Setenv("SMALLWORLDS_OIDC_ISSUER", "https://identity.example.test/realms/smallworlds/")
	t.Setenv("SMALLWORLDS_OIDC_CLIENT_ID", "smallworlds-console")
	t.Setenv("SMALLWORLDS_CONSOLE_URL", "https://console.example.test/")
	settings, err := SettingsFromEnvironment()
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	if settings.Issuer != "https://identity.example.test/realms/smallworlds" {
		t.Fatalf("issuer = %q, trailing slash not trimmed", settings.Issuer)
	}
	if settings.RedirectURI() != "https://console.example.test/api/v1/auth/callback" {
		t.Fatalf("redirect uri = %q", settings.RedirectURI())
	}
	if settings.Namespace != "operator-console" || settings.ArgoNamespace != "argocd" || settings.GatewayEntrypoint != "private-gateway" {
		t.Fatalf("defaults not applied: %+v", settings)
	}
}

// The catalog records policy; the engine needs expected reachability. A backend
// service with no Ingress is internal, an operator interface is private, and a
// member application is public — three different access rules.
func TestCapabilityRefsTranslateExposurePolicy(t *testing.T) {
	refs := map[string]assessment.CapabilityRef{}
	for _, ref := range CapabilityRefs(capability.DefaultCatalog()) {
		refs[ref.ID] = ref
	}
	for id, want := range map[string]assessment.Exposure{
		"operator-console":      assessment.ExposurePrivate,
		"argocd-ingress":        assessment.ExposurePrivate,
		"kube-prometheus-stack": assessment.ExposurePrivate,
		"keycloak":              assessment.ExposurePublic,
		"headscale":             assessment.ExposurePublic,
		"nextcloud":             assessment.ExposurePublic,
		"cloudnative-pg":        assessment.ExposureInternal,
		"cert-manager":          assessment.ExposureInternal,
	} {
		if refs[id].Exposure != want {
			t.Errorf("%s exposure = %q, want %q", id, refs[id].Exposure, want)
		}
	}
	// Statefulness comes from the protection inventory, so stale backups degrade
	// exactly the capabilities that own data.
	if !refs["nextcloud"].Stateful {
		t.Error("nextcloud owns declared datasets and must be stateful")
	}
	if refs["excalidraw"].Stateful {
		t.Error("excalidraw owns no dataset and must not be stateful")
	}
	// The console assesses itself (acceptance criterion 8).
	if _, present := refs["operator-console"]; !present {
		t.Error("the Operator Console must appear in the capability model")
	}
}

func getJSON(t *testing.T, client *http.Client, endpoint string, target any) {
	t.Helper()
	response, err := client.Get(endpoint)
	if err != nil {
		t.Fatalf("get %s: %v", endpoint, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("get %s = %d", endpoint, response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decode %s: %v", endpoint, err)
	}
}

func statusOf(t *testing.T, client *http.Client, endpoint string) int {
	t.Helper()
	response, err := client.Get(endpoint)
	if err != nil {
		t.Fatalf("get %s: %v", endpoint, err)
	}
	defer response.Body.Close()
	return response.StatusCode
}
