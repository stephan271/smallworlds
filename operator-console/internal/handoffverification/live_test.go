package handoffverification_test

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/clusterca"
	"github.com/stephan271/smallworlds/operator-console/internal/handoffverification"
)

func liveChain(t *testing.T) (rootPEM string, chain []*x509.Certificate) {
	t.Helper()
	now := time.Now()
	authority, err := clusterca.CreateAuthority("profile-1", now)
	if err != nil {
		t.Fatal(err)
	}
	intermediate, err := authority.IssueIntermediate(now)
	if err != nil {
		t.Fatal(err)
	}
	chainPEM, _, err := intermediate.IssueServerCertificate([]string{"console.smallworlds.internal"}, now)
	if err != nil {
		t.Fatal(err)
	}
	rest := []byte(chainPEM)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		chain = append(chain, certificate)
	}
	return authority.RootCertificatePEM(), chain
}

func liveTarget(rootPEM string) handoffverification.Target {
	return handoffverification.Target{
		Anchor:                  handoffverification.ClusterCARoot,
		BaseDomain:              "smallworlds.internal",
		GatewayHostname:         "gateway.smallworlds.internal",
		OperatorHosts:           []string{"console.smallworlds.internal"},
		RootFingerprint:         "SHA256:AA",
		RootCertificatePEM:      rootPEM,
		GatewayIdentityHostname: "gateway.smallworlds.internal",
	}
}

// A publicly addressed installation has no root to pin: its operator interfaces
// hold publicly trusted ACME certificates, and verification must apply the same
// trust an Operator's browser will. Presenting the private Cluster CA chain
// there must fail — a certificate no browser accepts is not a verified handoff.
func TestLiveVerifierUsesPublicTrustWhenThereIsNoPinnedRoot(t *testing.T) {
	_, chain := liveChain(t)
	verifier := &handoffverification.LiveVerifier{
		LookupHost:    func(context.Context, string) ([]string, error) { return []string{"100.64.0.1"}, nil },
		DialReachable: func(context.Context, string) error { return nil },
		DialTLS:       func(context.Context, string, string) ([]*x509.Certificate, error) { return chain, nil },
		Port:          "443",
	}
	target := handoffverification.Target{
		Anchor:                  handoffverification.PublicTrust,
		BaseDomain:              "ops.smallworlds.internal",
		GatewayHostname:         "gateway.ops.smallworlds.internal",
		OperatorHosts:           []string{"console.smallworlds.internal"},
		GatewayIdentityHostname: "gateway.ops.smallworlds.internal",
	}
	observations, err := verifier.Observe(context.Background(), target)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if observations.TLSTrusted {
		t.Fatal("a privately signed certificate was accepted against the public trust store")
	}
	// The other three checks are unaffected by the anchor.
	if !observations.PrivateReachable || !observations.DNSResolves || !observations.GatewayIdentityMatches {
		t.Fatalf("observations = %+v, want only the TLS check failing", observations)
	}
	if handoffverification.Evaluate(observations).PermitsClosure() {
		t.Fatal("closure permitted with untrusted operator TLS")
	}
}

// A target must carry exactly the trust material its anchor calls for: a pinned
// root alongside PublicTrust would silently widen what verification accepts,
// and a missing root under ClusterCARoot leaves nothing to verify against.
func TestTargetRejectsTrustMaterialThatDoesNotMatchItsAnchor(t *testing.T) {
	rootPEM, _ := liveChain(t)
	base := handoffverification.Target{
		BaseDomain:              "smallworlds.internal",
		GatewayHostname:         "gateway.smallworlds.internal",
		OperatorHosts:           []string{"console.smallworlds.internal"},
		GatewayIdentityHostname: "gateway.smallworlds.internal",
	}
	public := base
	public.Anchor = handoffverification.PublicTrust
	if err := public.Validate(); err != nil {
		t.Fatalf("a public-trust target with no pinned root was rejected: %v", err)
	}
	public.RootCertificatePEM = rootPEM
	if err := public.Validate(); !errors.Is(err, handoffverification.ErrInvalidTarget) {
		t.Fatal("a pinned root alongside public trust was accepted")
	}

	private := base
	private.Anchor = handoffverification.ClusterCARoot
	if err := private.Validate(); !errors.Is(err, handoffverification.ErrInvalidTarget) {
		t.Fatal("a Cluster CA target with no root was accepted")
	}

	unset := base
	if err := unset.Validate(); !errors.Is(err, handoffverification.ErrInvalidTarget) {
		t.Fatal("a target with no trust anchor was accepted")
	}
}

func TestLiveVerifierObservesAllChecksWhenHealthy(t *testing.T) {
	rootPEM, chain := liveChain(t)
	verifier := &handoffverification.LiveVerifier{
		LookupHost:    func(context.Context, string) ([]string, error) { return []string{"100.64.0.1"}, nil },
		DialReachable: func(context.Context, string) error { return nil },
		DialTLS:       func(context.Context, string, string) ([]*x509.Certificate, error) { return chain, nil },
		Port:          "443",
	}
	observations, err := verifier.Observe(context.Background(), liveTarget(rootPEM))
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	report := handoffverification.Evaluate(observations)
	if !report.Verified {
		t.Fatalf("healthy cluster did not verify: %#v", observations)
	}
}

func TestLiveVerifierReportsIndividualFailures(t *testing.T) {
	rootPEM, chain := liveChain(t)
	_, otherChain := liveChain(t) // a leaf that does not chain to rootPEM

	base := func() *handoffverification.LiveVerifier {
		return &handoffverification.LiveVerifier{
			LookupHost:    func(context.Context, string) ([]string, error) { return []string{"100.64.0.1"}, nil },
			DialReachable: func(context.Context, string) error { return nil },
			DialTLS:       func(context.Context, string, string) ([]*x509.Certificate, error) { return chain, nil },
		}
	}

	t.Run("dns failure", func(t *testing.T) {
		verifier := base()
		verifier.LookupHost = func(_ context.Context, host string) ([]string, error) {
			if host == "console.smallworlds.internal" {
				return nil, errors.New("nxdomain")
			}
			return []string{"100.64.0.1"}, nil
		}
		observations, _ := verifier.Observe(context.Background(), liveTarget(rootPEM))
		if observations.DNSResolves || observations.GatewayIdentityMatches {
			t.Fatalf("dns failure not reflected: %#v", observations)
		}
	})

	t.Run("gateway identity mismatch", func(t *testing.T) {
		verifier := base()
		verifier.LookupHost = func(_ context.Context, host string) ([]string, error) {
			if host == "gateway.smallworlds.internal" {
				return []string{"100.64.0.9"}, nil
			}
			return []string{"100.64.0.1"}, nil
		}
		observations, _ := verifier.Observe(context.Background(), liveTarget(rootPEM))
		if !observations.DNSResolves || observations.GatewayIdentityMatches {
			t.Fatalf("operator hosts not pointing at the gateway should fail identity: %#v", observations)
		}
	})

	t.Run("unreachable", func(t *testing.T) {
		verifier := base()
		verifier.DialReachable = func(context.Context, string) error { return errors.New("connection refused") }
		observations, _ := verifier.Observe(context.Background(), liveTarget(rootPEM))
		if observations.PrivateReachable {
			t.Fatalf("unreachable gateway reported reachable: %#v", observations)
		}
	})

	t.Run("untrusted tls", func(t *testing.T) {
		verifier := base()
		verifier.DialTLS = func(context.Context, string, string) ([]*x509.Certificate, error) { return otherChain, nil }
		observations, _ := verifier.Observe(context.Background(), liveTarget(rootPEM))
		if observations.TLSTrusted {
			t.Fatalf("leaf not chaining to the pinned root reported trusted: %#v", observations)
		}
	})
}

func TestLiveVerifierRejectsInvalidTarget(t *testing.T) {
	verifier := handoffverification.NewLiveVerifier()
	if _, err := verifier.Observe(context.Background(), handoffverification.Target{}); !errors.Is(err, handoffverification.ErrInvalidTarget) {
		t.Fatalf("invalid target error = %v", err)
	}
}
