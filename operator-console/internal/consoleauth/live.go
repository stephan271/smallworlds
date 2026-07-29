package consoleauth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	// crypto.Hash.New panics for a hash that is not linked in; the signature
	// verifiers below construct SHA-256 that way.
	_ "crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// The live exchanger is the one networked step of the console's login. Every
// other part of consoleauth is pure; this file is deliberately the only place
// that talks to Keycloak, so the login composition stays testable without an
// identity provider.

const (
	// documentLimit bounds how much of a discovery or JWKS response is read. Both
	// are small documents, and an endpoint that streams indefinitely must not be
	// able to hold a login open or exhaust the console's memory.
	documentLimit = 1 << 20
	// jwksMinRefreshInterval is the shortest gap between two JWKS fetches. An
	// unknown key id triggers a refresh (that is how Keycloak signature-key
	// rotation is picked up), but a token stream carrying forged key ids must not
	// turn the console into a request amplifier against Keycloak.
	jwksMinRefreshInterval = time.Minute
	// exchangeTimeout bounds a single token or JWKS request.
	exchangeTimeout = 15 * time.Second
)

// ErrInvalidSignature is returned when an ID token's signature does not verify
// against the issuer's published keys.
var ErrInvalidSignature = errors.New("id token signature does not verify")

// Endpoints are the OIDC endpoints the console needs, as published by the
// identity provider's discovery document.
type Endpoints struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
	EndSessionEndpoint    string `json:"end_session_endpoint"`
}

// Discover reads the OIDC discovery document for an issuer. The issuer is the
// realm URL — the console is configured with that one value and derives the rest,
// so a realm move cannot leave a stale endpoint behind.
//
// The returned issuer is checked against the requested one: a discovery document
// that names a different issuer would silently move the console's trust anchor,
// which is exactly the substitution the exact-issuer claim check exists to stop.
func Discover(ctx context.Context, client *http.Client, issuer string) (Endpoints, error) {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	if issuer == "" {
		return Endpoints{}, errors.New("consoleauth: issuer is required")
	}
	var endpoints Endpoints
	if err := fetchJSON(ctx, client, issuer+"/.well-known/openid-configuration", &endpoints); err != nil {
		return Endpoints{}, fmt.Errorf("consoleauth: oidc discovery: %w", err)
	}
	if strings.TrimRight(endpoints.Issuer, "/") != issuer {
		return Endpoints{}, fmt.Errorf("consoleauth: discovery document names issuer %q, expected %q", endpoints.Issuer, issuer)
	}
	if endpoints.AuthorizationEndpoint == "" || endpoints.TokenEndpoint == "" || endpoints.JWKSURI == "" {
		return Endpoints{}, errors.New("consoleauth: discovery document is incomplete")
	}
	return endpoints, nil
}

// LiveExchanger trades an authorization code for signature-verified ID-token
// claims against a real Keycloak realm. It performs the OIDC token request with
// the login's PKCE verifier, then verifies the returned ID token against the
// realm's published JWKS.
//
// It satisfies TokenExchanger, so the console wires this in production and a
// deterministic stub in tests.
type LiveExchanger struct {
	// Issuer is the realm URL. It is re-checked against the token's iss claim by
	// ValidateClaims; keeping it here lets the exchanger reject a JWKS served
	// from a different issuer before any signature is trusted.
	Issuer        string
	TokenEndpoint string
	JWKSURI       string
	ClientID      string
	// ClientSecret authenticates a confidential client. It is empty for a public
	// client, where PKCE alone proves the code belongs to this login.
	ClientSecret string
	RedirectURI  string
	HTTPClient   *http.Client
	Now          func() time.Time

	mu            sync.Mutex
	keys          map[string]crypto.PublicKey
	keysFetchedAt time.Time
}

// NewLiveExchanger discovers the realm's endpoints and returns an exchanger
// bound to them.
func NewLiveExchanger(ctx context.Context, client *http.Client, issuer, clientID, clientSecret, redirectURI string) (*LiveExchanger, error) {
	endpoints, err := Discover(ctx, client, issuer)
	if err != nil {
		return nil, err
	}
	return &LiveExchanger{
		Issuer:        endpoints.Issuer,
		TokenEndpoint: endpoints.TokenEndpoint,
		JWKSURI:       endpoints.JWKSURI,
		ClientID:      clientID,
		ClientSecret:  clientSecret,
		RedirectURI:   redirectURI,
		HTTPClient:    client,
	}, nil
}

func (exchanger *LiveExchanger) httpClient() *http.Client {
	if exchanger.HTTPClient != nil {
		return exchanger.HTTPClient
	}
	return &http.Client{Timeout: exchangeTimeout}
}

func (exchanger *LiveExchanger) now() time.Time {
	if exchanger.Now != nil {
		return exchanger.Now()
	}
	return time.Now().UTC()
}

// Exchange performs the OIDC token request and returns the verified ID-token
// claims. It verifies only the signature and parses the claims; issuer,
// audience, nonce, and expiry are validated by ValidateClaims, which owns those
// rules for both the live and the injected exchanger.
func (exchanger *LiveExchanger) Exchange(ctx context.Context, code, codeVerifier string) (Claims, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", exchanger.RedirectURI)
	form.Set("client_id", exchanger.ClientID)
	form.Set("code_verifier", codeVerifier)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, exchanger.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Claims{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	if exchanger.ClientSecret != "" {
		// client_secret_basic, Keycloak's default for a confidential client. The
		// secret never travels in the body, so it cannot land in an access log
		// that records form parameters.
		request.SetBasicAuth(url.QueryEscape(exchanger.ClientID), url.QueryEscape(exchanger.ClientSecret))
	}
	response, err := exchanger.httpClient().Do(request)
	if err != nil {
		return Claims{}, fmt.Errorf("consoleauth: token request: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, documentLimit))
	if err != nil {
		return Claims{}, err
	}
	if response.StatusCode != http.StatusOK {
		// The provider's error code is safe to surface (invalid_grant and
		// friends); its description may echo request material, so it is dropped.
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &failure)
		if failure.Error == "" {
			failure.Error = fmt.Sprintf("status %d", response.StatusCode)
		}
		return Claims{}, fmt.Errorf("consoleauth: token request rejected: %s", failure.Error)
	}
	var tokens struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tokens); err != nil {
		return Claims{}, err
	}
	if tokens.IDToken == "" {
		return Claims{}, errors.New("consoleauth: token response carried no id_token")
	}
	return exchanger.verify(ctx, tokens.IDToken)
}

// verify checks the ID token's signature against the realm's JWKS and parses its
// claims. An unknown key id refreshes the key set once (rate-limited) so a
// signing-key rotation does not require a console restart.
func (exchanger *LiveExchanger) verify(ctx context.Context, token string) (Claims, error) {
	signedPart, header, signature, err := splitJWT(token)
	if err != nil {
		return Claims{}, err
	}
	hash, verifier, err := algorithm(header.Algorithm)
	if err != nil {
		return Claims{}, err
	}
	key, err := exchanger.publicKey(ctx, header.KeyID)
	if err != nil {
		return Claims{}, err
	}
	digest := hash.New()
	digest.Write([]byte(signedPart))
	if err := verifier(key, digest.Sum(nil), signature); err != nil {
		return Claims{}, err
	}
	return parseClaims(token)
}

// publicKey returns the verification key for a key id, fetching the JWKS when
// the id is unknown.
func (exchanger *LiveExchanger) publicKey(ctx context.Context, keyID string) (crypto.PublicKey, error) {
	exchanger.mu.Lock()
	key, known := exchanger.keys[keyID]
	stale := exchanger.now().Sub(exchanger.keysFetchedAt) >= jwksMinRefreshInterval
	exchanger.mu.Unlock()
	if known {
		return key, nil
	}
	if !stale && len(exchanger.keys) > 0 {
		return nil, fmt.Errorf("consoleauth: unknown signing key %q", keyID)
	}
	keys, err := exchanger.fetchKeys(ctx)
	if err != nil {
		return nil, err
	}
	exchanger.mu.Lock()
	exchanger.keys = keys
	exchanger.keysFetchedAt = exchanger.now()
	key, known = keys[keyID]
	exchanger.mu.Unlock()
	if !known {
		return nil, fmt.Errorf("consoleauth: unknown signing key %q", keyID)
	}
	return key, nil
}

func (exchanger *LiveExchanger) fetchKeys(ctx context.Context) (map[string]crypto.PublicKey, error) {
	var document struct {
		Keys []jsonWebKey `json:"keys"`
	}
	if err := fetchJSON(ctx, exchanger.httpClient(), exchanger.JWKSURI, &document); err != nil {
		return nil, fmt.Errorf("consoleauth: jwks: %w", err)
	}
	keys := make(map[string]crypto.PublicKey, len(document.Keys))
	for _, key := range document.Keys {
		// Keys published for encryption cannot vouch for a signature, and a key
		// without an id cannot be selected by a token header.
		if key.Use != "" && key.Use != "sig" {
			continue
		}
		if key.KeyID == "" {
			continue
		}
		parsed, err := key.publicKey()
		if err != nil {
			continue
		}
		keys[key.KeyID] = parsed
	}
	if len(keys) == 0 {
		return nil, errors.New("consoleauth: jwks published no usable signing key")
	}
	return keys, nil
}

// --- JWT decoding ---

type jwtHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Type      string `json:"typ"`
}

// splitJWT returns the signed input (header.payload), the decoded header, and
// the decoded signature.
func splitJWT(token string) (string, jwtHeader, []byte, error) {
	first := strings.IndexByte(token, '.')
	last := strings.LastIndexByte(token, '.')
	if first <= 0 || last <= first || strings.Count(token, ".") != 2 {
		return "", jwtHeader{}, nil, errors.New("consoleauth: id token is not a compact JWS")
	}
	encodedHeader, encodedSignature := token[:first], token[last+1:]
	rawHeader, err := base64.RawURLEncoding.DecodeString(encodedHeader)
	if err != nil {
		return "", jwtHeader{}, nil, errors.New("consoleauth: id token header is not base64url")
	}
	var header jwtHeader
	if err := json.Unmarshal(rawHeader, &header); err != nil {
		return "", jwtHeader{}, nil, errors.New("consoleauth: id token header is not JSON")
	}
	signature, err := base64.RawURLEncoding.DecodeString(encodedSignature)
	if err != nil {
		return "", jwtHeader{}, nil, errors.New("consoleauth: id token signature is not base64url")
	}
	return token[:last], header, signature, nil
}

// algorithm resolves a JWS algorithm name to its hash and signature verifier.
// The set is an allowlist: "none" and MAC algorithms are absent, so a token
// claiming them is rejected before any key is consulted rather than being
// verified against a key of the wrong kind.
func algorithm(name string) (crypto.Hash, func(crypto.PublicKey, []byte, []byte) error, error) {
	switch name {
	case "RS256":
		return crypto.SHA256, verifyRSA, nil
	case "RS384":
		return crypto.SHA384, verifyRSA, nil
	case "RS512":
		return crypto.SHA512, verifyRSA, nil
	case "PS256":
		return crypto.SHA256, verifyRSAPSS, nil
	case "PS384":
		return crypto.SHA384, verifyRSAPSS, nil
	case "PS512":
		return crypto.SHA512, verifyRSAPSS, nil
	case "ES256":
		return crypto.SHA256, verifyECDSA, nil
	case "ES384":
		return crypto.SHA384, verifyECDSA, nil
	case "ES512":
		return crypto.SHA512, verifyECDSA, nil
	default:
		return 0, nil, fmt.Errorf("consoleauth: unsupported id token algorithm %q", name)
	}
}

func verifyRSA(key crypto.PublicKey, digest, signature []byte) error {
	rsaKey, ok := key.(*rsa.PublicKey)
	if !ok {
		return ErrInvalidSignature
	}
	if err := rsa.VerifyPKCS1v15(rsaKey, hashForDigest(len(digest)), digest, signature); err != nil {
		return ErrInvalidSignature
	}
	return nil
}

func verifyRSAPSS(key crypto.PublicKey, digest, signature []byte) error {
	rsaKey, ok := key.(*rsa.PublicKey)
	if !ok {
		return ErrInvalidSignature
	}
	hash := hashForDigest(len(digest))
	if err := rsa.VerifyPSS(rsaKey, hash, digest, signature, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: hash}); err != nil {
		return ErrInvalidSignature
	}
	return nil
}

func verifyECDSA(key crypto.PublicKey, digest, signature []byte) error {
	ecdsaKey, ok := key.(*ecdsa.PublicKey)
	if !ok {
		return ErrInvalidSignature
	}
	// JWS ECDSA signatures are the fixed-width R‖S pair, not the ASN.1 encoding
	// ecdsa.VerifyASN1 expects.
	if len(signature)%2 != 0 {
		return ErrInvalidSignature
	}
	half := len(signature) / 2
	r := new(big.Int).SetBytes(signature[:half])
	s := new(big.Int).SetBytes(signature[half:])
	if !ecdsa.Verify(ecdsaKey, digest, r, s) {
		return ErrInvalidSignature
	}
	return nil
}

// hashForDigest recovers the hash function from the digest length. The verifiers
// receive an already-computed digest, and the RSA primitives need to know which
// hash produced it so the DigestInfo prefix matches.
func hashForDigest(length int) crypto.Hash {
	switch length {
	case sha512.Size384:
		return crypto.SHA384
	case sha512.Size:
		return crypto.SHA512
	default:
		return crypto.SHA256
	}
}

// jsonWebKey is the subset of a JWK the console needs to build a verification key.
type jsonWebKey struct {
	KeyType   string `json:"kty"`
	KeyID     string `json:"kid"`
	Use       string `json:"use"`
	Algorithm string `json:"alg"`
	Modulus   string `json:"n"`
	Exponent  string `json:"e"`
	Curve     string `json:"crv"`
	X         string `json:"x"`
	Y         string `json:"y"`
}

func (key jsonWebKey) publicKey() (crypto.PublicKey, error) {
	switch key.KeyType {
	case "RSA":
		modulus, err := base64.RawURLEncoding.DecodeString(key.Modulus)
		if err != nil {
			return nil, err
		}
		exponent, err := base64.RawURLEncoding.DecodeString(key.Exponent)
		if err != nil {
			return nil, err
		}
		if len(exponent) == 0 || len(exponent) > 8 {
			return nil, errors.New("consoleauth: unusable RSA exponent")
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(modulus), E: int(new(big.Int).SetBytes(exponent).Int64())}, nil
	case "EC":
		var curve elliptic.Curve
		switch key.Curve {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, fmt.Errorf("consoleauth: unsupported curve %q", key.Curve)
		}
		x, err := base64.RawURLEncoding.DecodeString(key.X)
		if err != nil {
			return nil, err
		}
		y, err := base64.RawURLEncoding.DecodeString(key.Y)
		if err != nil {
			return nil, err
		}
		return &ecdsa.PublicKey{Curve: curve, X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}, nil
	default:
		return nil, fmt.Errorf("consoleauth: unsupported key type %q", key.KeyType)
	}
}

// idTokenClaims is the wire shape of a Keycloak ID token. It is translated into
// the provider-neutral Claims the rest of the console works with, so Keycloak's
// realm_access/resource_access layout stays contained in this file.
type idTokenClaims struct {
	Issuer            string          `json:"iss"`
	Audience          json.RawMessage `json:"aud"`
	AuthorizedParty   string          `json:"azp"`
	Nonce             string          `json:"nonce"`
	Subject           string          `json:"sub"`
	PreferredUsername string          `json:"preferred_username"`
	ExpiresAt         int64           `json:"exp"`
	IssuedAt          int64           `json:"iat"`
	NotBefore         int64           `json:"nbf"`
	RealmAccess       struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
	ResourceAccess map[string]struct {
		Roles []string `json:"roles"`
	} `json:"resource_access"`
}

func parseClaims(token string) (Claims, error) {
	parts := strings.Split(token, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, errors.New("consoleauth: id token payload is not base64url")
	}
	var raw idTokenClaims
	if err := json.Unmarshal(payload, &raw); err != nil {
		return Claims{}, errors.New("consoleauth: id token payload is not JSON")
	}
	audience, err := decodeAudience(raw.Audience)
	if err != nil {
		return Claims{}, err
	}
	claims := Claims{
		Issuer:            raw.Issuer,
		Audience:          audience,
		AuthorizedParty:   raw.AuthorizedParty,
		Nonce:             raw.Nonce,
		Subject:           raw.Subject,
		PreferredUsername: raw.PreferredUsername,
		ExpiresAt:         unixTime(raw.ExpiresAt),
		IssuedAt:          unixTime(raw.IssuedAt),
		NotBefore:         unixTime(raw.NotBefore),
		RealmRoles:        raw.RealmAccess.Roles,
	}
	if len(raw.ResourceAccess) > 0 {
		claims.ClientRoles = make(map[string][]string, len(raw.ResourceAccess))
		for client, access := range raw.ResourceAccess {
			claims.ClientRoles[client] = access.Roles
		}
	}
	return claims, nil
}

// decodeAudience accepts both shapes RFC 7519 permits for aud: a single string
// and an array of strings.
func decodeAudience(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}, nil
	}
	var multiple []string
	if err := json.Unmarshal(raw, &multiple); err != nil {
		return nil, errors.New("consoleauth: id token audience is neither a string nor an array")
	}
	return multiple, nil
}

func unixTime(seconds int64) time.Time {
	if seconds == 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}

func fetchJSON(ctx context.Context, client *http.Client, endpoint string, target any) error {
	if client == nil {
		client = &http.Client{Timeout: exchangeTimeout}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %d", endpoint, response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, documentLimit)).Decode(target)
}
