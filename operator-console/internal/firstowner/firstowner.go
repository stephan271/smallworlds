// Package firstowner owns the final step of the private administration handoff:
// a short-lived, single-use first-owner claim whose successful passkey
// registration permanently and irreversibly disables the bootstrap grant. It
// stores only public state (WebAuthn credential id and public key); there is no
// operation that can re-enable the bootstrap grant once disabled.
package firstowner

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrInvalidState is returned when a first-owner state fails validation.
var ErrInvalidState = errors.New("first-owner state is invalid")

// ErrClaimExpired is returned when the short-lived claim has expired or was
// already used.
var ErrClaimExpired = errors.New("first-owner claim expired")

// ErrGrantAlreadyDisabled is returned when the bootstrap grant has already been
// permanently disabled by a successful registration.
var ErrGrantAlreadyDisabled = errors.New("bootstrap grant already disabled")

// ErrInvalidRegistration is returned when a passkey registration is missing
// required public material.
var ErrInvalidRegistration = errors.New("passkey registration is invalid")

// ErrChallengeMismatch is returned when a registration does not echo the issued
// claim challenge.
var ErrChallengeMismatch = errors.New("passkey registration challenge mismatch")

const (
	claimTTL    = 10 * time.Minute
	maxClaimTTL = 15 * time.Minute
)

// Claim is the short-lived, single-use first-owner claim the launcher displays.
type Claim struct {
	Challenge string    `json:"challenge"`
	IssuedAt  time.Time `json:"issuedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	Used      bool      `json:"used"`
}

// State is the secret-free first-owner state for a profile.
type State struct {
	Claim                  Claim  `json:"claim"`
	OwnerRegistered        bool   `json:"ownerRegistered"`
	BootstrapGrantDisabled bool   `json:"bootstrapGrantDisabled"`
	CredentialID           string `json:"credentialId,omitempty"`
}

// Plan issues a fresh short-lived claim with a new WebAuthn challenge. The
// bootstrap grant remains enabled until a successful registration.
func Plan(now time.Time) (State, error) {
	challenge, err := generateChallenge()
	if err != nil {
		return State{}, err
	}
	issuedAt := now.UTC()
	return State{Claim: Claim{Challenge: challenge, IssuedAt: issuedAt, ExpiresAt: issuedAt.Add(claimTTL)}}, nil
}

// Validate enforces the claim's bounded lifetime and state consistency.
func (state State) Validate() error {
	if state.Claim.Challenge == "" {
		return fmt.Errorf("%w: challenge", ErrInvalidState)
	}
	if !state.Claim.ExpiresAt.After(state.Claim.IssuedAt) {
		return fmt.Errorf("%w: claim lifetime", ErrInvalidState)
	}
	if state.Claim.ExpiresAt.Sub(state.Claim.IssuedAt) > maxClaimTTL {
		return fmt.Errorf("%w: claim lifetime exceeds the bound", ErrInvalidState)
	}
	if state.OwnerRegistered != state.BootstrapGrantDisabled {
		return fmt.Errorf("%w: registration must disable the bootstrap grant", ErrInvalidState)
	}
	if state.BootstrapGrantDisabled && (!state.Claim.Used || state.CredentialID == "") {
		return fmt.Errorf("%w: disabled grant requires a consumed claim and credential", ErrInvalidState)
	}
	return nil
}

// ClaimValid reports whether the claim can still be used to register the owner.
func (state State) ClaimValid(now time.Time) bool {
	return !state.Claim.Used && now.UTC().Before(state.Claim.ExpiresAt)
}

// RegisterOwner consumes the claim and permanently disables the bootstrap grant.
// It is irreversible: once the grant is disabled it can never be registered
// again.
func (state State) RegisterOwner(now time.Time, credentialID string) (State, error) {
	if state.BootstrapGrantDisabled {
		return State{}, ErrGrantAlreadyDisabled
	}
	if !state.ClaimValid(now) {
		return State{}, ErrClaimExpired
	}
	if credentialID == "" {
		return State{}, ErrInvalidRegistration
	}
	state.Claim.Used = true
	state.OwnerRegistered = true
	state.BootstrapGrantDisabled = true
	state.CredentialID = credentialID
	return state, nil
}

// Marshal returns the canonical secret-free JSON for a validated state.
func (state State) Marshal() (string, error) {
	if err := state.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return "", fmt.Errorf("marshal first-owner state: %w", err)
	}
	return string(encoded), nil
}

// ParseState decodes and validates a stored state.
func ParseState(encoded string) (State, error) {
	var state State
	decoder := json.NewDecoder(strings.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return State{}, fmt.Errorf("%w: json", ErrInvalidState)
	}
	if err := state.Validate(); err != nil {
		return State{}, err
	}
	return state, nil
}

// Digest returns a stable content digest of the state.
func (state State) Digest() (string, error) {
	encoded, err := state.Marshal()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(encoded))
	return hex.EncodeToString(sum[:]), nil
}

// Registration is the public WebAuthn material a browser submits after a passkey
// ceremony.
type Registration struct {
	CredentialID string `json:"credentialId"`
	PublicKey    string `json:"publicKey"`
	Challenge    string `json:"challenge"`
}

// PasskeyVerifier verifies a passkey registration against the issued challenge
// and returns the credential id to persist.
type PasskeyVerifier interface {
	Verify(ctx context.Context, expectedChallenge string, registration Registration) (string, error)
}

// StructuralPasskeyVerifier verifies the challenge binding and required public
// material. Full WebAuthn attestation-signature verification is a follow-up
// integration; this deliberately does not fabricate a stronger guarantee.
type StructuralPasskeyVerifier struct{}

// Verify checks the required fields and constant-time compares the echoed
// challenge to the one issued in the claim.
func (StructuralPasskeyVerifier) Verify(_ context.Context, expectedChallenge string, registration Registration) (string, error) {
	if registration.CredentialID == "" || registration.PublicKey == "" {
		return "", ErrInvalidRegistration
	}
	if expectedChallenge == "" || subtle.ConstantTimeCompare([]byte(expectedChallenge), []byte(registration.Challenge)) != 1 {
		return "", ErrChallengeMismatch
	}
	return registration.CredentialID, nil
}

func generateChallenge() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate first-owner challenge: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
