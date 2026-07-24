package console

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/assessment"
	"github.com/stephan271/smallworlds/operator-console/internal/capability"
	"github.com/stephan271/smallworlds/operator-console/internal/consoleauth"
	"github.com/stephan271/smallworlds/operator-console/internal/operatordevice"
)

type fakeDirectory struct {
	devices []operatordevice.Device
	err     error
}

func (f *fakeDirectory) Devices(context.Context) ([]operatordevice.Device, error) {
	return f.devices, f.err
}

type fakeIssuer struct {
	key       string
	expiresAt time.Time
	label     string
	err       error
}

func (f *fakeIssuer) MintJoinKey(_ context.Context, label string, _ time.Duration) (MintedJoinKey, error) {
	f.label = label
	return MintedJoinKey{Key: f.key, ExpiresAt: f.expiresAt}, f.err
}

type fakeRevoker struct {
	evidence  RevocationEvidence
	err       error
	revokedID string
}

func (f *fakeRevoker) Revoke(_ context.Context, stableID string) (RevocationEvidence, error) {
	f.revokedID = stableID
	return f.evidence, f.err
}

func newDeviceAdminServer(t *testing.T, exchanger consoleauth.TokenExchanger, dir DeviceDirectory, issuer InvitationIssuer, revoker DeviceRevoker) *Server {
	t.Helper()
	server, err := New(Config{
		Issuer:                testIssuer,
		ClientID:              testClientID,
		AuthorizationEndpoint: testIssuer + "/protocol/openid-connect/auth",
		RedirectURI:           "https://console.test/api/v1/auth/callback",
		Exchanger:             exchanger,
		Assessor:              fakeAssessor{},
		Catalog:               []assessment.CapabilityRef{{ID: "nextcloud"}},
		DeploymentMode:        capability.LocalLAN,
		BaseDomain:            "sw.example.internal",
		Directory:             dir,
		Invitations:           issuer,
		Revoker:               revoker,
		SessionKey:            []byte("0123456789abcdef0123456789abcdef"),
		Leeway:                30 * time.Second,
		Now:                   func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return server
}

func inventory() []operatordevice.Device {
	return []operatordevice.Device{
		{StableID: "node-owner-1", Hostname: "alice-laptop", OwnerAccess: true, Self: true, Online: true},
		{StableID: "node-owner-2", Hostname: "alice-desktop", OwnerAccess: true, Online: true},
		{StableID: "node-lost-1", Hostname: "old-phone", OwnerAccess: false, Online: false},
	}
}

func TestDeviceAdminRequiresOwner(t *testing.T) {
	exchanger := &fakeExchanger{}
	server := newDeviceAdminServer(t, exchanger, &fakeDirectory{devices: inventory()}, &fakeIssuer{key: "tskey-abc"}, &fakeRevoker{})

	// Operator (propose but not administer) is forbidden on every device route.
	operator := loginSession(t, server, exchanger, "operator")
	routes := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/api/v1/administration/access", ""},
		{http.MethodPost, "/api/v1/administration/invitations", `{"label":"x"}`},
		{http.MethodPost, "/api/v1/administration/revocations/plan", `{"stableId":"node-lost-1"}`},
	}
	for _, r := range routes {
		var rec = post(t, server, r.path, r.body, operator)
		if r.method == http.MethodGet {
			rec = get(t, server, r.path, operator)
		}
		if rec.Code != http.StatusForbidden {
			t.Errorf("operator %s %s = %d, want 403", r.method, r.path, rec.Code)
		}
	}
	// Anonymous is unauthorized.
	if code := post(t, server, "/api/v1/administration/invitations", `{"label":"x"}`).Code; code != http.StatusUnauthorized {
		t.Errorf("anonymous invitation = %d, want 401", code)
	}
}

func TestCreateInvitationShowsKeyOnceWithGuidance(t *testing.T) {
	exchanger := &fakeExchanger{}
	server := newDeviceAdminServer(t, exchanger, &fakeDirectory{devices: inventory()},
		&fakeIssuer{key: "tskey-single-use-123", expiresAt: testNow.Add(15 * time.Minute)}, &fakeRevoker{})
	owner := loginSession(t, server, exchanger, "owner")

	resp := post(t, server, "/api/v1/administration/invitations", `{"label":"bob-laptop"}`, owner)
	if resp.Code != http.StatusCreated {
		t.Fatalf("create invitation = %d, want 201; body=%s", resp.Code, resp.Body)
	}
	var body struct {
		JoinKey        string `json:"joinKey"`
		KeyFingerprint string `json:"keyFingerprint"`
		IssuedBy       string `json:"issuedBy"`
		SingleUse      bool   `json:"singleUse"`
		Guidance       struct {
			ClusterCaTrustRequired bool `json:"clusterCaTrustRequired"`
			Steps                  []struct {
				Kind string `json:"kind"`
			} `json:"steps"`
		} `json:"guidance"`
	}
	json.Unmarshal(resp.Body.Bytes(), &body)
	if body.JoinKey != "tskey-single-use-123" || !body.SingleUse {
		t.Fatalf("expected the single-use join key shown once, got %+v", body)
	}
	if body.IssuedBy != "alice" {
		t.Fatalf("issuedBy = %q, want the owner (attributable)", body.IssuedBy)
	}
	// LAN-only mode: guidance must require Cluster CA trust.
	if !body.Guidance.ClusterCaTrustRequired {
		t.Fatal("LAN-only guidance must require Cluster CA trust")
	}
	hasCA := false
	for _, step := range body.Guidance.Steps {
		if step.Kind == "install-cluster-ca" {
			hasCA = true
		}
	}
	if !hasCA {
		t.Fatal("guidance missing the install-cluster-ca step")
	}

	// The Activity Record records the issuance without the join key.
	access := get(t, server, "/api/v1/administration/access", owner)
	if strings.Contains(access.Body.String(), "tskey-single-use-123") {
		t.Fatal("the join key must never appear in the Activity Record")
	}
	if !strings.Contains(access.Body.String(), body.KeyFingerprint) {
		t.Fatal("the Activity Record should reference the invitation by fingerprint")
	}
}

func TestInvitationHonestRefusalWhenUnwired(t *testing.T) {
	exchanger := &fakeExchanger{}
	// No issuer wired.
	server := newDeviceAdminServer(t, exchanger, &fakeDirectory{devices: inventory()}, nil, &fakeRevoker{})
	owner := loginSession(t, server, exchanger, "owner")
	resp := post(t, server, "/api/v1/administration/invitations", `{"label":"x"}`, owner)
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("create invitation = %d, want 503 when issuer unavailable", resp.Code)
	}
}

func TestAccessSummaryShowsAlternativeOwnerAccess(t *testing.T) {
	exchanger := &fakeExchanger{}
	server := newDeviceAdminServer(t, exchanger, &fakeDirectory{devices: inventory()}, &fakeIssuer{key: "k"}, &fakeRevoker{})
	owner := loginSession(t, server, exchanger, "owner")

	resp := get(t, server, "/api/v1/administration/access", owner)
	if resp.Code != http.StatusOK {
		t.Fatalf("access = %d, want 200; body=%s", resp.Code, resp.Body)
	}
	var body struct {
		Devices []deviceView `json:"devices"`
		Summary struct {
			TotalDevices int `json:"totalDevices"`
			OwnerDevices int `json:"ownerDevices"`
		} `json:"summary"`
	}
	json.Unmarshal(resp.Body.Bytes(), &body)
	if body.Summary.TotalDevices != 3 || body.Summary.OwnerDevices != 2 {
		t.Fatalf("summary = %+v, want 3 devices / 2 owner", body.Summary)
	}
}

func TestRevocationPlanApproveExecuteHappyPath(t *testing.T) {
	exchanger := &fakeExchanger{}
	revoker := &fakeRevoker{evidence: RevocationEvidence{Removed: true, AccessVerified: true, Detail: "device offline; probe from tailnet refused"}}
	server := newDeviceAdminServer(t, exchanger, &fakeDirectory{devices: inventory()}, &fakeIssuer{key: "k"}, revoker)
	owner := loginSession(t, server, exchanger, "owner")

	// Plan revocation of the lost, non-owner device: no lockout risk.
	planResp := post(t, server, "/api/v1/administration/revocations/plan", `{"stableId":"node-lost-1"}`, owner)
	if planResp.Code != http.StatusCreated {
		t.Fatalf("plan = %d, want 201; body=%s", planResp.Code, planResp.Body)
	}
	var planBody struct {
		PlanID     string                              `json:"planId"`
		Assessment operatordevice.RevocationAssessment `json:"assessment"`
	}
	json.Unmarshal(planResp.Body.Bytes(), &planBody)
	if planBody.PlanID == "" || planBody.Assessment.AffectedStableID != "node-lost-1" {
		t.Fatalf("unexpected plan body: %+v", planBody)
	}
	if planBody.Assessment.LockoutRisk || !planBody.Assessment.AlternativeOwnerAccess {
		t.Fatalf("assessment = %+v, want no lockout and alternative owner access", planBody.Assessment)
	}

	// Executing before approval is refused.
	if early := post(t, server, "/api/v1/administration/revocations/"+planBody.PlanID+"/execute", "", owner); early.Code != http.StatusConflict {
		t.Fatalf("execute before approval = %d, want 409", early.Code)
	}

	// Approve, then execute removes exactly the selected device and verifies loss.
	if approve := post(t, server, "/api/v1/administration/revocations/"+planBody.PlanID+"/approve", "", owner); approve.Code != http.StatusOK {
		t.Fatalf("approve = %d, want 200", approve.Code)
	}
	exec := post(t, server, "/api/v1/administration/revocations/"+planBody.PlanID+"/execute", "", owner)
	if exec.Code != http.StatusCreated {
		t.Fatalf("execute = %d, want 201; body=%s", exec.Code, exec.Body)
	}
	var execBody struct {
		Phase            string `json:"phase"`
		AffectedStableID string `json:"affectedStableId"`
		AccessVerified   bool   `json:"accessVerified"`
	}
	json.Unmarshal(exec.Body.Bytes(), &execBody)
	if execBody.Phase != "succeeded" || execBody.AffectedStableID != "node-lost-1" || !execBody.AccessVerified {
		t.Fatalf("execute body = %+v, want succeeded/node-lost-1/verified", execBody)
	}
	if revoker.revokedID != "node-lost-1" {
		t.Fatalf("revoker removed %q, want only node-lost-1", revoker.revokedID)
	}
}

func TestRevocationLabelsLockoutRisk(t *testing.T) {
	exchanger := &fakeExchanger{}
	server := newDeviceAdminServer(t, exchanger, &fakeDirectory{devices: inventory()}, &fakeIssuer{key: "k"}, &fakeRevoker{})
	owner := loginSession(t, server, exchanger, "owner")

	// node-owner-1 is the acting device (Self): self-revocation lockout label.
	planResp := post(t, server, "/api/v1/administration/revocations/plan", `{"stableId":"node-owner-1"}`, owner)
	var planBody struct {
		Assessment operatordevice.RevocationAssessment `json:"assessment"`
	}
	json.Unmarshal(planResp.Body.Bytes(), &planBody)
	if !planBody.Assessment.LockoutRisk || planBody.Assessment.LockoutReason != operatordevice.LockoutSelfRevocation {
		t.Fatalf("assessment = %+v, want self-revocation lockout risk", planBody.Assessment)
	}
}

func TestRevocationDriftRefusesExecution(t *testing.T) {
	exchanger := &fakeExchanger{}
	directory := &fakeDirectory{devices: inventory()}
	server := newDeviceAdminServer(t, exchanger, directory, &fakeIssuer{key: "k"}, &fakeRevoker{evidence: RevocationEvidence{Removed: true, AccessVerified: true}})
	owner := loginSession(t, server, exchanger, "owner")

	planResp := post(t, server, "/api/v1/administration/revocations/plan", `{"stableId":"node-lost-1"}`, owner)
	var planBody struct {
		PlanID string `json:"planId"`
	}
	json.Unmarshal(planResp.Body.Bytes(), &planBody)
	post(t, server, "/api/v1/administration/revocations/"+planBody.PlanID+"/approve", "", owner)

	// The device is gone from the inventory between approval and execution.
	directory.devices = []operatordevice.Device{
		{StableID: "node-owner-1", Hostname: "alice-laptop", OwnerAccess: true, Online: true},
	}
	exec := post(t, server, "/api/v1/administration/revocations/"+planBody.PlanID+"/execute", "", owner)
	if exec.Code != http.StatusConflict {
		t.Fatalf("execute after drift = %d, want 409", exec.Code)
	}
}

func TestRevocationUnknownDevice(t *testing.T) {
	exchanger := &fakeExchanger{}
	server := newDeviceAdminServer(t, exchanger, &fakeDirectory{devices: inventory()}, &fakeIssuer{key: "k"}, &fakeRevoker{})
	owner := loginSession(t, server, exchanger, "owner")
	resp := post(t, server, "/api/v1/administration/revocations/plan", `{"stableId":"node-ghost"}`, owner)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("plan for unknown device = %d, want 404", resp.Code)
	}
}

func TestRevocationExecuteHonestRefusalWhenRevokerUnwired(t *testing.T) {
	exchanger := &fakeExchanger{}
	// No revoker wired.
	server := newDeviceAdminServer(t, exchanger, &fakeDirectory{devices: inventory()}, &fakeIssuer{key: "k"}, nil)
	owner := loginSession(t, server, exchanger, "owner")

	planResp := post(t, server, "/api/v1/administration/revocations/plan", `{"stableId":"node-lost-1"}`, owner)
	var planBody struct {
		PlanID string `json:"planId"`
	}
	json.Unmarshal(planResp.Body.Bytes(), &planBody)
	post(t, server, "/api/v1/administration/revocations/"+planBody.PlanID+"/approve", "", owner)
	exec := post(t, server, "/api/v1/administration/revocations/"+planBody.PlanID+"/execute", "", owner)
	if exec.Code != http.StatusServiceUnavailable {
		t.Fatalf("execute = %d, want 503 when revoker unavailable", exec.Code)
	}
}

func TestAdminActivityHiddenFromProposals(t *testing.T) {
	exchanger := &fakeExchanger{}
	server := newDeviceAdminServer(t, exchanger, &fakeDirectory{devices: inventory()},
		&fakeIssuer{key: "k", expiresAt: testNow.Add(15 * time.Minute)}, &fakeRevoker{})
	owner := loginSession(t, server, exchanger, "owner")

	// Issue an invitation, creating an Owner-level Activity Record.
	if resp := post(t, server, "/api/v1/administration/invitations", `{"label":"bob-laptop"}`, owner); resp.Code != http.StatusCreated {
		t.Fatalf("create invitation = %d, want 201", resp.Code)
	}
	// The propose-level proposals workspace must not surface Owner device runs.
	proposals := get(t, server, "/api/v1/proposals", owner)
	var workspace struct {
		Runs []additionRunView `json:"runs"`
	}
	json.Unmarshal(proposals.Body.Bytes(), &workspace)
	if len(workspace.Runs) != 0 {
		t.Fatalf("proposals runs = %d, want device-admin runs hidden from the propose surface", len(workspace.Runs))
	}
}
