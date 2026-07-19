package bootstrapassets_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/bootstrapassets"
)

type memoryFetcher struct {
	contents map[string][]byte
	calls    []int64
}

func (fetcher *memoryFetcher) Fetch(_ context.Context, rawURL string, offset int64) (io.ReadCloser, int, error) {
	fetcher.calls = append(fetcher.calls, offset)
	contents, ok := fetcher.contents[rawURL]
	if !ok {
		return nil, 0, fmt.Errorf("unexpected URL %s", rawURL)
	}
	if offset > int64(len(contents)) {
		return nil, 416, nil
	}
	status := 200
	if offset > 0 {
		status = 206
	}
	return io.NopCloser(strings.NewReader(string(contents[offset:]))), status, nil
}

func TestAcquireVerifiesSignedDescriptorAndResumesPrivatePartialFile(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	contents := []byte("signed bootstrap archive contents")
	digest := sha256.Sum256(contents)
	digestText := fmt.Sprintf("%x", digest[:])
	descriptor := bootstrapassets.Descriptor{
		ID:          "bootstrap-contract",
		Release:     "v1.2.3",
		URL:         "https://assets.example.invalid/bootstrap-contract-v1.2.3.tar.gz",
		SHA256:      digestText,
		Signature:   base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(digestText))),
		PublicKey:   publicKey,
		Destination: "assets.example.invalid",
	}
	fetcher := &memoryFetcher{contents: map[string][]byte{descriptor.URL: contents}}
	manager, err := bootstrapassets.NewManager(t.TempDir(), bootstrapassets.Catalog{Descriptors: []bootstrapassets.Descriptor{descriptor}}, fetcher)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.WritePartialForTest(descriptor, contents[:9]); err != nil {
		t.Fatal(err)
	}
	status, err := manager.Acquire(context.Background(), descriptor.Release)
	if err != nil {
		t.Fatal(err)
	}
	if len(status) != 1 || status[0].State != bootstrapassets.StateReady || status[0].SHA256 != digestText {
		t.Fatalf("status = %#v", status)
	}
	if len(fetcher.calls) != 1 || fetcher.calls[0] != 9 {
		t.Fatalf("resume offsets = %v, want [9]", fetcher.calls)
	}
	encoded, err := json.Marshal(status[0])
	if err != nil || strings.Contains(string(encoded), "bootstrap-assets") {
		t.Fatalf("safe status exposes local cache path: %s", encoded)
	}
}

func TestAcquireRejectsUnknownReleaseAndInvalidIntegrityBeforeCaching(t *testing.T) {
	manager, err := bootstrapassets.NewManager(t.TempDir(), bootstrapassets.Catalog{}, &memoryFetcher{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Acquire(context.Background(), "v9.9.9"); err == nil {
		t.Fatal("unknown release acquired")
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	descriptor := bootstrapassets.Descriptor{ID: "tool", Release: "v1.2.3", URL: "https://assets.example.invalid/tool", SHA256: strings.Repeat("0", 64), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(strings.Repeat("f", 64)))), PublicKey: publicKey}
	fetcher := &memoryFetcher{contents: map[string][]byte{descriptor.URL: []byte("untrusted")}}
	manager, err = bootstrapassets.NewManager(t.TempDir(), bootstrapassets.Catalog{Descriptors: []bootstrapassets.Descriptor{descriptor}}, fetcher)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Acquire(context.Background(), descriptor.Release); err == nil {
		t.Fatal("invalid signature acquired")
	}
	if len(fetcher.calls) != 0 {
		t.Fatalf("invalid signature fetched remote data: %v", fetcher.calls)
	}
}

func TestCatalogRejectsNonHTTPSAndUnsafeAssetDescriptors(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("a", 64)
	for _, descriptor := range []bootstrapassets.Descriptor{
		{ID: "tool", Release: "v1.2.3", URL: "http://assets.example.invalid/tool", SHA256: digest, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(digest))), PublicKey: publicKey, Destination: "assets.example.invalid"},
		{ID: "tool", Release: "v1.2.3", URL: "https://token@assets.example.invalid/tool", SHA256: digest, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(digest))), PublicKey: publicKey, Destination: "assets.example.invalid"},
	} {
		if err := descriptor.Validate(); err == nil {
			t.Fatalf("unsafe descriptor accepted: %#v", descriptor)
		}
	}
}
