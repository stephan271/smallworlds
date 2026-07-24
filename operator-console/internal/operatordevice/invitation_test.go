package operatordevice

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)

func mustIssue(t *testing.T, ttl time.Duration) Invitation {
	t.Helper()
	inv, err := IssueInvitation("inv-1", "alice-laptop", "alice", Fingerprint("join-key-abc"), testNow, ttl)
	if err != nil {
		t.Fatalf("IssueInvitation: %v", err)
	}
	return inv
}

func TestIssueInvitationShortLivedSingleUseAttributable(t *testing.T) {
	inv := mustIssue(t, DefaultInvitationTTL)
	if !inv.SingleUse {
		t.Fatal("invitation must be single-use")
	}
	if inv.IssuedBy != "alice" {
		t.Fatalf("issuedBy = %q, want alice (attributable)", inv.IssuedBy)
	}
	if got := inv.ExpiresAt.Sub(inv.IssuedAt); got != DefaultInvitationTTL {
		t.Fatalf("lifetime = %v, want %v", got, DefaultInvitationTTL)
	}
	if inv.State(testNow) != StatePending {
		t.Fatalf("state = %q, want pending", inv.State(testNow))
	}
}

func TestInvitationCarriesNoSecret(t *testing.T) {
	inv := mustIssue(t, DefaultInvitationTTL)
	encoded, err := inv.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// The record may carry a fingerprint but never the join key itself.
	if strings.Contains(encoded, "join-key-abc") {
		t.Fatalf("serialized invitation leaked the join key: %s", encoded)
	}
	if !sha256Hex.MatchString(inv.KeyFingerprint) {
		t.Fatalf("key fingerprint = %q, want a sha256 digest", inv.KeyFingerprint)
	}
}

func TestInvitationTTLClamped(t *testing.T) {
	// Over the max is clamped down, not rejected.
	long, err := IssueInvitation("inv-2", "dev", "alice", Fingerprint("k"), testNow, 10*time.Hour)
	if err != nil {
		t.Fatalf("IssueInvitation(long): %v", err)
	}
	if got := long.ExpiresAt.Sub(long.IssuedAt); got != MaxInvitationTTL {
		t.Fatalf("clamped lifetime = %v, want %v", got, MaxInvitationTTL)
	}
	// Under the min is clamped up.
	short, err := IssueInvitation("inv-3", "dev", "alice", Fingerprint("k"), testNow, time.Second)
	if err != nil {
		t.Fatalf("IssueInvitation(short): %v", err)
	}
	if got := short.ExpiresAt.Sub(short.IssuedAt); got != MinInvitationTTL {
		t.Fatalf("clamped lifetime = %v, want %v", got, MinInvitationTTL)
	}
}

func TestRedeemSingleUse(t *testing.T) {
	inv := mustIssue(t, DefaultInvitationTTL)
	consumed, err := inv.Redeem(testNow.Add(time.Minute))
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if consumed.State(testNow.Add(time.Minute)) != StateConsumed {
		t.Fatalf("state = %q, want consumed", consumed.State(testNow.Add(time.Minute)))
	}
	// Reusing a consumed invitation fails clearly.
	if _, err := consumed.Redeem(testNow.Add(2 * time.Minute)); !errors.Is(err, ErrInvitationUsed) {
		t.Fatalf("reuse err = %v, want ErrInvitationUsed", err)
	}
}

func TestRedeemExpiredFailsClearly(t *testing.T) {
	inv := mustIssue(t, DefaultInvitationTTL)
	if _, err := inv.Redeem(inv.ExpiresAt); !errors.Is(err, ErrInvitationExpired) {
		t.Fatalf("expired redeem err = %v, want ErrInvitationExpired", err)
	}
}

func TestRevokedInvitationFailsClosed(t *testing.T) {
	inv := mustIssue(t, DefaultInvitationTTL)
	revoked, err := inv.Revoke(testNow.Add(time.Minute))
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if revoked.State(testNow.Add(time.Minute)) != StateRevoked {
		t.Fatalf("state = %q, want revoked", revoked.State(testNow.Add(time.Minute)))
	}
	if _, err := revoked.Redeem(testNow.Add(2 * time.Minute)); !errors.Is(err, ErrInvitationRevoked) {
		t.Fatalf("redeem-after-revoke err = %v, want ErrInvitationRevoked", err)
	}
	// Double revoke fails clearly too.
	if _, err := revoked.Revoke(testNow.Add(3 * time.Minute)); !errors.Is(err, ErrInvitationRevoked) {
		t.Fatalf("double revoke err = %v, want ErrInvitationRevoked", err)
	}
}

func TestRedeemWithWrongKeyFails(t *testing.T) {
	inv := mustIssue(t, DefaultInvitationTTL)
	if _, err := inv.RedeemWithKey("wrong-key", testNow.Add(time.Minute)); !errors.Is(err, ErrInvalidInvitation) {
		t.Fatalf("wrong-key err = %v, want ErrInvalidInvitation", err)
	}
	if _, err := inv.RedeemWithKey("join-key-abc", testNow.Add(time.Minute)); err != nil {
		t.Fatalf("correct-key redeem: %v", err)
	}
}

func TestParseInvitationRejectsMalformed(t *testing.T) {
	// Unknown field.
	if _, err := ParseInvitation(`{"id":"x","extra":1}`); !errors.Is(err, ErrInvalidInvitation) {
		t.Fatalf("unknown-field err = %v, want ErrInvalidInvitation", err)
	}
	// Structurally invalid (missing fingerprint) round-trips through Validate.
	bad := Invitation{ID: "x", Label: "d", IssuedBy: "alice", SingleUse: true, IssuedAt: testNow, ExpiresAt: testNow.Add(time.Hour)}
	if err := bad.Validate(); !errors.Is(err, ErrInvalidInvitation) {
		t.Fatalf("missing-fingerprint err = %v, want ErrInvalidInvitation", err)
	}
	// A valid record round-trips.
	inv := mustIssue(t, DefaultInvitationTTL)
	encoded, _ := inv.Marshal()
	if _, err := ParseInvitation(encoded); err != nil {
		t.Fatalf("round-trip parse: %v", err)
	}
}

func TestValidateRejectsUnboundedLifetime(t *testing.T) {
	inv := mustIssue(t, DefaultInvitationTTL)
	inv.ExpiresAt = inv.IssuedAt.Add(2 * MaxInvitationTTL) // hand-tampered long lifetime
	if err := inv.Validate(); !errors.Is(err, ErrInvalidInvitation) {
		t.Fatalf("unbounded lifetime err = %v, want ErrInvalidInvitation", err)
	}
}
