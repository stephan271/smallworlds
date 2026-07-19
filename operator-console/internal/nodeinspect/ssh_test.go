package nodeinspect_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"errors"
	"net"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/nodeinspect"
	"golang.org/x/crypto/ssh"
)

func TestPinnedHostKeyCallbackRequiresConfirmationAndRejectsMismatch(t *testing.T) {
	first := testPublicKey(t)
	second := testPublicKey(t)
	if err := nodeinspect.PinnedHostKeyCallback("")("node", &net.TCPAddr{}, first); !errors.Is(err, nodeinspect.ErrHostKeyUntrusted) {
		t.Fatalf("untrusted error = %v", err)
	}
	if err := nodeinspect.PinnedHostKeyCallback(nodeinspect.HostKeyFingerprint(first))("node", &net.TCPAddr{}, second); !errors.Is(err, nodeinspect.ErrHostKeyMismatch) {
		t.Fatalf("mismatch error = %v", err)
	}
	if err := nodeinspect.PinnedHostKeyCallback(nodeinspect.HostKeyFingerprint(first))("node", &net.TCPAddr{}, first); err != nil {
		t.Fatalf("pinned key rejected: %v", err)
	}
}

func TestPasswordAuthenticationNeverAcceptsAnEmptySecret(t *testing.T) {
	if _, err := (nodeinspect.Credentials{Kind: nodeinspect.PasswordAuthentication}).AuthMethod(); !errors.Is(err, nodeinspect.ErrAuthenticationUnavailable) {
		t.Fatalf("empty password error = %v", err)
	}
	if _, err := (nodeinspect.Credentials{Kind: nodeinspect.PasswordAuthentication, Password: "write-only-secret"}).AuthMethod(); err != nil {
		t.Fatal(err)
	}
}

func TestPassphraseProtectedPrivateKeyAuthenticationParsesOnlyWithTheProvidedPassphrase(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := ssh.MarshalPrivateKeyWithPassphrase(privateKey, "node-key", []byte("key-passphrase"))
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(encoded)
	if _, err := (nodeinspect.Credentials{Kind: nodeinspect.PrivateKeyAuthentication, PrivateKey: pemBytes, KeyPassphrase: "key-passphrase"}).AuthMethod(); err != nil {
		t.Fatal(err)
	}
	if _, err := (nodeinspect.Credentials{Kind: nodeinspect.PrivateKeyAuthentication, PrivateKey: pemBytes, KeyPassphrase: "wrong"}).AuthMethod(); err == nil {
		t.Fatal("wrong private-key passphrase accepted")
	}
}

func testPublicKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return publicKey
}
