package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/clustersecrets"
	"github.com/stephan271/smallworlds/operator-console/internal/overlayadoption"
	"github.com/stephan271/smallworlds/operator-console/internal/state"
	"github.com/stephan271/smallworlds/operator-console/internal/vault"
)

// generateClusterSecrets creates the Cluster Secrets a profile's cluster cannot
// start without and custodies them in the Launcher Vault, returning the key
// they were stored under. It is called only when the Operator supplied nothing
// and nothing was kept from an earlier run, so it never overwrites a manifest
// somebody chose.
func (server *Server) generateClusterSecrets(ctx context.Context, profileID string, overlay state.OverlayIdentity, cluster clustersecrets.Cluster) (string, error) {
	repository, err := server.overlayRepositoryCredential(profileID, overlay)
	if err != nil {
		return "", err
	}
	generated, err := clustersecrets.Generate(repository, cluster)
	if err != nil {
		return "", err
	}
	key := clusterSecretsVaultKey(profileID)
	if err := server.vault.Store(key, generated.Manifest); err != nil {
		return "", err
	}
	// Recorded like any other custodied secret, and marked as this console's own
	// work rather than the Operator's, so the interface can tell the two apart
	// without ever seeing the value.
	if err := server.store.UpsertCredentialReference(ctx, state.CredentialReference{ProfileID: profileID, Kind: "cluster-secrets-manifest", VaultKey: key, Source: "generated", ExpiresAt: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), RotationStatus: "current"}); err != nil {
		return "", err
	}
	return key, nil
}

// overlayRepositoryCredential rebuilds Argo CD's access to the private settings
// repository out of what the console established and already holds. Asking the
// Operator to type this back was asking them to copy a token out of a vault
// that exists so they would not have to.
func (server *Server) overlayRepositoryCredential(profileID string, overlay state.OverlayIdentity) (clustersecrets.Repository, error) {
	repository := clustersecrets.Repository{URL: overlay.RepositoryURL}
	switch overlay.Provider {
	case "generic-https":
		username, err := server.vault.Load(profileID + "/generic-git-username")
		if err != nil {
			return clustersecrets.Repository{}, err
		}
		token, err := server.vault.Load(profileID + "/generic-git-token")
		if err != nil {
			return clustersecrets.Repository{}, err
		}
		repository.Username, repository.Password = username, token
	case "github":
		token, err := server.vault.Load(profileID + "/github-ongoing-token")
		if errors.Is(err, vault.ErrSecretNotFound) {
			token, err = server.vault.Load(profileID + "/github-creation-token")
		}
		if err != nil {
			return clustersecrets.Repository{}, err
		}
		// The Applications in the overlay carry the clone URL, which is the HTML
		// URL with .git; Argo CD normalises the suffix away when it matches a
		// repository Secret, but writing the same string it will be compared
		// against removes the question.
		repository.URL = strings.TrimSuffix(overlay.RepositoryURL, ".git") + ".git"
		// GitHub authenticates by the token alone. The owner is the honest thing
		// to put in the username field rather than a placeholder.
		repository.Username, _, _ = strings.Cut(overlay.Repository, "/")
		repository.Password = token
	default:
		return clustersecrets.Repository{}, fmt.Errorf("no repository credential for git provider %q", overlay.Provider)
	}
	if repository.Username == "" {
		return clustersecrets.Repository{}, fmt.Errorf("no repository username for git provider %q", overlay.Provider)
	}
	return repository, nil
}

// revealClusterSecretCredentials returns the two logins a person actually uses.
// Everything else in the manifest is machine-to-machine and stays in the Vault.
//
// This is the one place the console hands a credential value back to the
// browser, and it is deliberate: these are credentials the console *created for*
// the Operator, not credentials the Operator entrusted to it. A generated
// Keycloak admin password nobody can ever read is not a safer secret, it is a
// locked door — the shell installer has always printed exactly these two.
func (server *Server) revealClusterSecretCredentials(response http.ResponseWriter, request *http.Request) {
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
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_cluster_secrets_request")
		return
	}
	if _, err := server.store.GetProfile(request.Context(), input.ProfileID); errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "cluster_secrets_read_failed")
		return
	}
	manifest, err := server.vault.Load(clusterSecretsVaultKey(input.ProfileID))
	if errors.Is(err, vault.ErrLocked) {
		writeError(response, http.StatusLocked, "vault_locked")
		return
	}
	if errors.Is(err, vault.ErrSecretNotFound) {
		writeError(response, http.StatusConflict, "cluster_secrets_absent")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "cluster_secrets_read_failed")
		return
	}
	credentials, err := clustersecrets.ReadCredentials(manifest)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "cluster_secrets_read_failed")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"credentials": credentials, "present": credentials.Present()})
}

// adoptOverlayRevision carries a reviewed and merged overlay commit to the
// cluster. It is the step that closed the gap between "the proposal is merged"
// and "the cluster runs it": the root Application is pinned to the commit
// approved at installation and rewritten never, so until now a merged release
// update deployed nothing.
//
// Deliberately separate from proposing. Merging is the Operator's act in their
// own Git provider, and this console does not watch for it and act on its own —
// adopting is a second, explicit approval that a reviewed commit may become
// what the cluster runs.
func (server *Server) adoptOverlayRevision(response http.ResponseWriter, request *http.Request) {
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
		Revision  string `json:"revision"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" {
		writeError(response, http.StatusBadRequest, "invalid_overlay_adoption")
		return
	}
	if err := overlayadoption.ValidateRevision(input.Revision); err != nil {
		writeError(response, http.StatusBadRequest, "overlay_revision_invalid")
		return
	}
	identity, err := server.store.GetOverlayIdentity(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusConflict, "gitops_overlay_required")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "overlay_adoption_failed")
		return
	}
	adopted, err := server.localBootstrap.Adopt(request.Context(), input.ProfileID, input.Revision)
	if err != nil {
		log.Printf("overlay adoption: profile %s: %v", input.ProfileID, err)
		writeError(response, http.StatusBadGateway, "overlay_adoption_failed")
		return
	}
	// Read back from the cluster rather than assumed from a patch that returned
	// no error, and only then recorded: the identity says what is deployed, so
	// it must not say so before the cluster agrees.
	if adopted != input.Revision {
		writeError(response, http.StatusBadGateway, "overlay_adoption_unconfirmed")
		return
	}
	identity.Commit = adopted
	identity.RecordedAt = time.Now().UTC()
	if err := server.store.RecordOverlayIdentity(request.Context(), identity); err != nil {
		writeError(response, http.StatusInternalServerError, "overlay_adoption_failed")
		return
	}
	writeJSON(response, http.StatusOK, identity)
}
