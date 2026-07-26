package bootstrapassets

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
)

// ManifestName is the signed index every SmallWorlds release publishes beside its
// bootstrap archive. Fetching it is what lets a launcher install a release that
// did not exist when the launcher itself was built, without ever trusting a
// digest it learned from the network on that digest's own word.
const ManifestName = "bootstrap-assets.manifest.json"

const manifestFormat = "smallworlds-bootstrap-assets/v1"
const manifestURLTemplate = "https://github.com/stephan271/smallworlds/releases/download/%s/" + ManifestName

// manifestSizeLimit bounds the read so a hostile or corrupt response cannot
// exhaust memory before its signature is ever considered.
const manifestSizeLimit = 64 * 1024

var ErrUntrustedManifest = errors.New("bootstrap asset manifest is not signed by the trusted release key")
var ErrInvalidRelease = errors.New("release identifier is invalid")

var safeRelease = regexp.MustCompile(`^v[0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,6}(-[0-9A-Za-z.]{1,32})?$`)

// ValidateRelease accepts the shape of a SmallWorlds release tag. It is a shape
// check only: whether a release exists, and whether this launcher may install
// it, is settled by its manifest's signature, never by its name.
func ValidateRelease(release string) error {
	if !safeRelease.MatchString(release) {
		return ErrInvalidRelease
	}
	return nil
}

type manifestDocument struct {
	Format  string `json:"format"`
	Release string `json:"release"`
	Assets  []struct {
		ID          string `json:"id"`
		URL         string `json:"url"`
		SHA256      string `json:"sha256"`
		Signature   string `json:"signature"`
		Destination string `json:"destination"`
	} `json:"assets"`
	SigningPublicKey string `json:"signingPublicKey"`
}

// DescriptorsFromManifest turns a published manifest into descriptors that carry
// the key compiled into this launcher, then holds them to the same validation as
// a compiled descriptor.
//
// The manifest states its own signing key. That key is deliberately not trusted
// as an authority — it is only required to equal the compiled one. Trusting it
// would let anyone able to attach a file to a release nominate the key that
// vouches for that file, which is no protection at all.
func DescriptorsFromManifest(contents []byte, release string, trusted ed25519.PublicKey) ([]Descriptor, error) {
	if len(trusted) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: no compiled release signing key", ErrUntrustedManifest)
	}
	var document manifestDocument
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("%w: decode manifest: %v", ErrInvalidDescriptor, err)
	}
	if document.Format != manifestFormat {
		return nil, fmt.Errorf("%w: unsupported manifest format %q", ErrInvalidDescriptor, document.Format)
	}
	// A manifest that names a different release than the one asked for would let
	// an older signed archive be served in a newer release's place.
	if document.Release != release {
		return nil, fmt.Errorf("%w: manifest describes release %q", ErrInvalidDescriptor, document.Release)
	}
	announced, err := base64.StdEncoding.DecodeString(document.SigningPublicKey)
	if err != nil || !bytes.Equal(announced, trusted) {
		return nil, fmt.Errorf("%w: manifest announces a different signing key", ErrUntrustedManifest)
	}
	if len(document.Assets) == 0 {
		return nil, ErrUnknownRelease
	}
	descriptors := make([]Descriptor, 0, len(document.Assets))
	for _, asset := range document.Assets {
		descriptor := Descriptor{
			ID:          asset.ID,
			Release:     release,
			URL:         asset.URL,
			SHA256:      asset.SHA256,
			Signature:   asset.Signature,
			PublicKey:   trusted,
			Destination: asset.Destination,
		}
		if err := descriptor.Validate(); err != nil {
			return nil, err
		}
		descriptors = append(descriptors, descriptor)
	}
	return descriptors, nil
}

// fetchManifest reads the manifest a release publishes. The body is bounded and
// nothing is written to the cache: this is evidence gathering, not acquisition.
func (manager *Manager) fetchManifest(ctx context.Context, release string) ([]byte, error) {
	body, _, err := manager.fetcher.Fetch(ctx, fmt.Sprintf(manifestURLTemplate, release), 0)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnknownRelease, err)
	}
	defer body.Close()
	contents, err := io.ReadAll(io.LimitReader(body, manifestSizeLimit+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read manifest: %v", ErrUnknownRelease, err)
	}
	if len(contents) > manifestSizeLimit {
		return nil, fmt.Errorf("%w: manifest is implausibly large", ErrInvalidDescriptor)
	}
	return contents, nil
}
