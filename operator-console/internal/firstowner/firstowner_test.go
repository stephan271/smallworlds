package firstowner_test

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/firstowner"
	"github.com/stephan271/smallworlds/operator-console/internal/webauthntest"
)

func TestRegistrationPermanentlyDisablesBootstrapGrant(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	state, err := firstowner.Plan(now)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if state.BootstrapGrantDisabled || state.OwnerRegistered {
		t.Fatal("bootstrap grant disabled before registration")
	}
	if !state.ClaimValid(now) {
		t.Fatal("fresh claim should be valid")
	}

	registered, err := state.RegisterOwner(now, "credential-1")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if !registered.OwnerRegistered || !registered.BootstrapGrantDisabled || !registered.Claim.Used {
		t.Fatalf("registration did not disable the grant: %#v", registered)
	}

	// Irreversible: a disabled grant can never be registered again.
	if _, err := registered.RegisterOwner(now, "credential-2"); !errors.Is(err, firstowner.ErrGrantAlreadyDisabled) {
		t.Fatalf("second registration error = %v, want already-disabled", err)
	}

	encoded, _ := registered.Marshal()
	if strings.Contains(encoded, "secret") || strings.Contains(encoded, "PRIVATE") {
		t.Fatalf("state leaked secret material: %s", encoded)
	}
}

func TestRegisterOwnerRejectsExpiredClaim(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	state, err := firstowner.Plan(now)
	if err != nil {
		t.Fatal(err)
	}
	later := now.Add(30 * time.Minute)
	if state.ClaimValid(later) {
		t.Fatal("claim should have expired")
	}
	if _, err := state.RegisterOwner(later, "credential-1"); !errors.Is(err, firstowner.ErrClaimExpired) {
		t.Fatalf("expired registration error = %v, want expired", err)
	}
}

func TestValidateRejectsInconsistentState(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	base, _ := firstowner.Plan(now)
	for name, mutate := range map[string]func(*firstowner.State){
		"disabled without registration": func(state *firstowner.State) { state.BootstrapGrantDisabled = true },
		"registered without disable":    func(state *firstowner.State) { state.OwnerRegistered = true },
		"long-lived claim":              func(state *firstowner.State) { state.Claim.ExpiresAt = state.Claim.IssuedAt.Add(time.Hour) },
		"missing challenge":             func(state *firstowner.State) { state.Claim.Challenge = "" },
	} {
		t.Run(name, func(t *testing.T) {
			state := base
			mutate(&state)
			if err := state.Validate(); err == nil {
				t.Fatal("inconsistent state accepted")
			}
		})
	}
}

func challenge(text string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(text))
}

func webAuthnVerifier() firstowner.WebAuthnPasskeyVerifier {
	return firstowner.NewWebAuthnPasskeyVerifier("127.0.0.1", firstowner.LoopbackOriginAllowed)
}

func TestWebAuthnVerifierAcceptsValidRegistrations(t *testing.T) {
	verifier := webAuthnVerifier()
	for _, format := range []string{"none", "packed"} {
		t.Run(format, func(t *testing.T) {
			issued := challenge("first-owner-registration")
			registration, err := webauthntest.Registration(webauthntest.Options{Challenge: issued, Format: format})
			if err != nil {
				t.Fatal(err)
			}
			credentialID, err := verifier.Verify(context.Background(), issued, registration)
			if err != nil {
				t.Fatalf("valid %s registration rejected: %v", format, err)
			}
			if credentialID != registration.CredentialID {
				t.Fatalf("credential id = %q, want %q", credentialID, registration.CredentialID)
			}
		})
	}
}

func TestWebAuthnVerifierRejectsTamperedRegistrations(t *testing.T) {
	verifier := webAuthnVerifier()
	issued := challenge("first-owner-registration")

	if _, err := verifier.Verify(context.Background(), issued, firstowner.Registration{}); !errors.Is(err, firstowner.ErrInvalidRegistration) {
		t.Fatalf("empty registration error = %v", err)
	}

	challengeMismatch, _ := webauthntest.Registration(webauthntest.Options{Challenge: issued, ChallengeForClientData: challenge("different")})
	if _, err := verifier.Verify(context.Background(), issued, challengeMismatch); !errors.Is(err, firstowner.ErrChallengeMismatch) {
		t.Fatalf("challenge mismatch error = %v", err)
	}

	for name, options := range map[string]webauthntest.Options{
		"foreign origin":  {Challenge: issued, Origin: "https://evil.example"},
		"foreign rp id":   {Challenge: issued, RPIDForHash: "evil.example"},
		"no user present": {Challenge: issued, NoUserPresent: true},
		"unsupported fmt": {Challenge: issued, Format: "tpm"},
	} {
		t.Run(name, func(t *testing.T) {
			registration, err := webauthntest.Registration(options)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := verifier.Verify(context.Background(), issued, registration); !errors.Is(err, firstowner.ErrInvalidRegistration) {
				t.Fatalf("tampered registration (%s) accepted or wrong error: %v", name, err)
			}
		})
	}
}
