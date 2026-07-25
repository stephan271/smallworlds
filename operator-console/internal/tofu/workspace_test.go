package tofu

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspacesAreIsolatedPerProfile(t *testing.T) {
	dataDirectory := t.TempDir()
	production, err := OpenWorkspace(dataDirectory, "profile-prod")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	development, err := OpenWorkspace(dataDirectory, "profile-dev")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	writeState(t, production, []byte(`{"serial":1,"resources":["prod"]}`))
	writeState(t, development, []byte(`{"serial":1,"resources":["dev"]}`))

	productionState, err := production.ReadState()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(productionState), "dev") {
		t.Fatal("profiles must not share state")
	}
	if _, err := OpenWorkspace(dataDirectory, "../escape"); !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("path traversal accepted: %v", err)
	}
}

func TestWorkspaceLockIsExclusive(t *testing.T) {
	workspace, err := OpenWorkspace(t.TempDir(), "profile-1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	lock, err := workspace.Acquire("launcher-host-a")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if _, err := workspace.Acquire("launcher-host-b"); !errors.Is(err, ErrWorkspaceLocked) {
		t.Fatalf("second holder acquired the lock: %v", err)
	}
	status, err := workspace.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.Locked || status.LockOwner != "launcher-host-a" || status.LockedAt.IsZero() {
		t.Fatalf("status %+v", status)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := lock.Release(); !errors.Is(err, ErrLockNotHeld) {
		t.Fatalf("double release returned %v", err)
	}
	second, err := workspace.Acquire("launcher-host-b")
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}

func TestStateWritesRequireTheLockAndKeepBackups(t *testing.T) {
	workspace, err := OpenWorkspace(t.TempDir(), "profile-1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := workspace.WriteState(nil, []byte(`{"serial":1}`)); !errors.Is(err, ErrLockNotHeld) {
		t.Fatalf("unlocked write returned %v", err)
	}
	if _, err := workspace.ReadState(); !errors.Is(err, ErrNoState) {
		t.Fatalf("empty workspace read returned %v", err)
	}
	for serial := 1; serial <= 3; serial++ {
		writeState(t, workspace, []byte(`{"serial":`+string(rune('0'+serial))+`}`))
	}
	backups, err := workspace.Backups()
	if err != nil {
		t.Fatalf("backups: %v", err)
	}
	if len(backups) != 2 {
		t.Fatalf("expected the two superseded states as backups, got %d", len(backups))
	}
	// Restoring recovers an earlier state without discarding the current one.
	lock, err := workspace.Acquire("launcher")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	oldest := backups[len(backups)-1]
	if err := workspace.RestoreBackup(lock, oldest); err != nil {
		t.Fatalf("restore: %v", err)
	}
	restored, err := workspace.ReadState()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(restored) != `{"serial":1}` {
		t.Fatalf("restored state %q", restored)
	}
	if err := workspace.RestoreBackup(lock, "../../etc/passwd"); !errors.Is(err, ErrNoState) {
		t.Fatalf("traversing restore returned %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}

func TestBackupsArePruned(t *testing.T) {
	workspace, err := OpenWorkspace(t.TempDir(), "profile-1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for serial := 0; serial < BackupRetention+5; serial++ {
		writeState(t, workspace, []byte(`{"serial":`+string(rune('0'+serial%10))+`}`))
	}
	backups, err := workspace.Backups()
	if err != nil {
		t.Fatalf("backups: %v", err)
	}
	if len(backups) != BackupRetention {
		t.Fatalf("kept %d backups, want %d", len(backups), BackupRetention)
	}
}

func TestStatusExposesNoPathsOrStateContents(t *testing.T) {
	dataDirectory := t.TempDir()
	workspace, err := OpenWorkspace(dataDirectory, "profile-1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	secretBearingState := []byte(`{"outputs":{"hcloud_token":"super-secret-token"}}`)
	writeState(t, workspace, secretBearingState)
	status, err := workspace.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	rendered := status.ProfileID + status.StateDigest + status.LockOwner
	for _, forbidden := range []string{"super-secret-token", dataDirectory, "tofu-workspaces", "terraform.tfstate"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("status leaked %q", forbidden)
		}
	}
	if !status.HasState || !status.Isolated || status.StateDigest == "" || status.UpdatedAt.IsZero() {
		t.Fatalf("status %+v", status)
	}

	// State and its backups stay owner-only on disk.
	stateInfo, err := os.Stat(filepath.Join(dataDirectory, "tofu-workspaces", "profile-1", "terraform.tfstate"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if permissions := stateInfo.Mode().Perm(); permissions != 0o600 {
		t.Fatalf("state permissions %o", permissions)
	}
}

func writeState(t *testing.T, workspace *Workspace, state []byte) {
	t.Helper()
	lock, err := workspace.Acquire("launcher")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := workspace.WriteState(lock, state); err != nil {
		t.Fatalf("write state: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}
