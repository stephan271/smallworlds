// Package consoleauth implements the in-cluster Operator Console's Keycloak
// OIDC authentication and server-side authorization (ADR 0011). It provides the
// pure, table-tested core: an Authorization Code + PKCE request with issued
// state and nonce, ID-token claims validation (issuer, audience/azp, nonce,
// expiry), mapping of Keycloak realm/client roles to the three Console Roles
// with default denial, and the role-to-permission authorization gate.
//
// The one step that must reach the network — exchanging the authorization code
// for a signature-verified ID token against Keycloak's JWKS — is an injectable
// TokenExchanger, so the login composition is deterministically testable without
// a live identity provider, mirroring the injectable-Verifier pattern used
// elsewhere in this module.
package consoleauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

// tokenBytes is the entropy, in bytes, of each generated state/nonce/verifier.
// 32 bytes base64url-encode to a 43-character PKCE verifier, the RFC 7636 floor.
const tokenBytes = 32

// AuthRequest is a pending Authorization Code + PKCE login. The launcher stores
// State, Nonce, and CodeVerifier server-side and redirects the browser to
// AuthorizationURL; the callback is validated against the stored values.
type AuthRequest struct {
	State            string `json:"state"`
	Nonce            string `json:"nonce"`
	CodeVerifier     string `json:"codeVerifier"`
	CodeChallenge    string `json:"codeChallenge"`
	AuthorizationURL string `json:"authorizationUrl"`
}

// NewAuthRequest builds an Authorization Code + PKCE (S256) request. It draws
// fresh state, nonce, and a PKCE code verifier from random, derives the S256
// code challenge, and assembles the authorization URL. The openid scope is
// always included.
func NewAuthRequest(authorizationEndpoint, clientID, redirectURI, extraScopes string, random io.Reader) (AuthRequest, error) {
	if random == nil {
		random = rand.Reader
	}
	base, err := url.Parse(strings.TrimSpace(authorizationEndpoint))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return AuthRequest{}, fmt.Errorf("consoleauth: invalid authorization endpoint")
	}
	state, err := randomToken(random)
	if err != nil {
		return AuthRequest{}, err
	}
	nonce, err := randomToken(random)
	if err != nil {
		return AuthRequest{}, err
	}
	verifier, err := randomToken(random)
	if err != nil {
		return AuthRequest{}, err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	scope := "openid"
	if trimmed := strings.TrimSpace(extraScopes); trimmed != "" {
		scope = scope + " " + trimmed
	}
	query := url.Values{}
	query.Set("response_type", "code")
	query.Set("client_id", clientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("scope", scope)
	query.Set("state", state)
	query.Set("nonce", nonce)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	base.RawQuery = query.Encode()

	return AuthRequest{
		State:            state,
		Nonce:            nonce,
		CodeVerifier:     verifier,
		CodeChallenge:    challenge,
		AuthorizationURL: base.String(),
	}, nil
}

// VerifyState constant-time compares the state returned on the OIDC callback
// with the state this request issued, defending against CSRF and mixed-up
// callbacks.
func (request AuthRequest) VerifyState(returnedState string) error {
	if request.State == "" || subtle.ConstantTimeCompare([]byte(request.State), []byte(returnedState)) != 1 {
		return ErrStateMismatch
	}
	return nil
}

func randomToken(random io.Reader) (string, error) {
	buffer := make([]byte, tokenBytes)
	if _, err := io.ReadFull(random, buffer); err != nil {
		return "", fmt.Errorf("consoleauth: read random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

// Claims are the ID-token claims the console relies on, already parsed from a
// signature-verified token by the TokenExchanger.
type Claims struct {
	Issuer            string              `json:"issuer"`
	Audience          []string            `json:"audience"`
	AuthorizedParty   string              `json:"authorizedParty"`
	Nonce             string              `json:"nonce"`
	Subject           string              `json:"subject"`
	PreferredUsername string              `json:"preferredUsername"`
	ExpiresAt         time.Time           `json:"expiresAt"`
	IssuedAt          time.Time           `json:"issuedAt"`
	NotBefore         time.Time           `json:"notBefore"`
	RealmRoles        []string            `json:"realmRoles"`
	ClientRoles       map[string][]string `json:"clientRoles"`
}

// Expectations parameterize claims validation for one login.
type Expectations struct {
	Issuer   string
	Audience string // the console's Keycloak client id
	Nonce    string // the nonce this login's AuthRequest issued
	Now      time.Time
	Leeway   time.Duration
}

var (
	// ErrStateMismatch is returned when the OIDC callback state does not match
	// the issued request.
	ErrStateMismatch = errors.New("oidc callback state does not match the authentication request")
	// ErrInvalidIssuer is returned when the token issuer does not match.
	ErrInvalidIssuer = errors.New("token issuer does not match")
	// ErrInvalidAudience is returned when the token audience does not include the
	// console client, or a multi-audience token's authorized party is not the
	// console client.
	ErrInvalidAudience = errors.New("token audience does not include the console client")
	// ErrInvalidNonce is returned when the token nonce does not match the login.
	ErrInvalidNonce = errors.New("token nonce does not match the authentication request")
	// ErrTokenExpired is returned when the token is past its expiry (with leeway).
	ErrTokenExpired = errors.New("token has expired")
	// ErrTokenNotYetValid is returned when the token's not-before is in the future.
	ErrTokenNotYetValid = errors.New("token is not yet valid")
)

// ValidateClaims enforces the OIDC ID-token checks the console requires: exact
// issuer, audience containing the console client (with authorized-party pinning
// for multi-audience tokens), nonce binding to this login, and time validity.
func ValidateClaims(claims Claims, expectations Expectations) error {
	if expectations.Issuer == "" || claims.Issuer != expectations.Issuer {
		return ErrInvalidIssuer
	}
	if !contains(claims.Audience, expectations.Audience) {
		return ErrInvalidAudience
	}
	// OIDC core: when the token has more than one audience, the authorized party
	// must be present and identify the console client.
	if len(claims.Audience) > 1 && claims.AuthorizedParty != expectations.Audience {
		return ErrInvalidAudience
	}
	if expectations.Nonce == "" || subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(expectations.Nonce)) != 1 {
		return ErrInvalidNonce
	}
	if !claims.ExpiresAt.IsZero() && expectations.Now.After(claims.ExpiresAt.Add(expectations.Leeway)) {
		return ErrTokenExpired
	}
	if !claims.NotBefore.IsZero() && expectations.Now.Add(expectations.Leeway).Before(claims.NotBefore) {
		return ErrTokenNotYetValid
	}
	return nil
}

// ConsoleRole maps the token's realm and console-client roles to the strongest
// recognized Console Role, or RoleNone (default deny) when none are present.
func (claims Claims) ConsoleRole(clientID string) ConsoleRole {
	names := make([]string, 0, len(claims.RealmRoles)+len(claims.ClientRoles[clientID]))
	names = append(names, claims.RealmRoles...)
	names = append(names, claims.ClientRoles[clientID]...)
	return HighestRole(names)
}

// TokenExchanger trades an authorization code (with its PKCE verifier) for the
// signature-verified ID-token claims. The production implementation performs the
// OIDC token request and verifies the JWT against the issuer's JWKS; tests
// inject a deterministic implementation.
type TokenExchanger interface {
	Exchange(ctx context.Context, code, codeVerifier string) (Claims, error)
}

// CompleteLogin ties an OIDC callback together: it verifies the returned state
// against the pending request, exchanges the code for verified claims, validates
// those claims against the request's nonce and the supplied expectations, and
// maps the token to a Console Role — denying by default when the user holds no
// role. The returned claims identify the authenticated user for the session.
func CompleteLogin(ctx context.Context, exchanger TokenExchanger, request AuthRequest, code, returnedState string, expectations Expectations) (Claims, ConsoleRole, error) {
	if err := request.VerifyState(returnedState); err != nil {
		return Claims{}, RoleNone, err
	}
	claims, err := exchanger.Exchange(ctx, code, request.CodeVerifier)
	if err != nil {
		return Claims{}, RoleNone, err
	}
	expectations.Nonce = request.Nonce
	if err := ValidateClaims(claims, expectations); err != nil {
		return Claims{}, RoleNone, err
	}
	role := claims.ConsoleRole(expectations.Audience)
	if role == RoleNone {
		return claims, RoleNone, ErrNoConsoleRole
	}
	return claims, role, nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
