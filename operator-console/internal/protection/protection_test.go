package protection

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/assessment"
)

var now = time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)

var nextcloudDB = Dataset{ID: "nextcloud-db", Capability: "nextcloud", DataType: DataDatabase, Producer: ProducerCNPGBarman, Schedule: "daily 02:00", Retention: "7 days"}

// TestSameDiskGarageIsNotDisasterProtection is the core honesty test: a dataset
// with a fresh local (same-disk Garage) Recovery Point but no offsite Recovery
// Point is never reported as disaster protected (storage doc §3, criterion 7).
func TestSameDiskGarageIsNotDisasterProtection(t *testing.T) {
	facts := DatasetFacts{
		JobCompletedAt:       now.Add(-2 * time.Hour),
		LocalRecoveryPointAt: now.Add(-2 * time.Hour), // fresh, but in same-disk Garage
		OffsiteConfigured:    false,
	}
	got := Assess(nextcloudDB, facts, now, DefaultPolicy())
	if got.DisasterProtected {
		t.Fatal("local-only Garage data must not be disaster protected")
	}
	if got.Level != LevelLocalOnly {
		t.Fatalf("level = %q, want local-only", got.Level)
	}
}

func TestAssessLevels(t *testing.T) {
	tests := []struct {
		name         string
		facts        DatasetFacts
		wantLevel    ProtectionLevel
		wantDisaster bool
	}{
		{
			name:      "no recovery points",
			facts:     DatasetFacts{JobFailed: true},
			wantLevel: LevelNone,
		},
		{
			name:      "local only",
			facts:     DatasetFacts{LocalRecoveryPointAt: now.Add(-time.Hour)},
			wantLevel: LevelLocalOnly,
		},
		{
			name: "offsite present and fresh is protected",
			facts: DatasetFacts{
				LocalRecoveryPointAt:   now.Add(-time.Hour),
				OffsiteConfigured:      true,
				OffsiteRecoveryPointAt: now.Add(-3 * time.Hour),
			},
			wantLevel:    LevelProtected,
			wantDisaster: true,
		},
		{
			name: "offsite stale",
			facts: DatasetFacts{
				LocalRecoveryPointAt:   now.Add(-time.Hour),
				OffsiteConfigured:      true,
				OffsiteRecoveryPointAt: now.Add(-72 * time.Hour), // older than 48h policy
			},
			wantLevel:    LevelStale,
			wantDisaster: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Assess(nextcloudDB, test.facts, now, DefaultPolicy())
			if got.Level != test.wantLevel {
				t.Errorf("level = %q, want %q", got.Level, test.wantLevel)
			}
			if got.DisasterProtected != test.wantDisaster {
				t.Errorf("disasterProtected = %v, want %v", got.DisasterProtected, test.wantDisaster)
			}
		})
	}
}

// TestJobCompletionDistinctFromRecoveryPoint proves a succeeded producer Job with
// no Recovery Point is not treated as protection (criterion 3).
func TestJobCompletionDistinctFromRecoveryPoint(t *testing.T) {
	facts := DatasetFacts{JobCompletedAt: now.Add(-time.Hour)} // Job ran, but no restore point
	got := Assess(nextcloudDB, facts, now, DefaultPolicy())
	if got.Level != LevelNone || got.DisasterProtected {
		t.Fatalf("job completion alone reported as protection: %+v", got)
	}
	if got.JobCompletedAt.IsZero() {
		t.Fatal("job completion time should still be surfaced for display")
	}
}

type fakeSource struct {
	facts map[string]DatasetFacts
	fail  map[string]bool
}

func (s fakeSource) ObserveDataset(_ context.Context, dataset Dataset) (DatasetFacts, time.Time, error) {
	if s.fail[dataset.ID] {
		return DatasetFacts{}, time.Time{}, errors.New("read failed")
	}
	return s.facts[dataset.ID], now, nil
}

func TestInventoryReportUnknownOnReadFailure(t *testing.T) {
	inventory := Inventory{
		Datasets: []Dataset{nextcloudDB},
		Source:   fakeSource{fail: map[string]bool{"nextcloud-db": true}},
		Clock:    func() time.Time { return now },
	}
	report := inventory.Report(context.Background())
	if len(report) != 1 || report[0].Level != LevelUnknown || report[0].Observed {
		t.Fatalf("read failure not reported as unknown: %+v", report)
	}
}

func TestInventoryReportNilSourceIsUnknown(t *testing.T) {
	inventory := Inventory{Datasets: DefaultInventory(), Clock: func() time.Time { return now }}
	for _, item := range inventory.Report(context.Background()) {
		if item.Level != LevelUnknown {
			t.Fatalf("nil source produced non-unknown status: %+v", item)
		}
	}
}

// TestCapabilityEvidenceTwoHopChain proves the aggregation across a capability's
// datasets: a capability with a local-only dataset yields no offsite Recovery
// Point, which the assessment engine degrades.
func TestCapabilityEvidenceTwoHopChain(t *testing.T) {
	datasets := []Dataset{
		{ID: "nextcloud-db", Capability: "nextcloud", DataType: DataDatabase},
		{ID: "nextcloud-files", Capability: "nextcloud", DataType: DataFilesystem},
	}
	source := fakeSource{facts: map[string]DatasetFacts{
		"nextcloud-db": {
			LocalRecoveryPointAt:   now.Add(-2 * time.Hour),
			OffsiteConfigured:      true,
			OffsiteRecoveryPointAt: now.Add(-3 * time.Hour),
		},
		"nextcloud-files": {
			LocalRecoveryPointAt: now.Add(-time.Hour), // local only, no offsite
		},
	}}
	report := Inventory{Datasets: datasets, Source: source, Clock: func() time.Time { return now }}.Report(context.Background())

	evidence := CapabilityEvidence("nextcloud", report, now)
	if evidence.Missing {
		t.Fatal("evidence unexpectedly missing")
	}
	if !evidence.OffsiteRecoveryPointAt.IsZero() {
		t.Fatal("a local-only dataset must leave the capability without an offsite Recovery Point")
	}

	// Feed the aggregate into the assessment engine: the stateful capability must
	// degrade for lacking offsite disaster protection.
	result := assessment.Assess(assessment.Input{
		Capability:    assessment.CapabilityRef{ID: "nextcloud", Exposure: assessment.ExposurePrivate, Stateful: true},
		Now:           now,
		Freshness:     assessment.Freshness{Evidence: time.Hour, RecoveryPoint: 24 * time.Hour},
		Configuration: assessment.ConfigurationEvidence{Observation: assessment.Observation{At: now}, Selected: true, RequiredValuesMet: true, DeclaredInGit: true},
		Delivery:      assessment.DeliveryEvidence{Observation: assessment.Observation{At: now}, ArgoSynced: true, ArgoHealthy: true},
		Runtime:       assessment.RuntimeEvidence{Observation: assessment.Observation{At: now}, WorkloadsReady: true, PVCsBound: true, ProbesPassing: true},
		Access:        assessment.AccessEvidence{Observation: assessment.Observation{At: now}, DNSResolves: true, CertificateReady: true, GatewayReachable: true},
		Protection:    evidence,
	})
	if result.State != assessment.StateDegraded {
		t.Fatalf("state = %q, want degraded for missing offsite protection", result.State)
	}
	if result.ReasonCode != assessment.ReasonProtectionNoOffsiteRecoveryPoint {
		t.Fatalf("reason = %q, want no-offsite-recovery-point", result.ReasonCode)
	}
}

func TestDefaultInventoryIsComplete(t *testing.T) {
	inventory := DefaultInventory()
	if len(inventory) == 0 {
		t.Fatal("default inventory is empty")
	}
	for _, dataset := range inventory {
		if dataset.ID == "" || dataset.Capability == "" || dataset.DataType == "" || dataset.Producer == "" || dataset.Schedule == "" || dataset.Retention == "" {
			t.Fatalf("dataset missing declared metadata: %+v", dataset)
		}
	}
}
