package hetznerprovision_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/hetznerprovision"
)

const projectToken = "aB3dE5gH7jK9mN1pQ3sT5vW7yZ9bD1fH3jL5nP7rT9vX1zC3eG5iK7mO9qS1uW3y"

func TestRedactRemovesKnownSecretValues(t *testing.T) {
	output := "Error: provider rejected token " + projectToken + " for project 9"
	redacted := hetznerprovision.Redact(output, projectToken)
	if strings.Contains(redacted, projectToken) {
		t.Fatalf("token survived redaction: %s", redacted)
	}
	if !strings.Contains(redacted, hetznerprovision.Placeholder) {
		t.Fatalf("no placeholder in %q", redacted)
	}
	// The surrounding diagnostic must survive: a redaction that destroys the
	// message leaves an operator unable to act on it.
	if !strings.Contains(redacted, "provider rejected token") {
		t.Fatalf("redaction destroyed the diagnostic: %s", redacted)
	}
}

// The launcher does not hold every secret that can appear in output — a k3s node
// token is generated on the cluster. The pattern sweep is what covers those.
func TestRedactRemovesSecretsTheLauncherNeverHeld(t *testing.T) {
	cases := map[string]string{
		"unheld project token": "apply failed with " + projectToken,
		"private key":          "key material:\n-----BEGIN EC PRIVATE KEY-----\nMHcCAQEEIABC\n-----END EC PRIVATE KEY-----\nend",
		"authorization header": "request had Authorization: Bearer abcdef123456",
		"k3s node token":       "joining with K10bdb1f7a2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f6a7b8c9d0e1f2a3b",
	}
	for name, output := range cases {
		t.Run(name, func(t *testing.T) {
			redacted := hetznerprovision.Redact(output)
			if !strings.Contains(redacted, hetznerprovision.Placeholder) {
				t.Fatalf("nothing redacted in %q", redacted)
			}
			for _, secret := range []string{projectToken, "MHcCAQEEIABC", "abcdef123456", "K10bdb1f7a2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f6a7b8c9d0e1f2a3b"} {
				if strings.Contains(output, secret) && strings.Contains(redacted, secret) {
					t.Fatalf("%q survived redaction: %s", secret, redacted)
				}
			}
		})
	}
}

// A short or empty "secret" must not be substituted, or a stray empty value
// would replace every position in the output and destroy the diagnostic.
func TestRedactIgnoresUselesslyShortSecrets(t *testing.T) {
	output := "applying 3 resources in nbg1"
	if redacted := hetznerprovision.Redact(output, "", " ", "a"); redacted != output {
		t.Fatalf("redacted = %q, want the output unchanged", redacted)
	}
}

func TestRedactedErrorRedactsTheErrorPath(t *testing.T) {
	err := errors.New("hcloud: unauthorized for token " + projectToken)
	if message := hetznerprovision.RedactedError(err, projectToken); strings.Contains(message, projectToken) {
		t.Fatalf("token leaked through the error path: %s", message)
	}
	if hetznerprovision.RedactedError(nil) != "" {
		t.Fatal("a nil error must produce no message")
	}
}

func TestTailLinesBoundsStoredOutput(t *testing.T) {
	var builder strings.Builder
	for index := 0; index < 50; index++ {
		builder.WriteString("line\n")
	}
	tail := hetznerprovision.TailLines(builder.String(), 5)
	// Five kept lines plus the explicit truncation marker.
	if kept := len(strings.Split(tail, "\n")); kept != 6 {
		t.Fatalf("tail has %d lines, want 5 kept plus a truncation marker", kept)
	}
	if !strings.Contains(tail, "45 earlier lines omitted") {
		t.Fatalf("truncation must be explicit: %s", tail)
	}
	if short := hetznerprovision.TailLines("a\nb", 5); short != "a\nb" {
		t.Fatalf("short output must pass through unchanged, got %q", short)
	}
}
