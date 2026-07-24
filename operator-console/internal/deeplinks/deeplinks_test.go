package deeplinks

import (
	"errors"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/assessment"
)

func TestNewDerivesOperatorHosts(t *testing.T) {
	targets, err := New("SW.example.internal")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if targets.GrafanaHost != "grafana.sw.example.internal" {
		t.Errorf("grafana host = %q", targets.GrafanaHost)
	}
	if targets.ArgoCDHost != "argocd.sw.example.internal" {
		t.Errorf("argocd host = %q", targets.ArgoCDHost)
	}
}

func TestNewRejectsInvalidBaseDomain(t *testing.T) {
	for _, bad := range []string{"", "nodot", "has space.internal", "..", "-bad.internal"} {
		if _, err := New(bad); !errors.Is(err, ErrInvalidBaseDomain) {
			t.Errorf("New(%q) err = %v, want ErrInvalidBaseDomain", bad, err)
		}
	}
}

func TestResolve(t *testing.T) {
	targets, err := New("sw.example.internal")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tests := []struct {
		name        string
		remediation assessment.Remediation
		wantURL     string
		wantOK      bool
	}{
		{
			name:        "grafana with dashboard reference",
			remediation: assessment.Remediation{Kind: assessment.RemediateGrafana, Reference: "nextcloud"},
			wantURL:     "https://grafana.sw.example.internal/dashboards?query=nextcloud",
			wantOK:      true,
		},
		{
			name:        "grafana without reference",
			remediation: assessment.Remediation{Kind: assessment.RemediateGrafana},
			wantURL:     "https://grafana.sw.example.internal/dashboards",
			wantOK:      true,
		},
		{
			name:        "argocd with application reference",
			remediation: assessment.Remediation{Kind: assessment.RemediateArgoCD, Reference: "nextcloud"},
			wantURL:     "https://argocd.sw.example.internal/applications/nextcloud",
			wantOK:      true,
		},
		{
			name:        "setup journey has no external link",
			remediation: assessment.Remediation{Kind: assessment.RemediateSetupJourney, Reference: "task"},
			wantOK:      false,
		},
		{
			name:        "git proposal has no external link",
			remediation: assessment.Remediation{Kind: assessment.RemediateGitProposal, Reference: "nextcloud"},
			wantOK:      false,
		},
		{
			name:        "documentation has no external link",
			remediation: assessment.Remediation{Kind: assessment.RemediateDocumentation, Reference: "doc/x.md"},
			wantOK:      false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotURL, gotOK := targets.Resolve(test.remediation)
			if gotOK != test.wantOK {
				t.Fatalf("ok = %v, want %v", gotOK, test.wantOK)
			}
			if gotURL != test.wantURL {
				t.Fatalf("url = %q, want %q", gotURL, test.wantURL)
			}
		})
	}
}

// TestZeroTargetsProduceNoLinks proves an unconfigured console omits external
// links rather than building a public or malformed URL.
func TestZeroTargetsProduceNoLinks(t *testing.T) {
	var zero Targets
	if url, ok := zero.Resolve(assessment.Remediation{Kind: assessment.RemediateGrafana, Reference: "x"}); ok || url != "" {
		t.Fatalf("zero targets resolved a link: %q", url)
	}
	if url, ok := zero.Resolve(assessment.Remediation{Kind: assessment.RemediateArgoCD, Reference: "x"}); ok || url != "" {
		t.Fatalf("zero targets resolved a link: %q", url)
	}
}
