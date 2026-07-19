package launcher_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"filippo.io/age"
	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
)

func TestRecoveryBundleExportEncryptsProfileAndVaultMaterial(t *testing.T) {
	handler, err := launcher.New(launcher.Config{
		DataDir:     t.TempDir(),
		LaunchToken: "recovery-export-launch",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "recovery-export-launch")
	profile := createProfile(t, handler, cookie, csrf, "Recovery Workshop", "en", "local-lan")
	unlockVaultForRecoveryTest(t, handler, cookie, csrf)
	secret := "recovery-secret-must-never-be-cleartext"
	credentialBody, err := json.Marshal(map[string]string{
		"value":     secret,
		"expiresAt": "2034-01-02T03:04:05Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	response := request(t, handler, http.MethodPut, "/api/v1/profiles/"+profile.ID+"/credentials/git-provider-token", credentialBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("store credential status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	response.Body.Close()

	exportBody, err := json.Marshal(map[string]string{
		"profileId":  profile.ID,
		"passphrase": "recovery bundle passphrase",
	})
	if err != nil {
		t.Fatal(err)
	}
	response = request(t, handler, http.MethodPost, "/api/v1/recovery-bundles/export", exportBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("export status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	bundle, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if contentType := response.Header.Get("Content-Type"); contentType != "application/octet-stream" {
		t.Fatalf("bundle content type = %q, want application/octet-stream", contentType)
	}
	if !bytes.HasPrefix(bundle, []byte("SWRECOVERY/1\n")) {
		t.Fatalf("bundle does not start with the minimal format/version header: %q", bundle)
	}
	if bytes.Contains(bundle, []byte(profile.Name)) || bytes.Contains(bundle, []byte(secret)) || bytes.Contains(bundle, []byte(profile.ID)) {
		t.Fatal("cleartext Recovery Bundle contains protected profile or vault material")
	}
}

func TestRecoveryBundleSupportsAdvancedAgeRecipients(t *testing.T) {
	source, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "recipient-source-launch"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	sourceCookie, sourceCSRF := exchange(t, source, "recipient-source-launch")
	profile := createProfile(t, source, sourceCookie, sourceCSRF, "Recipient Workshop", "en", "local-lan")
	unlockVaultForRecoveryTest(t, source, sourceCookie, sourceCSRF)
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	exportBody, err := json.Marshal(map[string]any{
		"profileId":  profile.ID,
		"recipients": []string{identity.Recipient().String()},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := request(t, source, http.MethodPost, "/api/v1/recovery-bundles/export", exportBody, sourceCookie, map[string]string{"X-CSRF-Token": sourceCSRF})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("recipient export status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	bundle, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	target, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "recipient-target-launch"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	targetCookie, targetCSRF := exchange(t, target, "recipient-target-launch")
	previewBody, err := json.Marshal(map[string]string{
		"bundle":   base64.StdEncoding.EncodeToString(bundle),
		"identity": identity.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	response = request(t, target, http.MethodPost, "/api/v1/recovery-bundles/preview", previewBody, targetCookie, map[string]string{"X-CSRF-Token": targetCSRF})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("recipient preview status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	preview, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if bytes.Contains(preview, []byte(identity.String())) {
		t.Fatal("recipient preview returns the private age identity")
	}
}

func TestRecoveryBundlePreviewShowsOnlySafeClusterIdentityBeforeImport(t *testing.T) {
	source, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "preview-source-launch"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	sourceCookie, sourceCSRF := exchange(t, source, "preview-source-launch")
	profile := createProfile(t, source, sourceCookie, sourceCSRF, "Preview Workshop", "en", "local-lan")
	unlockVaultForRecoveryTest(t, source, sourceCookie, sourceCSRF)
	secret := "preview-secret-must-not-be-returned"
	credentialBody, _ := json.Marshal(map[string]string{"value": secret, "expiresAt": "2034-01-02T03:04:05Z"})
	response := request(t, source, http.MethodPut, "/api/v1/profiles/"+profile.ID+"/credentials/git-provider-token", credentialBody, sourceCookie, map[string]string{"X-CSRF-Token": sourceCSRF})
	response.Body.Close()
	bundle := exportRecoveryBundleForTest(t, source, sourceCookie, sourceCSRF, profile.ID, "preview bundle passphrase")

	target, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "preview-target-launch"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	targetCookie, targetCSRF := exchange(t, target, "preview-target-launch")
	previewBody, err := json.Marshal(map[string]string{
		"bundle":     base64.StdEncoding.EncodeToString(bundle),
		"passphrase": "preview bundle passphrase",
	})
	if err != nil {
		t.Fatal(err)
	}
	response = request(t, target, http.MethodPost, "/api/v1/recovery-bundles/preview", previewBody, targetCookie, map[string]string{"X-CSRF-Token": targetCSRF})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("preview status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if bytes.Contains(body, []byte(secret)) || bytes.Contains(body, []byte(`vaultMaterial`)) {
		t.Fatalf("preview exposes protected material: %s", body)
	}
	var preview struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
		Profile struct {
			ID             string `json:"id"`
			Name           string `json:"name"`
			DeploymentMode string `json:"deploymentMode"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(body, &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Format != "smallworlds-recovery-bundle" || preview.Version != 1 || preview.Profile.ID != profile.ID || preview.Profile.Name != profile.Name || preview.Profile.DeploymentMode != profile.DeploymentMode {
		t.Fatalf("preview = %#v, want safe source identity", preview)
	}
}

func TestRecoveryBundleImportRestoresProfileAndCredentialMetadataAfterConfirmation(t *testing.T) {
	source, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "import-source-launch"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	sourceCookie, sourceCSRF := exchange(t, source, "import-source-launch")
	profile := createProfile(t, source, sourceCookie, sourceCSRF, "Transfer Workshop", "en", "local-lan")
	unlockVaultForRecoveryTest(t, source, sourceCookie, sourceCSRF)
	secret := "transferred-secret-must-not-be-returned"
	credentialBody, _ := json.Marshal(map[string]string{"value": secret, "expiresAt": "2034-01-02T03:04:05Z"})
	response := request(t, source, http.MethodPut, "/api/v1/profiles/"+profile.ID+"/credentials/git-provider-token", credentialBody, sourceCookie, map[string]string{"X-CSRF-Token": sourceCSRF})
	response.Body.Close()
	bundle := exportRecoveryBundleForTest(t, source, sourceCookie, sourceCSRF, profile.ID, "import bundle passphrase")

	target, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "import-target-launch"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	targetCookie, targetCSRF := exchange(t, target, "import-target-launch")
	unlockVaultForRecoveryTest(t, target, targetCookie, targetCSRF)
	importBody, err := json.Marshal(map[string]string{
		"bundle":            base64.StdEncoding.EncodeToString(bundle),
		"passphrase":        "import bundle passphrase",
		"expectedProfileId": profile.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	response = request(t, target, http.MethodPost, "/api/v1/recovery-bundles/import", importBody, targetCookie, map[string]string{"X-CSRF-Token": targetCSRF})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("import status = %d, want %d", response.StatusCode, http.StatusCreated)
	}
	importResponse, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if bytes.Contains(importResponse, []byte(secret)) {
		t.Fatal("import response contains protected vault material")
	}

	response = request(t, target, http.MethodGet, "/api/v1/profiles", nil, targetCookie, nil)
	var profiles []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(response.Body).Decode(&profiles); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(profiles) != 1 || profiles[0].ID != profile.ID || profiles[0].Name != profile.Name {
		t.Fatalf("imported profiles = %#v, want source profile", profiles)
	}

	response = request(t, target, http.MethodGet, "/api/v1/profiles/"+profile.ID+"/credentials", nil, targetCookie, nil)
	metadata, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if bytes.Contains(metadata, []byte(secret)) || !bytes.Contains(metadata, []byte(`"present":true`)) {
		t.Fatalf("imported credential metadata = %s, want redacted present credential", metadata)
	}
}

func TestRecoveryBundleRejectsBadCredentialsCorruptionMismatchAndDuplicateWithoutPartialImport(t *testing.T) {
	source, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "failure-source-launch"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	sourceCookie, sourceCSRF := exchange(t, source, "failure-source-launch")
	profile := createProfile(t, source, sourceCookie, sourceCSRF, "Failure Workshop", "en", "local-lan")
	unlockVaultForRecoveryTest(t, source, sourceCookie, sourceCSRF)
	bundle := exportRecoveryBundleForTest(t, source, sourceCookie, sourceCSRF, profile.ID, "failure bundle passphrase")

	target, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "failure-target-launch"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	targetCookie, targetCSRF := exchange(t, target, "failure-target-launch")
	unlockVaultForRecoveryTest(t, target, targetCookie, targetCSRF)
	corruptedBundle := append([]byte(nil), bundle...)
	corruptedBundle[len(corruptedBundle)-1] = 'x'

	for _, attempt := range []struct {
		name string
		body map[string]string
		want int
	}{
		{"wrong passphrase", map[string]string{"bundle": base64.StdEncoding.EncodeToString(bundle), "passphrase": "wrong recovery passphrase", "expectedProfileId": profile.ID}, http.StatusUnauthorized},
		{"corrupted bundle", map[string]string{"bundle": base64.StdEncoding.EncodeToString(corruptedBundle), "passphrase": "failure bundle passphrase", "expectedProfileId": profile.ID}, http.StatusUnauthorized},
		{"identity mismatch", map[string]string{"bundle": base64.StdEncoding.EncodeToString(bundle), "passphrase": "failure bundle passphrase", "expectedProfileId": "different-cluster"}, http.StatusConflict},
	} {
		t.Run(attempt.name, func(t *testing.T) {
			body, err := json.Marshal(attempt.body)
			if err != nil {
				t.Fatal(err)
			}
			response := request(t, target, http.MethodPost, "/api/v1/recovery-bundles/import", body, targetCookie, map[string]string{"X-CSRF-Token": targetCSRF})
			if response.StatusCode != attempt.want {
				t.Fatalf("import status = %d, want %d", response.StatusCode, attempt.want)
			}
			response.Body.Close()
			assertNoImportedProfiles(t, target, targetCookie)
		})
	}

	validBody, err := json.Marshal(map[string]string{"bundle": base64.StdEncoding.EncodeToString(bundle), "passphrase": "failure bundle passphrase", "expectedProfileId": profile.ID})
	if err != nil {
		t.Fatal(err)
	}
	response := request(t, target, http.MethodPost, "/api/v1/recovery-bundles/import", validBody, targetCookie, map[string]string{"X-CSRF-Token": targetCSRF})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("first valid import status = %d, want %d", response.StatusCode, http.StatusCreated)
	}
	response.Body.Close()
	response = request(t, target, http.MethodPost, "/api/v1/recovery-bundles/import", validBody, targetCookie, map[string]string{"X-CSRF-Token": targetCSRF})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate import status = %d, want %d", response.StatusCode, http.StatusConflict)
	}
	response.Body.Close()
	response = request(t, target, http.MethodGet, "/api/v1/profiles", nil, targetCookie, nil)
	var profiles []struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&profiles); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(profiles) != 1 || profiles[0].ID != profile.ID {
		t.Fatalf("profiles after duplicate import = %#v, want exactly imported profile", profiles)
	}
}

func TestRecoveryBundlePreservesVerifiedWorkflowHistory(t *testing.T) {
	source, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "history-source-launch"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	sourceCookie, sourceCSRF := exchange(t, source, "history-source-launch")
	profile := createProfile(t, source, sourceCookie, sourceCSRF, "History Workshop", "en", "local-lan")
	unlockVaultForRecoveryTest(t, source, sourceCookie, sourceCSRF)
	planID := createVerificationPlan(t, source, sourceCookie, sourceCSRF, profile.ID)
	response := request(t, source, http.MethodPost, "/api/v1/plans/"+planID+"/approve", nil, sourceCookie, map[string]string{"X-CSRF-Token": sourceCSRF})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("approve status = %d, want %d", response.StatusCode, http.StatusAccepted)
	}
	var sourceRun workflowRunResponse
	if err := json.NewDecoder(response.Body).Decode(&sourceRun); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	verified := waitForVerifiedRun(t, source, sourceCookie, sourceRun.ID)
	bundle := exportRecoveryBundleForTest(t, source, sourceCookie, sourceCSRF, profile.ID, "history bundle passphrase")

	target, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "history-target-launch"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	targetCookie, targetCSRF := exchange(t, target, "history-target-launch")
	unlockVaultForRecoveryTest(t, target, targetCookie, targetCSRF)
	importBody, _ := json.Marshal(map[string]string{"bundle": base64.StdEncoding.EncodeToString(bundle), "passphrase": "history bundle passphrase", "expectedProfileId": profile.ID})
	response = request(t, target, http.MethodPost, "/api/v1/recovery-bundles/import", importBody, targetCookie, map[string]string{"X-CSRF-Token": targetCSRF})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("import status = %d, want %d", response.StatusCode, http.StatusCreated)
	}
	response.Body.Close()
	response = request(t, target, http.MethodGet, "/api/v1/runs/"+sourceRun.ID, nil, targetCookie, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("restored run status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var restored workflowRunResponse
	if err := json.NewDecoder(response.Body).Decode(&restored); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if restored.State != "verified" || restored.CurrentCheckpoint != "verification-complete" || restored.Verification.Code != verified.Verification.Code {
		t.Fatalf("restored run = %#v, want verified source history %#v", restored, verified)
	}
}

func assertNoImportedProfiles(t *testing.T, handler *launcher.Server, cookie *http.Cookie) {
	t.Helper()
	response := request(t, handler, http.MethodGet, "/api/v1/profiles", nil, cookie, nil)
	defer response.Body.Close()
	var profiles []struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&profiles); err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 0 {
		t.Fatalf("profiles after rejected import = %#v, want none", profiles)
	}
}

func exportRecoveryBundleForTest(t *testing.T, handler *launcher.Server, cookie *http.Cookie, csrf, profileID, passphrase string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]string{"profileId": profileID, "passphrase": passphrase})
	if err != nil {
		t.Fatal(err)
	}
	response := request(t, handler, http.MethodPost, "/api/v1/recovery-bundles/export", body, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("export status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	bundle, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	return bundle
}

func unlockVaultForRecoveryTest(t *testing.T, handler *launcher.Server, cookie *http.Cookie, csrf string) {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"method":     "passphrase",
		"passphrase": "launcher recovery passphrase",
	})
	if err != nil {
		t.Fatal(err)
	}
	response := request(t, handler, http.MethodPost, "/api/v1/vault/unlock", body, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unlock vault status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	response.Body.Close()
}
