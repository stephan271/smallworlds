package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Profile struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Language       string    `json:"language"`
	DeploymentMode string    `json:"deploymentMode"`
	Revision       int64     `json:"revision"`
	CreatedAt      time.Time `json:"createdAt"`
}

type PlanRecord struct {
	ID              string
	ProfileID       string
	Intent          string
	Digest          string
	Status          string
	ProfileRevision int64
	CreatedAt       time.Time
}

type RunRecord struct {
	ID                     string
	PlanID                 string
	ProfileID              string
	State                  string
	CurrentCheckpoint      string
	CancellationState      string
	VerificationCode       string
	VerificationObservedAt *time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type EventRecord struct {
	ID         int64
	ProfileID  string
	RunID      string
	Type       string
	MessageKey string
	Parameters string
	OccurredAt time.Time
}

type VerificationRecord struct {
	ProfileRevision int64
	Code            string
	ObservedAt      time.Time
}

type Store struct {
	database *sql.DB
}

var ErrNotFound = errors.New("not found")

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create launcher data directory: %w", err)
	}
	if err := os.Chmod(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("protect launcher data directory: %w", err)
	}

	databasePath := filepath.Join(dataDir, "launcher.db")
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, fmt.Errorf("open launcher database: %w", err)
	}
	database.SetMaxOpenConns(1)
	store := &Store{database: database}
	if err := store.migrate(context.Background()); err != nil {
		database.Close()
		return nil, err
	}
	if err := os.Chmod(databasePath, 0o600); err != nil {
		database.Close()
		return nil, fmt.Errorf("protect launcher database: %w", err)
	}
	return store, nil
}

func (store *Store) Close() error {
	return store.database.Close()
}

func (store *Store) CreateProfile(ctx context.Context, profile Profile) (Profile, error) {
	createdAt := time.Now().UTC()
	_, err := store.database.ExecContext(ctx, `
		INSERT INTO profiles (id, name, language, deployment_mode, revision, created_at)
		VALUES (?, ?, ?, ?, 1, ?)
	`, profile.ID, profile.Name, profile.Language, profile.DeploymentMode, createdAt.Format(time.RFC3339Nano))
	if err != nil {
		return Profile{}, fmt.Errorf("create profile: %w", err)
	}
	profile.Revision = 1
	profile.CreatedAt = createdAt
	return profile, nil
}

func (store *Store) ListProfiles(ctx context.Context) ([]Profile, error) {
	rows, err := store.database.QueryContext(ctx, `
		SELECT id, name, language, deployment_mode, revision, created_at
		FROM profiles
		ORDER BY created_at, rowid
	`)
	if err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}
	defer rows.Close()

	profiles := make([]Profile, 0)
	for rows.Next() {
		var profile Profile
		var createdAt string
		if err := rows.Scan(&profile.ID, &profile.Name, &profile.Language, &profile.DeploymentMode, &profile.Revision, &createdAt); err != nil {
			return nil, fmt.Errorf("scan profile: %w", err)
		}
		profile.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse profile creation time: %w", err)
		}
		profiles = append(profiles, profile)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}
	return profiles, nil
}

func (store *Store) GetProfile(ctx context.Context, id string) (Profile, error) {
	var profile Profile
	var createdAt string
	err := store.database.QueryRowContext(ctx, `
		SELECT id, name, language, deployment_mode, revision, created_at
		FROM profiles
		WHERE id = ?
	`, id).Scan(&profile.ID, &profile.Name, &profile.Language, &profile.DeploymentMode, &profile.Revision, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	if err != nil {
		return Profile{}, fmt.Errorf("get profile: %w", err)
	}
	profile.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return Profile{}, fmt.Errorf("parse profile creation time: %w", err)
	}
	return profile, nil
}

func (store *Store) UpdateProfile(ctx context.Context, id, name, language, deploymentMode string) (Profile, error) {
	result, err := store.database.ExecContext(ctx, `
		UPDATE profiles
		SET name = ?, language = ?, deployment_mode = ?, revision = revision + 1
		WHERE id = ?
	`, name, language, deploymentMode, id)
	if err != nil {
		return Profile{}, fmt.Errorf("update profile: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return Profile{}, fmt.Errorf("read updated profile count: %w", err)
	}
	if rows == 0 {
		return Profile{}, ErrNotFound
	}
	return store.GetProfile(ctx, id)
}

func (store *Store) CreatePlan(ctx context.Context, plan PlanRecord) error {
	_, err := store.database.ExecContext(ctx, `
		INSERT INTO plans (id, profile_id, intent, digest, status, profile_revision, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, plan.ID, plan.ProfileID, plan.Intent, plan.Digest, plan.Status, plan.ProfileRevision, plan.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("create plan: %w", err)
	}
	return nil
}

func (store *Store) GetPlan(ctx context.Context, id string) (PlanRecord, error) {
	var plan PlanRecord
	var createdAt string
	err := store.database.QueryRowContext(ctx, `
		SELECT id, profile_id, intent, digest, status, profile_revision, created_at
		FROM plans
		WHERE id = ?
	`, id).Scan(&plan.ID, &plan.ProfileID, &plan.Intent, &plan.Digest, &plan.Status, &plan.ProfileRevision, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PlanRecord{}, ErrNotFound
	}
	if err != nil {
		return PlanRecord{}, fmt.Errorf("get plan: %w", err)
	}
	plan.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return PlanRecord{}, fmt.Errorf("parse plan creation time: %w", err)
	}
	return plan, nil
}

func (store *Store) UpdatePlanStatus(ctx context.Context, id, status string) error {
	result, err := store.database.ExecContext(ctx, `UPDATE plans SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		return fmt.Errorf("update plan status: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated plan count: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (store *Store) CreateRun(ctx context.Context, run RunRecord) error {
	_, err := store.database.ExecContext(ctx, `
		INSERT INTO runs (
			id, plan_id, profile_id, state, current_checkpoint, cancellation_state,
			verification_code, verification_observed_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, run.ID, run.PlanID, run.ProfileID, run.State, run.CurrentCheckpoint, run.CancellationState,
		run.VerificationCode, nullableTime(run.VerificationObservedAt), run.CreatedAt.Format(time.RFC3339Nano), run.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("create run: %w", err)
	}
	return nil
}

func (store *Store) GetRun(ctx context.Context, id string) (RunRecord, error) {
	var run RunRecord
	var verificationObservedAt sql.NullString
	var createdAt, updatedAt string
	err := store.database.QueryRowContext(ctx, `
		SELECT id, plan_id, profile_id, state, current_checkpoint, cancellation_state,
		       verification_code, verification_observed_at, created_at, updated_at
		FROM runs
		WHERE id = ?
	`, id).Scan(&run.ID, &run.PlanID, &run.ProfileID, &run.State, &run.CurrentCheckpoint,
		&run.CancellationState, &run.VerificationCode, &verificationObservedAt, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return RunRecord{}, ErrNotFound
	}
	if err != nil {
		return RunRecord{}, fmt.Errorf("get run: %w", err)
	}
	parsedCreatedAt, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return RunRecord{}, fmt.Errorf("parse run creation time: %w", err)
	}
	parsedUpdatedAt, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return RunRecord{}, fmt.Errorf("parse run update time: %w", err)
	}
	run.CreatedAt = parsedCreatedAt
	run.UpdatedAt = parsedUpdatedAt
	if verificationObservedAt.Valid {
		parsed, err := time.Parse(time.RFC3339Nano, verificationObservedAt.String)
		if err != nil {
			return RunRecord{}, fmt.Errorf("parse verification time: %w", err)
		}
		run.VerificationObservedAt = &parsed
	}
	return run, nil
}

func (store *Store) ListActiveRuns(ctx context.Context) ([]RunRecord, error) {
	rows, err := store.database.QueryContext(ctx, `
		SELECT id, plan_id, profile_id, state, current_checkpoint, cancellation_state,
		       verification_code, verification_observed_at, created_at, updated_at
		FROM runs
		WHERE state = 'running'
		ORDER BY created_at
	`)
	if err != nil {
		return nil, fmt.Errorf("list active runs: %w", err)
	}
	defer rows.Close()
	runs := make([]RunRecord, 0)
	for rows.Next() {
		var run RunRecord
		var verificationObservedAt sql.NullString
		var createdAt, updatedAt string
		if err := rows.Scan(&run.ID, &run.PlanID, &run.ProfileID, &run.State, &run.CurrentCheckpoint,
			&run.CancellationState, &run.VerificationCode, &verificationObservedAt, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan active run: %w", err)
		}
		parsedCreatedAt, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse active run creation time: %w", err)
		}
		parsedUpdatedAt, err := time.Parse(time.RFC3339Nano, updatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse active run update time: %w", err)
		}
		run.CreatedAt = parsedCreatedAt
		run.UpdatedAt = parsedUpdatedAt
		if verificationObservedAt.Valid {
			parsed, err := time.Parse(time.RFC3339Nano, verificationObservedAt.String)
			if err != nil {
				return nil, fmt.Errorf("parse active run verification time: %w", err)
			}
			run.VerificationObservedAt = &parsed
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list active runs: %w", err)
	}
	return runs, nil
}

func (store *Store) UpdateRun(ctx context.Context, id, stateValue, checkpoint, verificationCode string, verificationObservedAt *time.Time) error {
	result, err := store.database.ExecContext(ctx, `
		UPDATE runs
		SET state = ?, current_checkpoint = ?, verification_code = ?, verification_observed_at = ?, updated_at = ?
		WHERE id = ?
	`, stateValue, checkpoint, verificationCode, nullableTime(verificationObservedAt), time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return fmt.Errorf("update run: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated run count: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (store *Store) RequestRunCancellation(ctx context.Context, id string) (RunRecord, error) {
	result, err := store.database.ExecContext(ctx, `
		UPDATE runs
		SET cancellation_state = 'requested', updated_at = ?
		WHERE id = ? AND state = 'running'
	`, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return RunRecord{}, fmt.Errorf("request run cancellation: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return RunRecord{}, fmt.Errorf("read cancellation update count: %w", err)
	}
	if rows == 0 {
		return RunRecord{}, ErrNotFound
	}
	return store.GetRun(ctx, id)
}

func (store *Store) CompleteRunCancellation(ctx context.Context, id, checkpoint string) error {
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin cancellation completion: %w", err)
	}
	defer transaction.Rollback()
	var profileID string
	if err := transaction.QueryRowContext(ctx, `SELECT profile_id FROM runs WHERE id = ?`, id).Scan(&profileID); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("read cancelled run profile: %w", err)
	}
	now := time.Now().UTC()
	result, err := transaction.ExecContext(ctx, `
		UPDATE runs
		SET state = 'cancelled', current_checkpoint = ?, cancellation_state = 'completed', updated_at = ?
		WHERE id = ? AND state = 'running' AND cancellation_state = 'requested'
	`, checkpoint, now.Format(time.RFC3339Nano), id)
	if err != nil {
		return fmt.Errorf("complete run cancellation: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read completed cancellation count: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO events (profile_id, run_id, type, message_key, parameters, occurred_at)
		VALUES (?, ?, 'run.cancelled', 'activity.run.cancelled', ?, ?)
	`, profileID, id, `{"checkpoint":"`+checkpoint+`"}`, now.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("append cancellation event: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit cancellation completion: %w", err)
	}
	return nil
}

func (store *Store) CompleteRunVerification(ctx context.Context, id, checkpoint, verificationCode string, observedAt time.Time) error {
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin verification completion: %w", err)
	}
	defer transaction.Rollback()
	var profileID string
	if err := transaction.QueryRowContext(ctx, `SELECT profile_id FROM runs WHERE id = ?`, id).Scan(&profileID); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("read verified run profile: %w", err)
	}
	result, err := transaction.ExecContext(ctx, `
		UPDATE runs
		SET state = 'verified', current_checkpoint = ?, verification_code = ?, verification_observed_at = ?, updated_at = ?
		WHERE id = ? AND state = 'running'
	`, checkpoint, verificationCode, observedAt.Format(time.RFC3339Nano), observedAt.Format(time.RFC3339Nano), id)
	if err != nil {
		return fmt.Errorf("complete run verification: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read completed verification count: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO events (profile_id, run_id, type, message_key, parameters, occurred_at)
		VALUES (?, ?, 'run.verified', 'activity.run.verified', ?, ?)
	`, profileID, id, `{"evidence":"`+verificationCode+`"}`, observedAt.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("append verification event: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit verification completion: %w", err)
	}
	return nil
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func (store *Store) AppendEvent(ctx context.Context, event EventRecord) (EventRecord, error) {
	result, err := store.database.ExecContext(ctx, `
		INSERT INTO events (profile_id, run_id, type, message_key, parameters, occurred_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, event.ProfileID, event.RunID, event.Type, event.MessageKey, event.Parameters, event.OccurredAt.Format(time.RFC3339Nano))
	if err != nil {
		return EventRecord{}, fmt.Errorf("append event: %w", err)
	}
	event.ID, err = result.LastInsertId()
	if err != nil {
		return EventRecord{}, fmt.Errorf("read event id: %w", err)
	}
	return event, nil
}

func (store *Store) ListEvents(ctx context.Context, profileID string, afterID int64) ([]EventRecord, error) {
	rows, err := store.database.QueryContext(ctx, `
		SELECT id, profile_id, run_id, type, message_key, parameters, occurred_at
		FROM events
		WHERE profile_id = ? AND id > ?
		ORDER BY id
	`, profileID, afterID)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	events := make([]EventRecord, 0)
	for rows.Next() {
		var event EventRecord
		var occurredAt string
		if err := rows.Scan(&event.ID, &event.ProfileID, &event.RunID, &event.Type, &event.MessageKey, &event.Parameters, &occurredAt); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		parsed, err := time.Parse(time.RFC3339Nano, occurredAt)
		if err != nil {
			return nil, fmt.Errorf("parse event time: %w", err)
		}
		event.OccurredAt = parsed
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return events, nil
}

func (store *Store) LatestVerification(ctx context.Context, profileID string) (VerificationRecord, error) {
	var verification VerificationRecord
	var observedAt string
	err := store.database.QueryRowContext(ctx, `
		SELECT plans.profile_revision, runs.verification_code, runs.verification_observed_at
		FROM runs
		JOIN plans ON plans.id = runs.plan_id
		WHERE runs.profile_id = ? AND runs.state = 'verified'
		ORDER BY runs.updated_at DESC
		LIMIT 1
	`, profileID).Scan(&verification.ProfileRevision, &verification.Code, &observedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return VerificationRecord{}, ErrNotFound
	}
	if err != nil {
		return VerificationRecord{}, fmt.Errorf("read latest verification: %w", err)
	}
	verification.ObservedAt, err = time.Parse(time.RFC3339Nano, observedAt)
	if err != nil {
		return VerificationRecord{}, fmt.Errorf("parse latest verification time: %w", err)
	}
	return verification, nil
}

func (store *Store) migrate(ctx context.Context) error {
	statements := []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA busy_timeout = 5000`,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`,
	}
	for _, statement := range statements {
		if _, err := store.database.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize launcher database: %w", err)
		}
	}

	migrations := []struct {
		version   int
		statement string
	}{
		{1, `CREATE TABLE profiles (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			language TEXT NOT NULL,
			deployment_mode TEXT NOT NULL,
			revision INTEGER NOT NULL,
			created_at TEXT NOT NULL
		)`},
		{2, `CREATE TABLE plans (
			id TEXT PRIMARY KEY,
			profile_id TEXT NOT NULL REFERENCES profiles(id),
			intent TEXT NOT NULL,
			digest TEXT NOT NULL,
			status TEXT NOT NULL,
			profile_revision INTEGER NOT NULL,
			created_at TEXT NOT NULL
		)`},
		{3, `CREATE TABLE runs (
			id TEXT PRIMARY KEY,
			plan_id TEXT NOT NULL REFERENCES plans(id),
			profile_id TEXT NOT NULL REFERENCES profiles(id),
			state TEXT NOT NULL,
			current_checkpoint TEXT NOT NULL,
			cancellation_state TEXT NOT NULL,
			verification_code TEXT NOT NULL,
			verification_observed_at TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`},
		{4, `CREATE TABLE events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			profile_id TEXT NOT NULL REFERENCES profiles(id),
			run_id TEXT NOT NULL REFERENCES runs(id),
			type TEXT NOT NULL,
			message_key TEXT NOT NULL,
			parameters TEXT NOT NULL,
			occurred_at TEXT NOT NULL
		)`},
	}
	for _, migration := range migrations {
		if err := store.applyMigration(ctx, migration.version, migration.statement); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) applyMigration(ctx context.Context, version int, statement string) error {
	var applied int
	if err := store.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&applied); err != nil {
		return fmt.Errorf("read schema version %d: %w", version, err)
	}
	if applied == 1 {
		return nil
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %d: %w", version, err)
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, statement); err != nil {
		return fmt.Errorf("apply migration %d: %w", version, err)
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, version, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("record migration %d: %w", version, err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit migration %d: %w", version, err)
	}
	return nil
}
