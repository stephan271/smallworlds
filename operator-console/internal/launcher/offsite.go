package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/offsite"
	"github.com/stephan271/smallworlds/operator-console/internal/state"
	"github.com/stephan271/smallworlds/operator-console/internal/vault"
	"github.com/stephan271/smallworlds/operator-console/internal/workflow"
)

// unavailableOffsiteInspector is the launcher default: with no real S3 client it
// cannot inspect a destination bucket, so it reports reachability false and
// versioning unknown — honestly forcing an explicit acknowledgement rather than
// claiming versioning is enabled.
type unavailableOffsiteInspector struct{}

func (unavailableOffsiteInspector) Inspect(context.Context, offsite.Destination, offsite.Credentials) (offsite.Inspection, error) {
	return offsite.Inspection{Reachable: false, Versioning: offsite.VersioningUnknown}, nil
}

// offsiteRecord is the secret-free JSON persisted for a profile's offsite
// destination: the destination reference plus its last bucket inspection. No
// credential value is ever stored here — the access key and secret live only in
// the Launcher Vault.
type offsiteRecord struct {
	Reference  offsite.Reference  `json:"reference"`
	Inspection offsite.Inspection `json:"inspection"`
}

func offsiteVaultKeys(profileID string) (accessKey, secretKey string) {
	return profileID + "/offsite-access-key-id", profileID + "/offsite-secret-access-key"
}

func (server *Server) inspectOffsite(response http.ResponseWriter, request *http.Request) {
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
		ProfileID       string `json:"profileId"`
		Endpoint        string `json:"endpoint"`
		Region          string `json:"region"`
		Bucket          string `json:"bucket"`
		AccessKeyID     string `json:"accessKeyId"`
		SecretAccessKey string `json:"secretAccessKey"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 32*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_offsite_destination")
		return
	}
	if _, err := server.store.GetProfile(request.Context(), input.ProfileID); errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "offsite_inspection_failed")
		return
	}
	destination := offsite.Destination{Endpoint: input.Endpoint, Region: input.Region, Bucket: input.Bucket}
	if err := destination.Validate(); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_offsite_destination")
		return
	}
	credentials := offsite.Credentials{AccessKeyID: input.AccessKeyID, SecretAccessKey: input.SecretAccessKey}
	if !credentials.Valid() {
		writeError(response, http.StatusBadRequest, "invalid_offsite_credentials")
		return
	}

	// Custody the credential values in the Launcher Vault, never in Git or the
	// persisted reference, and never returned to the browser.
	accessKeyVaultKey, secretVaultKey := offsiteVaultKeys(input.ProfileID)
	if err := server.vault.Store(accessKeyVaultKey, credentials.AccessKeyID); errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "offsite_storage_failed")
		return
	}
	if err := server.vault.Store(secretVaultKey, credentials.SecretAccessKey); err != nil {
		writeError(response, http.StatusInternalServerError, "offsite_storage_failed")
		return
	}
	expiresAt := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, reference := range []state.CredentialReference{
		{ProfileID: input.ProfileID, Kind: "offsite-s3-access-key", VaultKey: accessKeyVaultKey, Source: "operator", ExpiresAt: expiresAt, RotationStatus: "current"},
		{ProfileID: input.ProfileID, Kind: "offsite-s3-secret", VaultKey: secretVaultKey, Source: "operator", ExpiresAt: expiresAt, RotationStatus: "current"},
	} {
		if err := server.store.UpsertCredentialReference(request.Context(), reference); err != nil {
			writeError(response, http.StatusInternalServerError, "offsite_storage_failed")
			return
		}
	}

	inspection, err := server.offsite.Inspect(request.Context(), destination, credentials)
	if err != nil {
		writeError(response, http.StatusBadGateway, "offsite_inspection_failed")
		return
	}
	record := offsiteRecord{
		Reference:  offsite.NewReference(destination, "", "", credentials.AccessKeyID, false),
		Inspection: inspection,
	}
	if !server.persistOffsiteRecord(request.Context(), input.ProfileID, record) {
		writeError(response, http.StatusInternalServerError, "offsite_storage_failed")
		return
	}
	writeJSON(response, http.StatusOK, offsiteView(record))
}

func (server *Server) offsiteStatus(response http.ResponseWriter, request *http.Request) {
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
	record, ok := server.loadOffsiteRecord(request.Context(), profileID)
	if !ok {
		writeError(response, http.StatusNotFound, "offsite_not_configured")
		return
	}
	writeJSON(response, http.StatusOK, offsiteView(record))
}

func (server *Server) planOffsite(response http.ResponseWriter, request *http.Request) {
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
		ProfileID    string `json:"profileId"`
		Acknowledged bool   `json:"acknowledged"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_offsite_plan_request")
		return
	}
	record, ok := server.loadOffsiteRecord(request.Context(), input.ProfileID)
	if !ok {
		writeError(response, http.StatusConflict, "offsite_not_configured")
		return
	}
	// Load the access key id from the Vault so the plan's fingerprint stays bound
	// to the custodied credential; the value never leaves this handler.
	accessKeyVaultKey, _ := offsiteVaultKeys(input.ProfileID)
	accessKeyID, err := server.vault.Load(accessKeyVaultKey)
	if errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	}
	if errors.Is(err, vault.ErrSecretNotFound) {
		writeError(response, http.StatusConflict, "offsite_credentials_missing")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "offsite_plan_failed")
		return
	}

	plan, err := offsite.Plan(record.Reference.Destination, record.Reference.Schedule, record.Reference.SecretName, accessKeyID, record.Inspection, input.Acknowledged)
	if errors.Is(err, offsite.ErrVersioningUnacknowledged) {
		writeError(response, http.StatusConflict, "offsite_versioning_acknowledgement_required")
		return
	}
	if errors.Is(err, offsite.ErrInvalidDestination) {
		writeError(response, http.StatusBadRequest, "invalid_offsite_destination")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "offsite_plan_failed")
		return
	}

	record.Reference = plan.Reference
	if !server.persistOffsiteRecord(request.Context(), input.ProfileID, record) {
		writeError(response, http.StatusInternalServerError, "offsite_plan_failed")
		return
	}

	workflowPlan, err := server.workflow.PlanChange(request.Context(), input.ProfileID, "ConfigureOffsiteProtection", plan.GitDiff, []workflow.Effect{
		{Code: "offsite.secret.injected", MessageKey: "plan.effect.offsite_secret"},
		{Code: "offsite.overlay.proposed", MessageKey: "plan.effect.offsite_overlay"},
	})
	if err != nil {
		writeError(response, http.StatusInternalServerError, "offsite_plan_failed")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{
		"plan":         workflowPlan,
		"secret":       plan.Secret,
		"gitDiff":      plan.GitDiff,
		"implications": plan.Implications,
	})
}

func (server *Server) persistOffsiteRecord(ctx context.Context, profileID string, record offsiteRecord) bool {
	encoded, err := json.Marshal(record)
	if err != nil {
		return false
	}
	return server.store.RecordOffsiteProtection(ctx, state.OffsiteProtectionReference{ProfileID: profileID, Reference: string(encoded), RecordedAt: time.Now().UTC()}) == nil
}

func (server *Server) loadOffsiteRecord(ctx context.Context, profileID string) (offsiteRecord, bool) {
	stored, err := server.store.GetOffsiteProtection(ctx, profileID)
	if err != nil {
		return offsiteRecord{}, false
	}
	var record offsiteRecord
	if err := json.Unmarshal([]byte(stored.Reference), &record); err != nil {
		return offsiteRecord{}, false
	}
	return record, true
}

// offsiteView is the secret-free browser projection of a stored offsite record:
// destination shape, the key fingerprint, and the inspection verdict — never the
// access key or secret.
func offsiteView(record offsiteRecord) map[string]any {
	return map[string]any{
		"destination":             record.Reference.Destination,
		"schedule":                record.Reference.Schedule,
		"secretName":              record.Reference.SecretName,
		"accessKeyFingerprint":    record.Reference.AccessKeyFingerprint,
		"versioningAcknowledged":  record.Reference.VersioningAcknowledged,
		"reachable":               record.Inspection.Reachable,
		"versioning":              record.Inspection.Versioning,
		"requiresAcknowledgement": record.Inspection.RequiresAcknowledgement(),
	}
}
