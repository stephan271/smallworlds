package tofu

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/fileprotection"
)

var (
	// ErrWorkspaceLocked is returned when another holder already owns the
	// workspace lock. Two reconciliations of one profile must never interleave.
	ErrWorkspaceLocked = errors.New("tofu: workspace is locked")
	// ErrLockNotHeld is returned when releasing a lock that has since been
	// taken over by someone else.
	ErrLockNotHeld = errors.New("tofu: workspace lock is not held")
	// ErrNoState is returned when a workspace has no state yet.
	ErrNoState = errors.New("tofu: workspace has no state")
	// ErrInvalidProfile is returned for a profile id that cannot name a
	// directory safely.
	ErrInvalidProfile = errors.New("tofu: invalid profile identifier")
)

// safeProfile keeps a profile id from escaping the workspace root; only ids the
// state store already produces are accepted.
var safeProfile = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,119}$`)

// BackupRetention is how many previous state versions a workspace keeps. State
// is the record of what exists in the provider, so losing it strands paid
// resources; a handful of generations is cheap insurance.
const BackupRetention = 10

// Workspace is one Cluster Profile's isolated OpenTofu state directory.
// Profiles never share a workspace, so a development installation cannot
// reconcile production's infrastructure even with the same toolchain.
type Workspace struct {
	profileID string
	root      string
}

// Lock is an exclusive claim on a workspace, held while state is being read for
// planning or written after reconciliation.
type Lock struct {
	workspace *Workspace
	token     string
}

// lockFile is the on-disk lock record. It names the holder so a stale lock can
// be explained rather than silently broken.
type lockFile struct {
	Token    string    `json:"token"`
	Owner    string    `json:"owner"`
	Acquired time.Time `json:"acquired"`
}

// Status is the safe projection of a workspace: no filesystem paths, no state
// contents, and no credential material — only what the console needs to show
// that the workspace is isolated, locked, and backed up.
type Status struct {
	ProfileID   string    `json:"profileId"`
	Isolated    bool      `json:"isolated"`
	HasState    bool      `json:"hasState"`
	StateDigest string    `json:"stateDigest,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt,omitempty"`
	Locked      bool      `json:"locked"`
	LockOwner   string    `json:"lockOwner,omitempty"`
	LockedAt    time.Time `json:"lockedAt,omitempty"`
	Backups     int       `json:"backups"`
}

// OpenWorkspace creates or opens the profile's workspace under the launcher's
// data directory with owner-only permissions.
func OpenWorkspace(dataDirectory, profileID string) (*Workspace, error) {
	if !safeProfile.MatchString(profileID) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidProfile, profileID)
	}
	root := filepath.Join(dataDirectory, "tofu-workspaces", profileID)
	if err := fileprotection.SecureDirectory(root); err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}
	if err := fileprotection.SecureDirectory(filepath.Join(root, "backups")); err != nil {
		return nil, fmt.Errorf("create workspace backups: %w", err)
	}
	return &Workspace{profileID: profileID, root: root}, nil
}

// ProfileID identifies the workspace's profile. The path is deliberately not
// exposed — callers get a handle, not a directory to hand to a subprocess of
// their choosing.
func (workspace *Workspace) ProfileID() string { return workspace.profileID }

// Acquire takes the exclusive lock, naming the holder. It never breaks an
// existing lock: an interleaved reconciliation is a worse failure than a
// blocked one.
func (workspace *Workspace) Acquire(owner string) (*Lock, error) {
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	record, err := json.Marshal(lockFile{Token: token, Owner: owner, Acquired: time.Now().UTC()})
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(workspace.lockPath(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		return nil, ErrWorkspaceLocked
	}
	if err != nil {
		return nil, fmt.Errorf("acquire workspace lock: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(record); err != nil {
		_ = os.Remove(workspace.lockPath())
		return nil, fmt.Errorf("write workspace lock: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = os.Remove(workspace.lockPath())
		return nil, err
	}
	return &Lock{workspace: workspace, token: token}, nil
}

// Release drops the lock, refusing if the lock on disk is no longer this one.
func (lock *Lock) Release() error {
	held, err := lock.workspace.readLock()
	if err != nil {
		return err
	}
	if held == nil || held.Token != lock.token {
		return ErrLockNotHeld
	}
	if err := os.Remove(lock.workspace.lockPath()); err != nil {
		return fmt.Errorf("release workspace lock: %w", err)
	}
	return nil
}

// WriteState replaces the workspace state, backing up the previous version
// first and writing atomically with owner-only permissions. It requires the
// lock, so a write can never land underneath another holder's plan.
func (workspace *Workspace) WriteState(lock *Lock, state []byte) error {
	if lock == nil || lock.workspace != workspace {
		return ErrLockNotHeld
	}
	held, err := workspace.readLock()
	if err != nil {
		return err
	}
	if held == nil || held.Token != lock.token {
		return ErrLockNotHeld
	}
	if previous, err := os.ReadFile(workspace.statePath()); err == nil {
		backup := filepath.Join(workspace.root, "backups", fmt.Sprintf("state-%d.json", time.Now().UTC().UnixNano()))
		if err := fileprotection.WriteFileAtomically(backup, previous); err != nil {
			return fmt.Errorf("back up workspace state: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := fileprotection.WriteFileAtomically(workspace.statePath(), state); err != nil {
		return fmt.Errorf("write workspace state: %w", err)
	}
	return workspace.pruneBackups()
}

// ReadState returns the current state contents. Callers are launcher-owned
// executors; the value is never returned over the API.
func (workspace *Workspace) ReadState() ([]byte, error) {
	state, err := os.ReadFile(workspace.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoState
	}
	return state, err
}

// RestoreBackup replaces the current state with a kept backup, itself backing
// up what it replaces, so an operator recovering from a bad apply cannot lose
// the state they are recovering from.
func (workspace *Workspace) RestoreBackup(lock *Lock, name string) error {
	if strings.Contains(name, string(os.PathSeparator)) || strings.Contains(name, "..") {
		return fmt.Errorf("%w: backup name", ErrNoState)
	}
	contents, err := os.ReadFile(filepath.Join(workspace.root, "backups", name))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNoState
	}
	if err != nil {
		return err
	}
	return workspace.WriteState(lock, contents)
}

// Backups lists the kept state generations, newest first.
func (workspace *Workspace) Backups() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(workspace.root, "backups"))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	return names, nil
}

// Status reports the workspace without revealing its location or contents. The
// state is identified by digest only: it holds provider credentials and
// resource detail, so it is never echoed to the browser.
func (workspace *Workspace) Status() (Status, error) {
	status := Status{ProfileID: workspace.profileID, Isolated: true}
	if info, err := os.Stat(workspace.statePath()); err == nil {
		contents, err := os.ReadFile(workspace.statePath())
		if err != nil {
			return Status{}, err
		}
		digest := sha256.Sum256(contents)
		status.HasState, status.StateDigest, status.UpdatedAt = true, hex.EncodeToString(digest[:]), info.ModTime().UTC()
	} else if !errors.Is(err, os.ErrNotExist) {
		return Status{}, err
	}
	held, err := workspace.readLock()
	if err != nil {
		return Status{}, err
	}
	if held != nil {
		status.Locked, status.LockOwner, status.LockedAt = true, held.Owner, held.Acquired
	}
	backups, err := workspace.Backups()
	if err != nil {
		return Status{}, err
	}
	status.Backups = len(backups)
	return status, nil
}

func (workspace *Workspace) pruneBackups() error {
	names, err := workspace.Backups()
	if err != nil {
		return err
	}
	for _, name := range names[min(len(names), BackupRetention):] {
		if err := os.Remove(filepath.Join(workspace.root, "backups", name)); err != nil {
			return err
		}
	}
	return nil
}

func (workspace *Workspace) readLock() (*lockFile, error) {
	contents, err := os.ReadFile(workspace.lockPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record lockFile
	if err := json.Unmarshal(contents, &record); err != nil {
		// An unreadable lock file is still a held lock; it is never ignored.
		return &lockFile{Owner: "unknown"}, nil
	}
	return &record, nil
}

func (workspace *Workspace) statePath() string {
	return filepath.Join(workspace.root, "terraform.tfstate")
}

func (workspace *Workspace) lockPath() string {
	return filepath.Join(workspace.root, "workspace.lock")
}

func randomToken() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
