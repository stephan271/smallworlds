package overlayadoption_test

import (
	"strings"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/overlayadoption"
)

// A branch or a tag can be moved under a running cluster afterwards, which is
// the whole reason the root Application is pinned to a commit in the first
// place. Adopting must not be the place that quietly gives that up.
func TestOnlyAFullCommitMayBeAdopted(t *testing.T) {
	good := "384e473f6259f46df0fe25b1da24f289b5fd6769"
	if err := overlayadoption.ValidateRevision(good); err != nil {
		t.Fatalf("a full commit was refused: %v", err)
	}
	for _, bad := range []string{
		"", "HEAD", "main", "v1.2.30",
		"384e473", // short
		"384e473f6259f46df0fe25b1da24f289b5fd676",  // 39 characters
		"384e473f6259f46df0fe25b1da24f289b5fd676Z", // not hex
		"384e473f6259f46df0fe25b1da24f289b5fd6769; rm -rf /",
	} {
		if err := overlayadoption.ValidateRevision(bad); err == nil {
			t.Errorf("%q was accepted as a reviewed commit", bad)
		}
		if _, err := overlayadoption.PatchCommand(bad); err == nil {
			t.Errorf("a command was rendered for %q", bad)
		}
	}
}

func TestThePatchMovesOneFieldOfTheRootApplication(t *testing.T) {
	revision := "384e473f6259f46df0fe25b1da24f289b5fd6769"
	command, err := overlayadoption.PatchCommand(revision)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"--type merge",                    // never a replacement
		"-n " + overlayadoption.Namespace, //
		overlayadoption.RootApplication,
		revision,
	} {
		if !strings.Contains(command, required) {
			t.Errorf("command is missing %q: %s", required, command)
		}
	}
	// The installation's own identity is not an update's business.
	for _, forbidden := range []string{"repoURL", "syncPolicy", "project", "--type=json", "replace"} {
		if strings.Contains(command, forbidden) {
			t.Errorf("command touches %q, which an adoption must leave alone: %s", forbidden, command)
		}
	}
}
