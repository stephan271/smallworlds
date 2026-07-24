package operatordevice

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Errors returned while assessing a device revocation.
var (
	// ErrInvalidDevice is returned when a device record is structurally malformed.
	ErrInvalidDevice = errors.New("operator device record is invalid")
	// ErrDeviceNotFound is returned when the revocation target is not among the
	// current devices — so a revocation can never act on an unknown identity.
	ErrDeviceNotFound = errors.New("operator device not found")
)

// Lockout reason codes label why a revocation risks locking the community out of
// administration. They are stable strings the console/UI localizes.
const (
	// LockoutNone means the revocation carries no lockout risk.
	LockoutNone = ""
	// LockoutLastOwnerDevice means the target is the only remaining device with
	// Owner access — removing it leaves no accountable way back in.
	LockoutLastOwnerDevice = "last-owner-device"
	// LockoutSelfRevocation means the target is the device the Owner is acting
	// from, so executing it may cut off the current session.
	LockoutSelfRevocation = "self-revocation"
)

var safeStableID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:._-]{0,127}$`)

// Device is the secret-free view of one Operator Device as reported by the
// Private Network coordination server. The StableID is the identity revocation
// targets: it survives a hostname change, so revoking acts on the device, not a
// mutable label.
type Device struct {
	StableID string `json:"stableId"`
	Hostname string `json:"hostname"`
	Label    string `json:"label,omitempty"`
	// OwnerAccess reports whether this device carries Console Owner access — the
	// devices whose count determines lockout risk.
	OwnerAccess bool `json:"ownerAccess"`
	// Self marks the device the requesting Owner is acting from, so a
	// self-revocation can be labeled distinctly.
	Self     bool       `json:"self"`
	Online   bool       `json:"online"`
	LastSeen *time.Time `json:"lastSeen,omitempty"`
}

// Validate enforces a safe stable identity and hostname.
func (device Device) Validate() error {
	if !safeStableID.MatchString(device.StableID) {
		return fmt.Errorf("%w: stable id", ErrInvalidDevice)
	}
	if !isHostname(strings.ToLower(strings.TrimSpace(device.Hostname))) {
		return fmt.Errorf("%w: hostname", ErrInvalidDevice)
	}
	return nil
}

// RevocationAssessment is the inspected result of planning a device revocation:
// the affected stable identity, whether removing it risks lockout and why, and
// how much alternative Owner access would remain afterward.
type RevocationAssessment struct {
	// AffectedStableID is the stable identity the plan binds and the executor
	// must remove — and only that identity.
	AffectedStableID string `json:"affectedStableId"`
	Target           Device `json:"target"`
	// RemainingOwnerDevices is the number of other devices retaining Owner access
	// after this revocation.
	RemainingOwnerDevices int `json:"remainingOwnerDevices"`
	// AlternativeOwnerAccess is true when at least one other Owner device remains.
	AlternativeOwnerAccess bool `json:"alternativeOwnerAccess"`
	// TotalDevices is the size of the inspected inventory, for context.
	TotalDevices int `json:"totalDevices"`
	// LockoutRisk flags a revocation that could remove the community's last
	// accountable path in, or cut off the acting Owner's own device.
	LockoutRisk bool `json:"lockoutRisk"`
	// LockoutReason is one of the Lockout* codes, empty when there is no risk.
	LockoutReason string `json:"lockoutReason,omitempty"`
}

// AssessRevocation inspects the current device inventory and reports the effect
// of revoking the given stable identity: the alternative Owner access that would
// remain, and whether the revocation risks a lockout (removing the last Owner
// device, or the acting Owner's own device). It never decides for the operator —
// it labels the risk and leaves approval to them.
func AssessRevocation(devices []Device, targetStableID string) (RevocationAssessment, error) {
	targetStableID = strings.TrimSpace(targetStableID)
	if !safeStableID.MatchString(targetStableID) {
		return RevocationAssessment{}, fmt.Errorf("%w: target stable id", ErrInvalidDevice)
	}

	var target *Device
	remainingOwnerDevices := 0
	seen := make(map[string]bool, len(devices))
	for index := range devices {
		device := devices[index]
		if err := device.Validate(); err != nil {
			return RevocationAssessment{}, err
		}
		if seen[device.StableID] {
			return RevocationAssessment{}, fmt.Errorf("%w: duplicate stable id %q", ErrInvalidDevice, device.StableID)
		}
		seen[device.StableID] = true
		if device.StableID == targetStableID {
			target = &devices[index]
			continue
		}
		if device.OwnerAccess {
			remainingOwnerDevices++
		}
	}
	if target == nil {
		return RevocationAssessment{}, ErrDeviceNotFound
	}

	assessment := RevocationAssessment{
		AffectedStableID:       target.StableID,
		Target:                 *target,
		RemainingOwnerDevices:  remainingOwnerDevices,
		AlternativeOwnerAccess: remainingOwnerDevices > 0,
		TotalDevices:           len(devices),
	}
	// Removing the last Owner-access device dominates: it can leave the community
	// with no accountable path back in. A self-revocation is a distinct, lesser
	// warning about cutting off the current session.
	switch {
	case target.OwnerAccess && remainingOwnerDevices == 0:
		assessment.LockoutRisk = true
		assessment.LockoutReason = LockoutLastOwnerDevice
	case target.Self:
		assessment.LockoutRisk = true
		assessment.LockoutReason = LockoutSelfRevocation
	}
	return assessment, nil
}
