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
		BaseDomain:              "smallworlds.internal",
		GatewayHostname:         "gateway.smallworlds.internal",
		OperatorHosts:           []string{"console.smallworlds.internal"},
		RootFingerprint:         "SHA256:AA",
		RootCertificatePEM:      rootPEM,
		GatewayIdentityHostname: "gateway.smallworlds.internal",
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
		if observations.TLSChainsToClusterCA {
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
