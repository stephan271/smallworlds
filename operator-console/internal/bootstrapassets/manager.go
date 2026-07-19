// Package bootstrapassets provides the Launcher-owned boundary for acquiring
// signed bootstrap dependencies. It deliberately resolves only descriptors
// compiled into a trusted catalog; callers cannot supply a URL or executable.
package bootstrapassets

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/stephan271/smallworlds/operator-console/internal/fileprotection"
)

var ErrUnknownRelease = errors.New("no compatible bootstrap assets for release")
var ErrInvalidDescriptor = errors.New("bootstrap asset descriptor is invalid")
var ErrIntegrity = errors.New("bootstrap asset integrity verification failed")

var safeID = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,119}$`)
var sha256Text = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Descriptor struct {
	ID          string
	Release     string
	URL         string
	SHA256      string
	Signature   string
	PublicKey   ed25519.PublicKey
	Destination string
}

type Catalog struct {
	Descriptors []Descriptor
}

func (catalog Catalog) Resolve(release string) ([]Descriptor, error) {
	descriptors := make([]Descriptor, 0)
	for _, descriptor := range catalog.Descriptors {
		if descriptor.Release == release {
			descriptors = append(descriptors, descriptor)
		}
	}
	if len(descriptors) == 0 {
		return nil, ErrUnknownRelease
	}
	sort.Slice(descriptors, func(left, right int) bool { return descriptors[left].ID < descriptors[right].ID })
	for _, descriptor := range descriptors {
		if err := descriptor.Validate(); err != nil {
			return nil, err
		}
	}
	return descriptors, nil
}

func (descriptor Descriptor) Validate() error {
	parsed, err := url.Parse(descriptor.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || !safeID.MatchString(descriptor.ID) || !sha256Text.MatchString(descriptor.SHA256) || len(descriptor.PublicKey) != ed25519.PublicKeySize || descriptor.Destination == "" {
		return ErrInvalidDescriptor
	}
	signature, err := base64.StdEncoding.DecodeString(descriptor.Signature)
	if err != nil || !ed25519.Verify(descriptor.PublicKey, []byte(descriptor.SHA256), signature) {
		return fmt.Errorf("%w: signature", ErrInvalidDescriptor)
	}
	return nil
}

// Fetcher makes resumable HTTP behavior independently contract-testable.
type Fetcher interface {
	Fetch(context.Context, string, int64) (body io.ReadCloser, statusCode int, err error)
}

type HTTPFetcher struct {
	Client *http.Client
}

func (fetcher HTTPFetcher) Fetch(ctx context.Context, rawURL string, offset int64) (io.ReadCloser, int, error) {
	client := fetcher.Client
	if client == nil {
		client = http.DefaultClient
	}
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, err
	}
	if offset > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	response, err := copy.Do(request)
	if err != nil {
		return nil, 0, err
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		response.Body.Close()
		return nil, response.StatusCode, fmt.Errorf("asset download returned HTTP %d", response.StatusCode)
	}
	return response.Body, response.StatusCode, nil
}

type State string

const (
	StateMissing State = "missing"
	StatePartial State = "partial"
	StateReady   State = "ready"
)

// Status is safe to return over the Launcher API: it contains identity and
// integrity evidence, never cache paths, URLs containing authority, or data.
type Status struct {
	ID          string `json:"id"`
	Release     string `json:"release"`
	Destination string `json:"destination"`
	SHA256      string `json:"sha256"`
	State       State  `json:"state"`
	Bytes       int64  `json:"bytes"`
}

type Manager struct {
	cacheDirectory string
	catalog        Catalog
	fetcher        Fetcher
}

func NewManager(dataDirectory string, catalog Catalog, fetcher Fetcher) (*Manager, error) {
	cacheDirectory := filepath.Join(dataDirectory, "bootstrap-assets")
	if err := fileprotection.SecureDirectory(cacheDirectory); err != nil {
		return nil, fmt.Errorf("create bootstrap asset cache: %w", err)
	}
	if fetcher == nil {
		fetcher = HTTPFetcher{}
	}
	return &Manager{cacheDirectory: cacheDirectory, catalog: catalog, fetcher: fetcher}, nil
}

// DefaultCatalog intentionally contains no remotely fetchable artifacts until
// release engineering publishes a signed descriptor. This preserves the closed
// source boundary rather than silently falling back to ambient PATH or URLs.
func DefaultCatalog() Catalog { return Catalog{} }

func (manager *Manager) Requirements(release string) ([]Status, error) {
	descriptors, err := manager.catalog.Resolve(release)
	if err != nil {
		return nil, err
	}
	statuses := make([]Status, 0, len(descriptors))
	for _, descriptor := range descriptors {
		status, err := manager.status(descriptor)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func (manager *Manager) Acquire(ctx context.Context, release string) ([]Status, error) {
	descriptors, err := manager.catalog.Resolve(release)
	if err != nil {
		return nil, err
	}
	statuses := make([]Status, 0, len(descriptors))
	for _, descriptor := range descriptors {
		status, err := manager.acquire(ctx, descriptor)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func (manager *Manager) status(descriptor Descriptor) (Status, error) {
	finalPath := manager.finalPath(descriptor)
	if info, err := os.Stat(finalPath); err == nil {
		valid, verifyErr := matchesDigest(finalPath, descriptor.SHA256)
		if verifyErr != nil {
			return Status{}, verifyErr
		}
		if valid {
			return safeStatus(descriptor, StateReady, info.Size()), nil
		}
		return Status{}, fmt.Errorf("%w: cached asset %s", ErrIntegrity, descriptor.ID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Status{}, err
	}
	if info, err := os.Stat(manager.partialPath(descriptor)); err == nil {
		return safeStatus(descriptor, StatePartial, info.Size()), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Status{}, err
	}
	return safeStatus(descriptor, StateMissing, 0), nil
}

func (manager *Manager) acquire(ctx context.Context, descriptor Descriptor) (Status, error) {
	status, err := manager.status(descriptor)
	if err != nil || status.State == StateReady {
		return status, err
	}
	partialPath := manager.partialPath(descriptor)
	offset := status.Bytes
	body, statusCode, err := manager.fetcher.Fetch(ctx, descriptor.URL, offset)
	if err != nil {
		return Status{}, fmt.Errorf("download bootstrap asset %s: %w", descriptor.ID, err)
	}
	defer body.Close()
	if offset > 0 && statusCode != http.StatusPartialContent {
		if err := os.Truncate(partialPath, 0); err != nil {
			return Status{}, fmt.Errorf("reset unresumable asset partial: %w", err)
		}
		offset = 0
	}
	file, err := os.OpenFile(partialPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return Status{}, fmt.Errorf("open bootstrap asset partial: %w", err)
	}
	if err := fileprotection.SecureFile(partialPath); err != nil {
		file.Close()
		return Status{}, err
	}
	if _, err := io.Copy(file, body); err != nil {
		file.Close()
		return Status{}, fmt.Errorf("write bootstrap asset partial: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return Status{}, err
	}
	if err := file.Close(); err != nil {
		return Status{}, err
	}
	valid, err := matchesDigest(partialPath, descriptor.SHA256)
	if err != nil {
		return Status{}, err
	}
	if !valid {
		return Status{}, fmt.Errorf("%w: %s", ErrIntegrity, descriptor.ID)
	}
	if err := os.Rename(partialPath, manager.finalPath(descriptor)); err != nil {
		return Status{}, fmt.Errorf("commit verified bootstrap asset: %w", err)
	}
	if err := fileprotection.SecureFile(manager.finalPath(descriptor)); err != nil {
		return Status{}, err
	}
	info, err := os.Stat(manager.finalPath(descriptor))
	if err != nil {
		return Status{}, err
	}
	return safeStatus(descriptor, StateReady, info.Size()), nil
}

func (manager *Manager) finalPath(descriptor Descriptor) string {
	return filepath.Join(manager.cacheDirectory, descriptor.ID+"-"+descriptor.Release+".asset")
}

func (manager *Manager) partialPath(descriptor Descriptor) string {
	return manager.finalPath(descriptor) + ".partial"
}

func safeStatus(descriptor Descriptor, state State, bytes int64) Status {
	return Status{ID: descriptor.ID, Release: descriptor.Release, Destination: descriptor.Destination, SHA256: descriptor.SHA256, State: state, Bytes: bytes}
}

func matchesDigest(path, expected string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return false, err
	}
	return fmt.Sprintf("%x", digest.Sum(nil)) == expected, nil
}

// WritePartialForTest seeds an interrupted transfer without exposing cache
// paths to callers. It is intentionally only useful to package contract tests.
func (manager *Manager) WritePartialForTest(descriptor Descriptor, contents []byte) error {
	if err := descriptor.Validate(); err != nil {
		return err
	}
	return os.WriteFile(manager.partialPath(descriptor), contents, 0600)
}
