package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/state"
)

func TestNodeTrustIsPinnedPerProfileAndCanBeReplacedDeliberately(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	profile, err := store.CreateProfile(context.Background(), state.Profile{ID: "profile", Name: "Node", Language: "en", DeploymentMode: "local-lan"})
	if err != nil {
		t.Fatal(err)
	}
	first := state.NodeTrust{ProfileID: profile.ID, Host: "node.example", Port: 22, Username: "operator", Fingerprint: "SHA256:first", ConfirmedAt: time.Now().UTC()}
	if err := store.RecordNodeTrust(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetNodeTrust(context.Background(), profile.ID)
	if err != nil || loaded.Fingerprint != first.Fingerprint {
		t.Fatalf("loaded = %#v, err = %v", loaded, err)
	}
	second := first
	second.Fingerprint = "SHA256:replacement"
	if err := store.RecordNodeTrust(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.GetNodeTrust(context.Background(), profile.ID)
	if err != nil || loaded.Fingerprint != second.Fingerprint {
		t.Fatalf("replacement = %#v, err = %v", loaded, err)
	}
}
