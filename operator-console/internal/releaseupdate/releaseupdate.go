// Package releaseupdate verifies SmallWorlds release metadata and builds the
// explicit GitOps Change Plan used to move a cluster between releases.
//
// Release metadata is accepted only when its exact JSON payload is signed by
// the configured release-engineering key. Planning is pure: it never downloads
// a tool, edits a live cluster, or merges a proposal.
package releaseupdate

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"

	"github.com/stephan271/smallworlds/operator-console/internal/assessment"
	"github.com/stephan271/smallworlds/operator-console/internal/capability"
)

var (
	ErrInvalidMetadata = errors.New("releaseupdate: invalid signed release metadata")
	ErrNoUpdate        = errors.New("releaseupdate: no newer signed release")
	ErrIncompatible    = errors.New("releaseupdate: release is incompatible")
)

var (
	releaseTag = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)(?:-[0-9A-Za-z.-]+)?$`)
	digest     = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	safeName   = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,159}$`)
)

// SignedMetadata is the wire/storage form of one release. Signature covers the
// exact Payload bytes, preventing any displayed field or pinned digest from
// being changed independently.
type SignedMetadata struct {
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
}

// Compatibility is the inclusive launcher, cluster, and catalog range supported
// by a release. Catalog versions are integers because the capability catalog
// itself is independently versioned from the SmallWorlds base tag.
type Compatibility struct {
	LauncherMin string `json:"launcherMin"`
	LauncherMax string `json:"launcherMax"`
	ClusterMin  string `json:"clusterMin"`
	ClusterMax  string `json:"clusterMax"`
	CatalogMin  int    `json:"catalogMin"`
	CatalogMax  int    `json:"catalogMax"`
}

type CapabilityChange struct {
	ID     string `json:"id"`
	Change string `json:"change"`
	Detail string `json:"detail"`
}

type Risks struct {
	Downtime []string `json:"downtime"`
	Data     []string `json:"data"`
	Exposure []string `json:"exposure"`
}

type Recovery struct {
	Expected string   `json:"expected"`
	Steps    []string `json:"steps"`
}

// Metadata is the complete signed release declaration shown in the Console.
// Images and Tools map stable component names to immutable sha256 digests.
type Metadata struct {
	Release           string             `json:"release"`
	BaseTag           string             `json:"baseTag"`
	CatalogVersion    int                `json:"catalogVersion"`
	Images            map[string]string  `json:"images"`
	Tools             map[string]string  `json:"tools"`
	Compatibility     Compatibility      `json:"compatibility"`
	ReleaseNotes      []string           `json:"releaseNotes"`
	CapabilityChanges []CapabilityChange `json:"capabilityChanges"`
	Risks             Risks              `json:"risks"`
	Recovery          Recovery           `json:"recovery"`
}

// ClusterProfile is the non-secret, exportable release identity and capability
// snapshot used for compatibility decisions.
type ClusterProfile struct {
	LauncherVersion string            `json:"launcherVersion"`
	ClusterVersion  string            `json:"clusterVersion"`
	BaseTag         string            `json:"baseTag"`
	CatalogVersion  int               `json:"catalogVersion"`
	DeploymentMode  string            `json:"deploymentMode"`
	Capabilities    []string          `json:"capabilities"`
	Images          map[string]string `json:"images,omitempty"`
	Tools           map[string]string `json:"tools,omitempty"`
}

type CompatibilityResult struct {
	Compatible bool     `json:"compatible"`
	Reasons    []string `json:"reasons"`
}

// AvailableRelease is safe to surface only after Catalog verification.
type AvailableRelease struct {
	Metadata       Metadata            `json:"metadata"`
	Compatibility  CompatibilityResult `json:"compatibility"`
	SignatureValid bool                `json:"signatureValid"`
}

// Catalog is a collection of opaque signed payloads rooted in one trusted key.
type Catalog struct {
	TrustedPublicKey ed25519.PublicKey
	Releases         []SignedMetadata
}

// Resolve verifies and returns one exact release.
func (catalog Catalog) Resolve(release string) (Metadata, error) {
	if len(catalog.TrustedPublicKey) != ed25519.PublicKeySize {
		return Metadata{}, fmt.Errorf("%w: invalid trusted key", ErrInvalidMetadata)
	}
	for _, envelope := range catalog.Releases {
		metadata, err := verify(catalog.TrustedPublicKey, envelope)
		if err != nil {
			return Metadata{}, err
		}
		if metadata.Release == release {
			return metadata, nil
		}
	}
	return Metadata{}, ErrNoUpdate
}

// Available returns the highest signed release newer than the profile base tag.
// It deliberately returns incompatible releases with reasons so the operator can
// inspect what is available; compatibility gates planning, not observation.
func (catalog Catalog) Available(profile ClusterProfile) (AvailableRelease, error) {
	if len(catalog.TrustedPublicKey) != ed25519.PublicKeySize {
		return AvailableRelease{}, fmt.Errorf("%w: invalid trusted key", ErrInvalidMetadata)
	}
	var selected Metadata
	found := false
	for _, envelope := range catalog.Releases {
		metadata, err := verify(catalog.TrustedPublicKey, envelope)
		if err != nil {
			return AvailableRelease{}, err
		}
		if compareVersion(metadata.BaseTag, profile.BaseTag) <= 0 {
			continue
		}
		if !found || compareVersion(metadata.BaseTag, selected.BaseTag) > 0 {
			selected, found = metadata, true
		}
	}
	if !found {
		return AvailableRelease{}, ErrNoUpdate
	}
	return AvailableRelease{
		Metadata:       selected,
		Compatibility:  CheckCompatibility(profile, selected.Compatibility),
		SignatureValid: true,
	}, nil
}

func verify(publicKey ed25519.PublicKey, envelope SignedMetadata) (Metadata, error) {
	if len(publicKey) != ed25519.PublicKeySize || len(envelope.Payload) == 0 {
		return Metadata{}, ErrInvalidMetadata
	}
	signature, err := base64.StdEncoding.DecodeString(envelope.Signature)
	if err != nil || !ed25519.Verify(publicKey, envelope.Payload, signature) {
		return Metadata{}, fmt.Errorf("%w: signature", ErrInvalidMetadata)
	}
	decoder := json.NewDecoder(bytes.NewReader(envelope.Payload))
	decoder.DisallowUnknownFields()
	var metadata Metadata
	if err := decoder.Decode(&metadata); err != nil {
		return Metadata{}, fmt.Errorf("%w: payload", ErrInvalidMetadata)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Metadata{}, fmt.Errorf("%w: trailing payload", ErrInvalidMetadata)
	}
	if err := metadata.validate(); err != nil {
		return Metadata{}, err
	}
	return metadata, nil
}

func (metadata Metadata) validate() error {
	if !validVersion(metadata.Release) || metadata.Release != metadata.BaseTag || metadata.CatalogVersion <= 0 {
		return fmt.Errorf("%w: release identity", ErrInvalidMetadata)
	}
	c := metadata.Compatibility
	if !validVersion(c.LauncherMin) || !validVersion(c.LauncherMax) ||
		!validVersion(c.ClusterMin) || !validVersion(c.ClusterMax) ||
		compareVersion(c.LauncherMin, c.LauncherMax) > 0 ||
		compareVersion(c.ClusterMin, c.ClusterMax) > 0 ||
		c.CatalogMin <= 0 || c.CatalogMax < c.CatalogMin {
		return fmt.Errorf("%w: compatibility range", ErrInvalidMetadata)
	}
	if len(metadata.ReleaseNotes) == 0 || metadata.Recovery.Expected == "" {
		return fmt.Errorf("%w: missing operator guidance", ErrInvalidMetadata)
	}
	for name, value := range metadata.Images {
		if !safeName.MatchString(name) || !digest.MatchString(value) {
			return fmt.Errorf("%w: image digest", ErrInvalidMetadata)
		}
	}
	for name, value := range metadata.Tools {
		if !safeName.MatchString(name) || !digest.MatchString(value) {
			return fmt.Errorf("%w: tool digest", ErrInvalidMetadata)
		}
	}
	if len(metadata.Images) == 0 || len(metadata.Tools) == 0 {
		return fmt.Errorf("%w: immutable digests required", ErrInvalidMetadata)
	}
	return nil
}

func CheckCompatibility(profile ClusterProfile, compatible Compatibility) CompatibilityResult {
	reasons := make([]string, 0, 3)
	if !within(profile.LauncherVersion, compatible.LauncherMin, compatible.LauncherMax) {
		reasons = append(reasons, "launcher-version-out-of-range")
	}
	if !within(profile.ClusterVersion, compatible.ClusterMin, compatible.ClusterMax) {
		reasons = append(reasons, "cluster-version-out-of-range")
	}
	if profile.CatalogVersion < compatible.CatalogMin || profile.CatalogVersion > compatible.CatalogMax {
		reasons = append(reasons, "catalog-version-out-of-range")
	}
	return CompatibilityResult{Compatible: len(reasons) == 0, Reasons: reasons}
}

func within(value, minimum, maximum string) bool {
	return validVersion(value) && compareVersion(value, minimum) >= 0 && compareVersion(value, maximum) <= 0
}

func validVersion(value string) bool { return releaseTag.MatchString(value) }

func compareVersion(left, right string) int {
	l := versionParts(left)
	r := versionParts(right)
	for i := range l {
		if l[i] < r[i] {
			return -1
		}
		if l[i] > r[i] {
			return 1
		}
	}
	return 0
}

func versionParts(value string) [3]int {
	match := releaseTag.FindStringSubmatch(value)
	var parts [3]int
	if match == nil {
		return parts
	}
	for i := 0; i < 3; i++ {
		parts[i], _ = strconv.Atoi(match[i+1])
	}
	return parts
}

// Plan contains every fact the operator must review. Files are intentionally
// omitted from JSON; GitDiff is the exact rendered representation of those files.
type Plan struct {
	FromBaseTag       string              `json:"fromBaseTag"`
	ToBaseTag         string              `json:"toBaseTag"`
	CatalogVersion    int                 `json:"catalogVersion"`
	Images            map[string]string   `json:"images"`
	Tools             map[string]string   `json:"tools"`
	Compatibility     CompatibilityResult `json:"compatibility"`
	ReleaseNotes      []string            `json:"releaseNotes"`
	CapabilityChanges []CapabilityChange  `json:"capabilityChanges"`
	Risks             Risks               `json:"risks"`
	Recovery          Recovery            `json:"recovery"`
	Files             map[string]string   `json:"-"`
	GitDiff           string              `json:"gitDiff"`
}

// Overlay is the operator's own overlay, which is what actually decides the
// release a cluster runs. A plan needs it because moving between releases means
// changing that overlay: the pinned base in every kustomization, and the
// release the cluster reports about itself.
type Overlay struct {
	RepositoryURL        string `json:"repositoryUrl"`
	Domain               string `json:"domain"`
	EnvironmentExtension string `json:"environmentExtension,omitempty"`
}

// BuildPlan refuses incompatible profiles and renders the overlay the update
// would commit. The caller must obtain metadata from Catalog.Resolve first.
//
// What it renders comes from the capability package — the same code that
// establishes an overlay in the first place. It used to render a
// smallworlds-release.yaml pins file instead, a file with no reader anywhere in
// the project, so the proposal it produced would have merged without moving the
// cluster to the new release at all.
func BuildPlan(profile ClusterProfile, metadata Metadata, catalog capability.Catalog, overlay Overlay) (Plan, error) {
	if err := metadata.validate(); err != nil {
		return Plan{}, err
	}
	compatibility := CheckCompatibility(profile, metadata.Compatibility)
	if !compatibility.Compatible {
		return Plan{}, ErrIncompatible
	}
	change, err := catalog.RenderChange(
		overlay.input(profile, profile.BaseTag),
		overlay.input(profile, metadata.BaseTag),
	)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{
		FromBaseTag: profile.BaseTag, ToBaseTag: metadata.BaseTag,
		CatalogVersion: metadata.CatalogVersion,
		Images:         cloneMap(metadata.Images), Tools: cloneMap(metadata.Tools),
		Compatibility:     compatibility,
		ReleaseNotes:      append([]string(nil), metadata.ReleaseNotes...),
		CapabilityChanges: append([]CapabilityChange(nil), metadata.CapabilityChanges...),
		Risks:             metadata.Risks, Recovery: metadata.Recovery,
		Files:   change.Files,
		GitDiff: change.Diff,
	}
	return plan, nil
}

// input describes the overlay as it stands at one base tag. The community
// selection comes from the observed cluster profile rather than from anything
// remembered here, so an update can never quietly re-decide which applications
// a community runs.
func (overlay Overlay) input(profile ClusterProfile, baseTag string) capability.OverlayInput {
	return capability.OverlayInput{
		Selection: capability.Selection{
			Mode:           capability.Custom,
			DeploymentMode: capability.DeploymentMode(profile.DeploymentMode),
			CommunityIDs:   communityCapabilities(profile),
		},
		Release:              baseTag,
		RepositoryURL:        overlay.RepositoryURL,
		Domain:               overlay.Domain,
		EnvironmentExtension: overlay.EnvironmentExtension,
	}
}

// communityCapabilities keeps only what an overlay names per application; the
// platform services are always installed and are not part of the selection.
func communityCapabilities(profile ClusterProfile) []string {
	catalog := capability.DefaultCatalog()
	selected := make([]string, 0, len(profile.Capabilities))
	for _, id := range profile.Capabilities {
		if entry, found := catalog.Entry(id); found && entry.Category == capability.CommunityApplication {
			selected = append(selected, id)
		}
	}
	sort.Strings(selected)
	return selected
}
func cloneMap(source map[string]string) map[string]string {
	target := make(map[string]string, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
}

type AdoptionState string

const (
	AdoptionAwaitingMerge AdoptionState = "awaiting-merge"
	AdoptionConverging    AdoptionState = "converging"
	AdoptionAdopted       AdoptionState = "adopted"
	AdoptionPartial       AdoptionState = "partial"
	AdoptionFailed        AdoptionState = "failed"
)

// AdoptionEvidence combines the merged Git identity, Argo convergence and the
// same capability assessments shown elsewhere in the Console.
type AdoptionEvidence struct {
	TargetRelease string                            `json:"targetRelease"`
	Merged        bool                              `json:"merged"`
	MergedCommit  string                            `json:"mergedCommit,omitempty"`
	ArgoRevision  string                            `json:"argoRevision,omitempty"`
	ArgoSynced    bool                              `json:"argoSynced"`
	ArgoHealthy   bool                              `json:"argoHealthy"`
	ArgoFailed    bool                              `json:"argoFailed"`
	Capabilities  []assessment.CapabilityAssessment `json:"capabilities"`
	State         AdoptionState                     `json:"state"`
	Reasons       []string                          `json:"reasons"`
}

// AssessAdoption gives partial and failed adoption explicit states rather than
// flattening them into a generic "updating" result.
func AssessAdoption(evidence AdoptionEvidence) AdoptionEvidence {
	reasons := make([]string, 0)
	if !evidence.Merged {
		evidence.State = AdoptionAwaitingMerge
		evidence.Reasons = []string{"operator-merge-not-observed"}
		return evidence
	}
	if evidence.ArgoFailed {
		evidence.State = AdoptionFailed
		evidence.Reasons = []string{"argo-adoption-failed"}
		return evidence
	}
	unhealthy, failed := 0, 0
	for _, capability := range evidence.Capabilities {
		switch capability.State {
		case assessment.StateFailed, assessment.StateBlocked:
			failed++
			reasons = append(reasons, capability.CapabilityID+":"+string(capability.State))
		case assessment.StateHealthy, assessment.StateDisabled:
		default:
			unhealthy++
			reasons = append(reasons, capability.CapabilityID+":"+string(capability.State))
		}
	}
	switch {
	case failed > 0:
		evidence.State = AdoptionFailed
	case evidence.ArgoSynced && evidence.ArgoHealthy && unhealthy == 0:
		evidence.State = AdoptionAdopted
	case (evidence.ArgoSynced || evidence.ArgoHealthy) && unhealthy > 0:
		evidence.State = AdoptionPartial
	default:
		evidence.State = AdoptionConverging
	}
	evidence.Reasons = reasons
	return evidence
}
