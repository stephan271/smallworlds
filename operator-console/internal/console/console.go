// Package console is the in-cluster Operator Console's HTTP surface (milestone
// M5). Unlike the Bootstrap Launcher — which binds to loopback and exchanges a
// one-time token — the in-cluster console authenticates Operators through
// Keycloak OIDC and governs every route with a server-side Console Role check
// (ADR 0011). It serves read-only Capability Assessments and, at the correct
// authority level, the proposal and administration surfaces.
//
// Sessions are stateless signed cookies (HMAC-SHA256) so the console survives a
// pod restart without server-side session storage; the OIDC login's pending
// state/nonce/PKCE-verifier ride a second short-lived signed cookie. The one
// networked step — exchanging the authorization code for a JWKS-verified ID
// token — is the injected consoleauth.TokenExchanger, so the whole surface is
// deterministically testable without a live Keycloak.
package console

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/assessment"
	"github.com/stephan271/smallworlds/operator-console/internal/consoleauth"
	"github.com/stephan271/smallworlds/operator-console/internal/deeplinks"
	"github.com/stephan271/smallworlds/operator-console/internal/protection"
)

// ProtectionReporter reports the dataset protection inventory. The
// protection.Inventory satisfies it; tests inject a deterministic implementation.
type ProtectionReporter interface {
	Report(ctx context.Context) []protection.DatasetProtection
}

const (
	sessionCookieName = "sw_console_session"
	loginCookieName   = "sw_console_login"
	sessionTTL        = 8 * time.Hour
	loginTTL          = 10 * time.Minute
)

// CapabilityAssessor produces a Capability Assessment for a catalog entry. The
// observers.Gatherer satisfies it; tests inject a deterministic implementation.
type CapabilityAssessor interface {
	Assess(ctx context.Context, ref assessment.CapabilityRef) assessment.CapabilityAssessment
}

// Config parameterizes an in-cluster console.
type Config struct {
	Issuer                string
	ClientID              string
	AuthorizationEndpoint string
	RedirectURI           string
	Exchanger             consoleauth.TokenExchanger
	Assessor              CapabilityAssessor
	Catalog               []assessment.CapabilityRef
	// Protection reports the dataset protection inventory for the /protection
	// endpoint. When nil, the endpoint returns an empty inventory.
	Protection ProtectionReporter
	// BaseDomain is the Private Network base domain used to build contextual
	// Grafana/Argo CD deep links. When empty or invalid, the console omits those
	// external links rather than fabricating them.
	BaseDomain string
	SessionKey []byte
	Leeway     time.Duration
	Now        func() time.Time
	Random     io.Reader
}

// Server is the in-cluster console HTTP handler.
type Server struct {
	config     Config
	catalog    []assessment.CapabilityRef
	byID       map[string]assessment.CapabilityRef
	sessionKey []byte
	now        func() time.Time
	random     io.Reader
	links      deeplinks.Targets
	mux        *http.ServeMux
}

// New builds a console Server. A missing SessionKey is filled with a fresh
// random key (restart then forces re-login); a missing clock uses time.Now.
func New(config Config) (*Server, error) {
	if config.Exchanger == nil {
		return nil, errors.New("console: token exchanger is required")
	}
	if config.Assessor == nil {
		return nil, errors.New("console: assessor is required")
	}
	sessionKey := config.SessionKey
	if len(sessionKey) == 0 {
		sessionKey = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, sessionKey); err != nil {
			return nil, err
		}
	}
	now := config.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	random := config.Random
	if random == nil {
		random = rand.Reader
	}
	server := &Server{
		config:     config,
		catalog:    append([]assessment.CapabilityRef(nil), config.Catalog...),
		byID:       make(map[string]assessment.CapabilityRef, len(config.Catalog)),
		sessionKey: sessionKey,
		now:        now,
		random:     random,
		mux:        http.NewServeMux(),
	}
	for _, ref := range server.catalog {
		server.byID[ref.ID] = ref
	}
	if config.BaseDomain != "" {
		if targets, err := deeplinks.New(config.BaseDomain); err == nil {
			server.links = targets
		}
	}
	server.routes()
	return server, nil
}

func (server *Server) routes() {
	server.mux.HandleFunc("GET /api/v1/session", server.handleSession)
	server.mux.HandleFunc("GET /api/v1/auth/login", server.handleLogin)
	server.mux.HandleFunc("GET /api/v1/auth/callback", server.handleCallback)
	server.mux.HandleFunc("POST /api/v1/auth/logout", server.handleLogout)

	server.mux.HandleFunc("GET /api/v1/overview", server.require(consoleauth.PermissionObserve, server.handleOverview))
	server.mux.HandleFunc("GET /api/v1/capabilities", server.require(consoleauth.PermissionObserve, server.handleCapabilities))
	server.mux.HandleFunc("GET /api/v1/capabilities/{id}", server.require(consoleauth.PermissionObserve, server.handleCapability))
	server.mux.HandleFunc("GET /api/v1/protection", server.require(consoleauth.PermissionObserve, server.handleProtection))
	server.mux.HandleFunc("GET /api/v1/proposals", server.require(consoleauth.PermissionPropose, server.handleProposals))
	server.mux.HandleFunc("GET /api/v1/administration/access", server.require(consoleauth.PermissionAdminister, server.handleAdministrationAccess))
}

func (server *Server) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("X-Frame-Options", "DENY")
	response.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; frame-ancestors 'none'")
	server.mux.ServeHTTP(response, request)
}

// require gates a handler behind a Console Role permission. Absence of a session
// is unauthenticated (401); a session without the permission is forbidden (403),
// with a distinct code for the default-deny no-role case.
func (server *Server) require(permission consoleauth.Permission, handler http.HandlerFunc) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		current, ok := server.readSession(request)
		if !ok {
			writeError(response, http.StatusUnauthorized, "authentication_required")
			return
		}
		switch err := consoleauth.Authorize(current.Role, permission); {
		case errors.Is(err, consoleauth.ErrNoConsoleRole):
			writeError(response, http.StatusForbidden, "no_console_role")
			return
		case errors.Is(err, consoleauth.ErrForbidden):
			writeError(response, http.StatusForbidden, "forbidden")
			return
		case err != nil:
			writeError(response, http.StatusForbidden, "forbidden")
			return
		}
		handler(response, request)
	}
}

// --- authentication handlers ---

type sessionResponse struct {
	Authenticated bool                     `json:"authenticated"`
	Subject       string                   `json:"subject,omitempty"`
	Username      string                   `json:"username,omitempty"`
	Role          consoleauth.ConsoleRole  `json:"role,omitempty"`
	Permissions   []consoleauth.Permission `json:"permissions"`
}

func (server *Server) handleSession(response http.ResponseWriter, request *http.Request) {
	current, ok := server.readSession(request)
	if !ok {
		writeJSON(response, http.StatusOK, sessionResponse{Permissions: []consoleauth.Permission{}})
		return
	}
	writeJSON(response, http.StatusOK, sessionResponse{
		Authenticated: true,
		Subject:       current.Subject,
		Username:      current.Username,
		Role:          current.Role,
		Permissions:   current.Role.Permissions(),
	})
}

func (server *Server) handleLogin(response http.ResponseWriter, request *http.Request) {
	authRequest, err := consoleauth.NewAuthRequest(server.config.AuthorizationEndpoint, server.config.ClientID, server.config.RedirectURI, "email profile", server.random)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "login_unavailable")
		return
	}
	server.setLoginCookie(response, authRequest)
	http.Redirect(response, request, authRequest.AuthorizationURL, http.StatusFound)
}

func (server *Server) handleCallback(response http.ResponseWriter, request *http.Request) {
	pending, ok := server.readLoginCookie(request)
	server.clearCookie(response, loginCookieName)
	if !ok {
		http.Redirect(response, request, "/?auth_error=login_expired", http.StatusFound)
		return
	}
	expectations := consoleauth.Expectations{
		Issuer:   server.config.Issuer,
		Audience: server.config.ClientID,
		Now:      server.now(),
		Leeway:   server.config.Leeway,
	}
	claims, role, err := consoleauth.CompleteLogin(request.Context(), server.config.Exchanger, pending, request.URL.Query().Get("code"), request.URL.Query().Get("state"), expectations)
	if err != nil {
		http.Redirect(response, request, "/?auth_error="+authErrorCode(err), http.StatusFound)
		return
	}
	server.setSessionCookie(response, sessionData{
		Subject:  claims.Subject,
		Username: claims.PreferredUsername,
		Role:     role,
		Expiry:   server.now().Add(sessionTTL),
	})
	http.Redirect(response, request, "/", http.StatusFound)
}

func (server *Server) handleLogout(response http.ResponseWriter, request *http.Request) {
	server.clearCookie(response, sessionCookieName)
	response.WriteHeader(http.StatusNoContent)
}

func authErrorCode(err error) string {
	switch {
	case errors.Is(err, consoleauth.ErrNoConsoleRole):
		return "no_console_role"
	case errors.Is(err, consoleauth.ErrStateMismatch):
		return "state_mismatch"
	case errors.Is(err, consoleauth.ErrInvalidIssuer),
		errors.Is(err, consoleauth.ErrInvalidAudience),
		errors.Is(err, consoleauth.ErrInvalidNonce),
		errors.Is(err, consoleauth.ErrTokenExpired),
		errors.Is(err, consoleauth.ErrTokenNotYetValid):
		return "invalid_token"
	default:
		return "login_failed"
	}
}

// --- observation and role-gated handlers ---

type capabilitySummary struct {
	ID         string                     `json:"id"`
	State      assessment.CapabilityState `json:"state"`
	ReasonCode string                     `json:"reasonCode"`
}

type overviewResponse struct {
	Total        int                                `json:"total"`
	ByState      map[assessment.CapabilityState]int `json:"byState"`
	Capabilities []capabilitySummary                `json:"capabilities"`
}

func (server *Server) handleOverview(response http.ResponseWriter, request *http.Request) {
	summaries := make([]capabilitySummary, 0, len(server.catalog))
	byState := make(map[assessment.CapabilityState]int)
	for _, ref := range server.catalog {
		result := server.config.Assessor.Assess(request.Context(), ref)
		summaries = append(summaries, capabilitySummary{ID: result.CapabilityID, State: result.State, ReasonCode: result.ReasonCode})
		byState[result.State]++
	}
	writeJSON(response, http.StatusOK, overviewResponse{Total: len(summaries), ByState: byState, Capabilities: summaries})
}

func (server *Server) handleCapabilities(response http.ResponseWriter, request *http.Request) {
	results := make([]assessment.CapabilityAssessment, 0, len(server.catalog))
	for _, ref := range server.catalog {
		results = append(results, server.config.Assessor.Assess(request.Context(), ref))
	}
	writeJSON(response, http.StatusOK, map[string]any{"capabilities": results})
}

func (server *Server) handleCapability(response http.ResponseWriter, request *http.Request) {
	ref, ok := server.byID[request.PathValue("id")]
	if !ok {
		writeError(response, http.StatusNotFound, "capability_not_found")
		return
	}
	writeJSON(response, http.StatusOK, server.capabilityView(server.config.Assessor.Assess(request.Context(), ref)))
}

// facetView is a facet enriched with the resolved contextual URL for its
// remediation route, when that route points at an external investigation tool.
type facetView struct {
	assessment.Facet
	RemediationURL string `json:"remediationUrl,omitempty"`
}

// capabilityView is a Capability Assessment whose facets carry resolved
// remediation deep links for the browser to open in a new tab.
type capabilityView struct {
	CapabilityID string                     `json:"capabilityId"`
	State        assessment.CapabilityState `json:"state"`
	ReasonCode   string                     `json:"reasonCode"`
	Facets       []facetView                `json:"facets"`
	ObservedAt   time.Time                  `json:"observedAt,omitempty"`
}

func (server *Server) capabilityView(result assessment.CapabilityAssessment) capabilityView {
	facets := make([]facetView, 0, len(result.Facets))
	for _, facet := range result.Facets {
		view := facetView{Facet: facet}
		if facet.Remediation != nil {
			if url, ok := server.links.Resolve(*facet.Remediation); ok {
				view.RemediationURL = url
			}
		}
		facets = append(facets, view)
	}
	return capabilityView{
		CapabilityID: result.CapabilityID,
		State:        result.State,
		ReasonCode:   result.ReasonCode,
		Facets:       facets,
		ObservedAt:   result.ObservedAt,
	}
}

// handleProtection serves the dataset protection inventory: every declared
// dataset with its distinct Job-completion, local Recovery Point, offsite
// Recovery Point, freshness, and Restore Drill evidence.
func (server *Server) handleProtection(response http.ResponseWriter, request *http.Request) {
	datasets := []protection.DatasetProtection{}
	if server.config.Protection != nil {
		datasets = server.config.Protection.Report(request.Context())
	}
	writeJSON(response, http.StatusOK, map[string]any{"datasets": datasets})
}

// handleProposals serves the GitOps proposal workspace, readable at Operator
// authority and above. The create/branch/PR flow arrives with the add-capability
// issue; the list is empty until then.
func (server *Server) handleProposals(response http.ResponseWriter, request *http.Request) {
	writeJSON(response, http.StatusOK, map[string]any{"proposals": []any{}})
}

// handleAdministrationAccess serves the operator-access administration view,
// reachable only at Owner authority. Device enrollment/revocation mutations
// arrive with the enrollment issue.
func (server *Server) handleAdministrationAccess(response http.ResponseWriter, request *http.Request) {
	writeJSON(response, http.StatusOK, map[string]any{"operators": []any{}})
}

// --- signed cookies ---

type sessionData struct {
	Subject  string                  `json:"sub"`
	Username string                  `json:"usr"`
	Role     consoleauth.ConsoleRole `json:"role"`
	Expiry   time.Time               `json:"exp"`
}

type loginData struct {
	State        string    `json:"state"`
	Nonce        string    `json:"nonce"`
	CodeVerifier string    `json:"cv"`
	Expiry       time.Time `json:"exp"`
}

func (server *Server) sign(payload []byte) string {
	mac := hmac.New(sha256.New, server.sessionKey)
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (server *Server) unsign(value string) ([]byte, bool) {
	encodedPayload, encodedSignature, found := strings.Cut(value, ".")
	if !found {
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(encodedPayload)
	if err != nil {
		return nil, false
	}
	signature, err := base64.RawURLEncoding.DecodeString(encodedSignature)
	if err != nil {
		return nil, false
	}
	mac := hmac.New(sha256.New, server.sessionKey)
	mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, false
	}
	return payload, true
}

func (server *Server) setSessionCookie(response http.ResponseWriter, data sessionData) {
	payload, _ := json.Marshal(data)
	http.SetCookie(response, &http.Cookie{
		Name:     sessionCookieName,
		Value:    server.sign(payload),
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		Expires:  data.Expiry,
		MaxAge:   int(time.Until(data.Expiry).Seconds()),
	})
}

func (server *Server) readSession(request *http.Request) (sessionData, bool) {
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil {
		return sessionData{}, false
	}
	payload, ok := server.unsign(cookie.Value)
	if !ok {
		return sessionData{}, false
	}
	var data sessionData
	if err := json.Unmarshal(payload, &data); err != nil {
		return sessionData{}, false
	}
	if server.now().After(data.Expiry) {
		return sessionData{}, false
	}
	return data, true
}

func (server *Server) setLoginCookie(response http.ResponseWriter, request consoleauth.AuthRequest) {
	payload, _ := json.Marshal(loginData{
		State:        request.State,
		Nonce:        request.Nonce,
		CodeVerifier: request.CodeVerifier,
		Expiry:       server.now().Add(loginTTL),
	})
	http.SetCookie(response, &http.Cookie{
		Name:     loginCookieName,
		Value:    server.sign(payload),
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		// Lax so the pending cookie survives the top-level redirect back from
		// Keycloak to the callback.
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(loginTTL.Seconds()),
	})
}

func (server *Server) readLoginCookie(request *http.Request) (consoleauth.AuthRequest, bool) {
	cookie, err := request.Cookie(loginCookieName)
	if err != nil {
		return consoleauth.AuthRequest{}, false
	}
	payload, ok := server.unsign(cookie.Value)
	if !ok {
		return consoleauth.AuthRequest{}, false
	}
	var data loginData
	if err := json.Unmarshal(payload, &data); err != nil {
		return consoleauth.AuthRequest{}, false
	}
	if server.now().After(data.Expiry) {
		return consoleauth.AuthRequest{}, false
	}
	return consoleauth.AuthRequest{State: data.State, Nonce: data.Nonce, CodeVerifier: data.CodeVerifier}, true
}

func (server *Server) clearCookie(response http.ResponseWriter, name string) {
	http.SetCookie(response, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeError(response http.ResponseWriter, status int, code string) {
	writeJSON(response, status, map[string]string{"code": code})
}
