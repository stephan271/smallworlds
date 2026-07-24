package offsite

import "time"

// ValidationResult classifies the outcome of a bounded backup/replication
// validation run. Failed local backup, failed replication, stale observation,
// and unsupported versioning stay distinguishable so each gets relevant
// remediation.
type ValidationResult string

const (
	// ValidationPending means the run has not produced a verdict yet.
	ValidationPending ValidationResult = "pending"
	// ValidationLocalBackupFailed means the local producer did not complete, so
	// there is nothing to replicate.
	ValidationLocalBackupFailed ValidationResult = "local-backup-failed"
	// ValidationReplicationFailed means local data exists but the offsite sync
	// failed.
	ValidationReplicationFailed ValidationResult = "replication-failed"
	// ValidationNoOffsiteEvidence means the run completed but no offsite Recovery
	// Point could be observed — a passing Job exit is not accepted as proof.
	ValidationNoOffsiteEvidence ValidationResult = "no-offsite-evidence"
	// ValidationEvidenceStale means an offsite Recovery Point exists but is older
	// than the policy window.
	ValidationEvidenceStale ValidationResult = "offsite-evidence-stale"
	// ValidationVersioningUnsupported means replication verified but the
	// destination cannot provide point-in-time protection.
	ValidationVersioningUnsupported ValidationResult = "versioning-unsupported"
	// ValidationVerified means a fresh offsite Recovery Point was observed and the
	// destination supports versioning.
	ValidationVerified ValidationResult = "offsite-verified"
)

// ValidationEvidence is what a validation run observes. Job completion is kept
// separate from the offsite Recovery Point: the verdict is drawn from observed
// evidence, not from either Job's exit status.
type ValidationEvidence struct {
	LocalBackupSucceeded   bool
	ReplicationSucceeded   bool
	OffsiteRecoveryPointAt time.Time
	Versioning             VersioningStatus
}

// ClassifyValidation derives the validation verdict from observed evidence,
// checking the offsite Recovery Point rather than trusting Job exit status.
func ClassifyValidation(evidence ValidationEvidence, now time.Time, offsiteMaxAge time.Duration) ValidationResult {
	if !evidence.LocalBackupSucceeded {
		return ValidationLocalBackupFailed
	}
	if !evidence.ReplicationSucceeded {
		return ValidationReplicationFailed
	}
	if evidence.OffsiteRecoveryPointAt.IsZero() {
		return ValidationNoOffsiteEvidence
	}
	if offsiteMaxAge > 0 && now.Sub(evidence.OffsiteRecoveryPointAt) > offsiteMaxAge {
		return ValidationEvidenceStale
	}
	if evidence.Versioning != VersioningEnabled {
		return ValidationVersioningUnsupported
	}
	return ValidationVerified
}
