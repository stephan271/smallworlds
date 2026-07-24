// Package gatewayaccess renders the LAN-only Private Gateway access policy and
// enforces its Host-header allowlist. Operator interfaces (Operator Console,
// Grafana, Argo CD) are reachable only through the Private Gateway over standard
// HTTPS; LAN and public ingress are denied, and only the exact operator
// hostnames are accepted so forged Host headers cannot reach the interfaces. The
// policy is derived from the established Private Network, keeping a single source
// of truth for the operator hostnames.
package gatewayaccess

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ErrInvalidPolicy is returned when a gateway access policy fails validation.
var ErrInvalidPolicy = errors.New("gateway access policy is invalid")

const (
	scheme        = "https"
	entrypoint    = "private-gateway"
	ingressDenied = "deny"
)

var safeDomain = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

// Policy is the API-safe description of how operator interfaces may be reached.
type Policy struct {
	BaseDomain      string   `json:"baseDomain"`
	GatewayHostname string   `json:"gatewayHostname"`
	Scheme          string   `json:"scheme"`
	Entrypoint      string   `json:"entrypoint"`
	LANIngress      string   `json:"lanIngress"`
	PublicIngress   string   `json:"publicIngress"`
	AllowedHosts    []string `json:"allowedHosts"`
}

// Plan derives the LAN-only access policy from the Private Network's base domain,
// gateway hostname, and operator hostnames.
func Plan(baseDomain, gatewayHostname string, operatorHosts []string) (Policy, error) {
	baseDomain = strings.ToLower(strings.TrimSpace(baseDomain))
	policy := Policy{
		BaseDomain:      baseDomain,
		GatewayHostname: strings.ToLower(strings.TrimSpace(gatewayHostname)),
		Scheme:          scheme,
		Entrypoint:      entrypoint,
		LANIngress:      ingressDenied,
		PublicIngress:   ingressDenied,
	}
	allowed := make([]string, 0, len(operatorHosts))
	for _, host := range operatorHosts {
		allowed = append(allowed, strings.ToLower(strings.TrimSpace(host)))
	}
	sort.Strings(allowed)
	policy.AllowedHosts = allowed
	if err := policy.Validate(); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

// Validate enforces the LAN-only invariants: HTTPS only, private-gateway
// entrypoint, denied LAN/public ingress, and a non-empty allowlist of operator
// hostnames that all sit under the base domain.
func (policy Policy) Validate() error {
	if !safeDomain.MatchString(policy.BaseDomain) || strings.Contains(policy.BaseDomain, "..") || !strings.Contains(policy.BaseDomain, ".") {
		return fmt.Errorf("%w: base domain", ErrInvalidPolicy)
	}
	if !isSubdomainOf(policy.GatewayHostname, policy.BaseDomain) {
		return fmt.Errorf("%w: gateway hostname", ErrInvalidPolicy)
	}
	if policy.Scheme != scheme {
		return fmt.Errorf("%w: operator interfaces must be HTTPS only", ErrInvalidPolicy)
	}
	if policy.Entrypoint != entrypoint {
		return fmt.Errorf("%w: entrypoint must be the private gateway", ErrInvalidPolicy)
	}
	if policy.LANIngress != ingressDenied || policy.PublicIngress != ingressDenied {
		return fmt.Errorf("%w: LAN and public ingress must be denied", ErrInvalidPolicy)
	}
	if len(policy.AllowedHosts) == 0 {
		return fmt.Errorf("%w: no operator hostnames", ErrInvalidPolicy)
	}
	seen := make(map[string]bool, len(policy.AllowedHosts))
	for _, host := range policy.AllowedHosts {
		if !isSubdomainOf(host, policy.BaseDomain) {
			return fmt.Errorf("%w: operator hostname %q", ErrInvalidPolicy, host)
		}
		if seen[host] {
			return fmt.Errorf("%w: duplicate operator hostname", ErrInvalidPolicy)
		}
		seen[host] = true
	}
	return nil
}

// HostAllowed reports whether a request Host header may reach an operator
// interface. Only the exact operator hostnames are accepted; a forged, LAN-IP,
// public-domain, or otherwise unexpected Host is rejected.
func (policy Policy) HostAllowed(hostHeader string) bool {
	normalized := normalizeHost(hostHeader)
	if normalized == "" {
		return false
	}
	for _, host := range policy.AllowedHosts {
		if normalized == host {
			return true
		}
	}
	return false
}

func normalizeHost(hostHeader string) string {
	host := strings.TrimSpace(strings.ToLower(hostHeader))
	host = strings.TrimSuffix(host, ".")
	if index := strings.LastIndex(host, ":"); index != -1 {
		if port := host[index+1:]; port != "" && allDigits(port) {
			host = host[:index]
		}
	}
	return host
}

func allDigits(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func isSubdomainOf(host, baseDomain string) bool {
	if !safeDomain.MatchString(host) || strings.Contains(host, "..") {
		return false
	}
	return strings.HasSuffix(host, "."+baseDomain) && host != baseDomain
}
