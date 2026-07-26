package bootstrapassets_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/bootstrapassets"
)

// signedManifest builds what admin-tools/publish-bootstrap-assets.sh attaches to
// a release: an ed25519 signature over the ASCII hex SHA-256 of the archive.
func signedManifest(t *testing.T, release string, signing ed25519.PrivateKey, announced ed25519.PublicKey) []byte {
	t.Helper()
	digest := sha256.Sum256([]byte("archive-" + release))
	text := hex.EncodeToString(digest[:])
	manifest := map[string]any{
		"format":  "smallworlds-bootstrap-assets/v1",
		"release": release,
		"assets": []map[string]string{{
			"id":          "bootstrap-linux-amd64",
			"url":         "https://github.com/stephan271/smallworlds/releases/download/" + release + "/smallworlds-bootstrap-" + release + "-linux-amd64.tar.gz",
			"sha256":      text,
			"signature":   base64.StdEncoding.EncodeToString(ed25519.Sign(signing, []byte(text))),
			"destination": "github.com",
		}},
		"signingPublicKey": base64.StdEncoding.EncodeToString(announced),
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestManifestIsAcceptedOnlyOnTheCompiledSigningKeysWord(t *testing.T) {
	trustedPublic, trustedPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	attackerPublic, attackerPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	descriptors, err := bootstrapassets.DescriptorsFromManifest(signedManifest(t, "v9.9.9", trustedPrivate, trustedPublic), "v9.9.9", trustedPublic)
	if err != nil || len(descriptors) != 1 || descriptors[0].Release != "v9.9.9" {
		t.Fatalf("trusted manifest rejected: descriptors=%#v err=%v", descriptors, err)
	}

	for name, testcase := range map[string]struct {
		contents []byte
		release  string
		want     error
	}{
		// The decisive case: a manifest signed by someone else, announcing their
		// own key. Believing the announcement would make the signature worthless,
		// since anyone able to attach a file to a release could supply both.
		"attacker key and signature": {signedManifest(t, "v9.9.9", attackerPrivate, attackerPublic), "v9.9.9", bootstrapassets.ErrUntrustedManifest},
		// Signed by the attacker but announcing the trusted key: the announcement
		// matches, so only verifying the signature itself catches this.
		"attacker signature under trusted key": {signedManifest(t, "v9.9.9", attackerPrivate, trustedPublic), "v9.9.9", bootstrapassets.ErrInvalidDescriptor},
		// A validly signed older manifest served in a newer release's place.
		"manifest for another release": {signedManifest(t, "v9.9.8", trustedPrivate, trustedPublic), "v9.9.9", bootstrapassets.ErrInvalidDescriptor},
		"unsupported format":           {[]byte(`{"format":"something/v2","release":"v9.9.9","assets":[],"signingPublicKey":""}`), "v9.9.9", bootstrapassets.ErrInvalidDescriptor},
		"malformed":                    {[]byte(`{`), "v9.9.9", bootstrapassets.ErrInvalidDescriptor},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := bootstrapassets.DescriptorsFromManifest(testcase.contents, testcase.release, trustedPublic); !errors.Is(err, testcase.want) {
				t.Fatalf("error = %v, want %v", err, testcase.want)
			}
		})
	}
}

func TestManifestAssetURLMustStayAnHTTPSReleaseURL(t *testing.T) {
	trustedPublic, trustedPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// A signature covers the digest, not the URL, so a tampered URL has to be
	// refused on its own terms. The digest check at download time would catch a
	// substituted archive, but the request must not be made at all.
	tampered := strings.Replace(string(signedManifest(t, "v9.9.9", trustedPrivate, trustedPublic)), "https://github.com", "http://evil.example", 1)
	if _, err := bootstrapassets.DescriptorsFromManifest([]byte(tampered), "v9.9.9", trustedPublic); !errors.Is(err, bootstrapassets.ErrInvalidDescriptor) {
		t.Fatalf("plaintext asset URL accepted: %v", err)
	}
}

// TestReleaseNotInTheCompiledCatalogIsResolvedFromItsManifest is the behaviour
// the operator asked for: installing a release newer than the launcher build.
func TestReleaseNotInTheCompiledCatalogIsResolvedFromItsManifest(t *testing.T) {
	trustedPublic, trustedPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifestURL := "https://github.com/stephan271/smallworlds/releases/download/v9.9.9/" + bootstrapassets.ManifestName
	fetcher := &memoryFetcher{contents: map[string][]byte{manifestURL: signedManifest(t, "v9.9.9", trustedPrivate, trustedPublic)}}
	// An empty compiled catalog stands for a launcher built before this release
	// existed. Only the trusted key is compiled in.
	manager, err := bootstrapassets.NewManager(t.TempDir(), bootstrapassets.Catalog{TrustedPublicKey: trustedPublic}, fetcher)
	if err != nil {
		t.Fatal(err)
	}
	statuses, err := manager.Requirements(t.Context(), "v9.9.9")
	if err != nil {
		t.Fatalf("release outside the compiled catalog was refused: %v", err)
	}
	if len(statuses) != 1 || statuses[0].State != bootstrapassets.StateMissing || statuses[0].Release != "v9.9.9" {
		t.Fatalf("statuses = %#v", statuses)
	}
	// The manifest is verified once and reused; a journey must not refetch and
	// reverify it at every step.
	if _, err := manager.Requirements(t.Context(), "v9.9.9"); err != nil {
		t.Fatal(err)
	}
	if len(fetcher.calls) != 1 {
		t.Fatalf("manifest fetched %d times", len(fetcher.calls))
	}
	// An identifier that is not a release tag is never fetched for: toolchain
	// artifacts share this catalog and publish no manifest, so unknown has to
	// stay unknown rather than turn into a request.
	if _, err := manager.Requirements(t.Context(), "not-a-version"); !errors.Is(err, bootstrapassets.ErrUnknownRelease) {
		t.Fatalf("malformed release accepted: %v", err)
	}
	if len(fetcher.calls) != 1 {
		t.Fatalf("a non-release identifier triggered a fetch: %d calls", len(fetcher.calls))
	}
}
