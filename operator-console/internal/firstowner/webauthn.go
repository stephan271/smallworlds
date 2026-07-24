package firstowner

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math/big"
	"net/url"
)

// COSE algorithm identifiers used by common passkeys.
const (
	coseES256 int64 = -7
	coseEdDSA int64 = -8
	coseES384 int64 = -35
)

// authenticator-data flag bits.
const (
	flagUserPresent            byte = 0x01
	flagAttestedCredentialData byte = 0x40
)

var errAttestation = errors.New("attestation statement verification failed")

// WebAuthnPasskeyVerifier performs a full WebAuthn registration ceremony: it
// validates clientDataJSON (type, challenge, origin), the authenticator data
// (RP ID hash, user-present and attested-credential-data flags), binds the
// credential id, extracts the credential public key, and verifies the
// attestation statement for the "none" and "packed" formats.
type WebAuthnPasskeyVerifier struct {
	RPID        string
	AllowOrigin func(origin string) bool
}

// NewWebAuthnPasskeyVerifier constructs a verifier for a relying-party id and an
// origin allow predicate.
func NewWebAuthnPasskeyVerifier(rpID string, allowOrigin func(origin string) bool) WebAuthnPasskeyVerifier {
	return WebAuthnPasskeyVerifier{RPID: rpID, AllowOrigin: allowOrigin}
}

// LoopbackOriginAllowed accepts only loopback origins, matching how the launcher
// serves the client over http on 127.0.0.1.
func LoopbackOriginAllowed(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	host := parsed.Hostname()
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

// Verify runs the registration ceremony and returns the credential id to persist.
func (verifier WebAuthnPasskeyVerifier) Verify(_ context.Context, expectedChallenge string, registration Registration) (string, error) {
	if registration.CredentialID == "" || registration.ClientDataJSON == "" || registration.AttestationObject == "" {
		return "", ErrInvalidRegistration
	}
	clientDataBytes, err := decodeBase64URL(registration.ClientDataJSON)
	if err != nil {
		return "", ErrInvalidRegistration
	}
	var clientData struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
		Origin    string `json:"origin"`
	}
	if err := json.Unmarshal(clientDataBytes, &clientData); err != nil || clientData.Type != "webauthn.create" {
		return "", ErrInvalidRegistration
	}
	expected, err := decodeBase64URL(expectedChallenge)
	got, gotErr := decodeBase64URL(clientData.Challenge)
	if err != nil || gotErr != nil || len(expected) == 0 || subtle.ConstantTimeCompare(expected, got) != 1 {
		return "", ErrChallengeMismatch
	}
	if verifier.AllowOrigin == nil || !verifier.AllowOrigin(clientData.Origin) {
		return "", ErrInvalidRegistration
	}

	attObjBytes, err := decodeBase64URL(registration.AttestationObject)
	if err != nil {
		return "", ErrInvalidRegistration
	}
	decoded, _, err := cborDecode(attObjBytes)
	if err != nil {
		return "", ErrInvalidRegistration
	}
	attestation, ok := decoded.(map[any]any)
	if !ok {
		return "", ErrInvalidRegistration
	}
	format, _ := cborString(attestation, "fmt")
	authData, ok := cborBytes(attestation, "authData")
	if !ok {
		return "", ErrInvalidRegistration
	}
	rpIDHash, flags, credentialID, coseKey, err := parseAuthenticatorData(authData)
	if err != nil {
		return "", ErrInvalidRegistration
	}
	rpHash := sha256.Sum256([]byte(verifier.RPID))
	if !bytes.Equal(rpIDHash, rpHash[:]) {
		return "", ErrInvalidRegistration
	}
	if flags&flagUserPresent == 0 {
		return "", ErrInvalidRegistration
	}
	requestedID, err := decodeBase64URL(registration.CredentialID)
	if err != nil || !bytes.Equal(requestedID, credentialID) {
		return "", ErrInvalidRegistration
	}
	publicKey, algorithm, err := parseCOSEKey(coseKey)
	if err != nil {
		return "", ErrInvalidRegistration
	}

	clientDataHash := sha256.Sum256(clientDataBytes)
	switch format {
	case "none":
		// A "none" attestation carries no statement to verify; the ceremony
		// checks above already bind the credential to this challenge and RP.
	case "packed":
		statement, _ := cborMap(attestation, "attStmt")
		if err := verifyPackedAttestation(statement, authData, clientDataHash[:], publicKey, algorithm); err != nil {
			return "", ErrInvalidRegistration
		}
	default:
		return "", ErrInvalidRegistration
	}
	return registration.CredentialID, nil
}

func parseAuthenticatorData(authData []byte) (rpIDHash []byte, flags byte, credentialID []byte, coseKey []byte, err error) {
	if len(authData) < 37 {
		return nil, 0, nil, nil, errAttestation
	}
	rpIDHash = authData[:32]
	flags = authData[32]
	if flags&flagAttestedCredentialData == 0 {
		return nil, 0, nil, nil, errAttestation
	}
	rest := authData[37:]
	if len(rest) < 18 {
		return nil, 0, nil, nil, errAttestation
	}
	credentialIDLength := int(binary.BigEndian.Uint16(rest[16:18]))
	rest = rest[18:]
	if len(rest) < credentialIDLength {
		return nil, 0, nil, nil, errAttestation
	}
	credentialID = rest[:credentialIDLength]
	coseKey = rest[credentialIDLength:]
	return rpIDHash, flags, credentialID, coseKey, nil
}

func parseCOSEKey(coseKey []byte) (any, int64, error) {
	decoded, _, err := cborDecode(coseKey)
	if err != nil {
		return nil, 0, err
	}
	key, ok := decoded.(map[any]any)
	if !ok {
		return nil, 0, errAttestation
	}
	keyType, _ := cborInt(key, int64(1))
	algorithm, _ := cborInt(key, int64(3))
	switch keyType {
	case 2: // EC2
		curveID, _ := cborInt(key, int64(-1))
		x, okX := cborBytes(key, int64(-2))
		y, okY := cborBytes(key, int64(-3))
		if !okX || !okY {
			return nil, 0, errAttestation
		}
		var curve elliptic.Curve
		switch curveID {
		case 1:
			curve = elliptic.P256()
		case 2:
			curve = elliptic.P384()
		default:
			return nil, 0, errAttestation
		}
		return &ecdsa.PublicKey{Curve: curve, X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}, algorithm, nil
	case 1: // OKP (Ed25519)
		curveID, _ := cborInt(key, int64(-1))
		x, ok := cborBytes(key, int64(-2))
		if curveID != 6 || !ok || len(x) != ed25519.PublicKeySize {
			return nil, 0, errAttestation
		}
		return ed25519.PublicKey(append([]byte(nil), x...)), algorithm, nil
	default:
		return nil, 0, errAttestation
	}
}

func verifyPackedAttestation(statement map[any]any, authData, clientDataHash []byte, credentialKey any, credentialAlgorithm int64) error {
	algorithm, okAlg := cborInt(statement, "alg")
	signature, okSig := cborBytes(statement, "sig")
	if !okAlg || !okSig || len(signature) == 0 {
		return errAttestation
	}
	signed := append(append([]byte(nil), authData...), clientDataHash...)
	if chain, ok := cborArray(statement, "x5c"); ok && len(chain) > 0 {
		leaf, ok := chain[0].([]byte)
		if !ok {
			return errAttestation
		}
		certificate, err := x509.ParseCertificate(leaf)
		if err != nil {
			return errAttestation
		}
		// The x5c leaf attests possession; full chain validation against FIDO
		// metadata trust anchors is out of scope and documented as future work.
		return verifySignature(certificate.PublicKey, algorithm, signed, signature)
	}
	// Self attestation: the statement algorithm must match the credential key.
	if algorithm != credentialAlgorithm {
		return errAttestation
	}
	return verifySignature(credentialKey, algorithm, signed, signature)
}

func verifySignature(publicKey any, algorithm int64, signed, signature []byte) error {
	switch algorithm {
	case coseES256, coseES384:
		key, ok := publicKey.(*ecdsa.PublicKey)
		if !ok {
			return errAttestation
		}
		var digest []byte
		if algorithm == coseES256 {
			sum := sha256.Sum256(signed)
			digest = sum[:]
		} else {
			sum := sha512.Sum384(signed)
			digest = sum[:]
		}
		if !ecdsa.VerifyASN1(key, digest, signature) {
			return errAttestation
		}
		return nil
	case coseEdDSA:
		key, ok := publicKey.(ed25519.PublicKey)
		if !ok || !ed25519.Verify(key, signed, signature) {
			return errAttestation
		}
		return nil
	default:
		return errAttestation
	}
}

func decodeBase64URL(value string) ([]byte, error) {
	if decoded, err := base64.RawURLEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return base64.URLEncoding.DecodeString(value)
}
