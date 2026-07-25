package handoffverification

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// LiveVerifier observes a target against a running LAN-only cluster. Its network
// primitives are injectable so the observation logic is deterministically
// testable; NewLiveVerifier wires the production stdlib implementations.
type LiveVerifier struct {
	LookupHost    func(ctx context.Context, host string) ([]string, error)
	DialReachable func(ctx context.Context, address string) error
	DialTLS       func(ctx context.Context, address, serverName string) ([]*x509.Certificate, error)
	// DiscoverOIDC fetches the identity provider's discovery document. Unlike
	// the operator TLS check it always applies the public trust store, because
	// the identity provider serves the community's own domain in every mode.
	DiscoverOIDC func(ctx context.Context, discoveryURL string) error
	Port         string
	Timeout      time.Duration
}

// NewLiveVerifier returns a verifier that performs real DNS resolution, TCP
// reachability, and TLS chain verification against the private cluster.
func NewLiveVerifier() *LiveVerifier {
	resolver := &net.Resolver{}
	dialer := &net.Dialer{}
	return &LiveVerifier{
		LookupHost: resolver.LookupHost,
		DialReachable: func(ctx context.Context, address string) error {
			conn, err := dialer.DialContext(ctx, "tcp", address)
			if err != nil {
				return err
			}
			return conn.Close()
		},
		DialTLS: func(ctx context.Context, address, serverName string) ([]*x509.Certificate, error) {
			// The presented chain is captured and then verified against the pinned
			// Cluster CA root below — not the system trust store, which does not
			// contain the private LAN-only CA. Verification is performed by Observe,
			// so skipping the handshake's own verification here is intentional.
			tlsDialer := &tls.Dialer{NetDialer: dialer, Config: &tls.Config{InsecureSkipVerify: true, ServerName: serverName}} //nolint:gosec // chain verified against the pinned Cluster CA root
			conn, err := tlsDialer.DialContext(ctx, "tcp", address)
			if err != nil {
				return nil, err
			}
			defer conn.Close()
			tlsConn, ok := conn.(*tls.Conn)
			if !ok {
				return nil, errors.New("unexpected non-TLS connection")
			}
			return tlsConn.ConnectionState().PeerCertificates, nil
		},
		DiscoverOIDC: func(ctx context.Context, discoveryURL string) error {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
			if err != nil {
				return err
			}
			response, err := (&http.Client{}).Do(request)
			if err != nil {
				return err
			}
			defer response.Body.Close()
			// Bound the read: a discovery document is small, and an endpoint that
			// streams indefinitely must not hold the verification open.
			var document struct {
				Issuer                string `json:"issuer"`
				AuthorizationEndpoint string `json:"authorization_endpoint"`
			}
			if response.StatusCode != http.StatusOK {
				return fmt.Errorf("oidc discovery returned %d", response.StatusCode)
			}
			if err := json.NewDecoder(io.LimitReader(response.Body, 256*1024)).Decode(&document); err != nil {
				return err
			}
			// A reachable endpoint that is not actually an identity provider —
			// an ingress default backend, say — must not count as verified.
			if document.Issuer == "" || document.AuthorizationEndpoint == "" {
				return errors.New("oidc discovery document is incomplete")
			}
			return nil
		},
		Port:    "443",
		Timeout: 3 * time.Second,
	}
}

// Observe performs the four checks. Unreachable or misconfigured state yields
// false observations (which block closure) rather than an error; an error is
// returned only for an unusable target.
func (verifier *LiveVerifier) Observe(ctx context.Context, target Target) (Observations, error) {
	if err := target.Validate(); err != nil {
		return Observations{}, err
	}
	var observations Observations

	operatorAddresses := make(map[string][]string, len(target.OperatorHosts))
	dnsResolves := len(target.OperatorHosts) > 0
	for _, host := range target.OperatorHosts {
		addresses, err := verifier.lookup(ctx, host)
		if err != nil || len(addresses) == 0 {
			dnsResolves = false
			continue
		}
		operatorAddresses[host] = addresses
	}
	observations.DNSResolves = dnsResolves

	gatewayAddresses, gatewayErr := verifier.lookup(ctx, target.GatewayIdentityHostname)
	identityMatches := dnsResolves && gatewayErr == nil && len(gatewayAddresses) > 0
	if identityMatches {
		gateway := make(map[string]bool, len(gatewayAddresses))
		for _, address := range gatewayAddresses {
			gateway[address] = true
		}
		for _, host := range target.OperatorHosts {
			if !sharesAddress(operatorAddresses[host], gateway) {
				identityMatches = false
				break
			}
		}
	}
	observations.GatewayIdentityMatches = identityMatches

	observations.PrivateReachable = verifier.reachable(ctx, target.GatewayHostname)
	observations.TLSTrusted = verifier.tlsChainsToAnchor(ctx, target)
	observations.OIDCReachable = verifier.oidcReachable(ctx, target)
	return observations, nil
}

// oidcReachable fetches the identity provider's discovery document. It is a
// plain read, and a failure — unreachable, bad certificate, not yet deployed —
// is reported as an unmet observation rather than an error, because a cluster
// still converging is a reason to wait, not to fail the run.
func (verifier *LiveVerifier) oidcReachable(ctx context.Context, target Target) bool {
	if verifier.DiscoverOIDC == nil {
		return false
	}
	ctx, cancel := verifier.withTimeout(ctx)
	defer cancel()
	return verifier.DiscoverOIDC(ctx, strings.TrimSuffix(target.IdentityIssuerURL, "/")+"/.well-known/openid-configuration") == nil
}

func (verifier *LiveVerifier) lookup(ctx context.Context, host string) ([]string, error) {
	ctx, cancel := verifier.withTimeout(ctx)
	defer cancel()
	return verifier.LookupHost(ctx, host)
}

func (verifier *LiveVerifier) reachable(ctx context.Context, host string) bool {
	ctx, cancel := verifier.withTimeout(ctx)
	defer cancel()
	return verifier.DialReachable(ctx, net.JoinHostPort(host, verifier.Port)) == nil
}

// tlsChainsToAnchor verifies the presented operator chain against whichever
// anchor the Deployment Mode uses: the pinned private Cluster CA root for a
// LAN-only installation, or the device's own public trust store for a publicly
// addressed one. Passing nil roots to Verify uses the system pool, which is the
// same trust an Operator's browser will apply — so a certificate that passes
// here is one they will actually be able to use.
func (verifier *LiveVerifier) tlsChainsToAnchor(ctx context.Context, target Target) bool {
	if len(target.OperatorHosts) == 0 {
		return false
	}
	host := target.OperatorHosts[0]
	ctx, cancel := verifier.withTimeout(ctx)
	defer cancel()
	certificates, err := verifier.DialTLS(ctx, net.JoinHostPort(host, verifier.Port), host)
	if err != nil || len(certificates) == 0 {
		return false
	}
	options := x509.VerifyOptions{DNSName: host, Intermediates: x509.NewCertPool()}
	for _, certificate := range certificates[1:] {
		options.Intermediates.AddCert(certificate)
	}
	if target.Anchor == ClusterCARoot {
		root, err := parseRootCertificate(target.RootCertificatePEM)
		if err != nil {
			return false
		}
		options.Roots = x509.NewCertPool()
		options.Roots.AddCert(root)
	}
	_, err = certificates[0].Verify(options)
	return err == nil
}

func (verifier *LiveVerifier) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if verifier.Timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, verifier.Timeout)
}

func sharesAddress(addresses []string, set map[string]bool) bool {
	for _, address := range addresses {
		if set[address] {
			return true
		}
	}
	return false
}

func parseRootCertificate(encoded string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(encoded))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("invalid root certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}
