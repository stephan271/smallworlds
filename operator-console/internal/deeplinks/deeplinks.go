// Package deeplinks builds the contextual "open in Grafana / Argo CD" URLs the
// Operator Console offers on an unhealthy facet's remediation route (ADR 0024).
// Every link is derived from the Private Network's base domain, so it targets
// the private operator hostnames (grafana.<base>, argocd.<base>) that resolve
// only through the Private Gateway — a public URL can never be produced here.
// The console opens these in a new tab rather than embedding an iframe.
package deeplinks

import (
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/stephan271/smallworlds/operator-console/internal/assessment"
)

// ErrInvalidBaseDomain is returned when the supplied base domain is not a safe,
// dotted hostname.
var ErrInvalidBaseDomain = errors.New("deeplinks: invalid base domain")

var safeDomain = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

// Targets holds the private operator hostnames the console links to. The zero
// value produces no links, so a console without a configured base domain simply
// omits external links rather than fabricating them.
type Targets struct {
	GrafanaHost string
	ArgoCDHost  string
}

// New derives the Grafana and Argo CD operator hostnames from the Private
// Network base domain, matching the hostnames the private network provisions.
func New(baseDomain string) (Targets, error) {
	baseDomain = strings.ToLower(strings.TrimSpace(baseDomain))
	if !safeDomain.MatchString(baseDomain) || strings.Contains(baseDomain, "..") || !strings.Contains(baseDomain, ".") {
		return Targets{}, ErrInvalidBaseDomain
	}
	return Targets{
		GrafanaHost: "grafana." + baseDomain,
		ArgoCDHost:  "argocd." + baseDomain,
	}, nil
}

// Resolve returns the contextual URL for a remediation route, and whether one
// applies. Only the external investigation tools (Grafana, Argo CD) resolve to a
// URL; setup-journey, git-proposal, runtime-action, and documentation routes are
// handled inside the console and return no external link.
func (targets Targets) Resolve(remediation assessment.Remediation) (string, bool) {
	switch remediation.Kind {
	case assessment.RemediateGrafana:
		if targets.GrafanaHost == "" {
			return "", false
		}
		endpoint := url.URL{Scheme: "https", Host: targets.GrafanaHost, Path: "/dashboards"}
		if reference := strings.TrimSpace(remediation.Reference); reference != "" {
			query := url.Values{}
			query.Set("query", reference)
			endpoint.RawQuery = query.Encode()
		}
		return endpoint.String(), true
	case assessment.RemediateArgoCD:
		if targets.ArgoCDHost == "" {
			return "", false
		}
		endpoint := url.URL{Scheme: "https", Host: targets.ArgoCDHost, Path: "/applications"}
		if reference := strings.TrimSpace(remediation.Reference); reference != "" {
			endpoint.Path = "/applications/" + reference
		}
		return endpoint.String(), true
	default:
		return "", false
	}
}
