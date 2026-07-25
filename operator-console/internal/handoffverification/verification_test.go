package handoffverification_test

import (
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/handoffverification"
)

func allPass() handoffverification.Observations {
	return handoffverification.Observations{PrivateReachable: true, DNSResolves: true, TLSTrusted: true, GatewayIdentityMatches: true}
}

func TestEvaluateVerifiesOnlyWhenAllChecksPass(t *testing.T) {
	report := handoffverification.Evaluate(allPass())
	if !report.Verified || !report.PermitsClosure() {
		t.Fatalf("all-pass observations did not verify: %#v", report)
	}
	names := []string{
		handoffverification.ReachabilityCheck,
		handoffverification.DNSCheck,
		handoffverification.TLSCheck,
		handoffverification.GatewayIdentityCheck,
	}
	if len(report.Checks) != len(names) {
		t.Fatalf("checks = %d, want %d", len(report.Checks), len(names))
	}
	for index, name := range names {
		if report.Checks[index].Name != name || !report.Checks[index].Passed {
			t.Fatalf("check %d = %#v, want %q passed", index, report.Checks[index], name)
		}
	}
}

func TestEvaluateBlocksClosureWhenAnyCheckFails(t *testing.T) {
	mutators := map[string]func(*handoffverification.Observations){
		"reachability": func(o *handoffverification.Observations) { o.PrivateReachable = false },
		"dns":          func(o *handoffverification.Observations) { o.DNSResolves = false },
		"tls":          func(o *handoffverification.Observations) { o.TLSTrusted = false },
		"identity":     func(o *handoffverification.Observations) { o.GatewayIdentityMatches = false },
	}
	for name, mutate := range mutators {
		t.Run(name, func(t *testing.T) {
			observations := allPass()
			mutate(&observations)
			report := handoffverification.Evaluate(observations)
			if report.Verified || report.PermitsClosure() {
				t.Fatalf("closure permitted with failing %s check: %#v", name, report)
			}
		})
	}
}

func TestTargetValidateRequiresAllExpectations(t *testing.T) {
	complete := handoffverification.Target{
		Anchor:     handoffverification.ClusterCARoot,
		BaseDomain: "smallworlds.internal", GatewayHostname: "gateway.smallworlds.internal",
		OperatorHosts: []string{"console.smallworlds.internal"}, RootFingerprint: "SHA256:AA",
		RootCertificatePEM: "-----BEGIN CERTIFICATE-----", GatewayIdentityHostname: "gateway.smallworlds.internal",
	}
	if err := complete.Validate(); err != nil {
		t.Fatalf("complete target rejected: %v", err)
	}
	incomplete := complete
	incomplete.RootFingerprint = ""
	if err := incomplete.Validate(); err == nil {
		t.Fatal("target missing the Cluster CA root fingerprint was accepted")
	}
}
