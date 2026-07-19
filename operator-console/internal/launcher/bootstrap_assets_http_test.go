package launcher_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/bootstrapassets"
	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
)

type assetFetcherStub struct{ contents []byte }

func (stub assetFetcherStub) Fetch(_ context.Context, _ string, offset int64) (io.ReadCloser, int, error) {
	if offset > int64(len(stub.contents)) {
		return nil, http.StatusRequestedRangeNotSatisfiable, fmt.Errorf("range beyond test asset")
	}
	status := http.StatusOK
	if offset > 0 {
		status = http.StatusPartialContent
	}
	return io.NopCloser(strings.NewReader(string(stub.contents[offset:]))), status, nil
}

func TestBootstrapAssetJourneyReturnsOnlySafeRequirementsAndCacheEvidence(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	contents := []byte("signed asset")
	digest := sha256.Sum256(contents)
	digestText := fmt.Sprintf("%x", digest[:])
	descriptor := bootstrapassets.Descriptor{ID: "bootstrap-contract", Release: "v1.2.3", URL: "https://assets.example.invalid/bootstrap-contract.tar.gz", SHA256: digestText, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(digestText))), PublicKey: publicKey, Destination: "assets.example.invalid"}
	assets, err := bootstrapassets.NewManager(t.TempDir(), bootstrapassets.Catalog{Descriptors: []bootstrapassets.Descriptor{descriptor}}, assetFetcherStub{contents: contents})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "assets-launch", BootstrapAssets: assets})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "assets-launch")
	response := request(t, handler, http.MethodGet, "/api/v1/bootstrap-assets?release=v1.2.3", nil, cookie, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("requirements status = %d", response.StatusCode)
	}
	if body := readAll(t, response); bytes.Contains(body, []byte(descriptor.URL)) || !bytes.Contains(body, []byte(`"state":"missing"`)) || !bytes.Contains(body, []byte(`"offlineBundleAvailability":"future"`)) {
		t.Fatalf("unsafe or incomplete requirements response: %s", body)
	}
	body, _ := json.Marshal(map[string]string{"release": "v1.2.3"})
	response = request(t, handler, http.MethodPost, "/api/v1/bootstrap-assets/acquire", body, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("acquire status = %d", response.StatusCode)
	}
	if body := readAll(t, response); bytes.Contains(body, []byte(descriptor.URL)) || !bytes.Contains(body, []byte(`"state":"ready"`)) {
		t.Fatalf("unsafe or incomplete acquire response: %s", body)
	}
}

func TestBootstrapAssetJourneyRejectsUnknownReleases(t *testing.T) {
	assets, err := bootstrapassets.NewManager(t.TempDir(), bootstrapassets.Catalog{}, assetFetcherStub{})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := launcher.New(launcher.Config{DataDir: t.TempDir(), LaunchToken: "unknown-assets", BootstrapAssets: assets})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	cookie, csrf := exchange(t, handler, "unknown-assets")
	response := request(t, handler, http.MethodGet, "/api/v1/bootstrap-assets?release=v9.9.9", nil, cookie, nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("unknown status = %d", response.StatusCode)
	}
	response.Body.Close()
	body, _ := json.Marshal(map[string]string{"release": "v9.9.9"})
	response = request(t, handler, http.MethodPost, "/api/v1/bootstrap-assets/acquire", body, cookie, map[string]string{"X-CSRF-Token": csrf})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("unknown acquire status = %d", response.StatusCode)
	}
	response.Body.Close()
}
