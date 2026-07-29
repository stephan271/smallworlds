package consoleauth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// signingKey is a test identity provider's signing key: it publishes a JWKS and
// mints ID tokens the way Keycloak does.
type signingKey struct {
	id  string
	rsa *rsa.PrivateKey
	ec  *ecdsa.PrivateKey
}

func newRSASigningKey(t *testing.T, id string) signingKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	return signingKey{id: id, rsa: key}
}

func newECSigningKey(t *testing.T, id string) signingKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ec key: %v", err)
	}
	return signingKey{id: id, ec: key}
}

func (key signingKey) jwk() map[string]string {
	if key.rsa != nil {
		return map[string]string{
			"kty": "RSA",
			"kid": key.id,
			"use": "sig",
			"alg": "RS256",
			"n":   base64.RawURLEncoding.EncodeToString(key.rsa.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.rsa.E)).Bytes()),
		}
	}
	byteLength := (key.ec.Curve.Params().BitSize + 7) / 8
	return map[string]string{
		"kty": "EC",
		"kid": key.id,
		"use": "sig",
		"alg": "ES256",
		"crv": "P-256",
		"x":   base64.RawURLEncoding.EncodeToString(leftPad(key.ec.X.Bytes(), byteLength)),
		"y":   base64.RawURLEncoding.EncodeToString(leftPad(key.ec.Y.Bytes(), byteLength)),
	}
}

func (key signingKey) sign(t *testing.T, claims map[string]any) string {
	t.Helper()
	algorithmName := "RS256"
	if key.ec != nil {
		algorithmName = "ES256"
	}
	header, err := json.Marshal(map[string]string{"alg": algorithmName, "kid": key.id, "typ": "JWT"})
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	signed := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signed))

	var signature []byte
	if key.rsa != nil {
		signature, err = rsa.SignPKCS1v15(rand.Reader, key.rsa, crypto.SHA256, digest[:])
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
	} else {
		r, s, signErr := ecdsa.Sign(rand.Reader, key.ec, digest[:])
		if signErr != nil {
			t.Fatalf("sign: %v", signErr)
		}
		byteLength := (key.ec.Curve.Params().BitSize + 7) / 8
		signature = append(leftPad(r.Bytes(), byteLength), leftPad(s.Bytes(), byteLength)...)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func leftPad(value []byte, length int) []byte {
	if len(value) >= length {
		return value
	}
	padded := make([]byte, length)
	copy(padded[length-len(value):], value)
	return padded
}

// identityProvider is a stand-in Keycloak realm: discovery, token, and JWKS.
type identityProvider struct {
	server      *httptest.Server
	keys        []signingKey
	idToken     string
	tokenStatus int
	tokenBody   string
	// issuerOverride makes the stand-in realm publish an issuer other than its
	// own address, the way Keycloak does when KC_HOSTNAME names a hostname the
	// cluster cannot reach from inside.
	issuerOverride string
	jwksOverride   string
	jwksFetches    atomic.Int64
	lastForm       url.Values
	lastAuth       string
}

func newIdentityProvider(t *testing.T, keys ...signingKey) *identityProvider {
	t.Helper()
	provider := &identityProvider{keys: keys, tokenStatus: http.StatusOK}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(response http.ResponseWriter, _ *http.Request) {
		base := provider.server.URL
		published := base
		if provider.issuerOverride != "" {
			published = provider.issuerOverride
		}
		jwks := published + "/protocol/openid-connect/certs"
		if provider.jwksOverride != "" {
			jwks = provider.jwksOverride
		}
		_ = json.NewEncoder(response).Encode(map[string]string{
			"issuer":                 published,
			"authorization_endpoint": published + "/protocol/openid-connect/auth",
			"token_endpoint":         published + "/protocol/openid-connect/token",
			"jwks_uri":               jwks,
		})
	})
	mux.HandleFunc("/protocol/openid-connect/token", func(response http.ResponseWriter, request *http.Request) {
		_ = request.ParseForm()
		provider.lastForm = request.PostForm
		provider.lastAuth = request.Header.Get("Authorization")
		if provider.tokenStatus != http.StatusOK {
			response.WriteHeader(provider.tokenStatus)
			_, _ = response.Write([]byte(provider.tokenBody))
			return
		}
		_ = json.NewEncoder(response).Encode(map[string]string{"id_token": provider.idToken, "token_type": "Bearer"})
	})
	mux.HandleFunc("/protocol/openid-connect/certs", func(response http.ResponseWriter, _ *http.Request) {
		provider.jwksFetches.Add(1)
		keys := make([]map[string]string, 0, len(provider.keys))
		for _, key := range provider.keys {
			keys = append(keys, key.jwk())
		}
		_ = json.NewEncoder(response).Encode(map[string]any{"keys": keys})
	})
	// A real Keycloak serves every realm endpoint under /realms/<realm>/. The
	// stand-in answers both there and at the root so a test can address it
	// either as the issuer itself or as an internal base the issuer's realm path
	// is appended to.
	realm := http.NewServeMux()
	realm.Handle("/realms/smallworlds/", http.StripPrefix("/realms/smallworlds", mux))
	realm.Handle("/", mux)
	provider.server = httptest.NewServer(realm)
	t.Cleanup(provider.server.Close)
	return provider
}

func (provider *identityProvider) exchanger(t *testing.T, clientID, clientSecret string) *LiveExchanger {
	t.Helper()
	exchanger, err := NewLiveExchanger(context.Background(), provider.server.Client(), provider.server.URL, "", clientID, clientSecret, "https://console.example.test/api/v1/auth/callback")
	if err != nil {
		t.Fatalf("build exchanger: %v", err)
	}
	return exchanger
}

func standardClaims(issuer, clientID, nonce string) map[string]any {
	return map[string]any{
		"iss":                issuer,
		"aud":                clientID,
		"azp":                clientID,
		"nonce":              nonce,
		"sub":                "6f1c0b3a-user",
		"preferred_username": "ada",
		"exp":                time.Now().Add(time.Hour).Unix(),
		"iat":                time.Now().Unix(),
		"realm_access":       map[string]any{"roles": []string{"offline_access"}},
		"resource_access":    map[string]any{clientID: map[string]any{"roles": []string{"operator"}}},
	}
}

func TestDiscoverReadsRealmEndpoints(t *testing.T) {
	provider := newIdentityProvider(t, newRSASigningKey(t, "key-1"))
	endpoints, err := Discover(context.Background(), provider.server.Client(), provider.server.URL+"/")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if endpoints.Issuer != provider.server.URL {
		t.Fatalf("issuer = %q, want %q", endpoints.Issuer, provider.server.URL)
	}
	if endpoints.TokenEndpoint == "" || endpoints.JWKSURI == "" || endpoints.AuthorizationEndpoint == "" {
		t.Fatalf("incomplete endpoints: %+v", endpoints)
	}
}

// A discovery document naming a different issuer would move the console's trust
// anchor without any signature check noticing, so it is refused outright.
func TestDiscoverRejectsMismatchedIssuer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(response).Encode(map[string]string{
			"issuer":                 "https://attacker.example.test/realms/smallworlds",
			"authorization_endpoint": "https://attacker.example.test/auth",
			"token_endpoint":         "https://attacker.example.test/token",
			"jwks_uri":               "https://attacker.example.test/certs",
		})
	}))
	defer server.Close()
	if _, err := Discover(context.Background(), server.Client(), server.URL); err == nil {
		t.Fatal("expected a mismatched issuer to be refused")
	}
}

func TestLiveExchangerReturnsVerifiedClaims(t *testing.T) {
	key := newRSASigningKey(t, "key-1")
	provider := newIdentityProvider(t, key)
	provider.idToken = key.sign(t, standardClaims(provider.server.URL, "smallworlds-console", "nonce-abc"))
	exchanger := provider.exchanger(t, "smallworlds-console", "")

	claims, err := exchanger.Exchange(context.Background(), "the-code", "the-verifier")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if claims.Subject != "6f1c0b3a-user" || claims.PreferredUsername != "ada" {
		t.Fatalf("unexpected identity: %+v", claims)
	}
	if claims.Nonce != "nonce-abc" || claims.Issuer != provider.server.URL {
		t.Fatalf("unexpected binding claims: %+v", claims)
	}
	if got := claims.ConsoleRole("smallworlds-console"); got != RoleOperator {
		t.Fatalf("console role = %q, want operator", got)
	}
	// The PKCE verifier must reach the provider, or the code could be redeemed by
	// anyone who intercepted it.
	if provider.lastForm.Get("code_verifier") != "the-verifier" || provider.lastForm.Get("code") != "the-code" {
		t.Fatalf("token request did not carry the code and verifier: %v", provider.lastForm)
	}
	if provider.lastForm.Get("grant_type") != "authorization_code" {
		t.Fatalf("unexpected grant type %q", provider.lastForm.Get("grant_type"))
	}
}

// A confidential client authenticates with client_secret_basic so the secret
// never lands in a body that a request log might record.
func TestLiveExchangerAuthenticatesConfidentialClient(t *testing.T) {
	key := newRSASigningKey(t, "key-1")
	provider := newIdentityProvider(t, key)
	provider.idToken = key.sign(t, standardClaims(provider.server.URL, "smallworlds-console", "nonce-abc"))
	exchanger := provider.exchanger(t, "smallworlds-console", "s3cr3t")

	if _, err := exchanger.Exchange(context.Background(), "the-code", "the-verifier"); err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if !strings.HasPrefix(provider.lastAuth, "Basic ") {
		t.Fatalf("expected basic authentication, got %q", provider.lastAuth)
	}
	if provider.lastForm.Get("client_secret") != "" {
		t.Fatal("client secret must not travel in the form body")
	}
}

func TestLiveExchangerRejectsForgedSignature(t *testing.T) {
	realKey := newRSASigningKey(t, "key-1")
	forgedKey := newRSASigningKey(t, "key-1") // same key id, different key material
	provider := newIdentityProvider(t, realKey)
	provider.idToken = forgedKey.sign(t, standardClaims(provider.server.URL, "smallworlds-console", "nonce-abc"))
	exchanger := provider.exchanger(t, "smallworlds-console", "")

	_, err := exchanger.Exchange(context.Background(), "the-code", "the-verifier")
	if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("error = %v, want ErrInvalidSignature", err)
	}
}

// "alg": "none" is the classic JWT downgrade. It must be refused by the
// algorithm allowlist, before any key is consulted.
func TestLiveExchangerRejectsUnsignedToken(t *testing.T) {
	provider := newIdentityProvider(t, newRSASigningKey(t, "key-1"))
	header, _ := json.Marshal(map[string]string{"alg": "none", "kid": "key-1"})
	payload, _ := json.Marshal(standardClaims(provider.server.URL, "smallworlds-console", "nonce-abc"))
	provider.idToken = base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload) + "."
	exchanger := provider.exchanger(t, "smallworlds-console", "")

	if _, err := exchanger.Exchange(context.Background(), "the-code", "the-verifier"); err == nil {
		t.Fatal("expected an unsigned token to be refused")
	}
}

func TestLiveExchangerVerifiesECDSATokens(t *testing.T) {
	key := newECSigningKey(t, "ec-key")
	provider := newIdentityProvider(t, key)
	provider.idToken = key.sign(t, standardClaims(provider.server.URL, "smallworlds-console", "nonce-abc"))
	exchanger := provider.exchanger(t, "smallworlds-console", "")

	claims, err := exchanger.Exchange(context.Background(), "the-code", "the-verifier")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if claims.Subject != "6f1c0b3a-user" {
		t.Fatalf("unexpected subject %q", claims.Subject)
	}
}

// Keycloak rotates its realm signing key. A token signed by a key the console
// has never seen must trigger exactly one JWKS refresh and then verify.
func TestLiveExchangerRefreshesKeysOnRotation(t *testing.T) {
	firstKey := newRSASigningKey(t, "key-1")
	provider := newIdentityProvider(t, firstKey)
	provider.idToken = firstKey.sign(t, standardClaims(provider.server.URL, "smallworlds-console", "nonce-abc"))
	exchanger := provider.exchanger(t, "smallworlds-console", "")
	clock := time.Now().UTC()
	exchanger.Now = func() time.Time { return clock }

	if _, err := exchanger.Exchange(context.Background(), "code", "verifier"); err != nil {
		t.Fatalf("first exchange: %v", err)
	}
	if got := provider.jwksFetches.Load(); got != 1 {
		t.Fatalf("jwks fetches = %d, want 1", got)
	}

	rotatedKey := newRSASigningKey(t, "key-2")
	provider.keys = []signingKey{rotatedKey}
	provider.idToken = rotatedKey.sign(t, standardClaims(provider.server.URL, "smallworlds-console", "nonce-abc"))
	clock = clock.Add(2 * jwksMinRefreshInterval)

	if _, err := exchanger.Exchange(context.Background(), "code", "verifier"); err != nil {
		t.Fatalf("exchange after rotation: %v", err)
	}
	if got := provider.jwksFetches.Load(); got != 2 {
		t.Fatalf("jwks fetches = %d, want 2 after rotation", got)
	}
}

// A stream of tokens carrying invented key ids must not turn the console into a
// request amplifier against Keycloak.
func TestLiveExchangerRateLimitsKeyRefresh(t *testing.T) {
	key := newRSASigningKey(t, "key-1")
	provider := newIdentityProvider(t, key)
	provider.idToken = key.sign(t, standardClaims(provider.server.URL, "smallworlds-console", "nonce-abc"))
	exchanger := provider.exchanger(t, "smallworlds-console", "")
	clock := time.Now().UTC()
	exchanger.Now = func() time.Time { return clock }
	if _, err := exchanger.Exchange(context.Background(), "code", "verifier"); err != nil {
		t.Fatalf("warm the key cache: %v", err)
	}

	unknown := newRSASigningKey(t, "invented")
	provider.idToken = unknown.sign(t, standardClaims(provider.server.URL, "smallworlds-console", "nonce-abc"))
	for attempt := 0; attempt < 5; attempt++ {
		if _, err := exchanger.Exchange(context.Background(), "code", "verifier"); err == nil {
			t.Fatal("expected an unknown key id to be refused")
		}
	}
	if got := provider.jwksFetches.Load(); got != 1 {
		t.Fatalf("jwks fetches = %d, want the initial fetch only", got)
	}
}

func TestLiveExchangerSurfacesProviderRejection(t *testing.T) {
	provider := newIdentityProvider(t, newRSASigningKey(t, "key-1"))
	provider.tokenStatus = http.StatusBadRequest
	provider.tokenBody = `{"error":"invalid_grant","error_description":"Code not valid"}`
	exchanger := provider.exchanger(t, "smallworlds-console", "")

	_, err := exchanger.Exchange(context.Background(), "stale-code", "verifier")
	if err == nil || !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("error = %v, want the provider's invalid_grant code", err)
	}
}

// A multi-audience token is the shape ValidateClaims pins with azp; the parser
// has to preserve both audiences for that check to be possible at all.
func TestParseClaimsAcceptsBothAudienceShapes(t *testing.T) {
	key := newRSASigningKey(t, "key-1")
	claims := standardClaims("https://identity.example.test", "smallworlds-console", "n")
	claims["aud"] = []string{"smallworlds-console", "account"}
	parsed, err := parseClaims(key.sign(t, claims))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(parsed.Audience) != 2 || parsed.Audience[0] != "smallworlds-console" {
		t.Fatalf("audience = %v", parsed.Audience)
	}
	if parsed.AuthorizedParty != "smallworlds-console" {
		t.Fatalf("azp = %q", parsed.AuthorizedParty)
	}
}

// The exchanger is the seam CompleteLogin composes with; proving the two fit
// together is what makes the production login path the same code path the pure
// tests cover.
func TestCompleteLoginThroughLiveExchanger(t *testing.T) {
	key := newRSASigningKey(t, "key-1")
	provider := newIdentityProvider(t, key)
	exchanger := provider.exchanger(t, "smallworlds-console", "")

	authRequest, err := NewAuthRequest(provider.server.URL+"/protocol/openid-connect/auth", "smallworlds-console", "https://console.example.test/callback", "email profile", nil)
	if err != nil {
		t.Fatalf("auth request: %v", err)
	}
	provider.idToken = key.sign(t, standardClaims(provider.server.URL, "smallworlds-console", authRequest.Nonce))

	claims, role, err := CompleteLogin(context.Background(), exchanger, authRequest, "code", authRequest.State, Expectations{
		Issuer:   provider.server.URL,
		Audience: "smallworlds-console",
		Now:      time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("complete login: %v", err)
	}
	if role != RoleOperator {
		t.Fatalf("role = %q, want operator", role)
	}
	if claims.PreferredUsername != "ada" {
		t.Fatalf("username = %q", claims.PreferredUsername)
	}
}

// In a Local LAN-only cluster the identity provider is served on the community's
// own hostname with a self-signed certificate this process cannot verify. The
// console must still be able to reach it — from inside the cluster — while the
// issuer it validates against stays the external identity the browser uses.
// This is the case that crashlooped the first deployed console.
func TestDiscoverViaReadsAnUnreachableIssuerFromInside(t *testing.T) {
	const externalIssuer = "https://identity.home.example/realms/smallworlds"
	provider := newIdentityProvider(t, newRSASigningKey(t, "key-1"))
	// The document names the external issuer, exactly as Keycloak does when
	// KC_HOSTNAME is set, while being served from the in-cluster address.
	provider.issuerOverride = externalIssuer

	endpoints, err := DiscoverVia(context.Background(), provider.server.Client(), externalIssuer, provider.server.URL)
	if err != nil {
		t.Fatalf("discover via the internal address: %v", err)
	}
	// Machine-to-machine endpoints move to the reachable address.
	if !strings.HasPrefix(endpoints.TokenEndpoint, provider.server.URL) {
		t.Fatalf("token endpoint = %q, want it on the internal address", endpoints.TokenEndpoint)
	}
	if !strings.HasPrefix(endpoints.JWKSURI, provider.server.URL) {
		t.Fatalf("jwks uri = %q, want it on the internal address", endpoints.JWKSURI)
	}
	// The browser-facing endpoint must not: an Operator has to be able to reach it.
	if !strings.HasPrefix(endpoints.AuthorizationEndpoint, "https://identity.home.example") {
		t.Fatalf("authorization endpoint = %q, want the external address", endpoints.AuthorizationEndpoint)
	}
	// The realm path survives the rewrite.
	if !strings.Contains(endpoints.TokenEndpoint, "/realms/smallworlds/protocol/openid-connect/token") {
		t.Fatalf("token endpoint lost its path: %q", endpoints.TokenEndpoint)
	}
}

// Fetching from elsewhere is only safe because the document still has to name
// the configured issuer. Without that check, pointing the console at any
// reachable address would move its trust anchor.
func TestDiscoverViaStillPinsTheConfiguredIssuer(t *testing.T) {
	provider := newIdentityProvider(t, newRSASigningKey(t, "key-1"))
	provider.issuerOverride = "https://attacker.example.test/realms/smallworlds"

	_, err := DiscoverVia(context.Background(), provider.server.Client(), "https://identity.home.example/realms/smallworlds", provider.server.URL)
	if err == nil {
		t.Fatal("expected a document naming a different issuer to be refused")
	}
}

// A provider entitled to publish an endpoint on a third host keeps it:
// rewriting one blindly would send a token request to an address nobody chose.
func TestDiscoverViaLeavesForeignEndpointsAlone(t *testing.T) {
	provider := newIdentityProvider(t, newRSASigningKey(t, "key-1"))
	provider.issuerOverride = "https://identity.home.example/realms/smallworlds"
	provider.jwksOverride = "https://keys.elsewhere.test/jwks.json"

	endpoints, err := DiscoverVia(context.Background(), provider.server.Client(), "https://identity.home.example/realms/smallworlds", provider.server.URL)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if endpoints.JWKSURI != "https://keys.elsewhere.test/jwks.json" {
		t.Fatalf("jwks uri = %q, want it untouched", endpoints.JWKSURI)
	}
}

// The whole login has to work through the internal address, not just discovery.
func TestLiveExchangerCompletesLoginThroughTheInternalAddress(t *testing.T) {
	const externalIssuer = "https://identity.home.example/realms/smallworlds"
	key := newRSASigningKey(t, "key-1")
	provider := newIdentityProvider(t, key)
	provider.issuerOverride = externalIssuer

	exchanger, err := NewLiveExchanger(context.Background(), provider.server.Client(), externalIssuer, provider.server.URL, "smallworlds-console", "", "https://console.home.example/api/v1/auth/callback")
	if err != nil {
		t.Fatalf("build exchanger: %v", err)
	}
	provider.idToken = key.sign(t, standardClaims(externalIssuer, "smallworlds-console", "nonce-abc"))

	claims, err := exchanger.Exchange(context.Background(), "the-code", "the-verifier")
	if err != nil {
		t.Fatalf("exchange through the internal address: %v", err)
	}
	if claims.Issuer != externalIssuer {
		t.Fatalf("issuer = %q, want the external identity", claims.Issuer)
	}
	if err := ValidateClaims(claims, Expectations{Issuer: externalIssuer, Audience: "smallworlds-console", Nonce: "nonce-abc", Now: time.Now().UTC()}); err != nil {
		t.Fatalf("claims from an internally fetched token must still validate: %v", err)
	}
}
