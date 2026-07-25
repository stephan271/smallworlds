package decommission

import (
	"testing"
	"time"
)

func fullInspectionForTest(t *testing.T) FullInspection {
	t.Helper()
	resources := []Resource{
		{Kind: "server", ProviderID: "compute-1", Tags: map[string]string{"smallworlds-profile": "profile-1"}, ClusterIdentity: "profile-1"},
		{Kind: "volume", ProviderID: "storage-1", Persistent: true, Tags: map[string]string{"smallworlds-profile": "profile-1"}, ClusterIdentity: "profile-1"},
		{Kind: "primary-ip", ProviderID: "network-1", Tags: map[string]string{"smallworlds-profile": "profile-1"}, ClusterIdentity: "profile-1"},
		{Kind: "dns-record", ProviderID: "dns-1", Tags: map[string]string{"smallworlds-profile": "profile-1"}, ClusterIdentity: "profile-1"},
		{Kind: "dns-zone", ProviderID: "zone-1", Tags: map[string]string{"smallworlds-profile": "profile-1"}, ClusterIdentity: "profile-1"},
		{Kind: "gitops-overlay", ProviderID: "overlay-1", Tags: map[string]string{"smallworlds-profile": "profile-1"}, ClusterIdentity: "profile-1"},
		{Kind: "server", ProviderID: "unknown-1"},
	}
	classified, err := ClassifyFull("profile-1", resources)
	if err != nil {
		t.Fatal(err)
	}
	return FullInspection{ProfileID: "profile-1", DeploymentMode: "hetzner", ProfileRevision: 1, ObservedAt: time.Now().UTC(), Resources: classified, Protection: ProtectionEvidence{BackupFreshness: "current", OffsiteRecoveryPoints: []string{"2026-07-25T00:00:00Z"}, RecoveryBundleStatus: "exported", Sufficient: true}}
}

func TestFullPlanDeletesOnlyProvenResourcesByStage(t *testing.T) {
	plan, err := BuildFullPlan(fullInspectionForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	if plan.RequiresOwnerOverride || plan.TypedConfirmation != "FULL DECOMMISSION profile-1 "+plan.Digest {
		t.Fatalf("plan = %#v", plan)
	}
	want := map[string]Stage{"compute-1": ComputeStage, "storage-1": StorageStage, "network-1": NetworkingStage, "dns-1": DNSStage}
	for _, item := range plan.Items {
		stage, deleting := want[item.ProviderID]
		if deleting && (item.Action != Remove || item.Stage != stage) {
			t.Fatalf("%s = %s/%s", item.ProviderID, item.Action, item.Stage)
		}
		if !deleting && item.Action != Retain {
			t.Fatalf("%s unexpectedly delete", item.ProviderID)
		}
	}
}

func TestFullPlanWarnsButAllowsOwnerOverrideForWeakProtection(t *testing.T) {
	inspection := fullInspectionForTest(t)
	inspection.Protection = ProtectionEvidence{BackupFreshness: "stale", RecoveryBundleStatus: "missing", Sufficient: false, Warnings: []string{"offsite recovery point is stale"}}
	plan, err := BuildFullPlan(inspection)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.RequiresOwnerOverride {
		t.Fatal("weak protection did not require owner override")
	}
}

func TestFullResumeCannotExpandApprovedDeletionScope(t *testing.T) {
	inspection := fullInspectionForTest(t)
	plan, err := BuildFullPlan(inspection)
	if err != nil {
		t.Fatal(err)
	}
	// A new, proven resource is intentionally not an approved deletion.
	inspection.Resources = append(inspection.Resources, InspectedResource{Resource: Resource{Kind: "server", ProviderID: "new-server", Tags: map[string]string{"smallworlds-profile": "profile-1"}, ClusterIdentity: "profile-1"}, Ownership: ProfileOwned})
	inspection.ObservedAt = time.Now().UTC().Add(time.Second)
	if err := ValidateFullResume(plan, inspection, 0); err != nil {
		t.Fatalf("new proven resource should stay outside plan: %v", err)
	}
	inspection.Resources = append(inspection.Resources, InspectedResource{Resource: Resource{Kind: "server", ProviderID: "ambiguous"}, Ownership: Unknown})
	if err := ValidateFullResume(plan, inspection, 0); err == nil {
		t.Fatal("new ambiguous resource allowed resume")
	}
}
