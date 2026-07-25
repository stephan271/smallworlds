package hetzner

import (
	"sort"
	"strings"

	"github.com/stephan271/smallworlds/operator-console/internal/capability"
)

// HetznerNameservers are the authoritative nameservers a Hetzner DNS zone is
// served from. Delegation means the registrar points the domain at these.
var HetznerNameservers = []string{"helium.ns.hetzner.de", "hydrogen.ns.hetzner.com", "oxygen.ns.hetzner.com"}

// DelegationStatus is the verdict of comparing the domain's observed
// nameservers against the zone's authoritative ones.
type DelegationStatus string

const (
	// DelegationConfirmed means every observed nameserver is authoritative for
	// the zone — certificates and DNS will resolve.
	DelegationConfirmed DelegationStatus = "confirmed"
	// DelegationPartial means the domain is served by a mix, so resolution is
	// unreliable until the registrar is corrected.
	DelegationPartial DelegationStatus = "partial"
	// DelegationMissing means the domain is delegated elsewhere entirely.
	DelegationMissing DelegationStatus = "missing"
	// DelegationUnknown means the lookup could not answer. It is never treated
	// as confirmed: a public installation stays blocked.
	DelegationUnknown DelegationStatus = "unknown"
	// DelegationNotRequired means the Deployment Mode serves no public names.
	DelegationNotRequired DelegationStatus = "not-required"
)

// Delegation is the secret-free result of the nameserver check.
type Delegation struct {
	Domain              string           `json:"domain"`
	Status              DelegationStatus `json:"status"`
	ExpectedNameservers []string         `json:"expectedNameservers"`
	ObservedNameservers []string         `json:"observedNameservers,omitempty"`
	ReasonKey           string           `json:"reasonKey"`
}

// Satisfied reports whether a public installation may proceed past the DNS
// prerequisite. Unknown is deliberately not satisfied.
func (delegation Delegation) Satisfied() bool {
	return delegation.Status == DelegationConfirmed || delegation.Status == DelegationNotRequired
}

// DelegationRequired reports whether a Deployment Mode serves public names and
// therefore needs the domain delegated before installation. Only the LAN-only
// mode is exempt.
func DelegationRequired(mode capability.DeploymentMode) bool {
	return mode != capability.LocalLAN
}

// CheckDelegation classifies the observed nameservers for a domain. observed
// being nil means the lookup did not answer, which is reported as unknown
// rather than missing — an unanswered lookup is not evidence of a problem, but
// it is also not evidence of readiness.
func CheckDelegation(domain string, observed []string, mode capability.DeploymentMode) Delegation {
	delegation := Delegation{Domain: domain, ExpectedNameservers: HetznerNameservers, ObservedNameservers: normalizeNameservers(observed)}
	if !DelegationRequired(mode) {
		delegation.Status, delegation.ReasonKey = DelegationNotRequired, "delegation-not-required-for-lan-only"
		return delegation
	}
	if len(delegation.ObservedNameservers) == 0 {
		delegation.Status, delegation.ReasonKey = DelegationUnknown, "delegation-lookup-inconclusive"
		return delegation
	}
	authoritative := map[string]bool{}
	for _, nameserver := range HetznerNameservers {
		authoritative[nameserver] = true
	}
	matched := 0
	for _, nameserver := range delegation.ObservedNameservers {
		if authoritative[nameserver] {
			matched++
		}
	}
	switch {
	case matched == len(delegation.ObservedNameservers):
		delegation.Status, delegation.ReasonKey = DelegationConfirmed, "delegation-confirmed"
	case matched > 0:
		delegation.Status, delegation.ReasonKey = DelegationPartial, "delegation-partially-configured"
	default:
		delegation.Status, delegation.ReasonKey = DelegationMissing, "delegation-points-elsewhere"
	}
	return delegation
}

func normalizeNameservers(observed []string) []string {
	normalized := make([]string, 0, len(observed))
	seen := map[string]bool{}
	for _, nameserver := range observed {
		value := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(nameserver), "."))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized
}
