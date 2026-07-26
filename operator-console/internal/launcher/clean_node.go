package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
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
		// Every path in the wipe is root-owned. Running unelevated made rm fail on
		// permission, which the operator saw as a removal that reported success and
		// then changed nothing. Authorize sudo once, then run non-interactively.
		name, arguments := "bash", []string{"-c", cmdString}
		if os.Geteuid() != 0 {
			if err := authorizeLocalSudo(request.Context(), input.Authentication.SudoPassword); err != nil {
				writeError(response, http.StatusBadRequest, "node_sudo_authorization_failed")
				return
			}
			name, arguments = "sudo", []string{"-n", "bash", "-c", cmdString}
		}
		cmd := exec.CommandContext(request.Context(), name, arguments...)
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
			// No password given means passwordless sudo, which is -n. Asking with
			// -S and nothing on stdin would just hang or fail on an empty read.
			cmdString = "sudo -n bash -c '" + cmdString + "'"
		}

		if err := session.Run(cmdString); err != nil {
			writeError(response, http.StatusInternalServerError, "node_clean_failed")
			return
		}
	}

	response.WriteHeader(http.StatusNoContent)
}

// authorizeLocalSudo refreshes the sudo timestamp so the wipe itself can run
// with -n. Mirrors the local bootstrap runner: a password when one was given,
// otherwise a passwordless check that fails cleanly rather than prompting.
func authorizeLocalSudo(ctx context.Context, password string) error {
	arguments := []string{"-n", "-v"}
	var stdin io.Reader
	if password != "" {
		arguments = []string{"-S", "-p", "", "-v"}
		stdin = strings.NewReader(password + "\n")
	}
	process := exec.CommandContext(ctx, "sudo", arguments...)
	process.Stdin = stdin
	process.Stdout = io.Discard
	process.Stderr = io.Discard
	return process.Run()
}
