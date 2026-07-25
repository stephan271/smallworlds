package hetzner

import (
	"strings"
	"testing"
)

const validTokenValue = "abcdefghij0123456789ABCDEFGHIJabcdefghij0123456789ABCDEFGHIJ0123"

func TestAssessToken(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		token      string
		probe      TokenProbe
		bound      string
		wantState  TokenState
		wantReason string
	}{
		{name: "valid read-write token", token: validTokenValue, probe: TokenProbe{ProjectID: "project-a", ReadAuthority: true, WriteAuthority: true}, wantState: TokenValid, wantReason: "token-validated"},
		{name: "malformed token never reaches the provider", token: "too-short", wantState: TokenMalformed, wantReason: "token-malformed"},
		{name: "rejected token", token: validTokenValue, probe: TokenProbe{Unauthorized: true}, wantState: TokenUnauthorized, wantReason: "token-rejected-by-provider"},
		{name: "rate limited probe is inconclusive", token: validTokenValue, probe: TokenProbe{RateLimited: true, ReadAuthority: true, WriteAuthority: true}, wantState: TokenInconclusive, wantReason: "token-check-rate-limited"},
		{name: "read-only token cannot provision", token: validTokenValue, probe: TokenProbe{ProjectID: "project-a", ReadAuthority: true}, wantState: TokenReadOnly, wantReason: "token-lacks-write-authority"},
		{name: "token for another project is refused", token: validTokenValue, probe: TokenProbe{ProjectID: "project-b", ReadAuthority: true, WriteAuthority: true}, bound: "project-a", wantState: TokenProjectMismatch, wantReason: "token-addresses-different-project"},
		{name: "same project rebinds cleanly", token: validTokenValue, probe: TokenProbe{ProjectID: "project-a", ReadAuthority: true, WriteAuthority: true}, bound: "project-a", wantState: TokenValid, wantReason: "token-validated"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			assessment := AssessToken(testCase.token, testCase.probe, testCase.bound)
			if assessment.State != testCase.wantState || assessment.ReasonKey != testCase.wantReason {
				t.Fatalf("got %s/%s, want %s/%s", assessment.State, assessment.ReasonKey, testCase.wantState, testCase.wantReason)
			}
			if assessment.Usable() != (testCase.wantState == TokenValid) {
				t.Fatalf("usable %v for state %s", assessment.Usable(), assessment.State)
			}
			if strings.Contains(assessment.Fingerprint, testCase.token) && assessment.Fingerprint != "" {
				t.Fatal("assessment leaked the token value")
			}
		})
	}
}

func TestTokenFingerprintIsStableAndShort(t *testing.T) {
	first, second := Fingerprint(validTokenValue), Fingerprint(validTokenValue)
	if first != second || len(first) != 12 {
		t.Fatalf("unstable fingerprint %q/%q", first, second)
	}
	if Fingerprint("  ") != "" {
		t.Fatal("blank value must have no fingerprint")
	}
	if Fingerprint(validTokenValue) == Fingerprint(validTokenValue[:63]+"X") {
		t.Fatal("different tokens must not share a fingerprint")
	}
}

func TestMissingAuthorityIsNamed(t *testing.T) {
	assessment := AssessToken(validTokenValue, TokenProbe{ProjectID: "project-a", ReadAuthority: true}, "")
	if len(assessment.MissingAuthority) != 1 || assessment.MissingAuthority[0] != "write" {
		t.Fatalf("missing authority %v", assessment.MissingAuthority)
	}
}
