package protection

import (
	"context"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/assessment"
)

// DefaultInventory returns the declared protected datasets of the current
// repository backup chain (doc/storage-and-backup.md §4). Every dataset names
// its owning capability, data type, expected producer, schedule, and retention —
// so the console can explain what protection *should* exist before any evidence
// is collected.
func DefaultInventory() []Dataset {
	return []Dataset{
		{ID: "nextcloud-db", Capability: "nextcloud", DataType: DataDatabase, Producer: ProducerCNPGBarman, Schedule: "daily 02:00", Retention: "7 days"},
		{ID: "immich-db", Capability: "immich", DataType: DataDatabase, Producer: ProducerCNPGBarman, Schedule: "daily 02:00", Retention: "7 days"},
		{ID: "plane-db", Capability: "plane", DataType: DataDatabase, Producer: ProducerCNPGBarman, Schedule: "daily 02:00", Retention: "7 days"},
		{ID: "forgejo-db", Capability: "forgejo", DataType: DataDatabase, Producer: ProducerCNPGBarman, Schedule: "daily 02:00", Retention: "7 days"},
		{ID: "stalwart-db", Capability: "stalwart", DataType: DataDatabase, Producer: ProducerCNPGBarman, Schedule: "daily 02:00", Retention: "7 days"},
		{ID: "keycloak-db", Capability: "keycloak", DataType: DataDatabase, Producer: ProducerCNPGBarman, Schedule: "daily 03:00", Retention: "7 days"},
		{ID: "cluster-resources", Capability: "velero", DataType: DataClusterResources, Producer: ProducerVelero, Schedule: "daily 02:00", Retention: "30 days"},
		{ID: "immich-library", Capability: "immich", DataType: DataFilesystem, Producer: ProducerPVBackupRclone, Schedule: "daily 00:30", Retention: "offsite versioning"},
		{ID: "forgejo-data", Capability: "forgejo", DataType: DataFilesystem, Producer: ProducerPVBackupRclone, Schedule: "daily 00:45", Retention: "offsite versioning"},
		{ID: "nextcloud-files", Capability: "nextcloud", DataType: DataFilesystem, Producer: ProducerPVBackupRclone, Schedule: "daily 01:00", Retention: "offsite versioning"},
	}
}

// Source observes a dataset's protection evidence from live resources (CNPG
// ScheduledBackups, Velero backups, pv-backup CronJobs, Garage objects, and the
// offsite replicator). It returns the facts, the time they were refreshed, and
// an error when the read failed. It is the seam where the production readers plug
// in; observers gather evidence and never decide status.
type Source interface {
	ObserveDataset(ctx context.Context, dataset Dataset) (DatasetFacts, time.Time, error)
}

// Inventory reports the protection status of a set of datasets by reading each
// through a Source and deriving its status under a Policy.
type Inventory struct {
	Datasets []Dataset
	Source   Source
	Policy   Policy
	Clock    func() time.Time
}

func (inventory Inventory) policy() Policy {
	if inventory.Policy.LocalMaxAge == 0 && inventory.Policy.OffsiteMaxAge == 0 {
		return DefaultPolicy()
	}
	return inventory.Policy
}

func (inventory Inventory) now() time.Time {
	if inventory.Clock != nil {
		return inventory.Clock()
	}
	return time.Now().UTC()
}

// Report returns the derived protection status of every dataset. A dataset whose
// Source read fails, or for which no Source is wired, is reported as unknown —
// never as protected.
func (inventory Inventory) Report(ctx context.Context) []DatasetProtection {
	policy := inventory.policy()
	results := make([]DatasetProtection, 0, len(inventory.Datasets))
	for _, dataset := range inventory.Datasets {
		if inventory.Source == nil {
			results = append(results, unknown(dataset))
			continue
		}
		facts, observedAt, err := inventory.Source.ObserveDataset(ctx, dataset)
		if err != nil {
			results = append(results, unknown(dataset))
			continue
		}
		results = append(results, Assess(dataset, facts, observedAt, policy))
	}
	return results
}

// CapabilityEvidence aggregates a capability's dataset protection into the
// assessment.ProtectionEvidence the assessment engine consumes, so stale or
// absent protection degrades a stateful capability. The aggregation takes the
// worst case across datasets: coverage requires every dataset to have a local
// Recovery Point, and the offsite Recovery Point is the oldest across datasets —
// zero if any dataset lacks one, which drives the "no offsite Recovery Point"
// degradation for the whole capability.
func CapabilityEvidence(capabilityID string, report []DatasetProtection, now time.Time) assessment.ProtectionEvidence {
	var owned []DatasetProtection
	for _, item := range report {
		if item.Dataset.Capability == capabilityID {
			owned = append(owned, item)
		}
	}
	if len(owned) == 0 {
		return assessment.ProtectionEvidence{Observation: assessment.Observation{Missing: true}}
	}

	evidence := assessment.ProtectionEvidence{
		Observation:        assessment.Observation{At: now},
		DatasetsCovered:    true,
		RetentionSatisfied: true,
	}
	var localMin, offsiteMin time.Time
	localMissing, offsiteMissing := false, false
	for _, item := range owned {
		if !item.Observed {
			return assessment.ProtectionEvidence{Observation: assessment.Observation{Missing: true}}
		}
		if item.RetentionBreached {
			evidence.RetentionSatisfied = false
		}
		if item.LocalRecoveryPointAt.IsZero() {
			localMissing = true
			evidence.DatasetsCovered = false
		} else {
			localMin = earlier(localMin, item.LocalRecoveryPointAt)
		}
		if item.OffsiteRecoveryPointAt.IsZero() {
			offsiteMissing = true
		} else {
			offsiteMin = earlier(offsiteMin, item.OffsiteRecoveryPointAt)
		}
		if item.RestoreDrillAt.After(evidence.RestoreDrillAt) {
			evidence.RestoreDrillAt = item.RestoreDrillAt
		}
		if !item.ObservedAt.IsZero() && item.ObservedAt.Before(evidence.Observation.At) {
			evidence.Observation.At = item.ObservedAt
		}
	}
	// A single missing Recovery Point across the capability's datasets leaves the
	// aggregate zero (worst case), driving the assessment engine's degradation.
	if !localMissing {
		evidence.LocalRecoveryPointAt = localMin
	}
	if !offsiteMissing {
		evidence.OffsiteRecoveryPointAt = offsiteMin
	}
	return evidence
}

// earlier returns the earlier of two times; a zero current means "unset", so the
// first non-zero candidate wins.
func earlier(current, candidate time.Time) time.Time {
	if current.IsZero() || candidate.Before(current) {
		return candidate
	}
	return current
}
