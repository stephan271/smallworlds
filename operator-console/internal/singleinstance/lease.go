package singleinstance

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Lease struct {
	owner          bool
	ownerID        string
	existingURL    string
	lockPath       string
	rendezvousPath string
}

type ownership struct {
	ID  string `json:"id"`
	PID int    `json:"pid"`
}

type rendezvous struct {
	OwnerID string `json:"ownerId"`
	URL     string `json:"url"`
}

func Acquire(dataDir string) (*Lease, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create launcher data directory: %w", err)
	}
	if err := os.Chmod(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("protect launcher data directory: %w", err)
	}
	lease := &Lease{
		lockPath:       filepath.Join(dataDir, "launcher.lock"),
		rendezvousPath: filepath.Join(dataDir, "launcher-rendezvous.json"),
	}
	lock, err := os.OpenFile(lease.lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		lease.ownerID, err = newOwnerID()
		if err != nil {
			lock.Close()
			_ = os.Remove(lease.lockPath)
			return nil, err
		}
		lease.owner = true
		if err := json.NewEncoder(lock).Encode(ownership{ID: lease.ownerID, PID: os.Getpid()}); err != nil {
			lock.Close()
			lease.Close()
			return nil, fmt.Errorf("write launcher ownership: %w", err)
		}
		if err := lock.Close(); err != nil {
			lease.Close()
			return nil, fmt.Errorf("close launcher ownership: %w", err)
		}
		_ = os.Remove(lease.rendezvousPath)
		return lease, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("acquire launcher ownership: %w", err)
	}

	currentOwner, err := readOwnership(lease.lockPath)
	if err != nil {
		return nil, err
	}
	if !processAlive(currentOwner.PID) {
		if err := removeStaleOwnership(lease.lockPath, lease.rendezvousPath, currentOwner); err != nil {
			return nil, err
		}
		return Acquire(dataDir)
	}

	deadline := time.Now().Add(time.Second)
	for {
		contents, readErr := os.ReadFile(lease.rendezvousPath)
		if readErr == nil {
			var existing rendezvous
			if err := json.Unmarshal(contents, &existing); err != nil {
				return nil, fmt.Errorf("read launcher rendezvous: %w", err)
			}
			if existing.OwnerID != "" && currentOwner.ID != "" && existing.OwnerID != currentOwner.ID {
				return nil, errors.New("launcher rendezvous does not match the active owner")
			}
			if err := validateLoopbackURL(existing.URL); err != nil {
				return nil, err
			}
			lease.existingURL = existing.URL
			return lease, nil
		}
		if !errors.Is(readErr, os.ErrNotExist) {
			return nil, fmt.Errorf("read launcher rendezvous: %w", readErr)
		}
		if time.Now().After(deadline) {
			return nil, errors.New("another launcher owns the profile but has not published its rendezvous")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (lease *Lease) IsOwner() bool {
	return lease.owner
}

func (lease *Lease) ExistingURL() string {
	return lease.existingURL
}

func (lease *Lease) Publish(address string) error {
	if !lease.owner {
		return errors.New("only the active launcher can publish a rendezvous")
	}
	if err := validateLoopbackURL(address); err != nil {
		return err
	}
	contents, err := json.Marshal(rendezvous{OwnerID: lease.ownerID, URL: address})
	if err != nil {
		return err
	}
	temporary := lease.rendezvousPath + ".new"
	if err := os.WriteFile(temporary, contents, 0o600); err != nil {
		return fmt.Errorf("write launcher rendezvous: %w", err)
	}
	if err := os.Rename(temporary, lease.rendezvousPath); err != nil {
		return fmt.Errorf("publish launcher rendezvous: %w", err)
	}
	return nil
}

func (lease *Lease) Close() error {
	if !lease.owner {
		return nil
	}
	lease.owner = false
	currentOwner, err := readOwnership(lease.lockPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if currentOwner.ID != lease.ownerID || currentOwner.PID != os.Getpid() {
		return nil
	}
	var joined error
	if err := os.Remove(lease.rendezvousPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		joined = errors.Join(joined, err)
	}
	if err := os.Remove(lease.lockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		joined = errors.Join(joined, err)
	}
	return joined
}

func newOwnerID() (string, error) {
	buffer := make([]byte, 18)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("create launcher owner identity: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func readOwnership(path string) (ownership, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return ownership{}, err
	}
	var result ownership
	if err := json.Unmarshal(contents, &result); err == nil && result.PID > 0 {
		return result, nil
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(contents)))
	if err != nil || pid <= 0 {
		return ownership{}, errors.New("launcher ownership file is invalid")
	}
	return ownership{PID: pid}, nil
}

func removeStaleOwnership(lockPath, rendezvousPath string, expected ownership) error {
	current, err := readOwnership(lockPath)
	if err != nil {
		return err
	}
	if current != expected {
		return errors.New("launcher ownership changed during stale-owner recovery")
	}
	if err := os.Remove(rendezvousPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale launcher rendezvous: %w", err)
	}
	if err := os.Remove(lockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale launcher ownership: %w", err)
	}
	return nil
}

func validateLoopbackURL(address string) error {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" {
		return errors.New("launcher rendezvous must be an HTTP loopback URL")
	}
	host := parsed.Hostname()
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("launcher rendezvous must use a loopback address")
	}
	return nil
}
