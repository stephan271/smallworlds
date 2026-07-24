package operatordevice

import (
	"errors"
	"testing"
)

func devices() []Device {
	return []Device{
		{StableID: "node-owner-1", Hostname: "alice-laptop", OwnerAccess: true, Self: true, Online: true},
		{StableID: "node-owner-2", Hostname: "alice-desktop", OwnerAccess: true, Online: true},
		{StableID: "node-observer-1", Hostname: "bob-tablet", OwnerAccess: false, Online: false},
	}
}

func TestAssessRevocationRecordsAffectedIdentity(t *testing.T) {
	assessment, err := AssessRevocation(devices(), "node-observer-1")
	if err != nil {
		t.Fatalf("AssessRevocation: %v", err)
	}
	if assessment.AffectedStableID != "node-observer-1" {
		t.Fatalf("affected id = %q, want node-observer-1", assessment.AffectedStableID)
	}
	if assessment.LockoutRisk {
		t.Fatal("revoking a non-owner device must not be a lockout risk")
	}
	if !assessment.AlternativeOwnerAccess || assessment.RemainingOwnerDevices != 2 {
		t.Fatalf("remaining owner access = %d (%v), want 2 and true", assessment.RemainingOwnerDevices, assessment.AlternativeOwnerAccess)
	}
}

func TestAssessRevocationLastOwnerDeviceIsLockout(t *testing.T) {
	only := []Device{
		{StableID: "node-owner-1", Hostname: "alice-laptop", OwnerAccess: true, Online: true},
		{StableID: "node-observer-1", Hostname: "bob-tablet", OwnerAccess: false, Online: true},
	}
	assessment, err := AssessRevocation(only, "node-owner-1")
	if err != nil {
		t.Fatalf("AssessRevocation: %v", err)
	}
	if !assessment.LockoutRisk || assessment.LockoutReason != LockoutLastOwnerDevice {
		t.Fatalf("lockout = %v/%q, want true/last-owner-device", assessment.LockoutRisk, assessment.LockoutReason)
	}
	if assessment.AlternativeOwnerAccess || assessment.RemainingOwnerDevices != 0 {
		t.Fatalf("alternative access = %v (%d), want none", assessment.AlternativeOwnerAccess, assessment.RemainingOwnerDevices)
	}
}

func TestAssessRevocationSelfRevocationLabeled(t *testing.T) {
	// node-owner-1 is Self, and another owner device remains, so it is not a
	// last-owner lockout but is still flagged as a self-revocation.
	assessment, err := AssessRevocation(devices(), "node-owner-1")
	if err != nil {
		t.Fatalf("AssessRevocation: %v", err)
	}
	if !assessment.LockoutRisk || assessment.LockoutReason != LockoutSelfRevocation {
		t.Fatalf("lockout = %v/%q, want true/self-revocation", assessment.LockoutRisk, assessment.LockoutReason)
	}
	if !assessment.AlternativeOwnerAccess || assessment.RemainingOwnerDevices != 1 {
		t.Fatalf("remaining owner access = %d, want 1", assessment.RemainingOwnerDevices)
	}
}

func TestAssessRevocationLastOwnerDominatesSelf(t *testing.T) {
	// The only owner device is also Self: the graver last-owner-device reason wins.
	only := []Device{{StableID: "node-owner-1", Hostname: "alice-laptop", OwnerAccess: true, Self: true, Online: true}}
	assessment, err := AssessRevocation(only, "node-owner-1")
	if err != nil {
		t.Fatalf("AssessRevocation: %v", err)
	}
	if assessment.LockoutReason != LockoutLastOwnerDevice {
		t.Fatalf("reason = %q, want last-owner-device to dominate self", assessment.LockoutReason)
	}
}

func TestAssessRevocationUnknownTarget(t *testing.T) {
	if _, err := AssessRevocation(devices(), "node-does-not-exist"); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("unknown target err = %v, want ErrDeviceNotFound", err)
	}
}

func TestAssessRevocationRejectsBadInventory(t *testing.T) {
	dup := []Device{
		{StableID: "node-1", Hostname: "a", OwnerAccess: true},
		{StableID: "node-1", Hostname: "b", OwnerAccess: false},
	}
	if _, err := AssessRevocation(dup, "node-1"); !errors.Is(err, ErrInvalidDevice) {
		t.Fatalf("duplicate id err = %v, want ErrInvalidDevice", err)
	}
	if _, err := AssessRevocation(devices(), "bad id!"); !errors.Is(err, ErrInvalidDevice) {
		t.Fatalf("bad target id err = %v, want ErrInvalidDevice", err)
	}
	malformed := []Device{{StableID: "node-1", Hostname: "bad_host!"}}
	if _, err := AssessRevocation(malformed, "node-1"); !errors.Is(err, ErrInvalidDevice) {
		t.Fatalf("malformed device err = %v, want ErrInvalidDevice", err)
	}
}
