package bootstrapassets_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/bootstrapassets"
)

func TestDefaultCatalogPinsTheReleaseSigningPublicKey(t *testing.T) {
	catalog := bootstrapassets.DefaultCatalog()
	if len(catalog.TrustedPublicKey) != ed25519.PublicKeySize {
		t.Fatalf("trusted public key length = %d, want %d", len(catalog.TrustedPublicKey), ed25519.PublicKeySize)
	}
	if encoded := base64.StdEncoding.EncodeToString(catalog.TrustedPublicKey); encoded != "eQCLQJVXRoXY1nSSKuhRsDMoLBh2EjkGo9GVe6vLP/0=" {
		t.Fatalf("unexpected compiled release signing public key: %q", encoded)
	}
	descriptors, err := catalog.Resolve("v1.2.25")
	if err != nil {
		t.Fatalf("default catalog did not resolve v1.2.25: %v", err)
	}
	if len(descriptors) != 1 || descriptors[0].SHA256 != "e07843ffb73227c6f1d9b70ed0aa4cd7e7c6e07f0b06a25bf8d04ffd5d7f2b38" {
		t.Fatalf("unexpected v1.2.25 descriptor: %#v", descriptors)
	}
	descriptors, err = catalog.Resolve("v1.2.26")
	if err != nil {
		t.Fatalf("default catalog did not resolve v1.2.26: %v", err)
	}
	if len(descriptors) != 1 || descriptors[0].SHA256 != "732e1a19bc31ecab367ddedc242599516625ac706ef719bccf5578bca05c0e99" {
		t.Fatalf("unexpected v1.2.26 descriptor: %#v", descriptors)
	}
}

func TestCatalogRejectsDescriptorSignedByAnotherKey(t *testing.T) {
	trustedPublicKey, trustedPrivateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	otherPublicKey, otherPrivateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("a", 64)
	descriptor := bootstrapassets.Descriptor{
		ID:          "bootstrap-linux-amd64",
		Release:     "v1.2.25",
		URL:         "https://github.com/stephan271/smallworlds/releases/download/v1.2.25/smallworlds-bootstrap-v1.2.25-linux-amd64.tar.gz",
		SHA256:      digest,
		Signature:   base64.StdEncoding.EncodeToString(ed25519.Sign(otherPrivateKey, []byte(digest))),
		PublicKey:   otherPublicKey,
		Destination: "github.com",
	}
	catalog := bootstrapassets.Catalog{TrustedPublicKey: trustedPublicKey, Descriptors: []bootstrapassets.Descriptor{descriptor}}
	if _, err := catalog.Resolve("v1.2.25"); err == nil || !strings.Contains(err.Error(), "unexpected release signing key") {
		t.Fatalf("expected unexpected signing key rejection, got %v", err)
	}

	descriptor.PublicKey = trustedPublicKey
	descriptor.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(trustedPrivateKey, []byte(digest)))
	catalog.Descriptors = []bootstrapassets.Descriptor{descriptor}
	if _, err := catalog.Resolve("v1.2.25"); err != nil {
		t.Fatalf("expected descriptor signed by the compiled key to resolve, got %v", err)
	}
}
