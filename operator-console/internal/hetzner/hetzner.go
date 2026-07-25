// Package hetzner models read-only inspection and infrastructure planning for
// the Hetzner Deployment Mode. It is deliberately pure: every provider fact —
// the token probe, the resource inventory, the price list, the observed
// nameservers — is *input*, so the classification, capacity, cost, and plan
// rules stay deterministic and contract-testable without touching a project.
//
// Two properties are load-bearing and encoded here rather than in the launcher:
//
//   - Nothing in this package mutates or can be made to mutate a project. The
//     result of planning is an immutable ChangePlan bound to the inventory it
//     was derived from, so provisioning (issue 19) can refuse a stale plan.
//   - A resource that merely *looks* like ours is never adopted. Ownership is
//     classified from provider labels and exact names; a similar name is
//     reported as unknown, and even an exact match must be explicitly adopted.
package hetzner

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
)

var (
	// ErrInvalidToken is returned when a project token is not a syntactically
	// valid Hetzner Cloud API token.
	ErrInvalidToken = errors.New("hetzner: invalid project token")
	// ErrInvalidChoice is returned when an infrastructure choice cannot be
	// satisfied by the observed provider catalog.
	ErrInvalidChoice = errors.New("hetzner: invalid infrastructure choice")
	// ErrInvalidNaming is returned when the resource naming inputs are incomplete.
	ErrInvalidNaming = errors.New("hetzner: invalid resource naming")
)

// Hetzner Cloud API tokens are 64 characters of base62. Checking the shape
// locally keeps an obviously malformed token from being sent to the provider.
var tokenText = regexp.MustCompile(`^[A-Za-z0-9]{64}$`)

// TokenProbe is the evidence a read-only provider probe returned for a token.
// It carries no token value: the launcher performs the calls and reports what
// it observed.
//
// WriteAuthority cannot be read from a token — Hetzner exposes no scope
// introspection — so the launcher derives it from a rejected-vs-accepted
// probe of a write endpoint and reports the outcome here.
type TokenProbe struct {
	// ProjectID is the stable provider identity of the project the token
	// addresses (Hetzner does not return a project id directly; the launcher
	// derives a stable identity from the project's resources).
	ProjectID string `json:"projectId"`
	// Unauthorized is true when the provider rejected the token outright.
	Unauthorized bool `json:"unauthorized"`
	// RateLimited is true when the probe could not complete because the
	// provider throttled it — an inconclusive result, never a failure verdict.
	RateLimited bool `json:"rateLimited"`
	// ReadAuthority is true when a read call succeeded.
	ReadAuthority bool `json:"readAuthority"`
	// WriteAuthority is true when the write probe was accepted (not 403).
	WriteAuthority bool `json:"writeAuthority"`
}

// TokenState is the verdict of assessing a probe.
type TokenState string

const (
	// TokenValid means the token addresses a project and holds read and write
	// authority — the only state from which planning may proceed.
	TokenValid TokenState = "valid"
	// TokenMalformed means the value is not a Hetzner token shape; no call made.
	TokenMalformed TokenState = "malformed"
	// TokenUnauthorized means the provider rejected the token.
	TokenUnauthorized TokenState = "unauthorized"
	// TokenReadOnly means the token can inspect but not provision.
	TokenReadOnly TokenState = "read-only"
	// TokenInconclusive means the provider throttled or could not answer, so
	// the token is neither accepted nor rejected.
	TokenInconclusive TokenState = "inconclusive"
	// TokenProjectMismatch means the token addresses a different project than
	// the one this Cluster Profile is already bound to.
	TokenProjectMismatch TokenState = "project-mismatch"
)

// TokenAssessment is the secret-free verdict shown to the operator and
// persisted. It identifies the token by fingerprint only — the value lives in
// the Launcher Vault and never appears in a response, a record, or a log.
type TokenAssessment struct {
	State       TokenState `json:"state"`
	Fingerprint string     `json:"fingerprint"`
	ProjectID   string     `json:"projectId"`
	// ReasonKey is the stable translatable explanation of the state.
	ReasonKey string `json:"reasonKey"`
	// MissingAuthority lists the authorities planning still needs.
	MissingAuthority []string `json:"missingAuthority,omitempty"`
}

// Usable reports whether planning may proceed on this token.
func (assessment TokenAssessment) Usable() bool { return assessment.State == TokenValid }

// ValidToken reports whether a value has the shape of a Hetzner Cloud API
// token. It is checked before any provider call is attempted.
func ValidToken(token string) bool { return tokenText.MatchString(strings.TrimSpace(token)) }

// AssessToken classifies a token probe. boundProjectID is the project this
// Cluster Profile was previously bound to (empty on first use); a token for a
// different project is refused rather than silently re-pointing the profile at
// other infrastructure.
func AssessToken(token string, probe TokenProbe, boundProjectID string) TokenAssessment {
	assessment := TokenAssessment{Fingerprint: Fingerprint(token), ProjectID: probe.ProjectID}
	switch {
	case !ValidToken(token):
		assessment.State, assessment.ReasonKey, assessment.Fingerprint = TokenMalformed, "token-malformed", ""
	case probe.Unauthorized:
		assessment.State, assessment.ReasonKey = TokenUnauthorized, "token-rejected-by-provider"
	case probe.RateLimited:
		assessment.State, assessment.ReasonKey = TokenInconclusive, "token-check-rate-limited"
	case !probe.ReadAuthority:
		assessment.State, assessment.ReasonKey = TokenInconclusive, "token-read-check-inconclusive"
		assessment.MissingAuthority = []string{"read"}
	case boundProjectID != "" && probe.ProjectID != "" && probe.ProjectID != boundProjectID:
		assessment.State, assessment.ReasonKey = TokenProjectMismatch, "token-addresses-different-project"
	case !probe.WriteAuthority:
		assessment.State, assessment.ReasonKey = TokenReadOnly, "token-lacks-write-authority"
		assessment.MissingAuthority = []string{"write"}
	default:
		assessment.State, assessment.ReasonKey = TokenValid, "token-validated"
	}
	return assessment
}

// digestOf is the canonical content digest used for inventory and plan
// identity.
func digestOf(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// Fingerprint is a short, non-reversible identifier for a credential value, so
// the console can show *which* token is in use without storing or echoing it.
func Fingerprint(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}
