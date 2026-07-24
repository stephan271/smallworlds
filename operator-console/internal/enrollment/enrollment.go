// Package enrollment owns the two distinct tailnet identities of the LAN-only
// private administration handoff: a short-lived, single-use credential the
// Launcher Host uses to enroll once, and a separate, stable identity for the
// Private Gateway that survives pod restart or reschedule. Secret key material
// is generated here for Launcher Vault custody and never appears in a Reference.
package enrollment

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ErrInvalidReference is returned when an enrollment reference fails validation.
var ErrInvalidReference = errors.New("enrollment reference is invalid")

// ErrLauncherAlreadyUsed is returned when the single-use Launcher Host
// credential has already been consumed.
var ErrLauncherAlreadyUsed = errors.New("launcher enrollment credential already used")

// ErrLauncherExpired is returned when the short-lived Launcher Host credential
// has expired.
var ErrLauncherExpired = errors.New("launcher enrollment credential expired")

const (
	// LauncherHostRole names the short-lived, single-use enrollment credential.
	LauncherHostRole = "launcher-host"
	// GatewayRole names the stable Private Gateway identity.
	GatewayRole = "private-gateway"
	namespace   = "smallworlds"

	// launcherTTL is the short lifetime of the single-use Launcher credential.
	launcherTTL = 10 * time.Minute
	// maxLauncherTTL bounds any accepted Launcher credential lifetime.
	maxLauncherTTL = 15 * time.Minute
)

var safeDomain = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
var safeLabel = regexp.MustCompile(`^[a-z0-9-]{1,63}$`)

// Credential is the secret-free description of one tailnet identity.
type Credential struct {
	Role      string     `json:"role"`
	Namespace string     `json:"namespace"`
	Hostname  string     `json:"hostname"`
	SingleUse bool       `json:"singleUse"`
	Stable    bool       `json:"stable"`
	IssuedAt  time.Time  `json:"issuedAt"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	Used      bool       `json:"used"`
}

// Reference bundles the Launcher Host and Private Gateway credentials for a
// profile. It contains no key material.
type Reference struct {
	BaseDomain string     `json:"baseDomain"`
	Launcher   Credential `json:"launcher"`
	Gateway    Credential `json:"gateway"`
}

// Plan derives the two distinct credentials under a private base domain: a
// short-lived single-use Launcher Host credential and a stable Private Gateway
// identity. The gateway hostname matches the Private Network's gateway.
func Plan(baseDomain string, now time.Time) (Reference, error) {
	baseDomain = strings.ToLower(strings.TrimSpace(baseDomain))
	if !safeDomain.MatchString(baseDomain) || strings.Contains(baseDomain, "..") || !strings.Contains(baseDomain, ".") {
		return Reference{}, fmt.Errorf("%w: base domain", ErrInvalidReference)
	}
	issuedAt := now.UTC()
	expiresAt := issuedAt.Add(launcherTTL)
	reference := Reference{
		BaseDomain: baseDomain,
		Launcher: Credential{
			Role:      LauncherHostRole,
			Namespace: namespace,
			Hostname:  "launcher." + baseDomain,
			SingleUse: true,
			Stable:    false,
			IssuedAt:  issuedAt,
			ExpiresAt: &expiresAt,
		},
		Gateway: Credential{
			Role:      GatewayRole,
			Namespace: namespace,
			Hostname:  "gateway." + baseDomain,
			SingleUse: false,
			Stable:    true,
			IssuedAt:  issuedAt,
		},
	}
	if err := reference.Validate(); err != nil {
		return Reference{}, err
	}
	return reference, nil
}

// Validate enforces the distinct lifetimes of the two credentials: the Launcher
// credential is short-lived and single-use; the Gateway identity is stable with
// no expiry.
func (reference Reference) Validate() error {
	if !safeDomain.MatchString(reference.BaseDomain) || strings.Contains(reference.BaseDomain, "..") || !strings.Contains(reference.BaseDomain, ".") {
		return fmt.Errorf("%w: base domain", ErrInvalidReference)
	}
	launcher := reference.Launcher
	if launcher.Role != LauncherHostRole || !launcher.SingleUse || launcher.Stable {
		return fmt.Errorf("%w: launcher credential must be single-use and non-stable", ErrInvalidReference)
	}
	if launcher.ExpiresAt == nil || !launcher.ExpiresAt.After(launcher.IssuedAt) {
		return fmt.Errorf("%w: launcher credential must be short-lived", ErrInvalidReference)
	}
	if launcher.ExpiresAt.Sub(launcher.IssuedAt) > maxLauncherTTL {
		return fmt.Errorf("%w: launcher credential lifetime exceeds the bound", ErrInvalidReference)
	}
	if !isSubdomainOf(launcher.Hostname, reference.BaseDomain) {
		return fmt.Errorf("%w: launcher hostname", ErrInvalidReference)
	}
	gateway := reference.Gateway
	if gateway.Role != GatewayRole || gateway.SingleUse || !gateway.Stable {
		return fmt.Errorf("%w: gateway identity must be stable and not single-use", ErrInvalidReference)
	}
	if gateway.ExpiresAt != nil {
		return fmt.Errorf("%w: stable gateway identity must not expire", ErrInvalidReference)
	}
	if gateway.Hostname != "gateway."+reference.BaseDomain {
		return fmt.Errorf("%w: gateway hostname", ErrInvalidReference)
	}
	if !safeLabel.MatchString(launcher.Namespace) || !safeLabel.MatchString(gateway.Namespace) {
		return fmt.Errorf("%w: namespace", ErrInvalidReference)
	}
	return nil
}

// LauncherValid reports whether the single-use Launcher credential can still be
// consumed at the given time.
func (reference Reference) LauncherValid(now time.Time) bool {
	launcher := reference.Launcher
	return !launcher.Used && launcher.ExpiresAt != nil && now.UTC().Before(*launcher.ExpiresAt)
}

// ConsumeLauncher marks the single-use Launcher credential used, returning the
// updated reference. It fails if the credential was already used or has expired.
func (reference Reference) ConsumeLauncher(now time.Time) (Reference, error) {
	if reference.Launcher.Used {
		return Reference{}, ErrLauncherAlreadyUsed
	}
	if !reference.LauncherValid(now) {
		return Reference{}, ErrLauncherExpired
	}
	reference.Launcher.Used = true
	return reference, nil
}

// Marshal returns the canonical secret-free JSON for a validated reference.
func (reference Reference) Marshal() (string, error) {
	if err := reference.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(reference)
	if err != nil {
		return "", fmt.Errorf("marshal enrollment reference: %w", err)
	}
	return string(encoded), nil
}

// ParseReference decodes and validates a stored reference.
func ParseReference(encoded string) (Reference, error) {
	var reference Reference
	decoder := json.NewDecoder(strings.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reference); err != nil {
		return Reference{}, fmt.Errorf("%w: json", ErrInvalidReference)
	}
	if err := reference.Validate(); err != nil {
		return Reference{}, err
	}
	return reference, nil
}

// Digest returns a stable content digest of the reference.
func (reference Reference) Digest() (string, error) {
	encoded, err := reference.Marshal()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(encoded))
	return hex.EncodeToString(sum[:]), nil
}

// GenerateCredentialSecret returns fresh random tailnet credential material for
// Launcher Vault custody. It never appears in a Reference.
func GenerateCredentialSecret() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate enrollment secret: %w", err)
	}
	return base64.RawStdEncoding.EncodeToString(buffer), nil
}

func isSubdomainOf(host, baseDomain string) bool {
	if !safeDomain.MatchString(host) || strings.Contains(host, "..") {
		return false
	}
	return strings.HasSuffix(host, "."+baseDomain) && host != baseDomain
}
