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

// DefaultOffsiteMaxAge is how recent an offsite Recovery Point must be for a
// validation run to accept it. It spans slightly more than the nightly
// replication cadence so a single missed run reads as stale rather than absent.
const DefaultOffsiteMaxAge = 26 * time.Hour

// Verified reports whether the result is the one fully-protected outcome. Every
// other result is a distinguishable gap with its own remediation.
func (result ValidationResult) Verified() bool {
	return result == ValidationVerified
}

// RemediationKey maps a validation outcome to a stable, translatable remediation
// message key, keeping failed local backup, failed replication, missing/stale
// evidence, and unsupported versioning distinguishable so each gets the right
// next step.
func (result ValidationResult) RemediationKey() string {
	switch result {
	case ValidationVerified:
		return "offsite.remediation.none"
	case ValidationLocalBackupFailed:
		return "offsite.remediation.local_backup_failed"
	case ValidationReplicationFailed:
		return "offsite.remediation.replication_failed"
	case ValidationNoOffsiteEvidence:
		return "offsite.remediation.no_offsite_evidence"
	case ValidationEvidenceStale:
		return "offsite.remediation.evidence_stale"
	case ValidationVersioningUnsupported:
		return "offsite.remediation.versioning_unsupported"
	default:
		return "offsite.remediation.pending"
	}
}

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
