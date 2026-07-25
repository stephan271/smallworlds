package hetznerprovision_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The renderer's other tests assert on the configuration's content. This one
// asserts it is genuinely OpenTofu: substring assertions cannot tell a valid
// configuration from a plausible-looking one, and the failure mode of a broken
// render is discovered against a real, paid project.
//
// It skips when no OpenTofu binary is present. The product never uses an ambient
// binary — the launcher resolves only pinned, digest-verified artifacts (see
// internal/tofu) — so this is a developer check, not a runtime dependency.
func TestRenderedModuleIsValidOpenTofu(t *testing.T) {
	binary, err := exec.LookPath("tofu")
	if err != nil {
		t.Skip("no OpenTofu binary on PATH; skipping the generated-HCL syntax check")
	}
	directory := t.TempDir()
	module := renderComplete(t, moduleInput())
	for name, contents := range module.Files {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// `tofu fmt -check` parses the configuration and reports any file it would
	// reformat, so it catches both a syntax error and generated output that
	// drifts from canonical formatting an operator would recognise.
	output, err := exec.Command(binary, "fmt", "-check", "-diff", directory).CombinedOutput()
	if err != nil {
		t.Fatalf("generated configuration is not valid, canonically formatted HCL:\n%s", output)
	}

	// `validate` cannot complete without downloading the provider, but it parses
	// and decodes the whole configuration first — so anything short of the
	// provider-installation error is a real defect in what we generated.
	output, err = exec.Command(binary, "-chdir="+directory, "validate").CombinedOutput()
	if err != nil && !strings.Contains(string(output), "Missing required provider") {
		t.Fatalf("generated configuration failed decoding:\n%s", output)
	}
}
