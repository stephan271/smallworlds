package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/state"
)

func TestBootstrapPlanBindingSurvivesStoreRestart(t *testing.T) {
	dataDirectory := t.TempDir()
	store, err := state.Open(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	profile := state.Profile{ID: "profile-1", Name: "Home", Language: "en", DeploymentMode: "local-lan", Revision: 1, CreatedAt: now}
	if _, err := store.CreateProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	plan := state.PlanRecord{ID: "plan-1", ProfileID: profile.ID, Intent: "BootstrapLocalNode", Digest: "digest", Status: "planned", ProfileRevision: 1, CreatedAt: now}
	if err := store.CreatePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordBootstrapPlan(ctx, state.BootstrapPlanRecord{PlanID: plan.ID, ProfileID: profile.ID, Binding: `{"release":"v1.2.26"}`, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = state.Open(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	record, err := store.GetBootstrapPlan(ctx, plan.ID)
	if err != nil || record.ProfileID != profile.ID || record.Binding != `{"release":"v1.2.26"}` {
		t.Fatalf("bootstrap plan = %#v, err = %v", record, err)
	}
}
