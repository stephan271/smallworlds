// Package webauthntest builds valid (and deliberately tamperable) WebAuthn
// registration payloads for tests. It mirrors what navigator.credentials.create
// returns, using an ES256 credential, so the firstowner WebAuthn verifier can be
// exercised deterministically without a real authenticator.
package webauthntest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"

	"github.com/stephan271/smallworlds/operator-console/internal/firstowner"
)

// Options controls how a registration payload is built. The zero value (with a
// Challenge set) produces a valid "none"-format registration for the loopback
// relying party.
type Options struct {
	Challenge              string // base64url challenge the relying party issued
	RPID                   string // defaults to 127.0.0.1
	Origin                 string // defaults to http://127.0.0.1:4174
	Format                 string // "none" (default) or "packed"
	RPIDForHash            string // overrides the RP ID hashed into authData
	ChallengeForClientData string // overrides the challenge echoed in clientDataJSON
	NoUserPresent          bool   // clears the user-present flag
}

// Registration builds a WebAuthn registration from the options.
func Registration(options Options) (firstowner.Registration, error) {
	rpID := valueOr(options.RPID, "127.0.0.1")
	origin := valueOr(options.Origin, "http://127.0.0.1:4174")
	format := valueOr(options.Format, "none")
	rpIDForHash := valueOr(options.RPIDForHash, rpID)
	challengeInClientData := valueOr(options.ChallengeForClientData, options.Challenge)

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return firstowner.Registration{}, err
	}
	credentialID := make([]byte, 16)
	if _, err := rand.Read(credentialID); err != nil {
		return firstowner.Registration{}, err
	}

	coseKey := encode(cborMap{
		{int64(1), int64(2)},              // kty: EC2
		{int64(3), int64(-7)},             // alg: ES256
		{int64(-1), int64(1)},             // crv: P-256
		{int64(-2), pad32(key.X.Bytes())}, // x
		{int64(-3), pad32(key.Y.Bytes())}, // y
	})

	rpHash := sha256.Sum256([]byte(rpIDForHash))
	var flags byte = 0x40 | 0x04 // attested credential data + user verified
	if !options.NoUserPresent {
		flags |= 0x01
	}
	authData := make([]byte, 0, 37+18+len(credentialID)+len(coseKey))
	authData = append(authData, rpHash[:]...)
	authData = append(authData, flags)
	authData = append(authData, 0, 0, 0, 0) // signature counter
	authData = append(authData, make([]byte, 16)...)
	authData = binary.BigEndian.AppendUint16(authData, uint16(len(credentialID)))
	authData = append(authData, credentialID...)
	authData = append(authData, coseKey...)

	clientData, err := json.Marshal(map[string]string{
		"type":      "webauthn.create",
		"challenge": challengeInClientData,
		"origin":    origin,
	})
	if err != nil {
		return firstowner.Registration{}, err
	}

	statement := cborMap{}
	if format == "packed" {
		clientDataHash := sha256.Sum256(clientData)
		signed := append(append([]byte(nil), authData...), clientDataHash[:]...)
		digest := sha256.Sum256(signed)
		signature, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
		if err != nil {
			return firstowner.Registration{}, err
		}
		statement = cborMap{{"alg", int64(-7)}, {"sig", signature}}
	}

	attestationObject := encode(cborMap{
		{"fmt", format},
		{"attStmt", statement},
		{"authData", authData},
	})

	return firstowner.Registration{
		CredentialID:      base64.RawURLEncoding.EncodeToString(credentialID),
		ClientDataJSON:    base64.RawURLEncoding.EncodeToString(clientData),
		AttestationObject: base64.RawURLEncoding.EncodeToString(attestationObject),
	}, nil
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func pad32(value []byte) []byte {
	if len(value) >= 32 {
		return value
	}
	padded := make([]byte, 32)
	copy(padded[32-len(value):], value)
	return padded
}

// cborMap is an ordered list of key/value pairs encoded as a CBOR map.
type cborMap []struct {
	key   any
	value any
}

func encode(value any) []byte {
	switch typed := value.(type) {
	case int64:
		if typed >= 0 {
			return encodeHead(0, uint64(typed))
		}
		return encodeHead(1, uint64(-1-typed))
	case uint64:
		return encodeHead(0, typed)
	case []byte:
		return append(encodeHead(2, uint64(len(typed))), typed...)
	case string:
		return append(encodeHead(3, uint64(len(typed))), typed...)
	case []any:
		out := encodeHead(4, uint64(len(typed)))
		for _, item := range typed {
			out = append(out, encode(item)...)
		}
		return out
	case cborMap:
		out := encodeHead(5, uint64(len(typed)))
		for _, pair := range typed {
			out = append(out, encode(pair.key)...)
			out = append(out, encode(pair.value)...)
		}
		return out
	default:
		panic("webauthntest: unsupported CBOR value")
	}
}

func encodeHead(major byte, argument uint64) []byte {
	prefix := major << 5
	switch {
	case argument < 24:
		return []byte{prefix | byte(argument)}
	case argument < 1<<8:
		return []byte{prefix | 24, byte(argument)}
	case argument < 1<<16:
		return binary.BigEndian.AppendUint16([]byte{prefix | 25}, uint16(argument))
	case argument < 1<<32:
		return binary.BigEndian.AppendUint32([]byte{prefix | 26}, uint32(argument))
	default:
		return binary.BigEndian.AppendUint64([]byte{prefix | 27}, argument)
	}
}
