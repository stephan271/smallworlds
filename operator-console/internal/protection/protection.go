// Package protection is the Operator Console's dataset protection inventory. It
// models the repository's real two-hop backup chain — app data → in-cluster
// Garage S3 → offsite mirror (doc/storage-and-backup.md) — and derives, for each
// declared dataset, an honest Protection Status.
//
// The central honesty rule (storage doc §3): Garage is a staging tier, not a
// backup tier. It runs replicationFactor 1 on the *same volume* as the primary
// data, so a local Recovery Point in Garage adds no physical redundancy — a disk
// failure destroys the primary and the local copy together. Only a fresh offsite
// Recovery Point is disaster protection. This package therefore distinguishes,
// per dataset, three separate facts that a naive view conflates: the producer
// Job completing, a local (same-disk) Recovery Point existing, and an offsite
// Recovery Point existing. Observers gather these facts; this package decides the
// status (the ADR-0020 split, applied to protection).
package protection

import "time"

// DataType classifies what a dataset holds.
type DataType string

const (
	DataDatabase         DataType = "database"
	DataFilesystem       DataType = "filesystem"
	DataObjectStore      DataType = "object-store"
	DataClusterResources DataType = "cluster-resources"
)

// Producer is the mechanism expected to produce a dataset's local Recovery Point.
type Producer string

const (
	ProducerCNPGBarman Producer = "cnpg-barman"
	ProducerVelero     Producer = "velero"
	// A filesystem copied into a bucket: the tenant's PVC, mounted read-only.
	ProducerPVBackupRclone Producer = "pv-backup-rclone"
	// A bucket copied into another bucket, keeping superseded objects under a
	// dated prefix rather than overwriting them.
	ProducerBucketRclone Producer = "bucket-rclone"
	// The append-only pod archive. Distinct from the rclone producers in a way
	// that matters to anyone reading a protection report: nothing here can
	// overwrite or delete what a pod already holds, and nothing carries it
	// offsite either.
	ProducerPodArchiveExport Producer = "pod-archive-export"
)

// Dataset is a declared protected dataset with its owning capability and the
// producer/schedule/retention the operator can expect.
type Dataset struct {
	ID         string   `json:"id"`
	Capability string   `json:"capability"`
	DataType   DataType `json:"dataType"`
	Producer   Producer `json:"producer"`
	Schedule   string   `json:"schedule"`
	Retention  string   `json:"retention"`
}

// DatasetFacts is the evidence an observer collects for one dataset. Job
// completion is deliberately separate from a Recovery Point: a producer Job can
// succeed without a usable restore point, and a Recovery Point can exist from an
// earlier run after a later Job fails.
type DatasetFacts struct {
	JobCompletedAt         time.Time
	JobFailed              bool
	LocalRecoveryPointAt   time.Time
	OffsiteConfigured      bool
	OffsiteRecoveryPointAt time.Time
	RetentionBreached      bool
	RestoreDrillAt         time.Time
	RestoreDrillPassed     bool
}

// Policy bounds how old a Recovery Point may be before it is stale.
type Policy struct {
	LocalMaxAge   time.Duration
	OffsiteMaxAge time.Duration
}

// DefaultPolicy allows two daily cycles before a Recovery Point is stale.
func DefaultPolicy() Policy {
	return Policy{LocalMaxAge: 48 * time.Hour, OffsiteMaxAge: 48 * time.Hour}
}

// ProtectionLevel is the headline protection status of a dataset.
type ProtectionLevel string

const (
	// LevelUnknown means no evidence could be collected.
	LevelUnknown ProtectionLevel = "unknown"
	// LevelNone means no Recovery Point exists at all.
	LevelNone ProtectionLevel = "none"
	// LevelLocalOnly means a local (same-disk Garage) Recovery Point exists but
	// there is no offsite Recovery Point — explicitly NOT disaster protection.
	LevelLocalOnly ProtectionLevel = "local-only"
	// LevelStale means an offsite Recovery Point exists but is older than policy.
	LevelStale ProtectionLevel = "stale"
	// LevelProtected means a fresh offsite Recovery Point exists.
	LevelProtected ProtectionLevel = "protected"
)

// DatasetProtection is the derived, display-ready protection status of a dataset,
// keeping every distinct fact visible so the UI never conflates them.
type DatasetProtection struct {
	Dataset                   Dataset         `json:"dataset"`
	Observed                  bool            `json:"observed"`
	ObservedAt                time.Time       `json:"observedAt,omitempty"`
	JobCompletedAt            time.Time       `json:"jobCompletedAt,omitempty"`
	JobFailed                 bool            `json:"jobFailed"`
	LocalRecoveryPointAt      time.Time       `json:"localRecoveryPointAt,omitempty"`
	LocalRecoveryPointStale   bool            `json:"localRecoveryPointStale"`
	OffsiteConfigured         bool            `json:"offsiteConfigured"`
	OffsiteRecoveryPointAt    time.Time       `json:"offsiteRecoveryPointAt,omitempty"`
	OffsiteRecoveryPointStale bool            `json:"offsiteRecoveryPointStale"`
	RetentionBreached         bool            `json:"retentionBreached"`
	RestoreDrillAt            time.Time       `json:"restoreDrillAt,omitempty"`
	RestoreDrillPassed        bool            `json:"restoreDrillPassed"`
	Level                     ProtectionLevel `json:"level"`
	// DisasterProtected is the honest bottom line: true only when a fresh offsite
	// Recovery Point exists. A local-only (same-disk Garage) Recovery Point is
	// never disaster protection.
	DisasterProtected bool `json:"disasterProtected"`
}

// Assess derives a dataset's protection status from observed facts. It assumes
// the facts were collected; a dataset whose observation failed is reported as
// unknown by the Inventory gatherer, not here.
func Assess(dataset Dataset, facts DatasetFacts, observedAt time.Time, policy Policy) DatasetProtection {
	localStale := !facts.LocalRecoveryPointAt.IsZero() && observedAt.Sub(facts.LocalRecoveryPointAt) > policy.LocalMaxAge
	offsiteStale := !facts.OffsiteRecoveryPointAt.IsZero() && observedAt.Sub(facts.OffsiteRecoveryPointAt) > policy.OffsiteMaxAge
	hasOffsite := facts.OffsiteConfigured && !facts.OffsiteRecoveryPointAt.IsZero()

	result := DatasetProtection{
		Dataset:                   dataset,
		Observed:                  true,
		ObservedAt:                observedAt,
		JobCompletedAt:            facts.JobCompletedAt,
		JobFailed:                 facts.JobFailed,
		LocalRecoveryPointAt:      facts.LocalRecoveryPointAt,
		LocalRecoveryPointStale:   localStale,
		OffsiteConfigured:         facts.OffsiteConfigured,
		OffsiteRecoveryPointAt:    facts.OffsiteRecoveryPointAt,
		OffsiteRecoveryPointStale: offsiteStale,
		RetentionBreached:         facts.RetentionBreached,
		RestoreDrillAt:            facts.RestoreDrillAt,
		RestoreDrillPassed:        facts.RestoreDrillPassed,
		DisasterProtected:         hasOffsite && !offsiteStale,
	}

	switch {
	case facts.LocalRecoveryPointAt.IsZero() && facts.OffsiteRecoveryPointAt.IsZero():
		result.Level = LevelNone
	case !hasOffsite:
		// A local Recovery Point exists, but it sits on the same disk as the
		// primary data: not disaster protection.
		result.Level = LevelLocalOnly
	case offsiteStale:
		result.Level = LevelStale
	default:
		result.Level = LevelProtected
	}
	return result
}

// unknown builds the protection status for a dataset whose observation failed.
func unknown(dataset Dataset) DatasetProtection {
	return DatasetProtection{Dataset: dataset, Level: LevelUnknown}
}
