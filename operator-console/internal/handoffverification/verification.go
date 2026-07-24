// Package handoffverification gates closing the temporary SSH/Kubernetes
// administration path behind four externally observed checks: private
// reachability, operator DNS resolution, operator TLS chaining to the Cluster CA
// root, and the Private Gateway presenting its expected stable identity. The
// gate logic is deterministic and testable; the live probing is an injectable
// Verifier, mirroring localbootstrap.Runner.
package handoffverification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	// TLSCheck confirms an operator TLS leaf chains to the Cluster CA root.
	TLSCheck = "operator-tls"
	// GatewayIdentityCheck confirms the gateway presents its expected stable
	// identity.
	GatewayIdentityCheck = "gateway-identity"
)

// Target is the secret-free set of expectations verified before closing the
// temporary administration path. RootCertificatePEM is the public Cluster CA
// root used to verify that operator TLS leaves chain to it.
type Target struct {
	BaseDomain              string
	GatewayHostname         string
	OperatorHosts           []string
	RootFingerprint         string
	RootCertificatePEM      string
	GatewayIdentityHostname string
}

// Validate ensures a target carries everything the four checks require.
func (target Target) Validate() error {
	if target.BaseDomain == "" || target.GatewayHostname == "" || len(target.OperatorHosts) == 0 || target.RootFingerprint == "" || target.RootCertificatePEM == "" || target.GatewayIdentityHostname == "" {
		return ErrInvalidTarget
	}
	return nil
}

// Observations are the raw results a Verifier reports for a target.
type Observations struct {
	PrivateReachable       bool
	DNSResolves            bool
	TLSChainsToClusterCA   bool
	GatewayIdentityMatches bool
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

// Report aggregates the four checks and whether the handoff is fully verified.
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
		{Name: TLSCheck, Passed: observations.TLSChainsToClusterCA},
		{Name: GatewayIdentityCheck, Passed: observations.GatewayIdentityMatches},
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
