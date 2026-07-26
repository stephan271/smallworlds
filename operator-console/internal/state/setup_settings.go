package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// SetupSettings is every non-secret answer an operator gives while walking the
// setup journey. It exists so the console can be closed and reopened — or the
// launcher restarted — without retyping a domain, a host name, or a release tag.
//
// The field set is deliberately closed rather than a free-form map: it is the
// contract the browser fills in, and a fixed shape is what lets the launcher
// guarantee no secret is ever written here. Secrets belong in the Launcher
// Vault and are addressed by the vault keys in credential_references; see
// SecretKinds.
type SetupSettings struct {
	ProfileID string `json:"profileId"`

	// What the community should be able to do.
	CapabilityMode        string   `json:"capabilityMode,omitempty"`
	CapabilityApps        []string `json:"capabilityApps,omitempty"`
	Release               string   `json:"release,omitempty"`
	SettingsRepositoryURL string   `json:"settingsRepositoryUrl,omitempty"`
	Domain                string   `json:"domain,omitempty"`

	// Where the settings repository lives.
	SettingsProvider     string `json:"settingsProvider,omitempty"`
	GitHubAuthority      string `json:"githubAuthority,omitempty"`
	GitHubRepositoryName string `json:"githubRepositoryName,omitempty"`
	GitUsername          string `json:"gitUsername,omitempty"`

	// The computer that will run the cluster.
	NodeTargetKind     string `json:"nodeTargetKind,omitempty"`
	NodeHost           string `json:"nodeHost,omitempty"`
	NodePort           int    `json:"nodePort,omitempty"`
	NodeUsername       string `json:"nodeUsername,omitempty"`
	NodeAuthentication string `json:"nodeAuthentication,omitempty"`
	DataDirectory      string `json:"dataDirectory,omitempty"`

	// Installing onto that computer.
	NodeName           string `json:"nodeName,omitempty"`
	Environment        string `json:"environment,omitempty"`
	ACMEEmail          string `json:"acmeEmail,omitempty"`
	ManageDNS          bool   `json:"manageDns,omitempty"`
	RouterAcknowledged bool   `json:"routerAcknowledged,omitempty"`

	// Renting a machine instead.
	HetznerDomain          string `json:"hetznerDomain,omitempty"`
	HetznerEnvExt          string `json:"hetznerEnvExt,omitempty"`
	HetznerTier            string `json:"hetznerTier,omitempty"`
	HetznerLocation        string `json:"hetznerLocation,omitempty"`
	HetznerServerType      string `json:"hetznerServerType,omitempty"`
	HetznerVolumeGb        int    `json:"hetznerVolumeGb,omitempty"`
	HetznerOperatorAddress string `json:"hetznerOperatorAddress,omitempty"`

	// Handing administration over to the cluster itself.
	HandoffBaseDomain string `json:"handoffBaseDomain,omitempty"`

	// Copying backups somewhere off the machine.
	OffsiteEndpoint string `json:"offsiteEndpoint,omitempty"`
	OffsiteRegion   string `json:"offsiteRegion,omitempty"`
	OffsiteBucket   string `json:"offsiteBucket,omitempty"`

	RecordedAt time.Time `json:"recordedAt"`
}

// RecordSetupSettings replaces a profile's saved answers wholesale. Callers pass
// the complete set; a partial write would silently drop fields the operator
// filled in on another step.
func (store *Store) RecordSetupSettings(ctx context.Context, settings SetupSettings) error {
	if settings.ProfileID == "" {
		return fmt.Errorf("record setup settings: missing profile")
	}
	if settings.RecordedAt.IsZero() {
		settings.RecordedAt = time.Now().UTC()
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("encode setup settings: %w", err)
	}
	_, err = store.database.ExecContext(ctx, `INSERT INTO setup_settings (profile_id, settings_json, recorded_at) VALUES (?, ?, ?) ON CONFLICT(profile_id) DO UPDATE SET settings_json=excluded.settings_json, recorded_at=excluded.recorded_at`, settings.ProfileID, string(encoded), settings.RecordedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("record setup settings: %w", err)
	}
	return nil
}

// GetSetupSettings returns a profile's saved answers, or ErrNotFound when the
// operator has not reached any step that stores one yet.
func (store *Store) GetSetupSettings(ctx context.Context, profileID string) (SetupSettings, error) {
	var encoded, recordedAt string
	err := store.database.QueryRowContext(ctx, `SELECT settings_json, recorded_at FROM setup_settings WHERE profile_id = ?`, profileID).Scan(&encoded, &recordedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SetupSettings{}, ErrNotFound
	}
	if err != nil {
		return SetupSettings{}, fmt.Errorf("get setup settings: %w", err)
	}
	var settings SetupSettings
	if err := json.Unmarshal([]byte(encoded), &settings); err != nil {
		return SetupSettings{}, fmt.Errorf("decode setup settings: %w", err)
	}
	settings.RecordedAt, err = time.Parse(time.RFC3339Nano, recordedAt)
	if err != nil {
		return SetupSettings{}, fmt.Errorf("parse setup settings record: %w", err)
	}
	settings.ProfileID = profileID
	return settings, nil
}
