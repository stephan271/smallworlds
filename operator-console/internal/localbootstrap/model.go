// Package localbootstrap owns the immutable contract between an inspected
// Local Cluster Node, an approved Change Plan, and its resumable execution.
package localbootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/nodeinspect"
)

var ErrInvalidBinding = errors.New("local bootstrap plan binding is invalid")

const SupportedRelease = "v1.2.27"

var safeCommit = regexp.MustCompile(`^[a-f0-9]{40,64}$`)
var safeProfileValue = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var safeOpaqueID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var safeDomain = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
var safeDataPath = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,254}$`)

type Configuration struct {
	Domain               string `json:"domain"`
	EnvironmentExtension string `json:"environmentExtension,omitempty"`
	DataDirectory        string `json:"dataDirectory"`
	NodeName             string `json:"nodeName"`
	ACMEEmail            string `json:"acmeEmail,omitempty"`
	ManageDNS            bool   `json:"manageDns"`
}

func (configuration Configuration) Validate() error {
	if !safeDomain.MatchString(configuration.Domain) || strings.Contains(configuration.Domain, "..") {
		return fmt.Errorf("%w: domain", ErrInvalidBinding)
	}
	if configuration.EnvironmentExtension != "" && (!strings.HasPrefix(configuration.EnvironmentExtension, ".") || !safeProfileValue.MatchString(strings.TrimPrefix(configuration.EnvironmentExtension, "."))) {
		return fmt.Errorf("%w: environment extension", ErrInvalidBinding)
	}
	if !safeDataPath.MatchString(configuration.DataDirectory) || path.Clean(configuration.DataDirectory) != configuration.DataDirectory || configuration.DataDirectory == "/" {
		return fmt.Errorf("%w: data directory", ErrInvalidBinding)
	}
	if !safeProfileValue.MatchString(configuration.NodeName) {
		return fmt.Errorf("%w: node name", ErrInvalidBinding)
	}
	if configuration.ACMEEmail != "" && (!strings.Contains(configuration.ACMEEmail, "@") || strings.ContainsAny(configuration.ACMEEmail, "\r\n'\"`$\\")) {
		return fmt.Errorf("%w: ACME email", ErrInvalidBinding)
	}
	return nil
}

type Binding struct {
	PlanID               string             `json:"planId"`
	ProfileID            string             `json:"profileId"`
	ProfileRevision      int64              `json:"profileRevision"`
	Target               nodeinspect.Target `json:"target"`
	HostFingerprint      string             `json:"hostFingerprint,omitempty"`
	NodeIdentity         string             `json:"nodeIdentity"`
	InspectionDigest     string             `json:"inspectionDigest"`
	InspectedAt          time.Time          `json:"inspectedAt"`
	Release              string             `json:"release"`
	AssetID              string             `json:"assetId"`
	AssetSHA256          string             `json:"assetSha256"`
	OverlayRepositoryURL string             `json:"overlayRepositoryUrl"`
	OverlayCommit        string             `json:"overlayCommit"`
	OverlayRelease       string             `json:"overlayRelease"`
	AuthenticationKind   string             `json:"authenticationKind"`
	SecretsVaultKey      string             `json:"secretsVaultKey,omitempty"`
	Configuration        Configuration      `json:"configuration"`
}

func (binding Binding) Validate() error {
	if !safeOpaqueID.MatchString(binding.PlanID) || !safeOpaqueID.MatchString(binding.ProfileID) || binding.ProfileRevision < 1 {
		return fmt.Errorf("%w: identity", ErrInvalidBinding)
	}
	if err := binding.Target.Validate("linux"); err != nil {
		return fmt.Errorf("%w: target", ErrInvalidBinding)
	}
	if binding.Target.Kind == nodeinspect.RemoteTarget && !strings.HasPrefix(binding.HostFingerprint, "SHA256:") || binding.Target.Kind == nodeinspect.SameHostTarget && binding.HostFingerprint != "" {
		return fmt.Errorf("%w: host fingerprint", ErrInvalidBinding)
	}
	if !strings.HasPrefix(binding.NodeIdentity, "sha256:") && !strings.HasPrefix(binding.NodeIdentity, "SHA256:") {
		return fmt.Errorf("%w: node identity", ErrInvalidBinding)
	}
	if !safeCommit.MatchString(binding.InspectionDigest) || binding.InspectedAt.IsZero() || binding.Release != SupportedRelease || !safeProfileValue.MatchString(binding.AssetID) || !safeCommit.MatchString(binding.AssetSHA256) {
		return fmt.Errorf("%w: inspected release", ErrInvalidBinding)
	}
	repository, err := url.Parse(binding.OverlayRepositoryURL)
	if err != nil || repository.Scheme != "https" || repository.Host == "" || repository.User != nil || repository.RawQuery != "" || repository.Fragment != "" || !safeCommit.MatchString(binding.OverlayCommit) || binding.OverlayRelease == "" {
		return fmt.Errorf("%w: overlay", ErrInvalidBinding)
	}
	if binding.AuthenticationKind != "agent" && binding.AuthenticationKind != "private-key" && binding.AuthenticationKind != "password" && binding.AuthenticationKind != "same-host" {
		return fmt.Errorf("%w: authentication kind", ErrInvalidBinding)
	}
	if binding.SecretsVaultKey != "" && binding.SecretsVaultKey != binding.ProfileID+"/cluster-secrets-manifest" {
		return fmt.Errorf("%w: secret reference", ErrInvalidBinding)
	}
	return binding.Configuration.Validate()
}

func (binding Binding) Marshal() (string, error) {
	if err := binding.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(binding)
	if err != nil {
		return "", fmt.Errorf("marshal local bootstrap binding: %w", err)
	}
	return string(encoded), nil
}

func ParseBinding(encoded string) (Binding, error) {
	var binding Binding
	decoder := json.NewDecoder(strings.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&binding); err != nil {
		return Binding{}, fmt.Errorf("%w: json", ErrInvalidBinding)
	}
	if err := binding.Validate(); err != nil {
		return Binding{}, err
	}
	return binding, nil
}

func InspectionDigest(report nodeinspect.Report) (string, error) {
	encoded, err := json.Marshal(report)
	if err != nil {
		return "", fmt.Errorf("encode node inspection: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (binding Binding) DigestDetail() string {
	binding.PlanID = ""
	encoded, _ := json.Marshal(binding)
	return string(encoded)
}

func (binding Binding) PlanDigest(intent string) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%d\n%s", intent, binding.ProfileID, binding.ProfileRevision, binding.DigestDetail())))
	return hex.EncodeToString(digest[:])
}
