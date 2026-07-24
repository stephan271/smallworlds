// Package operatordevice owns the accountable device-access journey for the
// Operator Console: a Console Owner issues short-lived, single-use Enrollment
// Invitations for additional Operator Devices, and revokes a lost device through
// an inspected, lockout-aware plan rather than direct Headscale administration.
//
// Everything here is a pure domain: the Invitation record is secret-free (it
// carries only a fingerprint of the single-use join key, never the key itself or
// any reusable cluster/Headscale administrator credential), the enrollment
// guidance is derived deterministically from the Deployment Mode and the Private
// Network, and the revocation assessment is computed from an injected device
// inventory. The networked steps — minting the single-use key, listing devices,
// and removing one — live behind seams in the console package.
package operatordevice

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Errors returned across the invitation lifecycle. They are deliberately
// distinct so an expired, reused, revoked, or malformed invitation fails clearly
// rather than being conflated into one opaque failure.
var (
	// ErrInvalidInvitation is returned when an invitation is structurally
	// malformed (bad identifier, label, actor, fingerprint, or lifetime).
	ErrInvalidInvitation = errors.New("operator device invitation is invalid")
	// ErrInvitationExpired is returned when a short-lived invitation has passed
	// its expiry.
	ErrInvitationExpired = errors.New("operator device invitation has expired")
	// ErrInvitationUsed is returned when a single-use invitation has already been
	// consumed.
	ErrInvitationUsed = errors.New("operator device invitation has already been used")
	// ErrInvitationRevoked is returned when an invitation was revoked before use.
	ErrInvitationRevoked = errors.New("operator device invitation was revoked")
)

const (
	// DefaultInvitationTTL is the short lifetime an invitation is issued with.
	DefaultInvitationTTL = 15 * time.Minute
	// MinInvitationTTL and MaxInvitationTTL bound any accepted lifetime so an
	// invitation is always short-lived — never a long-lived standing credential.
	MinInvitationTTL = time.Minute
	MaxInvitationTTL = time.Hour
)

// InvitationState is the derived lifecycle state of an invitation at a point in
// time. It accounts for expiry without a stored transition.
type InvitationState string

const (
	// StatePending is a fresh invitation that can still be consumed.
	StatePending InvitationState = "pending"
	// StateConsumed is a single-use invitation that has been redeemed.
	StateConsumed InvitationState = "consumed"
	// StateRevoked is an invitation the Owner revoked before it was used.
	StateRevoked InvitationState = "revoked"
	// StateExpired is an unused invitation past its expiry.
	StateExpired InvitationState = "expired"
)

var (
	safeInvitationID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	safeLabel        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,62}$`)
	safeActor        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 @._-]{0,127}$`)
	sha256Hex        = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// Invitation is the durable, secret-free record of a single-use Enrollment
// Invitation. It is safe to persist and to surface to an Owner: it names who
// issued it (attributable) and carries only a fingerprint of the one-time join
// key, never the key itself, a kubeconfig, or a Headscale administrator token.
type Invitation struct {
	ID string `json:"id"`
	// Label is the human name the Owner gives the joining device.
	Label string `json:"label"`
	// IssuedBy is the Owner who created the invitation — the accountability
	// anchor for the Activity Record.
	IssuedBy string `json:"issuedBy"`
	// KeyFingerprint is the SHA-256 of the single-use join key, so an audit can
	// tie a joined device back to this invitation without ever storing the key.
	KeyFingerprint string     `json:"keyFingerprint"`
	SingleUse      bool       `json:"singleUse"`
	IssuedAt       time.Time  `json:"issuedAt"`
	ExpiresAt      time.Time  `json:"expiresAt"`
	ConsumedAt     *time.Time `json:"consumedAt,omitempty"`
	RevokedAt      *time.Time `json:"revokedAt,omitempty"`
}

// Fingerprint returns the SHA-256 hex digest of a single-use join key. The
// digest is what the durable Invitation stores; the key itself is shown to the
// Owner exactly once and never persisted.
func Fingerprint(joinKey string) string {
	sum := sha256.Sum256([]byte(joinKey))
	return hex.EncodeToString(sum[:])
}

// IssueInvitation builds a short-lived, single-use invitation record bound to
// the fingerprint of an already-minted join key. The lifetime is clamped into
// [MinInvitationTTL, MaxInvitationTTL] so an invitation is never long-lived; the
// join key material stays with the caller and is never handed to this function.
func IssueInvitation(id, label, issuedBy, keyFingerprint string, now time.Time, ttl time.Duration) (Invitation, error) {
	if ttl <= 0 {
		ttl = DefaultInvitationTTL
	}
	if ttl < MinInvitationTTL {
		ttl = MinInvitationTTL
	}
	if ttl > MaxInvitationTTL {
		ttl = MaxInvitationTTL
	}
	issuedAt := now.UTC()
	invitation := Invitation{
		ID:             id,
		Label:          strings.TrimSpace(label),
		IssuedBy:       strings.TrimSpace(issuedBy),
		KeyFingerprint: strings.ToLower(strings.TrimSpace(keyFingerprint)),
		SingleUse:      true,
		IssuedAt:       issuedAt,
		ExpiresAt:      issuedAt.Add(ttl),
	}
	if err := invitation.Validate(); err != nil {
		return Invitation{}, err
	}
	return invitation, nil
}

// Validate enforces the structural invariants of a secret-free invitation: a
// safe identity, an attributable issuer, a real key fingerprint, and a bounded
// short lifetime. It never accepts a lifetime outside the allowed window.
func (invitation Invitation) Validate() error {
	if !safeInvitationID.MatchString(invitation.ID) {
		return fmt.Errorf("%w: id", ErrInvalidInvitation)
	}
	if !safeLabel.MatchString(invitation.Label) {
		return fmt.Errorf("%w: label", ErrInvalidInvitation)
	}
	if !safeActor.MatchString(invitation.IssuedBy) {
		return fmt.Errorf("%w: issuer", ErrInvalidInvitation)
	}
	if !sha256Hex.MatchString(invitation.KeyFingerprint) {
		return fmt.Errorf("%w: key fingerprint", ErrInvalidInvitation)
	}
	if !invitation.SingleUse {
		return fmt.Errorf("%w: invitation must be single-use", ErrInvalidInvitation)
	}
	if invitation.IssuedAt.IsZero() || !invitation.ExpiresAt.After(invitation.IssuedAt) {
		return fmt.Errorf("%w: invitation must be short-lived", ErrInvalidInvitation)
	}
	lifetime := invitation.ExpiresAt.Sub(invitation.IssuedAt)
	if lifetime < MinInvitationTTL || lifetime > MaxInvitationTTL {
		return fmt.Errorf("%w: invitation lifetime is out of bounds", ErrInvalidInvitation)
	}
	if invitation.ConsumedAt != nil && invitation.ConsumedAt.Before(invitation.IssuedAt) {
		return fmt.Errorf("%w: consumed before issue", ErrInvalidInvitation)
	}
	if invitation.RevokedAt != nil && invitation.RevokedAt.Before(invitation.IssuedAt) {
		return fmt.Errorf("%w: revoked before issue", ErrInvalidInvitation)
	}
	return nil
}

// State reports the derived lifecycle state at a point in time. Revocation and
// consumption are terminal facts; an unused invitation past expiry is expired.
func (invitation Invitation) State(now time.Time) InvitationState {
	switch {
	case invitation.RevokedAt != nil:
		return StateRevoked
	case invitation.ConsumedAt != nil:
		return StateConsumed
	case !now.UTC().Before(invitation.ExpiresAt):
		return StateExpired
	default:
		return StatePending
	}
}

// Redeem consumes the single-use invitation, returning the updated record. It
// fails clearly when the invitation was already used, was revoked, or has
// expired — the fail-safe outcomes a joining device must be told apart.
func (invitation Invitation) Redeem(now time.Time) (Invitation, error) {
	if err := invitation.Validate(); err != nil {
		return Invitation{}, err
	}
	switch invitation.State(now) {
	case StateRevoked:
		return Invitation{}, ErrInvitationRevoked
	case StateConsumed:
		return Invitation{}, ErrInvitationUsed
	case StateExpired:
		return Invitation{}, ErrInvitationExpired
	}
	consumedAt := now.UTC()
	invitation.ConsumedAt = &consumedAt
	return invitation, nil
}

// RedeemWithKey redeems only when the presented join key matches the recorded
// fingerprint in constant time, so a malformed or wrong key fails as clearly as
// an expired one and never leaks whether the fingerprint was close.
func (invitation Invitation) RedeemWithKey(joinKey string, now time.Time) (Invitation, error) {
	if err := invitation.Validate(); err != nil {
		return Invitation{}, err
	}
	presented := Fingerprint(joinKey)
	if subtle.ConstantTimeCompare([]byte(presented), []byte(invitation.KeyFingerprint)) != 1 {
		return Invitation{}, fmt.Errorf("%w: key does not match", ErrInvalidInvitation)
	}
	return invitation.Redeem(now)
}

// Revoke marks a still-pending invitation revoked. Revoking an invitation that
// was already consumed or already revoked fails clearly rather than silently
// re-revoking.
func (invitation Invitation) Revoke(now time.Time) (Invitation, error) {
	if err := invitation.Validate(); err != nil {
		return Invitation{}, err
	}
	switch invitation.State(now) {
	case StateRevoked:
		return Invitation{}, ErrInvitationRevoked
	case StateConsumed:
		return Invitation{}, ErrInvitationUsed
	}
	revokedAt := now.UTC()
	invitation.RevokedAt = &revokedAt
	return invitation, nil
}

// Marshal returns the canonical secret-free JSON for a validated invitation.
func (invitation Invitation) Marshal() (string, error) {
	if err := invitation.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(invitation)
	if err != nil {
		return "", fmt.Errorf("marshal invitation: %w", err)
	}
	return string(encoded), nil
}

// ParseInvitation decodes and validates a stored invitation, rejecting unknown
// fields so a malformed record fails safely.
func ParseInvitation(encoded string) (Invitation, error) {
	var invitation Invitation
	decoder := json.NewDecoder(strings.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&invitation); err != nil {
		return Invitation{}, fmt.Errorf("%w: json", ErrInvalidInvitation)
	}
	if err := invitation.Validate(); err != nil {
		return Invitation{}, err
	}
	return invitation, nil
}
