package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stephan271/smallworlds/operator-console/internal/state"
)

func TestSetupSettingsSurviveALauncherRestartSoAnswersAreTypedOnce(t *testing.T) {
	directory := t.TempDir()
	store, err := state.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.CreateProfile(context.Background(), state.Profile{ID: "profile", Name: "Community", Language: "en", DeploymentMode: "local-lan"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSetupSettings(context.Background(), profile.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("settings before first answer = %v", err)
	}
	saved := state.SetupSettings{
		ProfileID:             profile.ID,
		CapabilityMode:        "collaboration",
		CapabilityApps:        []string{"nextcloud", "immich"},
		Release:               "v1.2.27",
		SettingsRepositoryURL: "https://github.com/example/settings.git",
		Domain:                "home.example",
		NodeTargetKind:        "remote",
		NodeHost:              "node.example",
		NodePort:              22,
		NodeUsername:          "operator",
		NodeAuthentication:    "password",
		DataDirectory:         "/var/lib/smallworlds-data",
		ManageDNS:             true,
	}
	if err := store.RecordSetupSettings(context.Background(), saved); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := state.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	loaded, err := reopened.GetSetupSettings(context.Background(), profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Domain != saved.Domain || loaded.NodeHost != saved.NodeHost || loaded.NodePort != saved.NodePort || !loaded.ManageDNS {
		t.Fatalf("reopened settings = %#v", loaded)
	}
	if len(loaded.CapabilityApps) != 2 || loaded.CapabilityApps[0] != "nextcloud" {
		t.Fatalf("reopened apps = %#v", loaded.CapabilityApps)
	}
	if loaded.RecordedAt.IsZero() {
		t.Fatal("recorded time missing")
	}
}

func TestSetupSettingsAreReplacedWholesaleSoClearedAnswersDoNotLinger(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	profile, err := store.CreateProfile(context.Background(), state.Profile{ID: "profile", Name: "Community", Language: "en", DeploymentMode: "local-lan"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordSetupSettings(context.Background(), state.SetupSettings{ProfileID: profile.ID, NodeHost: "old.example", Domain: "home.example"}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordSetupSettings(context.Background(), state.SetupSettings{ProfileID: profile.ID, Domain: "home.example"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetSetupSettings(context.Background(), profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.NodeHost != "" {
		t.Fatalf("cleared host lingered = %q", loaded.NodeHost)
	}
	if loaded.Domain != "home.example" {
		t.Fatalf("retained domain = %q", loaded.Domain)
	}
}

func TestSetupSettingsTravelInARecoveryBundleAndAreForgottenWithTheProfile(t *testing.T) {
	source, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	profile, err := source.CreateProfile(context.Background(), state.Profile{ID: "profile", Name: "Community", Language: "en", DeploymentMode: "hetzner"})
	if err != nil {
		t.Fatal(err)
	}
	if err := source.RecordSetupSettings(context.Background(), state.SetupSettings{ProfileID: profile.ID, Domain: "home.example", HetznerLocation: "nbg1"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.ExportProfileSnapshot(context.Background(), profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SetupSettings == nil || snapshot.SetupSettings.HetznerLocation != "nbg1" {
		t.Fatalf("exported settings = %#v", snapshot.SetupSettings)
	}

	target, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	if err := target.ImportProfileSnapshot(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	imported, err := target.GetSetupSettings(context.Background(), profile.ID)
	if err != nil || imported.HetznerLocation != "nbg1" {
		t.Fatalf("imported settings = %#v, err = %v", imported, err)
	}

	if err := target.ForgetProfile(context.Background(), profile.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := target.GetSetupSettings(context.Background(), profile.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("settings after forget = %v", err)
	}
}
