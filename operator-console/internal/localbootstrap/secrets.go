package localbootstrap

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// ValidateSecretsManifest accepts one or more Kubernetes Secret documents and
// rejects every other resource kind. The payload is retained only in the Vault
// and is never rendered into a GitOps overlay or activity record.
func ValidateSecretsManifest(manifest string) error {
	decoder := yaml.NewDecoder(strings.NewReader(manifest))
	documents := 0
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
		if metadata.APIVersion != "v1" || metadata.Kind != "Secret" {
			return fmt.Errorf("cluster secrets manifest document %d must be a v1 Secret", documents)
		}
	}
	if documents == 0 {
		return fmt.Errorf("cluster secrets manifest is empty")
	}
	return nil
}
