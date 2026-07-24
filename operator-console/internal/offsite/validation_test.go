package offsite

import "testing"

func TestValidationRemediationKeysAreDistinctPerOutcome(t *testing.T) {
	results := []ValidationResult{
		ValidationVerified,
		ValidationLocalBackupFailed,
		ValidationReplicationFailed,
		ValidationNoOffsiteEvidence,
		ValidationEvidenceStale,
		ValidationVersioningUnsupported,
	}
	seen := map[string]ValidationResult{}
	for _, result := range results {
		key := result.RemediationKey()
		if key == "" {
			t.Fatalf("%s has no remediation key", result)
		}
		if prior, ok := seen[key]; ok {
			t.Fatalf("%s and %s share remediation key %q — outcomes must stay distinguishable", prior, result, key)
		}
		seen[key] = result
		if result.Verified() != (result == ValidationVerified) {
			t.Fatalf("%s.Verified() misreports the fully-protected outcome", result)
		}
	}
}
