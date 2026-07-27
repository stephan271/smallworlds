package localbootstrap

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// ValidateSecretsManifest accepts Kubernetes Secret documents and the bare
// Namespaces they need, and rejects every other resource kind. Durable cluster
// configuration belongs in the GitOps overlay, not in a field that bypasses it
// — but a Secret whose namespace does not exist yet is simply refused by the
// cluster, so forbidding the Namespace alongside it only moved the problem into
// a retry loop. The payload is retained only in the Vault and is never rendered
// into a GitOps overlay or activity record.
func ValidateSecretsManifest(manifest string) error {
	decoder := yaml.NewDecoder(strings.NewReader(manifest))
	documents := 0
	secrets := 0
	for {
		var metadata struct {
			APIVersion string `yaml:"apiVersion"`
			Kind       string `yaml:"kind"`
		}
		err := decoder.Decode(&metadata)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("decode cluster secrets manifest: %w", err)
		}
		if metadata.APIVersion == "" && metadata.Kind == "" {
			continue
		}
		documents++
		if metadata.APIVersion != "v1" || metadata.Kind != "Secret" && metadata.Kind != "Namespace" {
			return fmt.Errorf("cluster secrets manifest document %d must be a v1 Secret or Namespace", documents)
		}
		if metadata.Kind == "Secret" {
			secrets++
		}
	}
	// Namespaces alone are not Cluster Secrets: they are allowed to accompany the
	// Secrets, never to stand in for them.
	if secrets == 0 {
		return fmt.Errorf("cluster secrets manifest is empty")
	}
	return nil
}
