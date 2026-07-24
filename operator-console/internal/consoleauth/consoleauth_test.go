package consoleauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAuthorizeDefaultDeny(t *testing.T) {
	// A user without any Console Role is denied every permission (criterion 1).
	for _, permission := range []Permission{PermissionObserve, PermissionPropose, PermissionAdminister} {
		if err := Authorize(RoleNone, permission); !errors.Is(err, ErrNoConsoleRole) {
			t.Errorf("Authorize(none, %q) = %v, want ErrNoConsoleRole", permission, err)
		}
	}
}

func TestAuthorizePerRole(t *testing.T) {
	// Criterion 2: Observers cannot mutate; Operators can propose; Owners can
	// administer. Higher roles include lower permissions.
	tests := []struct {
		role       ConsoleRole
		permission Permission
		wantErr    error
	}{
		{RoleObserver, PermissionObserve, nil},
		{RoleObserver, PermissionPropose, ErrForbidden},
		{RoleObserver, PermissionAdminister, ErrForbidden},
		{RoleOperator, PermissionObserve, nil},
		{RoleOperator, PermissionPropose, nil},
		{RoleOperator, PermissionAdminister, ErrForbidden},
		{RoleOwner, PermissionObserve, nil},
		{RoleOwner, PermissionPropose, nil},
		{RoleOwner, PermissionAdminister, nil},
	}
	for _, test := range tests {
		err := Authorize(test.role, test.permission)
		if test.wantErr == nil && err != nil {
			t.Errorf("Authorize(%q, %q) = %v, want nil", test.role, test.permission, err)
		}
		if test.wantErr != nil && !errors.Is(err, test.wantErr) {
			t.Errorf("Authorize(%q, %q) = %v, want %v", test.role, test.permission, err, test.wantErr)
		}
	}
}

func TestHighestRole(t *testing.T) {
	tests := []struct {
		names []string
		want  ConsoleRole
	}{
		{nil, RoleNone},
		{[]string{"uma_authorization", "default-roles"}, RoleNone},
		{[]string{"observer"}, RoleObserver},
		{[]string{"Observer", "OPERATOR"}, RoleOperator},
		{[]string{"observer", "owner", "operator"}, RoleOwner},
		{[]string{" owner "}, RoleOwner},
	}
	for _, test := range tests {
		if got := HighestRole(test.names); got != test.want {
			t.Errorf("HighestRole(%v) = %q, want %q", test.names, got, test.want)
		}
	}
}

// fixedReader yields deterministic bytes so generated tokens are reproducible.
type fixedReader struct {
	value byte
}

func (r fixedReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.value
	}
	return len(p), nil
}

func TestNewAuthRequestPKCE(t *testing.T) {
	request, err := NewAuthRequest("https://id.example.test/realms/sw/protocol/openid-connect/auth", "operator-console", "https://console.example.test/callback", "email", fixedReader{value: 0x2a})
	if err != nil {
		t.Fatalf("NewAuthRequest: %v", err)
	}

	// The code challenge must be the S256 of the verifier.
	sum := sha256.Sum256([]byte(request.CodeVerifier))
	wantChallenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if request.CodeChallenge != wantChallenge {
		t.Errorf("code challenge = %q, want %q", request.CodeChallenge, wantChallenge)
	}
	// RFC 7636: the verifier is at least 43 characters.
	if len(request.CodeVerifier) < 43 {
		t.Errorf("code verifier too short: %d", len(request.CodeVerifier))
	}

	parsed, err := url.Parse(request.AuthorizationURL)
	if err != nil {
		t.Fatalf("parse authorization url: %v", err)
	}
	query := parsed.Query()
	for key, want := range map[string]string{
		"response_type":         "code",
		"client_id":             "operator-console",
		"code_challenge_method": "S256",
		"code_challenge":        request.CodeChallenge,
		"state":                 request.State,
		"nonce":                 request.Nonce,
	} {
		if got := query.Get(key); got != want {
			t.Errorf("query %q = %q, want %q", key, got, want)
		}
	}
	if scope := query.Get("scope"); !strings.HasPrefix(scope, "openid") || !strings.Contains(scope, "email") {
		t.Errorf("scope = %q, want openid plus email", scope)
	}
}

func TestNewAuthRequestRejectsBadEndpoint(t *testing.T) {
	if _, err := NewAuthRequest("not a url", "c", "https://cb", "", fixedReader{value: 1}); err == nil {
		t.Fatal("expected error for invalid authorization endpoint")
	}
}

func TestVerifyState(t *testing.T) {
	request := AuthRequest{State: "expected-state"}
	if err := request.VerifyState("expected-state"); err != nil {
		t.Errorf("VerifyState(match) = %v, want nil", err)
	}
	if err := request.VerifyState("forged"); !errors.Is(err, ErrStateMismatch) {
		t.Errorf("VerifyState(mismatch) = %v, want ErrStateMismatch", err)
	}
	empty := AuthRequest{}
	if err := empty.VerifyState(""); !errors.Is(err, ErrStateMismatch) {
		t.Errorf("VerifyState(empty) = %v, want ErrStateMismatch", err)
	}
}

var validNow = time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)

func validClaims() Claims {
	return Claims{
		Issuer:            "https://id.example.test/realms/sw",
		Audience:          []string{"operator-console"},
		Nonce:             "login-nonce",
		Subject:           "user-123",
		PreferredUsername: "alice",
		ExpiresAt:         validNow.Add(5 * time.Minute),
		IssuedAt:          validNow.Add(-time.Minute),
		ClientRoles:       map[string][]string{"operator-console": {"operator"}},
	}
}

func validExpectations() Expectations {
	return Expectations{
		Issuer:   "https://id.example.test/realms/sw",
		Audience: "operator-console",
		Nonce:    "login-nonce",
		Now:      validNow,
		Leeway:   30 * time.Second,
	}
}

func TestValidateClaims(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Claims)
		wantErr error
	}{
		{"valid", func(*Claims) {}, nil},
		{"wrong issuer", func(c *Claims) { c.Issuer = "https://evil.test" }, ErrInvalidIssuer},
		{"missing audience", func(c *Claims) { c.Audience = []string{"other-client"} }, ErrInvalidAudience},
		{
			name:    "multi-audience without matching azp",
			mutate:  func(c *Claims) { c.Audience = []string{"operator-console", "extra"}; c.AuthorizedParty = "extra" },
			wantErr: ErrInvalidAudience,
		},
		{"wrong nonce", func(c *Claims) { c.Nonce = "replayed" }, ErrInvalidNonce},
		{"expired", func(c *Claims) { c.ExpiresAt = validNow.Add(-time.Hour) }, ErrTokenExpired},
		{"not yet valid", func(c *Claims) { c.NotBefore = validNow.Add(time.Hour) }, ErrTokenNotYetValid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			claims := validClaims()
			test.mutate(&claims)
			err := ValidateClaims(claims, validExpectations())
			if test.wantErr == nil && err != nil {
				t.Fatalf("ValidateClaims = %v, want nil", err)
			}
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("ValidateClaims = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestValidateClaimsMultiAudienceWithMatchingAzp(t *testing.T) {
	claims := validClaims()
	claims.Audience = []string{"operator-console", "account"}
	claims.AuthorizedParty = "operator-console"
	if err := ValidateClaims(claims, validExpectations()); err != nil {
		t.Fatalf("ValidateClaims = %v, want nil for matching azp", err)
	}
}

// fakeExchanger returns preconfigured claims or an error without any network.
type fakeExchanger struct {
	claims      Claims
	err         error
	gotVerifier string
}

func (e *fakeExchanger) Exchange(_ context.Context, _ string, codeVerifier string) (Claims, error) {
	e.gotVerifier = codeVerifier
	return e.claims, e.err
}

func TestCompleteLoginSuccess(t *testing.T) {
	request := AuthRequest{State: "state-abc", Nonce: "login-nonce", CodeVerifier: "verifier-xyz"}
	exchanger := &fakeExchanger{claims: validClaims()}

	claims, role, err := CompleteLogin(context.Background(), exchanger, request, "auth-code", "state-abc", validExpectations())
	if err != nil {
		t.Fatalf("CompleteLogin = %v, want nil", err)
	}
	if role != RoleOperator {
		t.Fatalf("role = %q, want operator", role)
	}
	if claims.Subject != "user-123" {
		t.Fatalf("subject = %q, want user-123", claims.Subject)
	}
	if exchanger.gotVerifier != "verifier-xyz" {
		t.Fatalf("exchanger verifier = %q, want the request's PKCE verifier", exchanger.gotVerifier)
	}
}

func TestCompleteLoginDefaultDenyNoRole(t *testing.T) {
	request := AuthRequest{State: "state-abc", Nonce: "login-nonce", CodeVerifier: "v"}
	claims := validClaims()
	claims.ClientRoles = nil // authenticated but no console role
	exchanger := &fakeExchanger{claims: claims}

	_, role, err := CompleteLogin(context.Background(), exchanger, request, "code", "state-abc", validExpectations())
	if !errors.Is(err, ErrNoConsoleRole) {
		t.Fatalf("CompleteLogin = %v, want ErrNoConsoleRole", err)
	}
	if role != RoleNone {
		t.Fatalf("role = %q, want none", role)
	}
}

func TestCompleteLoginRejectsForgedState(t *testing.T) {
	request := AuthRequest{State: "state-abc", Nonce: "login-nonce", CodeVerifier: "v"}
	exchanger := &fakeExchanger{claims: validClaims()}

	if _, _, err := CompleteLogin(context.Background(), exchanger, request, "code", "forged", validExpectations()); !errors.Is(err, ErrStateMismatch) {
		t.Fatalf("CompleteLogin = %v, want ErrStateMismatch", err)
	}
	if exchanger.gotVerifier != "" {
		t.Fatal("token exchange must not run when state does not match")
	}
}

func TestCompleteLoginRejectsReplayedNonce(t *testing.T) {
	request := AuthRequest{State: "state-abc", Nonce: "fresh-nonce", CodeVerifier: "v"}
	claims := validClaims()
	claims.Nonce = "stale-nonce" // token bound to a different login
	exchanger := &fakeExchanger{claims: claims}

	if _, _, err := CompleteLogin(context.Background(), exchanger, request, "code", "state-abc", validExpectations()); !errors.Is(err, ErrInvalidNonce) {
		t.Fatalf("CompleteLogin = %v, want ErrInvalidNonce", err)
	}
}

func TestCompleteLoginPropagatesExchangeError(t *testing.T) {
	request := AuthRequest{State: "state-abc", Nonce: "login-nonce", CodeVerifier: "v"}
	exchangeErr := errors.New("token endpoint unavailable")
	exchanger := &fakeExchanger{err: exchangeErr}

	if _, _, err := CompleteLogin(context.Background(), exchanger, request, "code", "state-abc", validExpectations()); !errors.Is(err, exchangeErr) {
		t.Fatalf("CompleteLogin = %v, want the exchange error", err)
	}
}
