package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/state"
)

func TestClusterCAReferenceSurvivesRestartAndTracksDeviceTrust(t *testing.T) {
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
	if _, err := store.GetClusterCAReference(ctx, profile.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
	material := `{"reference":{"rootFingerprint":"SHA256:AA"},"rootCertificatePem":"-----BEGIN CERTIFICATE-----"}`
	if err := store.RecordClusterCAReference(ctx, state.ClusterCAReference{ProfileID: profile.ID, Material: material, DeviceTrustInstalled: false, RecordedAt: now}); err != nil {
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
	record, err := store.GetClusterCAReference(ctx, profile.ID)
	if err != nil || record.Material != material || record.DeviceTrustInstalled {
		t.Fatalf("cluster CA reference = %#v, err = %v", record, err)
	}

	if err := store.MarkClusterCADeviceTrustInstalled(ctx, profile.ID); err != nil {
		t.Fatal(err)
	}
	installed, err := store.GetClusterCAReference(ctx, profile.ID)
	if err != nil || !installed.DeviceTrustInstalled {
		t.Fatalf("device trust not marked installed: %#v, err = %v", installed, err)
	}

	if err := store.MarkClusterCADeviceTrustInstalled(ctx, "missing"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("marking a missing profile = %v", err)
	}
}
