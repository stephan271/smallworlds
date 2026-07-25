// Package handoffverification gates closing the temporary SSH/Kubernetes
// administration path behind five externally observed checks: private
// reachability, operator DNS resolution, operator TLS chaining to the Deployment
// Mode's trust anchor, the Private Gateway presenting its expected stable
// identity, and the cluster's identity provider serving OIDC discovery.
//
// The gate exists because closing that path is the one irreversible step of the
// journey: after it, the only way into the cluster is the private one. Every
// check is therefore a way the Operator could be locked out of the cluster they
// just paid for, and the gate opens only when none of them applies.
//
// The gate logic is deterministic and testable; the live probing is an
// injectable Verifier, mirroring localbootstrap.Runner.
package handoffverification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrVerificationUnavailable is returned by a Verifier that cannot reach the
// private cluster to observe the handoff state.
var ErrVerificationUnavailable = errors.New("handoff verification requires a reachable private cluster")

// ErrInvalidTarget is returned when a verification target is incomplete.
var ErrInvalidTarget = errors.New("handoff verification target is invalid")

const (
	// ReachabilityCheck confirms the Private Gateway is reachable over the
	// private network.
	ReachabilityCheck = "private-reachability"
	// DNSCheck confirms operator hostnames resolve to the Private Gateway.
	DNSCheck = "operator-dns"
	// TLSCheck confirms an operator TLS leaf chains to the mode's trust anchor.
	TLSCheck = "operator-tls"
	// GatewayIdentityCheck confirms the gateway presents its expected stable
	// identity.
	GatewayIdentityCheck = "gateway-identity"
	// OIDCCheck confirms the cluster's identity provider is serving its
	// discovery document over a trusted certificate. Operator interfaces
	// authenticate through it, so closing the temporary administration path
	// while it is down or holding a bad certificate locks the Operator out of
	// the cluster they just built — with no way back in.
	OIDCCheck = "operator-oidc"
)

// TrustAnchor is what operator TLS is required to chain to. It differs by
// Deployment Mode, and getting it wrong in either direction is a real failure:
// verifying a private CA against the public trust store always fails, and
// verifying a public certificate against a pinned private root would too.
type TrustAnchor string

const (
	// ClusterCARoot is the LAN-only anchor: a private Cluster CA root the
	// Operator installed on their device, pinned here by certificate.
	ClusterCARoot TrustAnchor = "cluster-ca-root"
	// PublicTrust is the anchor for publicly addressed installations, whose
	// operator interfaces hold publicly trusted ACME certificates.
	PublicTrust TrustAnchor = "public"
)

// Target is the secret-free set of expectations verified before closing the
// temporary administration path.
type Target struct {
	Anchor                  TrustAnchor
	BaseDomain              string
	GatewayHostname         string
	OperatorHosts           []string
	GatewayIdentityHostname string
	// IdentityIssuerURL is the cluster's OIDC issuer. Operator interfaces sign
	// in through it in every Deployment Mode.
	IdentityIssuerURL string
	// RootFingerprint and RootCertificatePEM identify the private Cluster CA
	// root. They are required for ClusterCARoot and must be absent for
	// PublicTrust — a stray pinned root there would silently widen what the
	// verification accepts.
	RootFingerprint    string
	RootCertificatePEM string
}

// Validate ensures a target carries everything the checks require, and
// exactly the trust material its anchor calls for.
func (target Target) Validate() error {
	if target.BaseDomain == "" || target.GatewayHostname == "" || len(target.OperatorHosts) == 0 || target.GatewayIdentityHostname == "" {
		return ErrInvalidTarget
	}
	if !strings.HasPrefix(target.IdentityIssuerURL, "https://") {
		return ErrInvalidTarget
	}
	switch target.Anchor {
	case ClusterCARoot:
		if target.RootFingerprint == "" || target.RootCertificatePEM == "" {
			return ErrInvalidTarget
		}
	case PublicTrust:
		if target.RootFingerprint != "" || target.RootCertificatePEM != "" {
			return ErrInvalidTarget
		}
	default:
		return ErrInvalidTarget
	}
	return nil
}

// Observations are the raw results a Verifier reports for a target.
type Observations struct {
	PrivateReachable       bool
	DNSResolves            bool
	TLSTrusted             bool
	GatewayIdentityMatches bool
	OIDCReachable          bool
}

// Verifier performs the live observation of a target. Implementations must not
// mutate the cluster.
type Verifier interface {
	Observe(ctx context.Context, target Target) (Observations, error)
}

// Check is one named verification result.
type Check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
}

// Report aggregates the checks and whether the handoff is fully verified.
type Report struct {
	Checks   []Check `json:"checks"`
	Verified bool    `json:"verified"`
}

// Evaluate builds the ordered report from raw observations. The handoff is
// verified only when every check passes.
func Evaluate(observations Observations) Report {
	checks := []Check{
		{Name: ReachabilityCheck, Passed: observations.PrivateReachable},
		{Name: DNSCheck, Passed: observations.DNSResolves},
		{Name: TLSCheck, Passed: observations.TLSTrusted},
		{Name: GatewayIdentityCheck, Passed: observations.GatewayIdentityMatches},
		{Name: OIDCCheck, Passed: observations.OIDCReachable},
	}
	verified := true
	for _, check := range checks {
		if !check.Passed {
			verified = false
		}
	}
	return Report{Checks: checks, Verified: verified}
}

// PermitsClosure reports whether the temporary administration path may be closed.
func (report Report) PermitsClosure() bool {
	return report.Verified
}

// Marshal returns the report as JSON for durable storage.
func (report Report) Marshal() (string, error) {
	encoded, err := json.Marshal(report)
	if err != nil {
		return "", fmt.Errorf("marshal handoff report: %w", err)
	}
	return string(encoded), nil
}
