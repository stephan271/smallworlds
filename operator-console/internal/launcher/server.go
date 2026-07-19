package launcher

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/stephan271/smallworlds/operator-console/internal/state"
	"github.com/stephan271/smallworlds/operator-console/internal/vault"
	"github.com/stephan271/smallworlds/operator-console/internal/workflow"
)

const sessionCookieName = "smallworlds_session"
const sessionLifetime = 12 * time.Hour

type Config struct {
	DataDir       string
	LaunchToken   string
	WrappingStore vault.WrappingStore
}

type session struct {
	csrfToken string
	expiresAt time.Time
}

type Server struct {
	launchToken string

	mu        sync.RWMutex
	tokenUsed bool
	sessions  map[string]session
	store     *state.Store
	vault     *vault.Vault
	workflow  *workflow.Engine
}

func New(config Config) (*Server, error) {
	if config.DataDir == "" {
		return nil, errors.New("data directory is required")
	}
	if config.LaunchToken == "" {
		return nil, errors.New("launch token is required")
	}

	store, err := state.Open(config.DataDir)
	if err != nil {
		return nil, err
	}
	workflowEngine := workflow.New(store)
	if err := workflowEngine.ResumeActive(context.Background()); err != nil {
		store.Close()
		return nil, err
	}
	return &Server{
		launchToken: config.LaunchToken,
		sessions:    make(map[string]session),
		store:       store,
		vault:       vault.New(config.DataDir, config.WrappingStore),
		workflow:    workflowEngine,
	}, nil
}

func (server *Server) Close() error {
	server.vault.Lock()
	return server.store.Close()
}

func (server *Server) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'")

	switch {
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/session/exchange":
		server.exchangeSession(response, request)
	case request.Method == http.MethodGet && request.URL.Path == "/api/v1/session":
		server.getSession(response, request)
	case request.URL.Path == "/api/v1/vault/unlock":
		server.unlockVault(response, request)
	case request.URL.Path == "/api/v1/vault":
		server.vaultStatus(response, request)
	case request.URL.Path == "/api/v1/profiles":
		server.profiles(response, request)
	case strings.HasPrefix(request.URL.Path, "/api/v1/profiles/"):
		server.profile(response, request)
	case request.URL.Path == "/api/v1/plans":
		server.plans(response, request)
	case strings.HasPrefix(request.URL.Path, "/api/v1/plans/"):
		server.plan(response, request)
	case strings.HasPrefix(request.URL.Path, "/api/v1/runs/"):
		server.run(response, request)
	case request.URL.Path == "/api/v1/events":
		server.events(response, request)
	default:
		http.NotFound(response, request)
	}
}

func (server *Server) unlockVault(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		Method     string `json:"method"`
		Passphrase string `json:"passphrase"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 16*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_vault_unlock")
		return
	}
	var status vault.Status
	var err error
	switch input.Method {
	case "passphrase":
		if input.Passphrase == "" {
			writeError(response, http.StatusBadRequest, "invalid_vault_unlock")
			return
		}
		if utf8.RuneCountInString(input.Passphrase) < 12 {
			writeError(response, http.StatusBadRequest, "vault_passphrase_too_short")
			return
		}
		status, err = server.vault.UnlockWithPassphrase(request.Context(), input.Passphrase)
	case "operating-system":
		if input.Passphrase != "" {
			writeError(response, http.StatusBadRequest, "invalid_vault_unlock")
			return
		}
		status, err = server.vault.UnlockWithOSCredentialStore(request.Context())
	default:
		writeError(response, http.StatusBadRequest, "invalid_vault_unlock")
		return
	}
	if errors.Is(err, vault.ErrIncorrectPassphrase) {
		writeError(response, http.StatusUnauthorized, "vault_passphrase_incorrect")
		return
	}
	if errors.Is(err, vault.ErrCredentialStoreUnavailable) {
		writeError(response, http.StatusServiceUnavailable, "os_credential_store_unavailable")
		return
	}
	if errors.Is(err, vault.ErrWrappingKeyMissing) {
		writeError(response, http.StatusConflict, "vault_wrapping_key_missing")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "vault_unlock_failed")
		return
	}
	writeJSON(response, http.StatusOK, status)
}

func (server *Server) vaultStatus(response http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedSession(request); !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", "GET")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	writeJSON(response, http.StatusOK, server.vault.Status(request.Context()))
}

type workflowEvent struct {
	ID         int64          `json:"id"`
	ProfileID  string         `json:"profileId"`
	RunID      string         `json:"runId"`
	Type       string         `json:"type"`
	MessageKey string         `json:"messageKey"`
	Parameters map[string]any `json:"parameters"`
	OccurredAt string         `json:"occurredAt"`
}

func (server *Server) events(response http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedSession(request); !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", "GET")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	profileID := request.URL.Query().Get("profileId")
	if profileID == "" {
		writeError(response, http.StatusBadRequest, "profile_required")
		return
	}
	cursorText := request.URL.Query().Get("cursor")
	if headerCursor := request.Header.Get("Last-Event-ID"); headerCursor != "" {
		cursorText = headerCursor
	}
	var cursor int64
	if cursorText != "" {
		parsed, err := strconv.ParseInt(cursorText, 10, 64)
		if err != nil || parsed < 0 {
			writeError(response, http.StatusBadRequest, "invalid_event_cursor")
			return
		}
		cursor = parsed
	}
	events, err := server.store.ListEvents(request.Context(), profileID, cursor)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "events_unavailable")
		return
	}
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Accel-Buffering", "no")
	response.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(response, "retry: 1000\n\n")
	for _, record := range events {
		parameters := make(map[string]any)
		if err := json.Unmarshal([]byte(record.Parameters), &parameters); err != nil {
			continue
		}
		payload, err := json.Marshal(workflowEvent{
			ID:         record.ID,
			ProfileID:  record.ProfileID,
			RunID:      record.RunID,
			Type:       record.Type,
			MessageKey: record.MessageKey,
			Parameters: parameters,
			OccurredAt: record.OccurredAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
		})
		if err != nil {
			continue
		}
		_, _ = fmt.Fprintf(response, "id: %d\nevent: workflow\ndata: %s\n\n", record.ID, payload)
	}
}

func (server *Server) run(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	parts := strings.Split(strings.TrimPrefix(request.URL.Path, "/api/v1/runs/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(response, request)
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" && request.Method == http.MethodPost {
		if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
			writeError(response, http.StatusForbidden, "csrf_required")
			return
		}
		run, err := server.workflow.Cancel(request.Context(), parts[0])
		if errors.Is(err, state.ErrNotFound) {
			writeError(response, http.StatusConflict, "run_not_cancellable")
			return
		}
		if err != nil {
			writeError(response, http.StatusInternalServerError, "run_cancellation_failed")
			return
		}
		writeJSON(response, http.StatusAccepted, run)
		return
	}
	if len(parts) != 1 || request.Method != http.MethodGet {
		http.NotFound(response, request)
		return
	}
	run, err := server.workflow.GetRun(request.Context(), parts[0])
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "run_not_found")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "run_unavailable")
		return
	}
	writeJSON(response, http.StatusOK, run)
}

func (server *Server) plans(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", "POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	var input struct {
		ProfileID string `json:"profileId"`
		Intent    string `json:"intent"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 16*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" || input.Intent != "VerifyLauncher" {
		writeError(response, http.StatusBadRequest, "invalid_plan_intent")
		return
	}
	plan, err := server.workflow.PlanVerification(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "plan_creation_failed")
		return
	}
	writeJSON(response, http.StatusCreated, plan)
}

func (server *Server) plan(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	parts := strings.Split(strings.TrimPrefix(request.URL.Path, "/api/v1/plans/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "approve" || request.Method != http.MethodPost {
		http.NotFound(response, request)
		return
	}
	if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
		writeError(response, http.StatusForbidden, "csrf_required")
		return
	}
	run, err := server.workflow.Approve(request.Context(), parts[0])
	if errors.Is(err, workflow.ErrPreconditionChanged) {
		writeError(response, http.StatusConflict, "plan_precondition_changed")
		return
	}
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "plan_not_found")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "plan_approval_failed")
		return
	}
	writeJSON(response, http.StatusAccepted, run)
}

func (server *Server) profile(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	parts := strings.Split(strings.TrimPrefix(request.URL.Path, "/api/v1/profiles/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(response, request)
		return
	}
	profileID := parts[0]

	if len(parts) == 2 && parts[1] == "journey" && request.Method == http.MethodGet {
		journey, err := server.workflow.Journey(request.Context(), profileID)
		if errors.Is(err, state.ErrNotFound) {
			writeError(response, http.StatusNotFound, "profile_not_found")
			return
		}
		if err != nil {
			writeError(response, http.StatusInternalServerError, "journey_unavailable")
			return
		}
		writeJSON(response, http.StatusOK, journey)
		return
	}
	if len(parts) >= 2 && parts[1] == "credentials" {
		server.profileCredentials(response, request, current, profileID, parts[2:])
		return
	}

	if len(parts) == 1 && request.Method == http.MethodPut {
		if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
			writeError(response, http.StatusForbidden, "csrf_required")
			return
		}
		var input struct {
			Name           string `json:"name"`
			Language       string `json:"language"`
			DeploymentMode string `json:"deploymentMode"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 16*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || !validProfileInput(input.Name, input.Language, input.DeploymentMode) {
			writeError(response, http.StatusBadRequest, "invalid_profile")
			return
		}
		profile, err := server.store.UpdateProfile(request.Context(), profileID, strings.TrimSpace(input.Name), input.Language, input.DeploymentMode)
		if errors.Is(err, state.ErrNotFound) {
			writeError(response, http.StatusNotFound, "profile_not_found")
			return
		}
		if err != nil {
			writeError(response, http.StatusInternalServerError, "profile_update_failed")
			return
		}
		writeJSON(response, http.StatusOK, profile)
		return
	}

	http.NotFound(response, request)
}

type credentialMetadata struct {
	Kind           string `json:"kind"`
	Present        bool   `json:"present"`
	Source         string `json:"source"`
	ExpiresAt      string `json:"expiresAt"`
	RotationStatus string `json:"rotationStatus"`
}

func (server *Server) profileCredentials(response http.ResponseWriter, request *http.Request, current session, profileID string, remainder []string) {
	if _, err := server.store.GetProfile(request.Context(), profileID); errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "credentials_unavailable")
		return
	}
	if len(remainder) == 0 && request.Method == http.MethodGet {
		if server.vault.Status(request.Context()).State != "unlocked" {
			writeError(response, http.StatusLocked, "vault_locked")
			return
		}
		references, err := server.store.ListCredentialReferences(request.Context(), profileID)
		if err != nil {
			writeError(response, http.StatusInternalServerError, "credentials_unavailable")
			return
		}
		metadata := make([]credentialMetadata, 0, len(references))
		for _, reference := range references {
			present, err := server.vault.Contains(reference.VaultKey)
			if err != nil {
				writeError(response, http.StatusLocked, "vault_locked")
				return
			}
			metadata = append(metadata, credentialMetadata{
				Kind:           reference.Kind,
				Present:        present,
				Source:         reference.Source,
				ExpiresAt:      reference.ExpiresAt.UTC().Format(time.RFC3339),
				RotationStatus: credentialRotationStatus(reference.ExpiresAt, time.Now()),
			})
		}
		writeJSON(response, http.StatusOK, metadata)
		return
	}
	if len(remainder) == 1 && request.Method == http.MethodPut {
		if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
			writeError(response, http.StatusForbidden, "csrf_required")
			return
		}
		kind := remainder[0]
		if kind != "git-provider-token" {
			writeError(response, http.StatusBadRequest, "unsupported_credential_kind")
			return
		}
		var input struct {
			Value     string `json:"value"`
			ExpiresAt string `json:"expiresAt"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 64*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || input.Value == "" {
			writeError(response, http.StatusBadRequest, "invalid_credential")
			return
		}
		expiresAt, err := time.Parse(time.RFC3339, input.ExpiresAt)
		if err != nil {
			writeError(response, http.StatusBadRequest, "invalid_credential_expiry")
			return
		}
		vaultKey := profileID + "/" + kind
		if err := server.vault.Store(vaultKey, input.Value); errors.Is(err, vault.ErrLocked) {
			writeError(response, http.StatusLocked, "vault_locked")
			return
		} else if err != nil {
			writeError(response, http.StatusInternalServerError, "credential_storage_failed")
			return
		}
		rotationStatus := credentialRotationStatus(expiresAt, time.Now())
		if err := server.store.UpsertCredentialReference(request.Context(), state.CredentialReference{
			ProfileID:      profileID,
			Kind:           kind,
			VaultKey:       vaultKey,
			Source:         "operator",
			ExpiresAt:      expiresAt,
			RotationStatus: rotationStatus,
		}); err != nil {
			writeError(response, http.StatusInternalServerError, "credential_storage_failed")
			return
		}
		writeJSON(response, http.StatusOK, credentialMetadata{
			Kind:           kind,
			Present:        true,
			Source:         "operator",
			ExpiresAt:      expiresAt.UTC().Format(time.RFC3339),
			RotationStatus: rotationStatus,
		})
		return
	}
	if len(remainder) == 1 && request.Method == http.MethodDelete {
		if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
			writeError(response, http.StatusForbidden, "csrf_required")
			return
		}
		kind := remainder[0]
		vaultKey := profileID + "/" + kind
		if err := server.vault.Delete(vaultKey); errors.Is(err, vault.ErrLocked) {
			writeError(response, http.StatusLocked, "vault_locked")
			return
		} else if errors.Is(err, vault.ErrSecretNotFound) {
			writeError(response, http.StatusNotFound, "credential_not_found")
			return
		} else if err != nil {
			writeError(response, http.StatusInternalServerError, "credential_removal_failed")
			return
		}
		if err := server.store.DeleteCredentialReference(request.Context(), profileID, kind); err != nil {
			writeError(response, http.StatusInternalServerError, "credential_removal_failed")
			return
		}
		response.WriteHeader(http.StatusNoContent)
		return
	}
	http.NotFound(response, request)
}

func credentialRotationStatus(expiresAt, now time.Time) string {
	if !expiresAt.After(now) {
		return "expired"
	}
	if expiresAt.Before(now.Add(30 * 24 * time.Hour)) {
		return "due-soon"
	}
	return "current"
}

func (server *Server) profiles(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}

	switch request.Method {
	case http.MethodGet:
		profiles, err := server.store.ListProfiles(request.Context())
		if err != nil {
			writeError(response, http.StatusInternalServerError, "profiles_unavailable")
			return
		}
		writeJSON(response, http.StatusOK, profiles)
	case http.MethodPost:
		if !sameToken(request.Header.Get("X-CSRF-Token"), current.csrfToken) {
			writeError(response, http.StatusForbidden, "csrf_required")
			return
		}
		var input struct {
			Name           string `json:"name"`
			Language       string `json:"language"`
			DeploymentMode string `json:"deploymentMode"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 16*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || !validProfileInput(input.Name, input.Language, input.DeploymentMode) {
			writeError(response, http.StatusBadRequest, "invalid_profile")
			return
		}
		id, err := randomToken()
		if err != nil {
			writeError(response, http.StatusInternalServerError, "profile_creation_failed")
			return
		}
		profile, err := server.store.CreateProfile(request.Context(), state.Profile{
			ID:             id,
			Name:           strings.TrimSpace(input.Name),
			Language:       input.Language,
			DeploymentMode: input.DeploymentMode,
		})
		if err != nil {
			writeError(response, http.StatusInternalServerError, "profile_creation_failed")
			return
		}
		writeJSON(response, http.StatusCreated, profile)
	default:
		response.Header().Set("Allow", "GET, POST")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func validProfileInput(name, language, deploymentMode string) bool {
	if strings.TrimSpace(name) == "" || len(name) > 120 {
		return false
	}
	if language != "en" && language != "de" {
		return false
	}
	switch deploymentMode {
	case "hetzner", "local-lan", "local-public":
		return true
	default:
		return false
	}
}

func (server *Server) exchangeSession(response http.ResponseWriter, request *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request")
		return
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	if server.tokenUsed || !sameToken(body.Token, server.launchToken) {
		writeError(response, http.StatusUnauthorized, "invalid_launch_token")
		return
	}

	sessionID, err := randomToken()
	if err != nil {
		writeError(response, http.StatusInternalServerError, "session_creation_failed")
		return
	}
	csrfToken, err := randomToken()
	if err != nil {
		writeError(response, http.StatusInternalServerError, "session_creation_failed")
		return
	}

	server.tokenUsed = true
	expiresAt := time.Now().Add(sessionLifetime)
	server.sessions[sessionID] = session{csrfToken: csrfToken, expiresAt: expiresAt}
	http.SetCookie(response, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionLifetime.Seconds()),
		Expires:  expiresAt,
	})
	writeJSON(response, http.StatusOK, map[string]string{"csrfToken": csrfToken})
}

func (server *Server) getSession(response http.ResponseWriter, request *http.Request) {
	current, ok := server.authenticatedSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"authenticated": true,
		"csrfToken":     current.csrfToken,
	})
}

func (server *Server) authenticatedSession(request *http.Request) (session, bool) {
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil {
		return session{}, false
	}
	server.mu.RLock()
	defer server.mu.RUnlock()
	current, ok := server.sessions[cookie.Value]
	if ok && time.Now().After(current.expiresAt) {
		return session{}, false
	}
	return current, ok
}

func sameToken(candidate, expected string) bool {
	if len(candidate) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(expected)) == 1
}

func randomToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeError(response http.ResponseWriter, status int, code string) {
	writeJSON(response, status, map[string]string{"code": code})
}
