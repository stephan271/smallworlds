package buildinfo

import "testing"

func TestDevelopmentBuildHasAnExplicitVersion(t *testing.T) {
	if Version == "" {
		t.Fatal("development build version must not be empty")
	}
}
