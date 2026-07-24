package console

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/assessment"
	"github.com/stephan271/smallworlds/operator-console/internal/consoleauth"
	"github.com/stephan271/smallworlds/operator-console/internal/protection"
)

const (
	testIssuer   = "https://id.test/realms/sw"
	testClientID = "operator-console"
)

var testNow = time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)

type fakeExchanger struct {
	claims consoleauth.Claims
	err    error
}

func (e *fakeExchanger) Exchange(context.Context, string, string) (consoleauth.Claims, error) {
	return e.claims, e.err
}

type fakeAssessor struct{}

func (fakeAssessor) Assess(_ context.Context, ref assessment.CapabilityRef) assessment.CapabilityAssessment {
	return assessment.CapabilityAssessment{CapabilityID: ref.ID, State: assessment.StateHealthy, ReasonCode: assessment.ReasonHealthy}
}

func newTestServer(t *testing.T, exchanger consoleauth.TokenExchanger) *Server {
	t.Helper()
	server, err := New(Config{
		Issuer:                testIssuer,
		ClientID:              testClientID,
		AuthorizationEndpoint: testIssuer + "/protocol/openid-connect/auth",
		RedirectURI:           "https://console.test/api/v1/auth/callback",
		Exchanger:             exchanger,
		Assessor:              fakeAssessor{},
		Catalog: []assessment.CapabilityRef{
			{ID: "nextcloud", Exposure: assessment.ExposurePrivate, Stateful: true},
			{ID: "keycloak", Exposure: assessment.ExposurePrivate, Stateful: false},
		},
		BaseDomain: "sw.example.internal",
		SessionKey: []byte("0123456789abcdef0123456789abcdef"),
		Leeway:     30 * time.Second,
		Now:        func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return server
}

func get(t *testing.T, server *Server, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	for _, cookie := range cookies {
		if cookie != nil {
			request.AddCookie(cookie)
		}
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

// pendingNonce reads the nonce out of a signed login cookie. The test knows the
// server's session key, so it can parse the cookie payload directly to mint a
// token echoing the nonce, the way Keycloak would.
func pendingNonce(t *testing.T, cookie *http.Cookie) string {
	t.Helper()
	encoded, _, _ := strings.Cut(cookie.Value, ".")
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode login cookie: %v", err)
	}
	var data struct {
		Nonce string `json:"nonce"`
	}
	if err := json.Unmarshal(payload, &data); err != nil {
		t.Fatalf("unmarshal login cookie: %v", err)
	}
	return data.Nonce
}

func claimsWithRole(nonce, roleName string) consoleauth.Claims {
	claims := consoleauth.Claims{
		Issuer:            testIssuer,
		Audience:          []string{testClientID},
		Nonce:             nonce,
		Subject:           "user-1",
		PreferredUsername: "alice",
		ExpiresAt:         testNow.Add(time.Hour),
		IssuedAt:          testNow.Add(-time.Minute),
	}
	if roleName != "" {
		claims.ClientRoles = map[string][]string{testClientID: {roleName}}
	}
	return claims
}

// login runs the OIDC login and callback with the given role name (empty for a
// user without any Console Role) and returns the callback recorder.
func login(t *testing.T, server *Server, exchanger *fakeExchanger, roleName string) *httptest.ResponseRecorder {
	t.Helper()
	start := get(t, server, "/api/v1/auth/login")
	loginCookie := findCookie(start.Result().Cookies(), loginCookieName)
	if loginCookie == nil {
		t.Fatal("login did not set a pending cookie")
	}
	location, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	state := location.Query().Get("state")

	exchanger.claims = claimsWithRole(pendingNonce(t, loginCookie), roleName)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback?code=auth-code&state="+state, nil)
	request.AddCookie(loginCookie)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

func loginSession(t *testing.T, server *Server, exchanger *fakeExchanger, roleName string) *http.Cookie {
	t.Helper()
	callback := login(t, server, exchanger, roleName)
	cookie := findCookie(callback.Result().Cookies(), sessionCookieName)
	if cookie == nil {
		t.Fatalf("login as %q did not set a session cookie", roleName)
	}
	return cookie
}

func TestSessionAnonymous(t *testing.T) {
	server := newTestServer(t, &fakeExchanger{})
	recorder := get(t, server, "/api/v1/session")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var body sessionResponse
	json.Unmarshal(recorder.Body.Bytes(), &body)
	if body.Authenticated {
		t.Fatal("anonymous session reported authenticated")
	}
}

func TestObserveRequiresAuthentication(t *testing.T) {
	server := newTestServer(t, &fakeExchanger{})
	if recorder := get(t, server, "/api/v1/overview"); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

func TestLoginFlowAndObserve(t *testing.T) {
	exchanger := &fakeExchanger{}
	server := newTestServer(t, exchanger)
	session := loginSession(t, server, exchanger, "operator")

	sessionRecorder := get(t, server, "/api/v1/session", session)
	var body sessionResponse
	json.Unmarshal(sessionRecorder.Body.Bytes(), &body)
	if !body.Authenticated || body.Role != consoleauth.RoleOperator || body.Username != "alice" {
		t.Fatalf("session = %+v, want authenticated operator alice", body)
	}

	overview := get(t, server, "/api/v1/overview", session)
	if overview.Code != http.StatusOK {
		t.Fatalf("overview status = %d, want 200", overview.Code)
	}
	var overviewBody overviewResponse
	json.Unmarshal(overview.Body.Bytes(), &overviewBody)
	if overviewBody.Total != 2 || overviewBody.ByState[assessment.StateHealthy] != 2 {
		t.Fatalf("overview = %+v, want 2 healthy capabilities", overviewBody)
	}

	if detail := get(t, server, "/api/v1/capabilities/nextcloud", session); detail.Code != http.StatusOK {
		t.Fatalf("capability detail status = %d, want 200", detail.Code)
	}
	if missing := get(t, server, "/api/v1/capabilities/does-not-exist", session); missing.Code != http.StatusNotFound {
		t.Fatalf("unknown capability status = %d, want 404", missing.Code)
	}
}

func TestDefaultDenyNoConsoleRole(t *testing.T) {
	exchanger := &fakeExchanger{}
	server := newTestServer(t, exchanger)

	callback := login(t, server, exchanger, "") // authenticated, but no console role
	if findCookie(callback.Result().Cookies(), sessionCookieName) != nil {
		t.Fatal("a user without a console role must not receive a session")
	}
	location := callback.Header().Get("Location")
	if !strings.Contains(location, "auth_error=no_console_role") {
		t.Fatalf("callback location = %q, want no_console_role", location)
	}
}

func TestAuthorizationMatrix(t *testing.T) {
	tests := []struct {
		role                     string
		overview, propose, admin int
	}{
		// The admin column checks the authorization gate, not the seam: observer and
		// operator are forbidden (403); an owner is admitted and — with no device
		// directory wired in this test server — honestly refuses with 503, which
		// still proves the owner passed the gate rather than being forbidden.
		{"observer", http.StatusOK, http.StatusForbidden, http.StatusForbidden},
		{"operator", http.StatusOK, http.StatusOK, http.StatusForbidden},
		{"owner", http.StatusOK, http.StatusOK, http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		t.Run(test.role, func(t *testing.T) {
			exchanger := &fakeExchanger{}
			server := newTestServer(t, exchanger)
			session := loginSession(t, server, exchanger, test.role)

			if code := get(t, server, "/api/v1/overview", session).Code; code != test.overview {
				t.Errorf("overview = %d, want %d", code, test.overview)
			}
			if code := get(t, server, "/api/v1/proposals", session).Code; code != test.propose {
				t.Errorf("proposals = %d, want %d", code, test.propose)
			}
			if code := get(t, server, "/api/v1/administration/access", session).Code; code != test.admin {
				t.Errorf("administration = %d, want %d", code, test.admin)
			}
		})
	}
}

func TestForgedSessionRejected(t *testing.T) {
	server := newTestServer(t, &fakeExchanger{})
	forged := &http.Cookie{Name: sessionCookieName, Value: "eyJyb2xlIjoib3duZXIifQ.not-a-valid-signature"}
	if recorder := get(t, server, "/api/v1/administration/access", forged); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for forged session", recorder.Code)
	}
}

func TestCallbackStateMismatch(t *testing.T) {
	exchanger := &fakeExchanger{}
	server := newTestServer(t, exchanger)

	start := get(t, server, "/api/v1/auth/login")
	loginCookie := findCookie(start.Result().Cookies(), loginCookieName)
	exchanger.claims = claimsWithRole(pendingNonce(t, loginCookie), "operator")

	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback?code=c&state=forged-state", nil)
	request.AddCookie(loginCookie)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if findCookie(recorder.Result().Cookies(), sessionCookieName) != nil {
		t.Fatal("a state mismatch must not establish a session")
	}
	if location := recorder.Header().Get("Location"); !strings.Contains(location, "auth_error=state_mismatch") {
		t.Fatalf("location = %q, want state_mismatch", location)
	}
}

// degradedArgoAssessor returns a capability whose delivery facet failed and
// routes to Argo CD, so the console must resolve a contextual deep link.
type degradedArgoAssessor struct{}

func (degradedArgoAssessor) Assess(_ context.Context, ref assessment.CapabilityRef) assessment.CapabilityAssessment {
	return assessment.CapabilityAssessment{
		CapabilityID: ref.ID,
		State:        assessment.StateFailed,
		ReasonCode:   assessment.ReasonDeliveryFailed,
		Facets: []assessment.Facet{{
			Kind:        assessment.FacetDelivery,
			State:       assessment.FacetFailed,
			ReasonCode:  assessment.ReasonDeliveryFailed,
			Remediation: &assessment.Remediation{Kind: assessment.RemediateArgoCD, Reference: ref.ID},
		}},
	}
}

func TestCapabilityDetailResolvesDeepLink(t *testing.T) {
	exchanger := &fakeExchanger{}
	server, err := New(Config{
		Issuer:                testIssuer,
		ClientID:              testClientID,
		AuthorizationEndpoint: testIssuer + "/protocol/openid-connect/auth",
		RedirectURI:           "https://console.test/api/v1/auth/callback",
		Exchanger:             exchanger,
		Assessor:              degradedArgoAssessor{},
		Catalog:               []assessment.CapabilityRef{{ID: "nextcloud", Exposure: assessment.ExposurePrivate}},
		BaseDomain:            "sw.example.internal",
		SessionKey:            []byte("0123456789abcdef0123456789abcdef"),
		Leeway:                30 * time.Second,
		Now:                   func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	session := loginSession(t, server, exchanger, "operator")

	detail := get(t, server, "/api/v1/capabilities/nextcloud", session)
	if detail.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", detail.Code)
	}
	var body struct {
		Facets []struct {
			Kind           string `json:"kind"`
			RemediationURL string `json:"remediationUrl"`
		} `json:"facets"`
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Facets) != 1 {
		t.Fatalf("facets = %d, want 1", len(body.Facets))
	}
	if got := body.Facets[0].RemediationURL; got != "https://argocd.sw.example.internal/applications/nextcloud" {
		t.Fatalf("remediationUrl = %q, want the private argocd app link", got)
	}
}

type fakeProtection struct {
	datasets []protection.DatasetProtection
}

func (f fakeProtection) Report(context.Context) []protection.DatasetProtection { return f.datasets }

func TestProtectionEndpoint(t *testing.T) {
	exchanger := &fakeExchanger{}
	server, err := New(Config{
		Issuer:                testIssuer,
		ClientID:              testClientID,
		AuthorizationEndpoint: testIssuer + "/protocol/openid-connect/auth",
		RedirectURI:           "https://console.test/api/v1/auth/callback",
		Exchanger:             exchanger,
		Assessor:              fakeAssessor{},
		Catalog:               []assessment.CapabilityRef{{ID: "nextcloud"}},
		Protection: fakeProtection{datasets: []protection.DatasetProtection{
			{Dataset: protection.Dataset{ID: "nextcloud-db", Capability: "nextcloud"}, Level: protection.LevelLocalOnly},
		}},
		SessionKey: []byte("0123456789abcdef0123456789abcdef"),
		Leeway:     30 * time.Second,
		Now:        func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Observe permission is required.
	if code := get(t, server, "/api/v1/protection").Code; code != http.StatusUnauthorized {
		t.Fatalf("anonymous protection status = %d, want 401", code)
	}
	session := loginSession(t, server, exchanger, "observer")
	recorder := get(t, server, "/api/v1/protection", session)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var body struct {
		Datasets []struct {
			Level   string `json:"level"`
			Dataset struct {
				ID string `json:"id"`
			} `json:"dataset"`
		} `json:"datasets"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Datasets) != 1 || body.Datasets[0].Level != "local-only" || body.Datasets[0].Dataset.ID != "nextcloud-db" {
		t.Fatalf("unexpected protection body: %+v", body.Datasets)
	}
}

func TestLogoutClearsSession(t *testing.T) {
	exchanger := &fakeExchanger{}
	server := newTestServer(t, exchanger)
	session := loginSession(t, server, exchanger, "operator")

	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	request.AddCookie(session)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", recorder.Code)
	}
	cleared := findCookie(recorder.Result().Cookies(), sessionCookieName)
	if cleared == nil || cleared.MaxAge >= 0 {
		t.Fatalf("logout did not clear the session cookie: %+v", cleared)
	}
}
