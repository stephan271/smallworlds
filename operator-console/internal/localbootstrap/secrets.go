package localbootstrap

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// ValidateSecretsManifest accepts Kubernetes Secret documents, the bare
// Namespaces they need, and the ConfigMap the setup jobs read before anything
// in Git is reconciled. Every other resource kind is rejected: durable cluster
// configuration belongs in the GitOps overlay, not in a field that bypasses it.
// The three exceptions all share one reason — they are consumed before Argo CD
// exists, so nothing in Git could have delivered them in time. The payload is
// retained only in the Vault and is never rendered into a GitOps overlay or
// activity record.
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
		if metadata.APIVersion != "v1" || metadata.Kind != "Secret" && metadata.Kind != "Namespace" && metadata.Kind != "ConfigMap" {
			return fmt.Errorf("cluster secrets manifest document %d must be a v1 Secret, Namespace or ConfigMap", documents)
		}
		if metadata.Kind == "Secret" {
			secrets++
		}
	}
	// Namespaces and ConfigMaps alone are not Cluster Secrets: they are allowed
	// to accompany the Secrets, never to stand in for them.
	if secrets == 0 {
		return fmt.Errorf("cluster secrets manifest is empty")
	}
	return nil
}
