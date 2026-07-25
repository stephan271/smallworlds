package launcher

import (
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"runtime"
	"strings"

	"github.com/stephan271/smallworlds/operator-console/internal/nodeinspect"
	"github.com/stephan271/smallworlds/operator-console/internal/state"
	"github.com/stephan271/smallworlds/operator-console/internal/vault"
)

func (server *Server) cleanNode(response http.ResponseWriter, request *http.Request) {
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
		ProfileID      string                    `json:"profileId"`
		Target         nodeTargetRequest         `json:"target"`
		Authentication nodeAuthenticationRequest `json:"authentication"`
		DataDirectory  string                    `json:"dataDirectory"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 512*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ProfileID == "" || input.DataDirectory == "" || nodeinspect.ValidateDataDirectory(input.DataDirectory) != nil {
		writeError(response, http.StatusBadRequest, "invalid_node_inspection")
		return
	}
	target := input.Target.target()
	if err := target.Validate(runtime.GOOS); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_node_target")
		return
	}
	_, err := server.store.GetProfile(request.Context(), input.ProfileID)
	if errors.Is(err, state.ErrNotFound) {
		writeError(response, http.StatusNotFound, "profile_not_found")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "node_clean_failed")
		return
	}

	cmdString := "[ -x /usr/local/bin/k3s-uninstall.sh ] && /usr/local/bin/k3s-uninstall.sh; rm -rf /var/lib/rancher/k3s /etc/rancher /etc/smallworlds " + input.DataDirectory

	if target.Kind == nodeinspect.SameHostTarget {
		cmd := exec.Command("bash", "-c", cmdString)
		if err := cmd.Run(); err != nil {
			writeError(response, http.StatusInternalServerError, "same_host_clean_failed")
			return
		}
	} else {
		trust, err := server.store.GetNodeTrust(request.Context(), input.ProfileID)
		if errors.Is(err, state.ErrNotFound) || trust.Host != target.Host || trust.Port != target.Port || trust.Username != target.Username {
			writeError(response, http.StatusConflict, "node_host_key_confirmation_required")
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

		client, err := nodeinspect.DialTrusted(request.Context(), target, credentials, trust.Fingerprint)
		if err != nil {
			if errors.Is(err, nodeinspect.ErrHostKeyMismatch) {
				writeError(response, http.StatusConflict, "node_host_key_mismatch")
			} else {
				writeError(response, http.StatusBadGateway, "node_connection_failed")
			}
			return
		}
		defer client.Close()

		// Execute wipe
		session, err := client.NewSession()
		if err != nil {
			writeError(response, http.StatusInternalServerError, "node_clean_failed")
			return
		}
		defer session.Close()

		// Handle sudo if credentials contain a sudo password
		if credentials.SudoPassword != "" {
			session.Stdin = strings.NewReader(credentials.SudoPassword + "\n")
			cmdString = "sudo -S -p '' bash -c '" + cmdString + "'"
		} else if target.Username != "root" {
			// They must use sudo if not root, assume passwordless sudo if they provided no password
			cmdString = "sudo -S -p '' bash -c '" + cmdString + "'"
		}

		if err := session.Run(cmdString); err != nil {
			writeError(response, http.StatusInternalServerError, "node_clean_failed")
			return
		}
	}

	response.WriteHeader(http.StatusNoContent)
}
