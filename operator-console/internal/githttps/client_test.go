package githttps_test

import (
	"errors"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/githttps"
)

func TestValidateRemoteURLRejectsSSHAndEmbeddedCredentials(t *testing.T) {
	for _, raw := range []string{"git@github.com:owner/repo.git", "ssh://git@example/repo.git", "https://token@example/repo.git", "https://git.example/repo.git?token=not-allowed"} {
		if _, err := githttps.ValidateRemoteURL(raw); !errors.Is(err, githttps.ErrUnsupportedRemote) {
			t.Fatalf("%q error = %v, want unsupported remote", raw, err)
		}
	}
	if _, err := githttps.ValidateRemoteURL("https://git.example/owner/repo.git"); err != nil {
		t.Fatal(err)
	}
}
