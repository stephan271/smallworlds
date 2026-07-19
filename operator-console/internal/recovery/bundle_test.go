package recovery_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/recovery"
	"github.com/stephan271/smallworlds/operator-console/internal/state"
)

func TestRecoveryBundleEncryptsAndRestoresCompleteTransferPayload(t *testing.T) {
	createdAt := time.Date(2031, 2, 3, 4, 5, 6, 0, time.UTC)
	payload := recovery.Payload{
		Format:              "smallworlds-recovery-bundle",
		Version:             1,
		Profile:             state.Profile{ID: "cluster-123", Name: "Workshop", Language: "en", DeploymentMode: "local-lan", Revision: 3, CreatedAt: createdAt},
		WorkflowSnapshot:    recovery.WorkflowSnapshot{Plans: []state.PlanRecord{{ID: "plan-1", ProfileID: "cluster-123", Status: "approved"}}},
		InfrastructureState: json.RawMessage(`{"provider":"local","network":"tailnet"}`),
		Kubeconfig:          "apiVersion: v1\nclusters:",
		ClusterCA:           "-----BEGIN CERTIFICATE-----",
		VaultMaterial:       map[string]string{"cluster-123/git-provider-token": "secret-value"},
	}
	bundle, err := recovery.ExportWithPassphrase(payload, "complete transfer passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(bundle, []byte(recovery.Header)) {
		t.Fatalf("bundle header = %q, want %q", bundle[:len(recovery.Header)], recovery.Header)
	}
	for _, protected := range []string{payload.Profile.Name, string(payload.InfrastructureState), payload.Kubeconfig, payload.ClusterCA, "secret-value"} {
		if bytes.Contains(bundle, []byte(protected)) {
			t.Fatalf("encrypted bundle exposes protected content %q", protected)
		}
	}
	restored, err := recovery.OpenWithPassphrase(bundle, "complete transfer passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if restored.Profile != payload.Profile || string(restored.InfrastructureState) != string(payload.InfrastructureState) || restored.Kubeconfig != payload.Kubeconfig || restored.ClusterCA != payload.ClusterCA || restored.VaultMaterial["cluster-123/git-provider-token"] != "secret-value" || len(restored.WorkflowSnapshot.Plans) != 1 {
		t.Fatalf("restored payload = %#v, want complete transfer payload", restored)
	}
}
