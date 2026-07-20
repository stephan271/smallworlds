package localbootstrap_test

import (
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/localbootstrap"
)

func TestValidateSecretsManifestAcceptsOnlyKubernetesSecrets(t *testing.T) {
	valid := "apiVersion: v1\nkind: Secret\nmetadata:\n  name: first\nstringData:\n  token: value\n---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: second\n"
	if err := localbootstrap.ValidateSecretsManifest(valid); err != nil {
		t.Fatalf("valid secrets rejected: %v", err)
	}
	for name, manifest := range map[string]string{
		"comment spoof": "apiVersion: v1\nkind: ConfigMap\n# kind: Secret\n",
		"workload":      "apiVersion: apps/v1\nkind: Deployment\n",
		"malformed":     "apiVersion: [\nkind: Secret\n",
		"empty":         "---\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := localbootstrap.ValidateSecretsManifest(manifest); err == nil {
				t.Fatal("unsafe manifest accepted")
			}
		})
	}
}
