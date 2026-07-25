package decommission

import (
	"errors"
	"testing"
	"time"
)

func inspection(t *testing.T) Inspection {
	t.Helper()
	resources, err := Classify("p1", []Resource{
		{Kind: "server", ProviderID: "server-1", Tags: map[string]string{"smallworlds-profile": "p1"}, ClusterIdentity: "p1", MonthlyEUR: 8.49},
		{Kind: "volume", ProviderID: "volume-1", Tags: map[string]string{"smallworlds-profile": "p1"}, ClusterIdentity: "p1", Persistent: true, MonthlyEUR: 4.40},
		{Kind: "primary-ip", ProviderID: "ip-1", Tags: map[string]string{"smallworlds-profile": "p1"}, ClusterIdentity: "p1", MonthlyEUR: .60},
		{Kind: "dns-zone", ProviderID: "zone-1", Shared: true, MonthlyEUR: 1},
		{Kind: "gitops-overlay", ProviderID: "overlay-1"},
		{Kind: "firewall", ProviderID: "mystery", Name: "looks-like-smallworlds"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return Inspection{ProfileID: "p1", DeploymentMode: "hetzner", ProfileRevision: 1, ObservedAt: time.Now().UTC(), Resources: resources, RetainedData: []string{"volume-1"}, RecoveryPath: "Restore retained volume or use Recovery Bundle.", Protection: []string{"offsite recovery point current"}}
}

func TestPreservePlanOnlyRemovesProvenComputeAndBlocksUnknown(t *testing.T) {
	plan, err := BuildPlan(inspection(t))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Approvable() || len(plan.Blockers) != 1 {
		t.Fatalf("blockers = %#v", plan.Blockers)
	}
	actions := map[string]Action{}
	for _, item := range plan.Items {
		actions[item.ProviderID] = item.Action
	}
	if actions["server-1"] != Remove || actions["volume-1"] != Retain || actions["ip-1"] != Retain || actions["zone-1"] != Retain || actions["overlay-1"] != Retain || actions["mystery"] != Retain {
		t.Fatalf("actions = %#v", actions)
	}
	if len(plan.ContinuingCosts) != 3 {
		t.Fatalf("continuing costs = %#v", plan.ContinuingCosts)
	}
}

func TestResumeNeverWidensDeletionSet(t *testing.T) {
	base := inspection(t)
	filtered := make([]InspectedResource, 0, len(base.Resources))
	for _, resource := range base.Resources {
		if resource.ProviderID != "mystery" {
			filtered = append(filtered, resource)
		}
	}
	base.Resources = filtered
	plan, err := BuildPlan(base)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Approvable() {
		t.Fatalf("unexpected blockers: %v", plan.Blockers)
	}
	changed := base
	changed.Resources = append(changed.Resources, InspectedResource{Resource: Resource{Kind: "server", ProviderID: "server-2", Tags: map[string]string{"smallworlds-profile": "p1"}, ClusterIdentity: "p1"}, Ownership: ProfileOwned})
	if err := ValidateResume(plan, changed, 0); err != nil {
		t.Fatalf("new resource should be retained, not widen plan: %v", err)
	}
	changed.Resources = changed.Resources[1:] // server-1 disappeared before it was checkpointed
	if err := ValidateResume(plan, changed, 0); !errors.Is(err, ErrChanged) {
		t.Fatalf("missing planned target = %v", err)
	}
}
