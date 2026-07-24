package clusterca_test

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/clusterca"
)

func mustAuthority(t *testing.T) (clusterca.Authority, clusterca.Intermediate, time.Time) {
	t.Helper()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	authority, err := clusterca.CreateAuthority("profile-1", now)
	if err != nil {
		t.Fatalf("create authority: %v", err)
	}
	intermediate, err := authority.IssueIntermediate(now)
	if err != nil {
		t.Fatalf("issue intermediate: %v", err)
	}
	return authority, intermediate, now
}

func parse(t *testing.T, certificatePEM string) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode([]byte(certificatePEM))
	if block == nil {
		t.Fatal("no PEM block")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return certificate
}

func TestRootIsConstrainedAndOnlyIntermediateSignsLeaves(t *testing.T) {
	authority, intermediate, now := mustAuthority(t)

	root := parse(t, authority.RootCertificatePEM())
	if !root.IsCA || root.MaxPathLen != 1 || root.MaxPathLenZero {
		t.Fatalf("root path length not constrained to a single intermediate: isCA=%v maxPathLen=%d zero=%v", root.IsCA, root.MaxPathLen, root.MaxPathLenZero)
	}

	middle := parse(t, intermediate.CertificatePEM())
	if !middle.IsCA || !middle.MaxPathLenZero {
		t.Fatalf("intermediate can issue further CAs: isCA=%v maxPathLenZero=%v", middle.IsCA, middle.MaxPathLenZero)
	}

	chainPEM, _, err := intermediate.IssueServerCertificate([]string{"console.smallworlds.internal"}, now)
	if err != nil {
		t.Fatalf("issue leaf: %v", err)
	}
	leaf := parse(t, chainPEM)

	roots := x509.NewCertPool()
	roots.AddCert(root)
	intermediates := x509.NewCertPool()
	intermediates.AddCert(middle)
	if _, err := leaf.Verify(x509.VerifyOptions{
		DNSName:       "console.smallworlds.internal",
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   now,
	}); err != nil {
		t.Fatalf("leaf does not chain to the Cluster CA root: %v", err)
	}

	// A leaf that was not issued by the intermediate must not verify.
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: "console.smallworlds.internal", Roots: roots, CurrentTime: now}); err == nil {
		t.Fatal("leaf verified without the intermediate in the chain")
	}
}

func TestDeviceTrustAndClusterMaterialNeverExposeRootKey(t *testing.T) {
	authority, intermediate, _ := mustAuthority(t)

	rootKey, err := authority.RootPrivateKeyPEM()
	if err != nil {
		t.Fatalf("root key: %v", err)
	}

	trust := authority.DeviceTrust()
	if !strings.Contains(trust.RootCertificatePEM, "CERTIFICATE") {
		t.Fatal("device trust missing root certificate")
	}
	encoded, err := json.Marshal(trust)
	if err != nil {
		t.Fatalf("marshal device trust: %v", err)
	}
	if strings.Contains(string(encoded), "PRIVATE KEY") {
		t.Fatalf("device trust leaked a private key: %s", encoded)
	}
	if !strings.HasPrefix(trust.Fingerprint, "SHA256:") {
		t.Fatalf("device trust fingerprint = %q", trust.Fingerprint)
	}

	// The only signing key handed to the cluster is the intermediate's; the
	// root key must never appear in cluster-bound material.
	intermediateKey, err := intermediate.PrivateKeyPEM()
	if err != nil {
		t.Fatalf("intermediate key: %v", err)
	}
	clusterMaterial := intermediate.CertificatePEM() + intermediateKey + authority.RootCertificatePEM()
	if strings.Contains(clusterMaterial, rootKey) {
		t.Fatal("root private key delivered to the cluster")
	}
	if intermediateKey == rootKey {
		t.Fatal("intermediate reused the root private key")
	}
}

func TestReferenceIsSecretFreeAndDigestBindsIdentity(t *testing.T) {
	authority, intermediate, _ := mustAuthority(t)
	reference := authority.Reference(intermediate)

	encoded, err := json.Marshal(reference)
	if err != nil {
		t.Fatalf("marshal reference: %v", err)
	}
	if strings.Contains(string(encoded), "PRIVATE KEY") {
		t.Fatalf("reference leaked secret material: %s", encoded)
	}
	if err := reference.Validate(); err != nil {
		t.Fatalf("valid reference rejected: %v", err)
	}

	digest, err := reference.Digest()
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	again, _ := reference.Digest()
	if digest != again {
		t.Fatal("digest is not stable")
	}
	tampered := reference
	tampered.RootFingerprint = "SHA256:00"
	if changed, _ := tampered.Digest(); changed == digest {
		t.Fatal("digest does not bind the root identity")
	}
}

func TestReferenceRejectsIntermediateOutlivingRoot(t *testing.T) {
	authority, intermediate, _ := mustAuthority(t)
	reference := authority.Reference(intermediate)
	reference.IntermediateNotAfter = reference.RootNotAfter.Add(time.Hour)
	if err := reference.Validate(); err == nil {
		t.Fatal("intermediate outliving the root was accepted")
	}
}

func TestLoadAuthorityRoundTripsAndRejectsMismatchedKey(t *testing.T) {
	authority, intermediate, now := mustAuthority(t)
	rootCert := authority.RootCertificatePEM()
	rootKey, err := authority.RootPrivateKeyPEM()
	if err != nil {
		t.Fatalf("root key: %v", err)
	}

	loaded, err := clusterca.LoadAuthority(rootCert, rootKey)
	if err != nil {
		t.Fatalf("load authority: %v", err)
	}
	if _, err := loaded.IssueIntermediate(now); err != nil {
		t.Fatalf("resumed authority cannot issue: %v", err)
	}

	intermediateKey, err := intermediate.PrivateKeyPEM()
	if err != nil {
		t.Fatalf("intermediate key: %v", err)
	}
	if _, err := clusterca.LoadAuthority(rootCert, intermediateKey); err == nil {
		t.Fatal("mismatched key accepted for root certificate")
	}
}

func TestIssueServerCertificateRejectsUnsafeHostnames(t *testing.T) {
	_, intermediate, now := mustAuthority(t)
	for name, hostnames := range map[string][]string{
		"empty":     {},
		"traversal": {"console..internal"},
		"injection": {"console.internal\nHost: evil"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := intermediate.IssueServerCertificate(hostnames, now); err == nil {
				t.Fatal("unsafe hostname accepted")
			}
		})
	}
}
