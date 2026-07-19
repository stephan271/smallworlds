package launcher_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
	"github.com/stephan271/smallworlds/operator-console/internal/vault"
)

func TestVaultStatusOffersPassphraseFallbackWhenOSStoreIsUnavailable(t *testing.T) {
	handler, err := launcher.New(launcher.Config{
		DataDir:       t.TempDir(),
		LaunchToken:   "vault-status-launch",
		WrappingStore: unavailableWrappingStore{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, _ := exchange(t, handler, "vault-status-launch")

	response := request(t, handler, http.MethodGet, "/api/v1/vault", nil, cookie, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("vault status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	defer response.Body.Close()
	var status struct {
		State                       string `json:"state"`
		OSCredentialStoreAvailable  bool   `json:"osCredentialStoreAvailable"`
		PassphraseFallbackAvailable bool   `json:"passphraseFallbackAvailable"`
	}
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.State != "locked" || status.OSCredentialStoreAvailable || !status.PassphraseFallbackAvailable {
		t.Fatalf("vault status = %#v, want locked with passphrase fallback", status)
	}
}

func TestPersistedLauncherStateIsRestrictedToCurrentUser(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ACL verification runs on the Windows platform adapter")
	}
	dataDir := t.TempDir()
	handler, err := launcher.New(launcher.Config{
		DataDir:       dataDir,
		LaunchToken:   "permissions-launch",
		WrappingStore: unavailableWrappingStore{},
	})
	if err != nil {
		t.Fatal(err)
	}
	cookie, csrf := exchange(t, handler, "permissions-launch")
	body, _ := json.Marshal(map[string]string{"method": "passphrase", "passphrase": "permissions passphrase"})
	response := request(t, handler, http.MethodPost, "/api/v1/vault/unlock", body, cookie, map[string]string{"X-CSRF-Token": csrf})
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unlock status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if err := handler.Close(); err != nil {
		t.Fatal(err)
	}

	err = filepath.WalkDir(dataDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		want := os.FileMode(0o600)
		if entry.IsDir() {
			want = 0o700
		}
		if permissions := info.Mode().Perm(); permissions != want {
			t.Errorf("%s permissions = %04o, want %04o", filepath.Base(path), permissions, want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUnavailableOSCredentialStoreReturnsStableRedactedError(t *testing.T) {
	handler, err := launcher.New(launcher.Config{
		DataDir:       t.TempDir(),
		LaunchToken:   "unavailable-store-launch",
		WrappingStore: unavailableWrappingStore{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "unavailable-store-launch")
	response := request(t, handler, http.MethodPost, "/api/v1/vault/unlock", []byte(`{"method":"operating-system"}`), cookie, map[string]string{"X-CSRF-Token": csrf})
	defer response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unavailable store status = %d, want %d", response.StatusCode, http.StatusServiceUnavailable)
	}
	var failure struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(response.Body).Decode(&failure); err != nil {
		t.Fatal(err)
	}
	if failure.Code != "os_credential_store_unavailable" {
		t.Fatalf("unavailable store code = %q, want os_credential_store_unavailable", failure.Code)
	}
}

func TestOSCredentialStoreUnlocksVaultAcrossRestart(t *testing.T) {
	dataDir := t.TempDir()
	wrappingStore := &memoryWrappingStore{}
	firstLauncher, err := launcher.New(launcher.Config{
		DataDir:       dataDir,
		LaunchToken:   "first-os-vault-launch",
		WrappingStore: wrappingStore,
	})
	if err != nil {
		t.Fatal(err)
	}
	firstCookie, firstCSRF := exchange(t, firstLauncher, "first-os-vault-launch")
	body := []byte(`{"method":"operating-system"}`)
	response := request(t, firstLauncher, http.MethodPost, "/api/v1/vault/unlock", body, firstCookie, map[string]string{"X-CSRF-Token": firstCSRF})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("initial OS-store unlock status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var status struct {
		State        string `json:"state"`
		UnlockMethod string `json:"unlockMethod"`
	}
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if status.State != "unlocked" || status.UnlockMethod != "operating-system" {
		t.Fatalf("vault status = %#v, want OS-store-unlocked", status)
	}
	if err := firstLauncher.Close(); err != nil {
		t.Fatal(err)
	}

	secondLauncher, err := launcher.New(launcher.Config{
		DataDir:       dataDir,
		LaunchToken:   "second-os-vault-launch",
		WrappingStore: wrappingStore,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondLauncher.Close() })
	secondCookie, secondCSRF := exchange(t, secondLauncher, "second-os-vault-launch")
	response = request(t, secondLauncher, http.MethodPost, "/api/v1/vault/unlock", body, secondCookie, map[string]string{"X-CSRF-Token": secondCSRF})
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("reopened OS-store unlock status = %d, want %d", response.StatusCode, http.StatusOK)
	}
}

func TestStoredCredentialReturnsOnlyMetadataAndIsAbsentFromLauncherFiles(t *testing.T) {
	dataDir := t.TempDir()
	handler, err := launcher.New(launcher.Config{
		DataDir:       dataDir,
		LaunchToken:   "credential-launch",
		WrappingStore: unavailableWrappingStore{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "credential-launch")
	profile := createProfile(t, handler, cookie, csrf, "Workshop", "en", "local-lan")
	unlockBody, err := json.Marshal(map[string]string{"method": "passphrase", "passphrase": "vault passphrase"})
	if err != nil {
		t.Fatal(err)
	}
	response := request(t, handler, http.MethodPost, "/api/v1/vault/unlock", unlockBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unlock status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	response.Body.Close()

	credentialValue := "github_pat_secret-never-returned"
	expiresAt := "2030-01-02T03:04:05Z"
	credentialBody, err := json.Marshal(map[string]string{"value": credentialValue, "expiresAt": expiresAt})
	if err != nil {
		t.Fatal(err)
	}
	response = request(t, handler, http.MethodPut, "/api/v1/profiles/"+profile.ID+"/credentials/git-provider-token", credentialBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("store credential status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	storedResponse, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	assertCredentialResponseIsRedacted(t, storedResponse, credentialValue)

	response = request(t, handler, http.MethodGet, "/api/v1/profiles/"+profile.ID+"/credentials", nil, cookie, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list credentials status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	listedResponse, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	assertCredentialResponseIsRedacted(t, listedResponse, credentialValue)
	var metadata []struct {
		Kind           string `json:"kind"`
		Present        bool   `json:"present"`
		Source         string `json:"source"`
		ExpiresAt      string `json:"expiresAt"`
		RotationStatus string `json:"rotationStatus"`
	}
	if err := json.Unmarshal(listedResponse, &metadata); err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 1 || metadata[0].Kind != "git-provider-token" || !metadata[0].Present || metadata[0].Source != "operator" || metadata[0].ExpiresAt != expiresAt || metadata[0].RotationStatus != "current" {
		t.Fatalf("credential metadata = %#v, want presence/source/expiry/rotation only", metadata)
	}

	err = filepath.WalkDir(dataDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(contents, []byte(credentialValue)) {
			t.Errorf("credential value found in persisted launcher file %s", filepath.Base(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCredentialValueIsRedactedFromPlansRunsEventsAndReadResponses(t *testing.T) {
	handler, err := launcher.New(launcher.Config{
		DataDir:       t.TempDir(),
		LaunchToken:   "redaction-launch",
		WrappingStore: unavailableWrappingStore{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "redaction-launch")
	profile := createProfile(t, handler, cookie, csrf, "Workshop", "en", "local-lan")
	unlockBody, _ := json.Marshal(map[string]string{"method": "passphrase", "passphrase": "redaction passphrase"})
	response := request(t, handler, http.MethodPost, "/api/v1/vault/unlock", unlockBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	response.Body.Close()
	secret := "secret-redaction-sentinel-7b924"
	credentialBody, _ := json.Marshal(map[string]string{"value": secret, "expiresAt": "2034-01-02T03:04:05Z"})
	response = request(t, handler, http.MethodPut, "/api/v1/profiles/"+profile.ID+"/credentials/git-provider-token", credentialBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	response.Body.Close()

	for _, path := range []string{"/api/v1/profiles", "/api/v1/profiles/" + profile.ID + "/journey", "/api/v1/profiles/" + profile.ID + "/credentials"} {
		response = request(t, handler, http.MethodGet, path, nil, cookie, nil)
		assertHTTPBodyDoesNotContainSecret(t, response, secret)
	}
	planBody, _ := json.Marshal(map[string]string{"profileId": profile.ID, "intent": "VerifyLauncher"})
	response = request(t, handler, http.MethodPost, "/api/v1/plans", planBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	planResponse, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	assertCredentialResponseIsRedacted(t, planResponse, secret)
	var plan struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(planResponse, &plan); err != nil {
		t.Fatal(err)
	}
	response = request(t, handler, http.MethodPost, "/api/v1/plans/"+plan.ID+"/approve", nil, cookie, map[string]string{"X-CSRF-Token": csrf})
	runResponse, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	assertCredentialResponseIsRedacted(t, runResponse, secret)
	var run struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(runResponse, &run); err != nil {
		t.Fatal(err)
	}
	waitForVerifiedRun(t, handler, cookie, run.ID)
	for _, path := range []string{"/api/v1/runs/" + run.ID, "/api/v1/events?profileId=" + profile.ID + "&cursor=0"} {
		response = request(t, handler, http.MethodGet, path, nil, cookie, nil)
		assertHTTPBodyDoesNotContainSecret(t, response, secret)
	}
}

func assertHTTPBodyDoesNotContainSecret(t *testing.T, response *http.Response, secret string) {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte(secret)) {
		t.Fatalf("HTTP response exposes credential value: %s", body)
	}
}

func TestOperatorReplacesAndRemovesCredentialDeliberately(t *testing.T) {
	handler, err := launcher.New(launcher.Config{
		DataDir:       t.TempDir(),
		LaunchToken:   "credential-lifecycle-launch",
		WrappingStore: unavailableWrappingStore{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "credential-lifecycle-launch")
	profile := createProfile(t, handler, cookie, csrf, "Workshop", "en", "local-lan")
	unlockBody, _ := json.Marshal(map[string]string{"method": "passphrase", "passphrase": "vault passphrase"})
	response := request(t, handler, http.MethodPost, "/api/v1/vault/unlock", unlockBody, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unlock status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	response.Body.Close()

	endpoint := "/api/v1/profiles/" + profile.ID + "/credentials/git-provider-token"
	for _, credential := range []map[string]string{
		{"value": "first-sensitive-value", "expiresAt": "2030-01-02T03:04:05Z"},
		{"value": "replacement-sensitive-value", "expiresAt": "2031-02-03T04:05:06Z"},
	} {
		body, _ := json.Marshal(credential)
		response = request(t, handler, http.MethodPut, endpoint, body, cookie, map[string]string{"X-CSRF-Token": csrf})
		if response.StatusCode != http.StatusOK {
			t.Fatalf("store credential status = %d, want %d", response.StatusCode, http.StatusOK)
		}
		responseBytes, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		assertCredentialResponseIsRedacted(t, responseBytes, credential["value"])
	}

	response = request(t, handler, http.MethodGet, "/api/v1/profiles/"+profile.ID+"/credentials", nil, cookie, nil)
	var replaced []struct {
		ExpiresAt string `json:"expiresAt"`
	}
	if err := json.NewDecoder(response.Body).Decode(&replaced); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(replaced) != 1 || replaced[0].ExpiresAt != "2031-02-03T04:05:06Z" {
		t.Fatalf("replacement metadata = %#v, want new expiry", replaced)
	}

	response = request(t, handler, http.MethodDelete, endpoint, nil, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("remove credential status = %d, want %d", response.StatusCode, http.StatusNoContent)
	}
	response.Body.Close()
	response = request(t, handler, http.MethodGet, "/api/v1/profiles/"+profile.ID+"/credentials", nil, cookie, nil)
	defer response.Body.Close()
	var remaining []json.RawMessage
	if err := json.NewDecoder(response.Body).Decode(&remaining); err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("remaining credential metadata = %#v, want none", remaining)
	}
}

func assertCredentialResponseIsRedacted(t *testing.T, response []byte, secret string) {
	t.Helper()
	if bytes.Contains(response, []byte(secret)) || bytes.Contains(response, []byte(`"value"`)) {
		t.Fatalf("credential response exposes a value field or secret: %s", response)
	}
}

func TestOperatorUnlocksVaultWithPassphraseFallback(t *testing.T) {
	handler, err := launcher.New(launcher.Config{
		DataDir:       t.TempDir(),
		LaunchToken:   "vault-unlock-launch",
		WrappingStore: unavailableWrappingStore{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "vault-unlock-launch")
	passphrase := "correct horse battery staple"
	body, err := json.Marshal(map[string]string{"method": "passphrase", "passphrase": passphrase})
	if err != nil {
		t.Fatal(err)
	}

	response := request(t, handler, http.MethodPost, "/api/v1/vault/unlock", body, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unlock status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	responseBody := new(bytes.Buffer)
	if _, err := responseBody.ReadFrom(response.Body); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if bytes.Contains(responseBody.Bytes(), []byte(passphrase)) {
		t.Fatal("unlock response contains the passphrase")
	}
	var status struct {
		State        string `json:"state"`
		UnlockMethod string `json:"unlockMethod"`
	}
	if err := json.Unmarshal(responseBody.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.State != "unlocked" || status.UnlockMethod != "passphrase" {
		t.Fatalf("vault status = %#v, want passphrase-unlocked", status)
	}

	response = request(t, handler, http.MethodGet, "/api/v1/vault", nil, cookie, nil)
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.State != "unlocked" || status.UnlockMethod != "passphrase" {
		t.Fatalf("subsequent vault status = %#v, want passphrase-unlocked", status)
	}
}

func TestNewVaultRejectsShortPassphraseWithStableRedactedError(t *testing.T) {
	handler, err := launcher.New(launcher.Config{
		DataDir:       t.TempDir(),
		LaunchToken:   "short-passphrase-launch",
		WrappingStore: unavailableWrappingStore{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "short-passphrase-launch")
	passphrase := "too-short"
	body, _ := json.Marshal(map[string]string{"method": "passphrase", "passphrase": passphrase})
	response := request(t, handler, http.MethodPost, "/api/v1/vault/unlock", body, cookie, map[string]string{"X-CSRF-Token": csrf})
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("short passphrase status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}
	responseBytes, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(responseBytes, []byte(passphrase)) {
		t.Fatal("short-passphrase response exposes the submitted passphrase")
	}
	var failure struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(responseBytes, &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Code != "vault_passphrase_too_short" {
		t.Fatalf("short passphrase code = %q, want vault_passphrase_too_short", failure.Code)
	}
}

func TestPassphraseVaultLocksOnRestartAndRejectsWrongPassphrase(t *testing.T) {
	dataDir := t.TempDir()
	firstLauncher, err := launcher.New(launcher.Config{
		DataDir:       dataDir,
		LaunchToken:   "first-vault-launch",
		WrappingStore: unavailableWrappingStore{},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstCookie, firstCSRF := exchange(t, firstLauncher, "first-vault-launch")
	correctPassphrase := "a durable vault passphrase"
	unlockBody, err := json.Marshal(map[string]string{"method": "passphrase", "passphrase": correctPassphrase})
	if err != nil {
		t.Fatal(err)
	}
	response := request(t, firstLauncher, http.MethodPost, "/api/v1/vault/unlock", unlockBody, firstCookie, map[string]string{"X-CSRF-Token": firstCSRF})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("initial unlock status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	response.Body.Close()
	if err := firstLauncher.Close(); err != nil {
		t.Fatal(err)
	}

	secondLauncher, err := launcher.New(launcher.Config{
		DataDir:       dataDir,
		LaunchToken:   "second-vault-launch",
		WrappingStore: unavailableWrappingStore{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondLauncher.Close() })
	secondCookie, secondCSRF := exchange(t, secondLauncher, "second-vault-launch")

	wrongPassphrase := "definitely wrong"
	wrongBody, err := json.Marshal(map[string]string{"method": "passphrase", "passphrase": wrongPassphrase})
	if err != nil {
		t.Fatal(err)
	}
	response = request(t, secondLauncher, http.MethodPost, "/api/v1/vault/unlock", wrongBody, secondCookie, map[string]string{"X-CSRF-Token": secondCSRF})
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong passphrase status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
	responseBytes, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if bytes.Contains(responseBytes, []byte(wrongPassphrase)) || bytes.Contains(responseBytes, []byte(correctPassphrase)) {
		t.Fatal("unlock failure response contains secret material")
	}
	var failure struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(responseBytes, &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Code != "vault_passphrase_incorrect" {
		t.Fatalf("wrong passphrase code = %q, want vault_passphrase_incorrect", failure.Code)
	}

	response = request(t, secondLauncher, http.MethodPost, "/api/v1/vault/unlock", unlockBody, secondCookie, map[string]string{"X-CSRF-Token": secondCSRF})
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("correct passphrase status = %d, want %d", response.StatusCode, http.StatusOK)
	}
}

func TestCredentialMetadataRemainsAvailableAfterRestartAndUnlock(t *testing.T) {
	dataDir := t.TempDir()
	firstLauncher, err := launcher.New(launcher.Config{
		DataDir:       dataDir,
		LaunchToken:   "first-metadata-launch",
		WrappingStore: unavailableWrappingStore{},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstCookie, firstCSRF := exchange(t, firstLauncher, "first-metadata-launch")
	profile := createProfile(t, firstLauncher, firstCookie, firstCSRF, "Workshop", "en", "local-lan")
	passphrase := "restart metadata passphrase"
	unlockBody, _ := json.Marshal(map[string]string{"method": "passphrase", "passphrase": passphrase})
	response := request(t, firstLauncher, http.MethodPost, "/api/v1/vault/unlock", unlockBody, firstCookie, map[string]string{"X-CSRF-Token": firstCSRF})
	response.Body.Close()
	secret := "secret-visible-only-to-vault-consumers"
	credentialBody, _ := json.Marshal(map[string]string{"value": secret, "expiresAt": "2032-03-04T05:06:07Z"})
	response = request(t, firstLauncher, http.MethodPut, "/api/v1/profiles/"+profile.ID+"/credentials/git-provider-token", credentialBody, firstCookie, map[string]string{"X-CSRF-Token": firstCSRF})
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("store credential status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if err := firstLauncher.Close(); err != nil {
		t.Fatal(err)
	}

	secondLauncher, err := launcher.New(launcher.Config{
		DataDir:       dataDir,
		LaunchToken:   "second-metadata-launch",
		WrappingStore: unavailableWrappingStore{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondLauncher.Close() })
	secondCookie, secondCSRF := exchange(t, secondLauncher, "second-metadata-launch")
	response = request(t, secondLauncher, http.MethodPost, "/api/v1/vault/unlock", unlockBody, secondCookie, map[string]string{"X-CSRF-Token": secondCSRF})
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("reopened unlock status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	response = request(t, secondLauncher, http.MethodGet, "/api/v1/profiles/"+profile.ID+"/credentials", nil, secondCookie, nil)
	defer response.Body.Close()
	responseBytes, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	assertCredentialResponseIsRedacted(t, responseBytes, secret)
	var metadata []struct {
		Present bool `json:"present"`
	}
	if err := json.Unmarshal(responseBytes, &metadata); err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 1 || !metadata[0].Present {
		t.Fatalf("reopened credential metadata = %#v, want present", metadata)
	}
}

type unavailableWrappingStore struct{}

func (unavailableWrappingStore) Available(context.Context) bool { return false }

func (unavailableWrappingStore) Load(context.Context) ([]byte, bool, error) {
	return nil, false, errors.New("credential store is unavailable")
}

func (unavailableWrappingStore) Save(context.Context, []byte) error {
	return vault.ErrCredentialStoreUnavailable
}

type memoryWrappingStore struct {
	mu  sync.Mutex
	key []byte
}

func (*memoryWrappingStore) Available(context.Context) bool { return true }

func (store *memoryWrappingStore) Load(context.Context) ([]byte, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.key) == 0 {
		return nil, false, nil
	}
	return append([]byte(nil), store.key...), true, nil
}

func (store *memoryWrappingStore) Save(_ context.Context, key []byte) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.key = append(store.key[:0], key...)
	return nil
}
