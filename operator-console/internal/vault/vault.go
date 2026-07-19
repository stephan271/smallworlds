package vault

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"filippo.io/age"
	"github.com/stephan271/smallworlds/operator-console/internal/fileprotection"
)

var ErrCredentialStoreUnavailable = errors.New("operating-system credential store unavailable")
var ErrIncorrectPassphrase = errors.New("incorrect vault passphrase")
var ErrLocked = errors.New("launcher vault is locked")
var ErrSecretNotFound = errors.New("vault secret not found")
var ErrWrappingKeyMissing = errors.New("vault wrapping key is missing")

type WrappingStore interface {
	Available(context.Context) bool
	Load(context.Context) (key []byte, found bool, err error)
	Save(context.Context, []byte) error
}

type Status struct {
	State                       string `json:"state"`
	OSCredentialStoreAvailable  bool   `json:"osCredentialStoreAvailable"`
	PassphraseFallbackAvailable bool   `json:"passphraseFallbackAvailable"`
	UnlockMethod                string `json:"unlockMethod,omitempty"`
}

type Vault struct {
	wrappingStore WrappingStore
	path          string

	mu           sync.RWMutex
	unlocked     bool
	unlockMethod string
	passphrase   []byte
	contents     contents
}

type contents struct {
	Version     int               `json:"version"`
	Credentials map[string]string `json:"credentials"`
}

func New(dataDir string, wrappingStore WrappingStore) *Vault {
	if wrappingStore == nil {
		wrappingStore = NewSystemWrappingStore()
	}
	return &Vault{
		wrappingStore: wrappingStore,
		path:          filepath.Join(dataDir, "launcher.vault.age"),
	}
}

func (vault *Vault) Status(ctx context.Context) Status {
	vault.mu.RLock()
	unlocked := vault.unlocked
	unlockMethod := vault.unlockMethod
	vault.mu.RUnlock()
	state := "locked"
	if unlocked {
		state = "unlocked"
	}
	return Status{
		State:                       state,
		OSCredentialStoreAvailable:  vault.wrappingStore.Available(ctx),
		PassphraseFallbackAvailable: true,
		UnlockMethod:                unlockMethod,
	}
}

func (vault *Vault) UnlockWithPassphrase(ctx context.Context, passphrase string) (Status, error) {
	stored, err := os.ReadFile(vault.path)
	if errors.Is(err, os.ErrNotExist) {
		initial := contents{Version: 1, Credentials: make(map[string]string)}
		if err := vault.write(passphrase, initial); err != nil {
			return Status{}, err
		}
		vault.setUnlocked(passphrase, initial)
		return vault.Status(ctx), nil
	}
	if err != nil {
		return Status{}, fmt.Errorf("read launcher vault: %w", err)
	}
	decrypted, err := decrypt(passphrase, stored)
	if err != nil {
		return Status{}, ErrIncorrectPassphrase
	}
	var contents contents
	decoder := json.NewDecoder(bytes.NewReader(decrypted))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&contents); err != nil || contents.Version != 1 || contents.Credentials == nil {
		return Status{}, ErrIncorrectPassphrase
	}
	vault.setUnlocked(passphrase, contents)
	return vault.Status(ctx), nil
}

func (vault *Vault) UnlockWithOSCredentialStore(ctx context.Context) (Status, error) {
	key, found, err := vault.wrappingStore.Load(ctx)
	if err != nil {
		return Status{}, ErrCredentialStoreUnavailable
	}
	if !found {
		if _, err := os.Stat(vault.path); err == nil {
			return Status{}, ErrWrappingKeyMissing
		} else if !errors.Is(err, os.ErrNotExist) {
			return Status{}, fmt.Errorf("inspect launcher vault: %w", err)
		}
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return Status{}, fmt.Errorf("generate vault wrapping key: %w", err)
		}
		if err := vault.wrappingStore.Save(ctx, key); err != nil {
			return Status{}, ErrCredentialStoreUnavailable
		}
	}
	passphrase := base64.RawStdEncoding.EncodeToString(key)
	status, err := vault.UnlockWithPassphrase(ctx, passphrase)
	if err != nil {
		return Status{}, err
	}
	vault.mu.Lock()
	vault.unlockMethod = "operating-system"
	vault.mu.Unlock()
	status.UnlockMethod = "operating-system"
	return status, nil
}

func (vault *Vault) setUnlocked(passphrase string, contents contents) {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	vault.unlocked = true
	vault.unlockMethod = "passphrase"
	vault.passphrase = append(vault.passphrase[:0], passphrase...)
	vault.contents = contents
}

func (vault *Vault) Store(key, value string) error {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	if !vault.unlocked {
		return ErrLocked
	}
	updated := contents{Version: vault.contents.Version, Credentials: make(map[string]string, len(vault.contents.Credentials)+1)}
	for existingKey, existingValue := range vault.contents.Credentials {
		updated.Credentials[existingKey] = existingValue
	}
	updated.Credentials[key] = value
	if err := vault.write(string(vault.passphrase), updated); err != nil {
		return err
	}
	vault.contents = updated
	return nil
}

func (vault *Vault) Delete(key string) error {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	if !vault.unlocked {
		return ErrLocked
	}
	if _, ok := vault.contents.Credentials[key]; !ok {
		return ErrSecretNotFound
	}
	updated := contents{Version: vault.contents.Version, Credentials: make(map[string]string, len(vault.contents.Credentials)-1)}
	for existingKey, existingValue := range vault.contents.Credentials {
		if existingKey != key {
			updated.Credentials[existingKey] = existingValue
		}
	}
	if err := vault.write(string(vault.passphrase), updated); err != nil {
		return err
	}
	vault.contents = updated
	return nil
}

func (vault *Vault) Contains(key string) (bool, error) {
	vault.mu.RLock()
	defer vault.mu.RUnlock()
	if !vault.unlocked {
		return false, ErrLocked
	}
	_, present := vault.contents.Credentials[key]
	return present, nil
}

func (vault *Vault) Lock() {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	for index := range vault.passphrase {
		vault.passphrase[index] = 0
	}
	vault.passphrase = nil
	vault.contents = contents{}
	vault.unlocked = false
	vault.unlockMethod = ""
}

func (vault *Vault) write(passphrase string, contents contents) error {
	plaintext, err := json.Marshal(contents)
	if err != nil {
		return fmt.Errorf("encode launcher vault: %w", err)
	}
	recipient, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return fmt.Errorf("prepare launcher vault encryption: %w", err)
	}
	var encrypted bytes.Buffer
	writer, err := age.Encrypt(&encrypted, recipient)
	if err != nil {
		return fmt.Errorf("encrypt launcher vault: %w", err)
	}
	if _, err := writer.Write(plaintext); err != nil {
		return fmt.Errorf("write encrypted launcher vault: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finish encrypted launcher vault: %w", err)
	}
	if err := fileprotection.WriteFileAtomically(vault.path, encrypted.Bytes()); err != nil {
		return fmt.Errorf("persist launcher vault: %w", err)
	}
	return nil
}

func decrypt(passphrase string, encrypted []byte) ([]byte, error) {
	identity, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, err
	}
	reader, err := age.Decrypt(bytes.NewReader(encrypted), identity)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(reader, 8*1024*1024))
}
