package gatewayaccess_test

import (
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/gatewayaccess"
)

func operatorHosts() []string {
	return []string{"console.smallworlds.internal", "grafana.smallworlds.internal", "argocd.smallworlds.internal"}
}

func TestPlanRendersHTTPSOnlyPrivateGatewayPolicy(t *testing.T) {
	policy, err := gatewayaccess.Plan("smallworlds.internal", "gateway.smallworlds.internal", operatorHosts())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if policy.Scheme != "https" || policy.Entrypoint != "private-gateway" {
		t.Fatalf("not HTTPS-only via private gateway: %#v", policy)
	}
	if policy.LANIngress != "deny" || policy.PublicIngress != "deny" {
		t.Fatalf("LAN/public ingress not denied: %#v", policy)
	}
	if len(policy.AllowedHosts) != 3 {
		t.Fatalf("allowed hosts = %v", policy.AllowedHosts)
	}
}

func TestHostAllowedAcceptsOnlyExactOperatorHostnames(t *testing.T) {
	policy, err := gatewayaccess.Plan("smallworlds.internal", "gateway.smallworlds.internal", operatorHosts())
	if err != nil {
		t.Fatal(err)
	}
	allowed := []string{
		"console.smallworlds.internal",
		"CONSOLE.smallworlds.internal",
		"grafana.smallworlds.internal:443",
		"argocd.smallworlds.internal.",
	}
	for _, host := range allowed {
		if !policy.HostAllowed(host) {
			t.Fatalf("legitimate operator host rejected: %q", host)
		}
	}
	forged := []string{
		"",
		"smallworlds.internal",
		"gateway.smallworlds.internal",
		"evil.example",
		"console.smallworlds.internal.evil.example",
		"192.168.178.52",
		"console.attacker.internal",
		"consolexsmallworlds.internal",
	}
	for _, host := range forged {
		if policy.HostAllowed(host) {
			t.Fatalf("forged/unexpected Host accepted: %q", host)
		}
	}
}

func TestValidateRejectsWeakenedPolicies(t *testing.T) {
	for name, mutate := range map[string]func(*gatewayaccess.Policy){
		"http scheme":     func(policy *gatewayaccess.Policy) { policy.Scheme = "http" },
		"lan allowed":     func(policy *gatewayaccess.Policy) { policy.LANIngress = "allow" },
		"public allowed":  func(policy *gatewayaccess.Policy) { policy.PublicIngress = "allow" },
		"foreign host":    func(policy *gatewayaccess.Policy) { policy.AllowedHosts[0] = "console.evil.example" },
		"empty allowlist": func(policy *gatewayaccess.Policy) { policy.AllowedHosts = nil },
		"bad entrypoint":  func(policy *gatewayaccess.Policy) { policy.Entrypoint = "lan" },
	} {
		t.Run(name, func(t *testing.T) {
			policy, err := gatewayaccess.Plan("smallworlds.internal", "gateway.smallworlds.internal", operatorHosts())
			if err != nil {
				t.Fatal(err)
			}
			mutate(&policy)
			if err := policy.Validate(); err == nil {
				t.Fatal("weakened policy accepted")
			}
		})
	}
}
