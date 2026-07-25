// Package hetznerprovision owns the contract between an approved Hetzner
// infrastructure Change Plan and its reproducible execution.
//
// Approval and execution are separated in time — an operator approves a plan,
// then the launcher (possibly after a restart, a network interruption, or a
// vault re-unlock) executes it. Everything the operator saw when they approved
// can change in between: a resource can appear in the project, the Primary IP
// can be reassigned, the registrar can be re-pointed, the overlay can move to a
// new commit, or another launcher can have written OpenTofu state. So the
// binding records every fact the approval rested on, and Revalidate re-checks
// each one against freshly observed evidence immediately before execution.
//
// The gate is deliberately conservative: an unobservable fact is never treated
// as unchanged. Paid, hard-to-reverse infrastructure is the thing being decided,
// and a refused execution is recoverable while a duplicated server is not.
package hetznerprovision

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/hetzner"
)

// ErrInvalidBinding is returned when an execution binding is incomplete or
// internally inconsistent.
var ErrInvalidBinding = errors.New("hetzner provisioning binding is invalid")

var (
	safeDigest    = regexp.MustCompile(`^[a-f0-9]{64}$`)
	safeCommit    = regexp.MustCompile(`^[a-f0-9]{40,64}$`)
	safeOpaqueID  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	safeRelease   = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
	safeToolchain = regexp.MustCompile(`^tofu-[0-9.]+\+hcloud-[0-9.]+$`)
	safeAddress   = regexp.MustCompile(`^[0-9a-fA-F.:]{2,45}$`)
)

// Binding is the immutable record of what an operator approved. It carries no
// secrets: the project token stays in the Launcher Vault and the OpenTofu state
// is identified by digest only.
type Binding struct {
	PlanID          string         `json:"planId"`
	ProfileID       string         `json:"profileId"`
	ProfileRevision int64          `json:"profileRevision"`
	ProjectID       string         `json:"projectId"`
	Naming          hetzner.Naming `json:"naming"`

	// PlanDigest and InventoryDigest bind the exact reviewed plan and the exact
	// inventory it was derived from.
	PlanDigest      string `json:"planDigest"`
	InventoryDigest string `json:"inventoryDigest"`

	// PublicAddress is the Primary IP the approved plan expects to use, empty
	// when the plan creates one. It is checked separately from the inventory
	// digest because an address can be reassigned without any resource being
	// added or removed.
	PublicAddress string `json:"publicAddress,omitempty"`

	// Delegation is the nameserver verdict the plan was approved under.
	Delegation hetzner.DelegationStatus `json:"delegation"`

	// Adoptions are the stable provider identities the operator explicitly chose
	// to take over. Execution may touch no other pre-existing resource.
	Adoptions []string `json:"adoptions,omitempty"`

	Release              string `json:"release"`
	OverlayRepositoryURL string `json:"overlayRepositoryUrl"`
	OverlayCommit        string `json:"overlayCommit"`
	OverlayRelease       string `json:"overlayRelease"`
	ToolchainRelease     string `json:"toolchainRelease"`

	// StateDigest is the workspace state digest observed at approval, empty when
	// the profile had no state yet. A state that changed underneath the approval
	// means another actor reconciled this profile, so the plan is not ours to
	// apply any more.
	StateDigest string `json:"stateDigest,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
}

// Validate enforces that a binding names everything the gate needs to re-check.
// A binding missing a fact could otherwise pass revalidation by having nothing
// to compare.
func (binding Binding) Validate() error {
	if !safeOpaqueID.MatchString(binding.PlanID) || !safeOpaqueID.MatchString(binding.ProfileID) || binding.ProfileRevision < 1 {
		return fmt.Errorf("%w: identity", ErrInvalidBinding)
	}
	if strings.TrimSpace(binding.ProjectID) == "" {
		return fmt.Errorf("%w: project", ErrInvalidBinding)
	}
	if err := binding.Naming.Validate(); err != nil {
		return fmt.Errorf("%w: naming", ErrInvalidBinding)
	}
	if binding.Naming.ProfileID != binding.ProfileID {
		return fmt.Errorf("%w: naming profile", ErrInvalidBinding)
	}
	if !safeDigest.MatchString(binding.PlanDigest) || !safeDigest.MatchString(binding.InventoryDigest) {
		return fmt.Errorf("%w: plan digest", ErrInvalidBinding)
	}
	if binding.PublicAddress != "" && !safeAddress.MatchString(binding.PublicAddress) {
		return fmt.Errorf("%w: public address", ErrInvalidBinding)
	}
	// Only a delegation verdict that satisfied the plan can have been approved;
	// recording an unsatisfied one would let the gate compare "still broken" to
	// "still broken" and pass.
	if !(hetzner.Delegation{Status: binding.Delegation}).Satisfied() {
		return fmt.Errorf("%w: delegation", ErrInvalidBinding)
	}
	for _, adoption := range binding.Adoptions {
		if strings.TrimSpace(adoption) == "" {
			return fmt.Errorf("%w: adoption", ErrInvalidBinding)
		}
	}
	if !safeRelease.MatchString(binding.Release) || binding.OverlayRelease != binding.Release {
		return fmt.Errorf("%w: release", ErrInvalidBinding)
	}
	repository, err := url.Parse(binding.OverlayRepositoryURL)
	if err != nil || repository.Scheme != "https" || repository.Host == "" || repository.User != nil || repository.RawQuery != "" || repository.Fragment != "" {
		return fmt.Errorf("%w: overlay repository", ErrInvalidBinding)
	}
	if !safeCommit.MatchString(binding.OverlayCommit) {
		return fmt.Errorf("%w: overlay commit", ErrInvalidBinding)
	}
	if !safeToolchain.MatchString(binding.ToolchainRelease) {
		return fmt.Errorf("%w: toolchain release", ErrInvalidBinding)
	}
	if binding.StateDigest != "" && !safeDigest.MatchString(binding.StateDigest) {
		return fmt.Errorf("%w: state digest", ErrInvalidBinding)
	}
	if binding.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created at", ErrInvalidBinding)
	}
	return nil
}

// Adopted reports whether a stable provider identity is one the operator
// explicitly approved adopting.
func (binding Binding) Adopted(providerID string) bool {
	for _, adoption := range binding.Adoptions {
		if adoption == providerID {
			return true
		}
	}
	return false
}

// Marshal returns the canonical secret-free JSON of a validated binding.
func (binding Binding) Marshal() (string, error) {
	if err := binding.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(binding)
	if err != nil {
		return "", fmt.Errorf("marshal hetzner provisioning binding: %w", err)
	}
	return string(encoded), nil
}

// ParseBinding decodes and validates a stored binding.
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

// DigestDetail is the stable content the workflow plan digest is computed over,
// excluding the plan id the digest is stored against.
func (binding Binding) DigestDetail() string {
	binding.PlanID = ""
	encoded, _ := json.Marshal(binding)
	return string(encoded)
}

// PlanDigestFor binds an intent, the profile at a specific revision, and every
// approved fact into the digest the workflow plan is approved under.
func (binding Binding) PlanDigestFor(intent string) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%d\n%s", intent, binding.ProfileID, binding.ProfileRevision, binding.DigestDetail())))
	return hex.EncodeToString(digest[:])
}

// BindPlan derives the execution binding from an approved Change Plan and the
// surrounding facts. The plan is the single source of the naming, project,
// inventory digest, and delegation verdict, so the binding cannot disagree with
// what the operator reviewed.
func BindPlan(plan hetzner.ChangePlan, environment Environment) (Binding, error) {
	if !plan.Approvable() {
		return Binding{}, fmt.Errorf("%w: plan is blocked", ErrInvalidBinding)
	}
	binding := Binding{
		PlanID:               environment.PlanID,
		ProfileID:            plan.ProfileID,
		ProfileRevision:      environment.ProfileRevision,
		ProjectID:            plan.ProjectID,
		Naming:               hetzner.Naming{Domain: plan.Domain, EnvExt: plan.EnvExt, ProfileID: plan.ProfileID},
		PlanDigest:           plan.Digest,
		InventoryDigest:      plan.InventoryDigest,
		PublicAddress:        environment.PublicAddress,
		Delegation:           plan.Delegation.Status,
		Adoptions:            adoptedIdentities(plan),
		Release:              environment.Release,
		OverlayRepositoryURL: environment.OverlayRepositoryURL,
		OverlayCommit:        environment.OverlayCommit,
		OverlayRelease:       environment.OverlayRelease,
		ToolchainRelease:     environment.ToolchainRelease,
		StateDigest:          environment.StateDigest,
		CreatedAt:            environment.CreatedAt,
	}
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = time.Now().UTC()
	}
	binding.CreatedAt = binding.CreatedAt.UTC()
	if err := binding.Validate(); err != nil {
		return Binding{}, err
	}
	return binding, nil
}

// Environment is everything a binding needs that the Change Plan does not
// itself carry: the workflow plan it belongs to, the profile revision, the
// selected release and overlay commit, the pinned toolchain, and the workspace
// state as observed at approval.
type Environment struct {
	PlanID               string
	ProfileRevision      int64
	PublicAddress        string
	Release              string
	OverlayRepositoryURL string
	OverlayCommit        string
	OverlayRelease       string
	ToolchainRelease     string
	StateDigest          string
	CreatedAt            time.Time
}

// adoptedIdentities lists the stable provider identities the plan takes over.
// Only an explicitly adopted item appears: reused shared resources and resources
// this profile already owns are not adoptions.
func adoptedIdentities(plan hetzner.ChangePlan) []string {
	var identities []string
	for _, item := range plan.Items {
		if item.Action == hetzner.ActionAdopt && item.ProviderID != "" {
			identities = append(identities, item.ProviderID)
		}
	}
	return identities
}

// PublicAddressOf extracts the Primary IP address an inventory observed, empty
// when the installation has none yet. The address lives in the resource detail
// rather than the inventory digest, so it is compared on its own.
func PublicAddressOf(inventory hetzner.Inventory) string {
	for _, finding := range inventory.Findings {
		if finding.Expectation.Kind == hetzner.KindPrimaryIP && finding.Match != nil {
			return strings.TrimSpace(finding.Match.Detail)
		}
	}
	return ""
}
