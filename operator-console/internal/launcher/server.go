package launcher

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/stephan271/smallworlds/operator-console/internal/bootstrapassets"
	"github.com/stephan271/smallworlds/operator-console/internal/capability"
	"github.com/stephan271/smallworlds/operator-console/internal/githttps"
	"github.com/stephan271/smallworlds/operator-console/internal/github"
	"github.com/stephan271/smallworlds/operator-console/internal/nodeinspect"
	"github.com/stephan271/smallworlds/operator-console/internal/recovery"
	"github.com/stephan271/smallworlds/operator-console/internal/state"
	"github.com/stephan271/smallworlds/operator-console/internal/vault"
	"github.com/stephan271/smallworlds/operator-console/internal/workflow"
)

const sessionCookieName = "smallworlds_session"
const sessionLifetime = 12 * time.Hour

type Config struct {
	DataDir          string
	LaunchToken      string
	WrappingStore    vault.WrappingStore
	GitHubClient     *github.Client
	GenericGitClient GenericGitClient
	BootstrapAssets  *bootstrapassets.Manager
}

// GenericGitClient permits deterministic Launcher contract tests while the
// production client remains an embedded Go implementation with no git binary.
type GenericGitClient interface {
	ValidateAccess(context.Context, string, string, string) error
	RemoteContainsCommit(context.Context, string, string, string, string) (bool, error)
	InitializeEmptyRemote(context.Context, string, string, string, map[string]string) (githttps.Identity, error)
	CreateProposalBranch(context.Context, string, string, string, string, map[string]string) (githttps.Proposal, error)
}

type session struct {
	csrfToken string
	expiresAt time.Time
}

type Server struct {
	launchToken string

	mu         sync.RWMutex
	tokenUsed  bool
	sessions   map[string]session
	store      *state.Store
	vault      *vault.Vault
	workflow   *workflow.Engine
	github     *github.Client
	genericGit GenericGitClient
	assets     *bootstrapassets.Manager
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
	githubClient := config.GitHubClient
	if githubClient == nil {
		githubClient = github.New("https://api.github.com", nil)
	}
	genericGitClient := config.GenericGitClient
	if genericGitClient == nil {
		genericGitClient = githttps.New()
	}
	assetManager := config.BootstrapAssets
	if assetManager == nil {
		assetManager, err = bootstrapassets.NewManager(config.DataDir, bootstrapassets.DefaultCatalog(), nil)
		if err != nil {
			store.Close()
			return nil, err
		}
	}
	return &Server{
		launchToken: config.LaunchToken,
		sessions:    make(map[string]session),
		store:       store,
		vault:       vault.New(config.DataDir, config.WrappingStore),
		workflow:    workflowEngine,
		github:      githubClient,
		genericGit:  genericGitClient,
		assets:      assetManager,
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
	case request.URL.Path == "/api/v1/recovery-bundles/export":
		server.exportRecoveryBundle(response, request)
	case request.URL.Path == "/api/v1/recovery-bundles/preview":
		server.previewRecoveryBundle(response, request)
	case request.URL.Path == "/api/v1/recovery-bundles/import":
		server.importRecoveryBundle(response, request)
	case request.URL.Path == "/api/v1/capabilities":
		server.capabilities(response, request)
	case request.URL.Path == "/api/v1/capabilities/plan":
		server.capabilityPlan(response, request)
	case request.URL.Path == "/api/v1/github/token/validate":
		server.validateGitHubToken(response, request)
	case request.URL.Path == "/api/v1/github/overlay/establish":
		server.establishGitHubOverlay(response, request)
	case request.URL.Path == "/api/v1/generic-git/token/validate":
		server.validateGenericGitCredentials(response, request)
	case request.URL.Path == "/api/v1/generic-git/overlay/establish":
		server.establishGenericGitOverlay(response, request)
	case request.URL.Path == "/api/v1/generic-git/overlay/propose":
		server.proposeGenericGitOverlay(response, request)
	case request.URL.Path == "/api/v1/bootstrap-assets":
		server.bootstrapAssets(response, request)
	case request.URL.Path == "/api/v1/bootstrap-assets/acquire":
		server.acquireBootstrapAssets(response, request)
	case request.URL.Path == "/api/v1/nodes/probe":
		server.probeNode(response, request)
	case request.URL.Path == "/api/v1/nodes/capabilities":
		server.nodeCapabilities(response, request)
	case request.URL.Path == "/api/v1/nodes/trust":
		server.trustNode(response, request)
	case request.URL.Path == "/api/v1/nodes/inspect":
		server.inspectNode(response, request)
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

func (server *Server) previewRecoveryBundle(response http.ResponseWriter, request *http.Request) {
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
		Bundle     string `json:"bundle"`
		Passphrase string `json:"passphrase"`
		Identity   string `json:"identity"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 24*1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.Bundle == "" || !validRecoveryCredential(input.Passphrase, input.Identity) {
		writeError(response, http.StatusBadRequest, "invalid_recovery_bundle")
		return
	}
	bundle, err := base64.StdEncoding.DecodeString(input.Bundle)
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_recovery_bundle")
		return
	}
	payload, err := openRecoveryBundle(bundle, input.Passphrase, input.Identity)
	if errors.Is(err, recovery.ErrCannotDecrypt) {
		writeError(response, http.StatusUnauthorized, "recovery_bundle_credentials_incorrect")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_recovery_bundle")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"format":  payload.Format,
		"version": payload.Version,
		"profile": map[string]string{
			"id":             payload.Profile.ID,
			"name":           payload.Profile.Name,
			"deploymentMode": payload.Profile.DeploymentMode,
		},
	})
}

func (server *Server) exportRecoveryBundle(response http.ResponseWriter, request *http.Request) {
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
		ProfileID  string   `json:"profileId"`
		Passphrase string   `json:"passphrase"`
		Recipients []string `json:"recipients"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" || !validRecoveryExportCredential(input.Passphrase, input.Recipients) {
		writeError(response, http.StatusBadRequest, "invalid_recovery_bundle_export")
		return
	}
	snapshot, err := server.store.ExportProfileSnapshot(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "recovery_bundle_export_failed")
		return
	}
	vaultMaterial, err := server.vault.ExportPrefix(input.ProfileID + "/")
	if errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "recovery_bundle_export_failed")
		return
	}
	payload := recovery.Payload{
		Format:  "smallworlds-recovery-bundle",
		Version: 1,
		Profile: snapshot.Profile,
		WorkflowSnapshot: recovery.WorkflowSnapshot{
			Plans:  snapshot.Plans,
			Runs:   snapshot.Runs,
			Events: snapshot.Events,
		},
		InfrastructureState:  json.RawMessage(`{}`),
		VaultMaterial:        vaultMaterial,
		CredentialReferences: snapshot.CredentialReferences,
	}
	var bundle []byte
	if input.Passphrase != "" {
		bundle, err = recovery.ExportWithPassphrase(payload, input.Passphrase)
	} else {
		bundle, err = recovery.ExportWithRecipients(payload, input.Recipients)
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "recovery_bundle_export_failed")
		return
	}
	response.Header().Set("Content-Type", "application/octet-stream")
	response.Header().Set("Content-Disposition", `attachment; filename="smallworlds-recovery.bundle"`)
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(bundle)
}

func (server *Server) importRecoveryBundle(response http.ResponseWriter, request *http.Request) {
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
		Bundle            string `json:"bundle"`
		Passphrase        string `json:"passphrase"`
		Identity          string `json:"identity"`
		ExpectedProfileID string `json:"expectedProfileId"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 24*1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.Bundle == "" || input.ExpectedProfileID == "" || !validRecoveryCredential(input.Passphrase, input.Identity) {
		writeError(response, http.StatusBadRequest, "invalid_recovery_bundle")
		return
	}
	bundle, err := base64.StdEncoding.DecodeString(input.Bundle)
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_recovery_bundle")
		return
	}
	payload, err := openRecoveryBundle(bundle, input.Passphrase, input.Identity)
	if errors.Is(err, recovery.ErrCannotDecrypt) {
		writeError(response, http.StatusUnauthorized, "recovery_bundle_credentials_incorrect")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_recovery_bundle")
		return
	}
	if !sameToken(payload.Profile.ID, input.ExpectedProfileID) {
		writeError(response, http.StatusConflict, "recovery_bundle_identity_mismatch")
		return
	}
	snapshot := state.ProfileSnapshot{
		Profile:              payload.Profile,
		Plans:                payload.WorkflowSnapshot.Plans,
		Runs:                 payload.WorkflowSnapshot.Runs,
		Events:               payload.WorkflowSnapshot.Events,
		CredentialReferences: payload.CredentialReferences,
	}
	if err := server.store.CanImportProfileSnapshot(request.Context(), snapshot); errors.Is(err, state.ErrConflict) {
		writeError(response, http.StatusConflict, "lifecycle_authority_already_exists")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "recovery_bundle_import_failed")
		return
	}
	if err := server.vault.Import(payload.VaultMaterial); errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	} else if errors.Is(err, vault.ErrSecretConflict) {
		writeError(response, http.StatusConflict, "recovery_bundle_vault_conflict")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "recovery_bundle_import_failed")
		return
	}
	if err := server.store.ImportProfileSnapshot(request.Context(), snapshot); err != nil {
		_ = server.vault.RemoveImported(payload.VaultMaterial)
		if errors.Is(err, state.ErrConflict) {
			writeError(response, http.StatusConflict, "lifecycle_authority_already_exists")
			return
		}
		writeError(response, http.StatusInternalServerError, "recovery_bundle_import_failed")
		return
	}
	if err := server.workflow.ResumeActive(request.Context()); err != nil {
		writeError(response, http.StatusInternalServerError, "recovery_bundle_import_failed")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{
		"profile": map[string]string{
			"id":   payload.Profile.ID,
			"name": payload.Profile.Name,
		},
	})
}

func validRecoveryCredential(passphrase, identity string) bool {
	return (utf8.RuneCountInString(passphrase) >= 12 && identity == "") || (passphrase == "" && identity != "")
}

func validRecoveryExportCredential(passphrase string, recipients []string) bool {
	return (utf8.RuneCountInString(passphrase) >= 12 && len(recipients) == 0) || (passphrase == "" && len(recipients) > 0)
}

func openRecoveryBundle(bundle []byte, passphrase, identity string) (recovery.Payload, error) {
	if passphrase != "" {
		return recovery.OpenWithPassphrase(bundle, passphrase)
	}
	return recovery.OpenWithIdentity(bundle, identity)
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

type capabilityRequest struct {
	ProfileID     string                   `json:"profileId"`
	Mode          capability.SelectionMode `json:"mode"`
	CommunityIDs  []string                 `json:"communityIds"`
	Release       string                   `json:"release"`
	RepositoryURL string                   `json:"repositoryUrl"`
	Domain        string                   `json:"domain"`
}

func (server *Server) validateGitHubToken(response http.ResponseWriter, request *http.Request) {
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
		ProfileID string           `json:"profileId"`
		Token     string           `json:"token"`
		Authority github.Authority `json:"authority"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 32*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" || input.Token == "" || (input.Authority != github.CreationAuthority && input.Authority != github.OngoingAuthority) {
		writeError(response, http.StatusBadRequest, "invalid_github_token")
		return
	}
	if _, err := server.store.GetProfile(request.Context(), input.ProfileID); errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "github_token_validation_failed")
		return
	}
	status, err := server.github.ValidateToken(request.Context(), input.Token, input.Authority)
	if errors.Is(err, github.ErrRateLimited) {
		writeError(response, http.StatusTooManyRequests, "github_rate_limited")
		return
	}
	if errors.Is(err, github.ErrUnauthorized) || errors.Is(err, github.ErrInsufficientAuthority) {
		writeError(response, http.StatusForbidden, "github_token_insufficient_authority")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadGateway, "github_token_validation_failed")
		return
	}
	vaultKey := input.ProfileID + "/github-" + string(input.Authority) + "-token"
	if err := server.vault.Store(vaultKey, input.Token); errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "github_token_storage_failed")
		return
	}
	expiresAt := status.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	if err := server.store.UpsertCredentialReference(request.Context(), state.CredentialReference{ProfileID: input.ProfileID, Kind: "github-" + string(input.Authority) + "-token", VaultKey: vaultKey, Source: "operator", ExpiresAt: expiresAt, RotationStatus: credentialRotationStatus(expiresAt, time.Now())}); err != nil {
		writeError(response, http.StatusInternalServerError, "github_token_storage_failed")
		return
	}
	if input.Authority == github.OngoingAuthority {
		creationKey := input.ProfileID + "/github-creation-token"
		if err := server.vault.Delete(creationKey); err != nil && !errors.Is(err, vault.ErrSecretNotFound) {
			writeError(response, http.StatusInternalServerError, "github_token_rotation_failed")
			return
		}
		if err := server.store.DeleteCredentialReference(request.Context(), input.ProfileID, "github-creation-token"); err != nil && !errors.Is(err, state.ErrNotFound) {
			writeError(response, http.StatusInternalServerError, "github_token_rotation_failed")
			return
		}
	}
	writeJSON(response, http.StatusOK, map[string]any{"owner": status.Owner, "expiresAt": status.ExpiresAt, "authority": input.Authority, "stored": true})
}

func (server *Server) establishGitHubOverlay(response http.ResponseWriter, request *http.Request) {
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
		capabilityRequest
		PlanID         string `json:"planId"`
		RepositoryName string `json:"repositoryName"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 96*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" || input.PlanID == "" || input.RepositoryName == "" {
		writeError(response, http.StatusBadRequest, "invalid_github_overlay")
		return
	}
	plan, err := server.store.GetPlan(request.Context(), input.PlanID)
	if errors.Is(err, state.ErrNotFound) || plan.ProfileID != input.ProfileID || plan.Intent != "ApplyCapabilities" || plan.Status != "approved" {
		writeError(response, http.StatusConflict, "github_overlay_plan_not_approved")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "github_overlay_failed")
		return
	}
	profile, err := server.store.GetProfile(request.Context(), input.ProfileID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "github_overlay_failed")
		return
	}
	overlay, err := capability.DefaultCatalog().RenderOverlay(capability.OverlayInput{Selection: capability.Selection{Mode: input.Mode, DeploymentMode: capability.DeploymentMode(profile.DeploymentMode), CommunityIDs: input.CommunityIDs}, Release: input.Release, RepositoryURL: "https://github.com/placeholder/" + input.RepositoryName + ".git", Domain: input.Domain})
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_github_overlay")
		return
	}
	token, err := server.vault.Load(input.ProfileID + "/github-creation-token")
	if errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	}
	if errors.Is(err, vault.ErrSecretNotFound) {
		writeError(response, http.StatusConflict, "github_creation_token_missing")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "github_overlay_failed")
		return
	}
	repository, err := server.github.CreatePrivateRepository(request.Context(), token, input.RepositoryName)
	if err != nil {
		writeError(response, http.StatusBadGateway, "github_repository_creation_failed")
		return
	}
	for path, contents := range overlay.Files {
		overlay.Files[path] = strings.ReplaceAll(contents, "https://github.com/placeholder/"+input.RepositoryName+".git", repository.HTMLURL+".git")
	}
	commit, err := server.github.WriteInitialFiles(request.Context(), token, repository, overlay.Files)
	if err != nil {
		writeError(response, http.StatusBadGateway, "github_overlay_initialization_failed")
		return
	}
	identity := state.OverlayIdentity{ProfileID: input.ProfileID, Provider: "github", Repository: repository.FullName, RepositoryURL: repository.HTMLURL, Release: input.Release, Commit: commit, RecordedAt: time.Now().UTC()}
	if err := server.store.RecordOverlayIdentity(request.Context(), identity); err != nil {
		writeError(response, http.StatusInternalServerError, "github_overlay_identity_failed")
		return
	}
	writeJSON(response, http.StatusCreated, identity)
}

func (server *Server) validateGenericGitCredentials(response http.ResponseWriter, request *http.Request) {
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
		ProfileID     string `json:"profileId"`
		RepositoryURL string `json:"repositoryUrl"`
		Username      string `json:"username"`
		Token         string `json:"token"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 32*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" || input.Username == "" || input.Token == "" {
		writeError(response, http.StatusBadRequest, "invalid_generic_git_credentials")
		return
	}
	if _, err := githttps.ValidateRemoteURL(input.RepositoryURL); err != nil {
		writeError(response, http.StatusBadRequest, "unsupported_git_remote")
		return
	}
	if _, err := server.store.GetProfile(request.Context(), input.ProfileID); errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "generic_git_validation_failed")
		return
	}
	if err := server.genericGit.ValidateAccess(request.Context(), input.RepositoryURL, input.Username, input.Token); errors.Is(err, githttps.ErrAuthentication) {
		writeError(response, http.StatusForbidden, "generic_git_authentication_failed")
		return
	} else if err != nil {
		writeError(response, http.StatusBadGateway, "generic_git_validation_failed")
		return
	}
	usernameKey := input.ProfileID + "/generic-git-username"
	tokenKey := input.ProfileID + "/generic-git-token"
	if err := server.vault.Store(usernameKey, input.Username); errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "generic_git_storage_failed")
		return
	}
	if err := server.vault.Store(tokenKey, input.Token); errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "generic_git_storage_failed")
		return
	}
	expiresAt := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, reference := range []state.CredentialReference{
		{ProfileID: input.ProfileID, Kind: "generic-git-username", VaultKey: usernameKey, Source: "operator", ExpiresAt: expiresAt, RotationStatus: "current"},
		{ProfileID: input.ProfileID, Kind: "generic-git-token", VaultKey: tokenKey, Source: "operator", ExpiresAt: expiresAt, RotationStatus: "current"},
	} {
		if err := server.store.UpsertCredentialReference(request.Context(), reference); err != nil {
			writeError(response, http.StatusInternalServerError, "generic_git_storage_failed")
			return
		}
	}
	writeJSON(response, http.StatusOK, map[string]any{"repositoryUrl": input.RepositoryURL, "stored": true})
}

func (server *Server) establishGenericGitOverlay(response http.ResponseWriter, request *http.Request) {
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
		capabilityRequest
		PlanID string `json:"planId"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 96*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" || input.PlanID == "" {
		writeError(response, http.StatusBadRequest, "invalid_generic_git_overlay")
		return
	}
	if _, err := githttps.ValidateRemoteURL(input.RepositoryURL); err != nil {
		writeError(response, http.StatusBadRequest, "unsupported_git_remote")
		return
	}
	plan, err := server.store.GetPlan(request.Context(), input.PlanID)
	if errors.Is(err, state.ErrNotFound) || plan.ProfileID != input.ProfileID || plan.Intent != "ApplyCapabilities" || plan.Status != "approved" {
		writeError(response, http.StatusConflict, "generic_git_overlay_plan_not_approved")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "generic_git_overlay_failed")
		return
	}
	profile, err := server.store.GetProfile(request.Context(), input.ProfileID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "generic_git_overlay_failed")
		return
	}
	overlay, err := capability.DefaultCatalog().RenderOverlay(capability.OverlayInput{Selection: capability.Selection{Mode: input.Mode, DeploymentMode: capability.DeploymentMode(profile.DeploymentMode), CommunityIDs: input.CommunityIDs}, Release: input.Release, RepositoryURL: input.RepositoryURL, Domain: input.Domain})
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_generic_git_overlay")
		return
	}
	if !matchesOverlayPlan(plan, profile, overlay.Diff) {
		writeError(response, http.StatusConflict, "generic_git_overlay_plan_mismatch")
		return
	}
	username, err := server.vault.Load(input.ProfileID + "/generic-git-username")
	if errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	}
	if errors.Is(err, vault.ErrSecretNotFound) {
		writeError(response, http.StatusConflict, "generic_git_credentials_missing")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "generic_git_overlay_failed")
		return
	}
	token, err := server.vault.Load(input.ProfileID + "/generic-git-token")
	if errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	}
	if errors.Is(err, vault.ErrSecretNotFound) {
		writeError(response, http.StatusConflict, "generic_git_credentials_missing")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "generic_git_overlay_failed")
		return
	}
	if recorded, err := server.store.GetOverlayIdentity(request.Context(), input.ProfileID); err == nil {
		if recorded.Provider != "generic-https" || recorded.RepositoryURL != input.RepositoryURL {
			writeError(response, http.StatusConflict, "generic_git_overlay_identity_conflict")
			return
		}
		present, verifyErr := server.genericGit.RemoteContainsCommit(request.Context(), input.RepositoryURL, username, token, recorded.Commit)
		if errors.Is(verifyErr, githttps.ErrAuthentication) {
			writeError(response, http.StatusForbidden, "generic_git_authentication_failed")
			return
		}
		if verifyErr != nil {
			writeError(response, http.StatusBadGateway, "generic_git_resume_check_failed")
			return
		}
		if !present {
			writeError(response, http.StatusConflict, "generic_git_remote_state_changed")
			return
		}
		writeJSON(response, http.StatusOK, recorded)
		return
	} else if !errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusInternalServerError, "generic_git_overlay_failed")
		return
	}
	remoteIdentity, err := server.genericGit.InitializeEmptyRemote(request.Context(), input.RepositoryURL, username, token, overlay.Files)
	if errors.Is(err, githttps.ErrAuthentication) {
		writeError(response, http.StatusForbidden, "generic_git_authentication_failed")
		return
	}
	if errors.Is(err, githttps.ErrRemoteNotEmpty) || errors.Is(err, githttps.ErrConcurrentChange) {
		writeError(response, http.StatusConflict, "generic_git_remote_conflict")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadGateway, "generic_git_overlay_initialization_failed")
		return
	}
	identity := state.OverlayIdentity{ProfileID: input.ProfileID, Provider: "generic-https", Repository: remoteIdentity.RepositoryURL, RepositoryURL: remoteIdentity.RepositoryURL, Release: input.Release, Commit: remoteIdentity.Commit, RecordedAt: time.Now().UTC()}
	if err := server.store.RecordOverlayIdentity(request.Context(), identity); err != nil {
		writeError(response, http.StatusInternalServerError, "generic_git_overlay_identity_failed")
		return
	}
	writeJSON(response, http.StatusCreated, identity)
}

func (server *Server) proposeGenericGitOverlay(response http.ResponseWriter, request *http.Request) {
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
		capabilityRequest
		PlanID string `json:"planId"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 96*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" || input.PlanID == "" {
		writeError(response, http.StatusBadRequest, "invalid_generic_git_proposal")
		return
	}
	if _, err := githttps.ValidateRemoteURL(input.RepositoryURL); err != nil {
		writeError(response, http.StatusBadRequest, "unsupported_git_remote")
		return
	}
	plan, err := server.store.GetPlan(request.Context(), input.PlanID)
	if errors.Is(err, state.ErrNotFound) || plan.ProfileID != input.ProfileID || plan.Intent != "ApplyCapabilities" || plan.Status != "approved" {
		writeError(response, http.StatusConflict, "generic_git_proposal_plan_not_approved")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "generic_git_proposal_failed")
		return
	}
	profile, err := server.store.GetProfile(request.Context(), input.ProfileID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "generic_git_proposal_failed")
		return
	}
	overlay, err := capability.DefaultCatalog().RenderOverlay(capability.OverlayInput{Selection: capability.Selection{Mode: input.Mode, DeploymentMode: capability.DeploymentMode(profile.DeploymentMode), CommunityIDs: input.CommunityIDs}, Release: input.Release, RepositoryURL: input.RepositoryURL, Domain: input.Domain})
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_generic_git_proposal")
		return
	}
	if !matchesOverlayPlan(plan, profile, overlay.Diff) {
		writeError(response, http.StatusConflict, "generic_git_proposal_plan_mismatch")
		return
	}
	recorded, err := server.store.GetOverlayIdentity(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) || recorded.Provider != "generic-https" || recorded.RepositoryURL != input.RepositoryURL {
		writeError(response, http.StatusConflict, "generic_git_overlay_identity_missing")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "generic_git_proposal_failed")
		return
	}
	username, err := server.vault.Load(input.ProfileID + "/generic-git-username")
	if errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	}
	if errors.Is(err, vault.ErrSecretNotFound) {
		writeError(response, http.StatusConflict, "generic_git_credentials_missing")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "generic_git_proposal_failed")
		return
	}
	token, err := server.vault.Load(input.ProfileID + "/generic-git-token")
	if errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	}
	if errors.Is(err, vault.ErrSecretNotFound) {
		writeError(response, http.StatusConflict, "generic_git_credentials_missing")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "generic_git_proposal_failed")
		return
	}
	proposal, err := server.genericGit.CreateProposalBranch(request.Context(), input.RepositoryURL, username, token, githttps.ProposalBranchForDiff(overlay.Diff), overlay.Files)
	if errors.Is(err, githttps.ErrAuthentication) {
		writeError(response, http.StatusForbidden, "generic_git_authentication_failed")
		return
	}
	if errors.Is(err, githttps.ErrConcurrentChange) {
		writeError(response, http.StatusConflict, "generic_git_proposal_conflict")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadGateway, "generic_git_proposal_failed")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]string{
		"branch":              proposal.Branch,
		"commit":              proposal.Commit,
		"mergeInstructionKey": "generic_git_manual_merge",
	})
}

func matchesOverlayPlan(plan state.PlanRecord, profile state.Profile, overlayDiff string) bool {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%d\n%s", "ApplyCapabilities", profile.ID, profile.Revision, overlayDiff)))
	return plan.Digest == fmt.Sprintf("%x", digest[:])
}

func (server *Server) bootstrapAssets(response http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedSession(request); !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", "GET")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	release := request.URL.Query().Get("release")
	if release == "" {
		writeError(response, http.StatusBadRequest, "bootstrap_asset_release_required")
		return
	}
	assets, err := server.assets.Requirements(release)
	if errors.Is(err, bootstrapassets.ErrUnknownRelease) {
		writeError(response, http.StatusConflict, "bootstrap_asset_release_unavailable")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "bootstrap_asset_status_failed")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"release":                   release,
		"assets":                    assets,
		"offlineBundleAvailability": "future",
	})
}

func (server *Server) acquireBootstrapAssets(response http.ResponseWriter, request *http.Request) {
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
		Release string `json:"release"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.Release == "" {
		writeError(response, http.StatusBadRequest, "invalid_bootstrap_asset_request")
		return
	}
	assets, err := server.assets.Acquire(request.Context(), input.Release)
	if errors.Is(err, bootstrapassets.ErrUnknownRelease) {
		writeError(response, http.StatusConflict, "bootstrap_asset_release_unavailable")
		return
	}
	if errors.Is(err, bootstrapassets.ErrIntegrity) {
		writeError(response, http.StatusBadGateway, "bootstrap_asset_integrity_failed")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadGateway, "bootstrap_asset_acquisition_failed")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{
		"release":                   input.Release,
		"assets":                    assets,
		"offlineBundleAvailability": "future",
	})
}

type nodeTargetRequest struct {
	Kind     nodeinspect.TargetKind `json:"kind"`
	Host     string                 `json:"host"`
	Port     int                    `json:"port"`
	Username string                 `json:"username"`
}

func (server *Server) nodeCapabilities(response http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedSession(request); !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", "GET")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	writeJSON(response, http.StatusOK, map[string]bool{"sameHostSupported": runtime.GOOS == "linux"})
}

func (input nodeTargetRequest) target() nodeinspect.Target {
	return nodeinspect.Target{Kind: input.Kind, Host: input.Host, Port: input.Port, Username: input.Username}
}

func (server *Server) probeNode(response http.ResponseWriter, request *http.Request) {
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
		ProfileID string            `json:"profileId"`
		Target    nodeTargetRequest `json:"target"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_node_target")
		return
	}
	target := input.Target.target()
	if err := target.Validate(runtime.GOOS); err != nil || target.Kind != nodeinspect.RemoteTarget {
		writeError(response, http.StatusBadRequest, "invalid_node_target")
		return
	}
	if _, err := server.store.GetProfile(request.Context(), input.ProfileID); errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	}
	fingerprint, err := nodeinspect.ProbeHostKey(request.Context(), target)
	if err != nil {
		writeError(response, http.StatusBadGateway, "node_host_key_probe_failed")
		return
	}
	if trust, err := server.store.GetNodeTrust(request.Context(), input.ProfileID); err == nil && (trust.Host != target.Host || trust.Port != target.Port || trust.Username != target.Username || trust.Fingerprint != fingerprint) {
		writeError(response, http.StatusConflict, "node_host_key_mismatch")
		return
	} else if err != nil && !errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusInternalServerError, "node_host_key_probe_failed")
		return
	}
	if err := server.store.RecordPendingNodeTrust(request.Context(), state.PendingNodeTrust{ProfileID: input.ProfileID, Host: target.Host, Port: target.Port, Username: target.Username, Fingerprint: fingerprint, ObservedAt: time.Now().UTC()}); err != nil {
		writeError(response, http.StatusInternalServerError, "node_host_key_probe_failed")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"target": target, "fingerprint": fingerprint, "requiresConfirmation": true})
}

func (server *Server) trustNode(response http.ResponseWriter, request *http.Request) {
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
		ProfileID   string            `json:"profileId"`
		Target      nodeTargetRequest `json:"target"`
		Fingerprint string            `json:"fingerprint"`
		Confirmed   bool              `json:"confirmed"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" || !input.Confirmed || !strings.HasPrefix(input.Fingerprint, "SHA256:") {
		writeError(response, http.StatusBadRequest, "invalid_node_trust_confirmation")
		return
	}
	target := input.Target.target()
	if err := target.Validate(runtime.GOOS); err != nil || target.Kind != nodeinspect.RemoteTarget {
		writeError(response, http.StatusBadRequest, "invalid_node_target")
		return
	}
	if _, err := server.store.GetProfile(request.Context(), input.ProfileID); errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	}
	pending, err := server.store.GetPendingNodeTrust(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) || pending.Host != target.Host || pending.Port != target.Port || pending.Username != target.Username || pending.Fingerprint != input.Fingerprint || time.Since(pending.ObservedAt) > 10*time.Minute {
		writeError(response, http.StatusConflict, "node_host_key_confirmation_required")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "node_trust_storage_failed")
		return
	}
	if err := server.store.RecordNodeTrust(request.Context(), state.NodeTrust{ProfileID: input.ProfileID, Host: target.Host, Port: target.Port, Username: target.Username, Fingerprint: input.Fingerprint, ConfirmedAt: time.Now().UTC()}); err != nil {
		writeError(response, http.StatusInternalServerError, "node_trust_storage_failed")
		return
	}
	if err := server.store.DeletePendingNodeTrust(request.Context(), input.ProfileID); err != nil {
		writeError(response, http.StatusInternalServerError, "node_trust_storage_failed")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"target": target, "fingerprint": input.Fingerprint})
}

func (server *Server) inspectNode(response http.ResponseWriter, request *http.Request) {
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
		ProfileID      string            `json:"profileId"`
		Target         nodeTargetRequest `json:"target"`
		Authentication struct {
			Kind          nodeinspect.AuthenticationKind `json:"kind"`
			Password      string                         `json:"password"`
			PrivateKey    string                         `json:"privateKey"`
			KeyPassphrase string                         `json:"keyPassphrase"`
			SudoPassword  string                         `json:"sudoPassword"`
		} `json:"authentication"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 512*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_node_inspection")
		return
	}
	target := input.Target.target()
	if err := target.Validate(runtime.GOOS); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_node_target")
		return
	}
	profile, err := server.store.GetProfile(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "node_inspection_failed")
		return
	}
	assessment, err := capability.DefaultCatalog().Assess(capability.Selection{Mode: capability.Minimal, DeploymentMode: capability.DeploymentMode(profile.DeploymentMode)})
	if err != nil {
		writeError(response, http.StatusInternalServerError, "node_requirements_failed")
		return
	}
	requirements := nodeinspect.Requirements{ProfileID: profile.ID, MemoryMi: assessment.Resources.MemoryMi, DiskGi: assessment.Resources.StorageGi, RequiredPorts: []int{80, 443, 6443}}
	if target.Kind == nodeinspect.SameHostTarget {
		report, err := nodeinspect.InspectSameHost(profile.ID)
		if err != nil {
			writeError(response, http.StatusConflict, "same_host_inspection_unsupported")
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"target": target, "report": report, "assessment": nodeinspect.Assess(report, requirements)})
		return
	}
	trust, err := server.store.GetNodeTrust(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) || trust.Host != target.Host || trust.Port != target.Port || trust.Username != target.Username {
		writeError(response, http.StatusConflict, "node_host_key_confirmation_required")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "node_inspection_failed")
		return
	}
	credentials, err := server.storeNodeCredentials(request.Context(), input.ProfileID, input.Authentication)
	if errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_node_credentials")
		return
	}
	report, result, err := nodeinspect.InspectRemote(request.Context(), target, credentials, trust.Fingerprint, profile.ID, requirements)
	if errors.Is(err, nodeinspect.ErrHostKeyMismatch) {
		writeError(response, http.StatusConflict, "node_host_key_mismatch")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadGateway, "node_inspection_failed")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"target": target, "report": report, "assessment": result})
}

func (server *Server) storeNodeCredentials(ctx context.Context, profileID string, input struct {
	Kind          nodeinspect.AuthenticationKind `json:"kind"`
	Password      string                         `json:"password"`
	PrivateKey    string                         `json:"privateKey"`
	KeyPassphrase string                         `json:"keyPassphrase"`
	SudoPassword  string                         `json:"sudoPassword"`
}) (nodeinspect.Credentials, error) {
	credentials := nodeinspect.Credentials{Kind: input.Kind, Password: input.Password, PrivateKey: []byte(input.PrivateKey), KeyPassphrase: input.KeyPassphrase, SudoPassword: input.SudoPassword}
	if input.Kind != nodeinspect.AgentAuthentication && input.Kind != nodeinspect.PrivateKeyAuthentication && input.Kind != nodeinspect.PasswordAuthentication {
		return nodeinspect.Credentials{}, fmt.Errorf("unsupported node authentication")
	}
	if input.Kind == nodeinspect.PasswordAuthentication && input.Password == "" || input.Kind == nodeinspect.PrivateKeyAuthentication && input.PrivateKey == "" {
		return nodeinspect.Credentials{}, fmt.Errorf("missing node authentication material")
	}
	for _, secret := range []struct{ key, value string }{{"password", input.Password}, {"private-key", input.PrivateKey}, {"key-passphrase", input.KeyPassphrase}, {"sudo-password", input.SudoPassword}} {
		if secret.value == "" {
			continue
		}
		if err := server.vault.Store(profileID+"/node-"+secret.key, secret.value); err != nil {
			return nodeinspect.Credentials{}, err
		}
		expiresAt := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
		if err := server.store.UpsertCredentialReference(ctx, state.CredentialReference{ProfileID: profileID, Kind: "node-" + secret.key, VaultKey: profileID + "/node-" + secret.key, Source: "operator", ExpiresAt: expiresAt, RotationStatus: credentialRotationStatus(expiresAt, time.Now())}); err != nil {
			return nodeinspect.Credentials{}, err
		}
	}
	return credentials, nil
}

func (server *Server) capabilities(response http.ResponseWriter, request *http.Request) {
	if _, ok := server.authenticatedSession(request); !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", "GET")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	writeJSON(response, http.StatusOK, capability.DefaultCatalog())
}

func (server *Server) capabilityPlan(response http.ResponseWriter, request *http.Request) {
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
	var input capabilityRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_capability_selection")
		return
	}
	profile, err := server.store.GetProfile(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "capability_unavailable")
		return
	}
	overlay, err := capability.DefaultCatalog().RenderOverlay(capability.OverlayInput{Selection: capability.Selection{Mode: input.Mode, DeploymentMode: capability.DeploymentMode(profile.DeploymentMode), CommunityIDs: input.CommunityIDs}, Release: input.Release, RepositoryURL: input.RepositoryURL, Domain: input.Domain})
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_capability_selection")
		return
	}
	plan, err := server.workflow.PlanChange(request.Context(), profile.ID, "ApplyCapabilities", overlay.Diff, []workflow.Effect{{Code: "gitops.overlay.previewed", MessageKey: "plan.effect.gitops_overlay"}})
	if err != nil {
		writeError(response, http.StatusInternalServerError, "plan_creation_failed")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"plan": plan, "overlay": overlay})
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
