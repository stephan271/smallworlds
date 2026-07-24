package firstowner_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/firstowner"
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

func TestStructuralPasskeyVerifierEnforcesChallengeBinding(t *testing.T) {
	verifier := firstowner.StructuralPasskeyVerifier{}
	registration := firstowner.Registration{CredentialID: "cred", PublicKey: "pub", Challenge: "abc"}
	if id, err := verifier.Verify(context.Background(), "abc", registration); err != nil || id != "cred" {
		t.Fatalf("valid registration rejected: id=%q err=%v", id, err)
	}
	if _, err := verifier.Verify(context.Background(), "different", registration); !errors.Is(err, firstowner.ErrChallengeMismatch) {
		t.Fatalf("challenge mismatch not detected: %v", err)
	}
	if _, err := verifier.Verify(context.Background(), "abc", firstowner.Registration{Challenge: "abc"}); !errors.Is(err, firstowner.ErrInvalidRegistration) {
		t.Fatalf("missing credential material not rejected: %v", err)
	}
}
