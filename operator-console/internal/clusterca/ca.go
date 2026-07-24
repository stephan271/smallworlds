// Package clusterca owns the LAN-only Cluster CA trust hierarchy for the Local
// private administration handoff. The Lifecycle Authority holds an offline root
// whose private key never leaves the Launcher Host; the cluster only ever
// receives a path-length-constrained intermediate so cert-manager can mint leaf
// certificates for operator hostnames; and an Operator Device explicitly
// installs the root certificate — never a private key — so operator interfaces
// are trusted over HTTPS without a publicly registered domain.
package clusterca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
)

// ErrInvalidAuthority is returned when root material is missing, malformed, or a
// loaded key does not match its certificate.
var ErrInvalidAuthority = errors.New("cluster CA authority is invalid")

// ErrInvalidReference is returned when secret-free CA metadata fails validation.
var ErrInvalidReference = errors.New("cluster CA reference is invalid")

// ErrInvalidHostname is returned when a requested leaf hostname is unsafe.
var ErrInvalidHostname = errors.New("cluster CA hostname is invalid")

const (
	rootValidity         = 20 * 365 * 24 * time.Hour
	intermediateValidity = 5 * 365 * 24 * time.Hour
	leafValidity         = 90 * 24 * time.Hour
	maxClockSkew         = time.Hour
)

// safeProfileID matches the launcher's opaque profile identifiers
// (base64.RawURLEncoding), which may lead with any of [A-Za-z0-9_-].
var safeProfileID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var safeHostname = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

// Authority is the sensitive Cluster CA root held only by the Lifecycle
// Authority. The private key is intended for Launcher Vault custody and never
// leaves the Launcher Host.
type Authority struct {
	certificate *x509.Certificate
	privateKey  *ecdsa.PrivateKey
}

// Intermediate is the path-length-constrained signing material delivered to the
// cluster as a Cluster Secret (outside Git) so cert-manager can issue leaf
// certificates. It can sign only end-entity leaves, never further sub-CAs.
type Intermediate struct {
	certificate *x509.Certificate
	privateKey  *ecdsa.PrivateKey
	root        *x509.Certificate
}

// CreateAuthority generates a fresh offline Cluster CA root bound to a profile.
// The root is constrained so a valid chain below it may contain exactly one
// intermediate CA and then only end-entity leaves.
func CreateAuthority(profileID string, now time.Time) (Authority, error) {
	if !safeProfileID.MatchString(profileID) {
		return Authority{}, fmt.Errorf("%w: profile", ErrInvalidAuthority)
	}
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		return Authority{}, fmt.Errorf("generate root key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return Authority{}, err
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "SmallWorlds Cluster CA Root " + profileID, Organization: []string{"SmallWorlds"}},
		NotBefore:             now.Add(-maxClockSkew).UTC(),
		NotAfter:              now.Add(rootValidity).UTC(),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}
	certificate, err := selfSign(template, key)
	if err != nil {
		return Authority{}, err
	}
	return Authority{certificate: certificate, privateKey: key}, nil
}

// LoadAuthority reconstructs a root from stored PEM (Launcher Vault custody) so
// an interrupted handoff can resume issuing and verifying without regeneration.
func LoadAuthority(rootCertificatePEM, rootPrivateKeyPEM string) (Authority, error) {
	certificate, err := parseCertificate(rootCertificatePEM)
	if err != nil {
		return Authority{}, err
	}
	if !certificate.IsCA {
		return Authority{}, fmt.Errorf("%w: root is not a CA", ErrInvalidAuthority)
	}
	key, err := parsePrivateKey(rootPrivateKeyPEM)
	if err != nil {
		return Authority{}, err
	}
	public, ok := certificate.PublicKey.(*ecdsa.PublicKey)
	if !ok || public.X.Cmp(key.PublicKey.X) != 0 || public.Y.Cmp(key.PublicKey.Y) != 0 {
		return Authority{}, fmt.Errorf("%w: key does not match certificate", ErrInvalidAuthority)
	}
	return Authority{certificate: certificate, privateKey: key}, nil
}

// IssueIntermediate signs a cluster-bound intermediate CA whose own path length
// is zero, so cert-manager can mint leaf certificates but cannot issue further
// certificate authorities. Its lifetime is clamped to the root's.
func (authority Authority) IssueIntermediate(now time.Time) (Intermediate, error) {
	if authority.certificate == nil || authority.privateKey == nil {
		return Intermediate{}, ErrInvalidAuthority
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Intermediate{}, fmt.Errorf("generate intermediate key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return Intermediate{}, err
	}
	notAfter := now.Add(intermediateValidity).UTC()
	if notAfter.After(authority.certificate.NotAfter) {
		notAfter = authority.certificate.NotAfter
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "SmallWorlds Cluster CA Intermediate", Organization: []string{"SmallWorlds"}},
		NotBefore:             now.Add(-maxClockSkew).UTC(),
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, authority.certificate, &key.PublicKey, authority.privateKey)
	if err != nil {
		return Intermediate{}, fmt.Errorf("sign intermediate: %w", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return Intermediate{}, fmt.Errorf("parse intermediate: %w", err)
	}
	return Intermediate{certificate: certificate, privateKey: key, root: authority.certificate}, nil
}

// IssueServerCertificate mints a leaf TLS certificate for operator hostnames.
// It exists so the launcher can prove the LAN-only trust chain end to end; in
// production cert-manager performs the same signing from the intermediate. The
// returned chain PEM concatenates the leaf and the intermediate.
func (intermediate Intermediate) IssueServerCertificate(hostnames []string, now time.Time) (chainPEM string, keyPEM string, err error) {
	if intermediate.certificate == nil || intermediate.privateKey == nil {
		return "", "", ErrInvalidAuthority
	}
	if len(hostnames) == 0 {
		return "", "", fmt.Errorf("%w: no hostnames", ErrInvalidHostname)
	}
	for _, hostname := range hostnames {
		if !safeHostname.MatchString(hostname) || strings.Contains(hostname, "..") {
			return "", "", fmt.Errorf("%w: %q", ErrInvalidHostname, hostname)
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate leaf key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return "", "", err
	}
	notAfter := now.Add(leafValidity).UTC()
	if notAfter.After(intermediate.certificate.NotAfter) {
		notAfter = intermediate.certificate.NotAfter
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: hostnames[0], Organization: []string{"SmallWorlds"}},
		DNSNames:              append([]string(nil), hostnames...),
		NotBefore:             now.Add(-maxClockSkew).UTC(),
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, intermediate.certificate, &key.PublicKey, intermediate.privateKey)
	if err != nil {
		return "", "", fmt.Errorf("sign leaf: %w", err)
	}
	keyPEM, err = encodePrivateKey(key)
	if err != nil {
		return "", "", err
	}
	return encodeCertificate(der) + intermediate.CertificatePEM(), keyPEM, nil
}

// RootCertificatePEM returns the public root certificate.
func (authority Authority) RootCertificatePEM() string {
	return encodeCertificate(authority.certificate.Raw)
}

// RootPrivateKeyPEM returns the secret root key for Launcher Vault custody. It
// is never delivered to the cluster or to an Operator Device.
func (authority Authority) RootPrivateKeyPEM() (string, error) {
	return encodePrivateKey(authority.privateKey)
}

// CertificatePEM returns the intermediate certificate.
func (intermediate Intermediate) CertificatePEM() string {
	return encodeCertificate(intermediate.certificate.Raw)
}

// PrivateKeyPEM returns the secret intermediate key. This is the only signing
// key delivered to the cluster, injected as a Cluster Secret outside Git.
func (intermediate Intermediate) PrivateKeyPEM() (string, error) {
	return encodePrivateKey(intermediate.privateKey)
}

// DeviceTrust is the explicit, key-free trust material an Operator installs on
// the current device so its browser trusts operator interfaces.
type DeviceTrust struct {
	RootCertificatePEM string    `json:"rootCertificatePem"`
	Fingerprint        string    `json:"fingerprint"`
	Subject            string    `json:"subject"`
	NotAfter           time.Time `json:"notAfter"`
}

// DeviceTrust returns the root certificate to install on the current Operator
// Device. It never contains a private key.
func (authority Authority) DeviceTrust() DeviceTrust {
	return DeviceTrust{
		RootCertificatePEM: encodeCertificate(authority.certificate.Raw),
		Fingerprint:        fingerprint(authority.certificate.Raw),
		Subject:            authority.certificate.Subject.CommonName,
		NotAfter:           authority.certificate.NotAfter,
	}
}

// Reference is secret-free CA metadata safe to persist in SQLite and bind into
// a Change Plan. It contains no private key material.
type Reference struct {
	RootSubject             string    `json:"rootSubject"`
	RootSerial              string    `json:"rootSerial"`
	RootFingerprint         string    `json:"rootFingerprint"`
	RootNotBefore           time.Time `json:"rootNotBefore"`
	RootNotAfter            time.Time `json:"rootNotAfter"`
	IntermediateSubject     string    `json:"intermediateSubject"`
	IntermediateSerial      string    `json:"intermediateSerial"`
	IntermediateFingerprint string    `json:"intermediateFingerprint"`
	IntermediateNotAfter    time.Time `json:"intermediateNotAfter"`
}

// Reference derives the secret-free metadata for a root and its intermediate.
func (authority Authority) Reference(intermediate Intermediate) Reference {
	return Reference{
		RootSubject:             authority.certificate.Subject.CommonName,
		RootSerial:              serialText(authority.certificate.SerialNumber),
		RootFingerprint:         fingerprint(authority.certificate.Raw),
		RootNotBefore:           authority.certificate.NotBefore,
		RootNotAfter:            authority.certificate.NotAfter,
		IntermediateSubject:     intermediate.certificate.Subject.CommonName,
		IntermediateSerial:      serialText(intermediate.certificate.SerialNumber),
		IntermediateFingerprint: fingerprint(intermediate.certificate.Raw),
		IntermediateNotAfter:    intermediate.certificate.NotAfter,
	}
}

// Validate enforces the structural invariants of secret-free CA metadata.
func (reference Reference) Validate() error {
	if reference.RootSubject == "" || reference.IntermediateSubject == "" {
		return fmt.Errorf("%w: subject", ErrInvalidReference)
	}
	if !isHex(reference.RootSerial) || !isHex(reference.IntermediateSerial) {
		return fmt.Errorf("%w: serial", ErrInvalidReference)
	}
	if !strings.HasPrefix(reference.RootFingerprint, "SHA256:") || !strings.HasPrefix(reference.IntermediateFingerprint, "SHA256:") {
		return fmt.Errorf("%w: fingerprint", ErrInvalidReference)
	}
	if !reference.RootNotAfter.After(reference.RootNotBefore) {
		return fmt.Errorf("%w: root validity", ErrInvalidReference)
	}
	if reference.IntermediateNotAfter.After(reference.RootNotAfter) {
		return fmt.Errorf("%w: intermediate outlives root", ErrInvalidReference)
	}
	return nil
}

// Digest returns a stable content digest binding the CA identity into a plan.
func (reference Reference) Digest() (string, error) {
	if err := reference.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(reference)
	if err != nil {
		return "", fmt.Errorf("encode cluster CA reference: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func selfSign(template *x509.Certificate, key *ecdsa.PrivateKey) (*x509.Certificate, error) {
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("self-sign root: %w", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parse root: %w", err)
	}
	return certificate, nil
}

func randomSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}
	return serial.Add(serial, big.NewInt(1)), nil
}

func encodeCertificate(der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func encodePrivateKey(key *ecdsa.PrivateKey) (string, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", fmt.Errorf("marshal private key: %w", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})), nil
}

func parseCertificate(encoded string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(encoded))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%w: certificate PEM", ErrInvalidAuthority)
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAuthority, err)
	}
	return certificate, nil
}

func parsePrivateKey(encoded string) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(encoded))
	if block == nil || block.Type != "EC PRIVATE KEY" {
		return nil, fmt.Errorf("%w: key PEM", ErrInvalidAuthority)
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAuthority, err)
	}
	return key, nil
}

func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for index, value := range sum {
		parts[index] = fmt.Sprintf("%02X", value)
	}
	return "SHA256:" + strings.Join(parts, ":")
}

func serialText(serial *big.Int) string {
	return fmt.Sprintf("%x", serial)
}

func isHex(value string) bool {
	if value == "" {
		return false
	}
	_, err := hex.DecodeString(padHex(value))
	return err == nil
}

func padHex(value string) string {
	if len(value)%2 == 1 {
		return "0" + value
	}
	return value
}
