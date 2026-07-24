package offsite

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var validDestination = Destination{
	Endpoint: "https://s3.eu-central-003.backblazeb2.com",
	Region:   "eu-central-003",
	Bucket:   "community-backups",
}

var validCreds = Credentials{AccessKeyID: "003abcdef", SecretAccessKey: "K003topsecretvalue"}

func TestDestinationValidate(t *testing.T) {
	if err := validDestination.Validate(); err != nil {
		t.Fatalf("valid destination rejected: %v", err)
	}
	bad := []Destination{
		{Endpoint: "http://insecure.example", Region: "r", Bucket: "b-ucket"}, // not https
		{Endpoint: "https://s3.example", Region: "", Bucket: "b-ucket"},       // no region
		{Endpoint: "https://s3.example", Region: "r", Bucket: "Bad_Bucket"},   // bad bucket
		{Endpoint: "://broken", Region: "r", Bucket: "b-ucket"},               // unparseable
	}
	for _, destination := range bad {
		if err := destination.Validate(); !errors.Is(err, ErrInvalidDestination) {
			t.Errorf("Validate(%+v) = %v, want ErrInvalidDestination", destination, err)
		}
	}
}

func TestReferenceIsSecretFree(t *testing.T) {
	reference := NewReference(validDestination, "", "", validCreds.AccessKeyID, true)
	if reference.AccessKeyFingerprint == "" || reference.AccessKeyFingerprint == validCreds.AccessKeyID {
		t.Fatalf("access key not fingerprinted: %q", reference.AccessKeyFingerprint)
	}
	if reference.Schedule != DefaultSchedule || reference.SecretName != DefaultSecretName {
		t.Fatalf("defaults not applied: %+v", reference)
	}
	if reference.ConfigDigest == "" {
		t.Fatal("missing config digest")
	}
}

func TestVersioningAcknowledgement(t *testing.T) {
	tests := []struct {
		status  VersioningStatus
		wantAck bool
	}{
		{VersioningEnabled, false},
		{VersioningDisabled, true},
		{VersioningUnsupported, true},
		{VersioningUnknown, true},
	}
	for _, test := range tests {
		if got := (Inspection{Versioning: test.status}).RequiresAcknowledgement(); got != test.wantAck {
			t.Errorf("RequiresAcknowledgement(%s) = %v, want %v", test.status, got, test.wantAck)
		}
	}
}

func TestPlanRequiresAcknowledgementForUnverifiableVersioning(t *testing.T) {
	inspection := Inspection{Reachable: true, Versioning: VersioningUnsupported}
	if _, err := Plan(validDestination, "", "", validCreds.AccessKeyID, inspection, false); !errors.Is(err, ErrVersioningUnacknowledged) {
		t.Fatalf("err = %v, want ErrVersioningUnacknowledged", err)
	}
	if _, err := Plan(validDestination, "", "", validCreds.AccessKeyID, inspection, true); err != nil {
		t.Fatalf("acknowledged plan rejected: %v", err)
	}
}

func TestPlanSeparatesSecretFromGitDiff(t *testing.T) {
	inspection := Inspection{Reachable: true, Versioning: VersioningEnabled}
	plan, err := Plan(validDestination, "", "", validCreds.AccessKeyID, inspection, false)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Secret.SecretName != DefaultSecretName {
		t.Errorf("secret name = %q", plan.Secret.SecretName)
	}
	if len(plan.Secret.Keys) != 2 {
		t.Errorf("secret keys = %v, want two", plan.Secret.Keys)
	}
	// The non-secret Git diff describes the destination shape.
	for _, want := range []string{validDestination.Endpoint, validDestination.Region, validDestination.Bucket, DefaultSecretName} {
		if !strings.Contains(plan.GitDiff, want) {
			t.Errorf("git diff missing %q", want)
		}
	}
	if plan.Implications.Protection != ImplicationProtection {
		t.Errorf("protection implication = %q", plan.Implications.Protection)
	}
}

// TestGitDiffContainsNoSecret is the secret-scan: the non-secret Git diff must
// never contain the access key id or secret access key (criterion 7).
func TestGitDiffContainsNoSecret(t *testing.T) {
	inspection := Inspection{Reachable: true, Versioning: VersioningEnabled}
	plan, err := Plan(validDestination, "", "", validCreds.AccessKeyID, inspection, false)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if strings.Contains(plan.GitDiff, validCreds.AccessKeyID) {
		t.Fatal("git diff leaked the access key id")
	}
	if strings.Contains(plan.GitDiff, validCreds.SecretAccessKey) {
		t.Fatal("git diff leaked the secret access key")
	}
	// The reference persisted alongside must not carry the raw key either.
	if strings.Contains(plan.Reference.AccessKeyFingerprint, validCreds.AccessKeyID) {
		t.Fatal("reference leaked the access key id")
	}
}

func TestClassifyValidation(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	maxAge := 48 * time.Hour
	tests := []struct {
		name     string
		evidence ValidationEvidence
		want     ValidationResult
	}{
		{"local backup failed", ValidationEvidence{}, ValidationLocalBackupFailed},
		{
			name:     "replication failed",
			evidence: ValidationEvidence{LocalBackupSucceeded: true},
			want:     ValidationReplicationFailed,
		},
		{
			name:     "no offsite evidence despite success",
			evidence: ValidationEvidence{LocalBackupSucceeded: true, ReplicationSucceeded: true},
			want:     ValidationNoOffsiteEvidence,
		},
		{
			name: "offsite stale",
			evidence: ValidationEvidence{
				LocalBackupSucceeded:   true,
				ReplicationSucceeded:   true,
				OffsiteRecoveryPointAt: now.Add(-72 * time.Hour),
				Versioning:             VersioningEnabled,
			},
			want: ValidationEvidenceStale,
		},
		{
			name: "versioning unsupported",
			evidence: ValidationEvidence{
				LocalBackupSucceeded:   true,
				ReplicationSucceeded:   true,
				OffsiteRecoveryPointAt: now.Add(-time.Hour),
				Versioning:             VersioningUnsupported,
			},
			want: ValidationVersioningUnsupported,
		},
		{
			name: "verified",
			evidence: ValidationEvidence{
				LocalBackupSucceeded:   true,
				ReplicationSucceeded:   true,
				OffsiteRecoveryPointAt: now.Add(-time.Hour),
				Versioning:             VersioningEnabled,
			},
			want: ValidationVerified,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ClassifyValidation(test.evidence, now, maxAge); got != test.want {
				t.Fatalf("result = %q, want %q", got, test.want)
			}
		})
	}
}
