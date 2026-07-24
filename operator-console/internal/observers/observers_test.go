package observers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/assessment"
)

var now = time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)

type fakeConfig struct {
	facts ConfigurationFacts
	at    time.Time
	err   error
}

func (f fakeConfig) ObserveConfiguration(context.Context, string) (ConfigurationFacts, time.Time, error) {
	return f.facts, f.at, f.err
}

type fakeDelivery struct {
	facts DeliveryFacts
	at    time.Time
	err   error
}

func (f fakeDelivery) ObserveDelivery(context.Context, string) (DeliveryFacts, time.Time, error) {
	return f.facts, f.at, f.err
}

type fakeRuntime struct {
	facts RuntimeFacts
	at    time.Time
	err   error
}

func (f fakeRuntime) ObserveRuntime(context.Context, string) (RuntimeFacts, time.Time, error) {
	return f.facts, f.at, f.err
}

type fakeAccess struct {
	facts AccessFacts
	at    time.Time
	err   error
}

func (f fakeAccess) ObserveAccess(context.Context, string) (AccessFacts, time.Time, error) {
	return f.facts, f.at, f.err
}

type fakeProtection struct {
	facts ProtectionFacts
	at    time.Time
	err   error
}

func (f fakeProtection) ObserveProtection(context.Context, string) (ProtectionFacts, time.Time, error) {
	return f.facts, f.at, f.err
}

func healthyRef() assessment.CapabilityRef {
	return assessment.CapabilityRef{ID: "nextcloud", Exposure: assessment.ExposurePrivate, Stateful: true}
}

// healthyGatherer wires deterministic sources that all report a fresh, healthy
// private stateful capability.
func healthyGatherer() Gatherer {
	return Gatherer{
		Configuration: fakeConfig{at: now, facts: ConfigurationFacts{Selected: true, RequiredValuesMet: true, DeclaredInGit: true}},
		Delivery:      fakeDelivery{at: now, facts: DeliveryFacts{ApplicationFound: true, SyncStatus: SyncSynced, HealthStatus: HealthHealthy, OperationPhase: PhaseSucceeded, LastReconciledAt: now}},
		Runtime:       fakeRuntime{at: now, facts: RuntimeFacts{WorkloadsFound: true, AllWorkloadsReady: true, AllPVCsBound: true, ProbesPassing: true}},
		Access:        fakeAccess{at: now, facts: AccessFacts{DNSResolves: true, CertificateReady: true, GatewayReachable: true}},
		Protection:    fakeProtection{at: now, facts: ProtectionFacts{DatasetsCovered: true, LocalRecoveryPointAt: now.Add(-time.Hour), OffsiteRecoveryPointAt: now.Add(-2 * time.Hour), RetentionSatisfied: true}},
		Freshness:     assessment.Freshness{Evidence: time.Hour, RecoveryPoint: 24 * time.Hour},
		Clock:         func() time.Time { return now },
	}
}

func TestGatherHealthy(t *testing.T) {
	got := healthyGatherer().Assess(context.Background(), healthyRef())
	if got.State != assessment.StateHealthy {
		t.Fatalf("state = %q, want healthy", got.State)
	}
	if !got.ObservedAt.Equal(now) {
		t.Errorf("observedAt = %v, want %v", got.ObservedAt, now)
	}
}

// TestNilSourceIsUnknownNotHealthy proves an unwired observer yields Missing
// evidence, so the capability can never falsely read healthy.
func TestNilSourceIsUnknownNotHealthy(t *testing.T) {
	gatherer := healthyGatherer()
	gatherer.Runtime = nil

	input := gatherer.Gather(context.Background(), healthyRef())
	if !input.Runtime.Missing {
		t.Fatalf("nil runtime source did not produce Missing evidence")
	}
	if got := assessment.Assess(input); got.State == assessment.StateHealthy {
		t.Fatalf("state = healthy, want not healthy for unknown runtime")
	}
}

// TestReadErrorIsMissing proves a source that fails to read becomes Missing
// (unknown), not a fabricated value.
func TestReadErrorIsMissing(t *testing.T) {
	gatherer := healthyGatherer()
	gatherer.Delivery = fakeDelivery{err: errors.New("argo api unreachable")}

	input := gatherer.Gather(context.Background(), healthyRef())
	if !input.Delivery.Missing {
		t.Fatalf("delivery read error did not produce Missing evidence")
	}
	if got := assessment.Assess(input); got.State == assessment.StateHealthy {
		t.Fatalf("state = healthy, want not healthy for unknown delivery")
	}
}

// TestApplicationNotFoundIsPending proves a capability declared in Git but not
// yet reconciled by Argo reads as planned, not degraded or healthy.
func TestApplicationNotFoundIsPending(t *testing.T) {
	gatherer := healthyGatherer()
	gatherer.Delivery = fakeDelivery{at: now, facts: DeliveryFacts{ApplicationFound: false}}
	gatherer.Runtime = fakeRuntime{at: now, facts: RuntimeFacts{}} // no workloads yet
	gatherer.Access = fakeAccess{at: now, facts: AccessFacts{}}
	gatherer.Protection = fakeProtection{at: now, facts: ProtectionFacts{}}

	input := gatherer.Gather(context.Background(), healthyRef())
	if input.Delivery.Missing {
		t.Fatalf("not-found application must be observed, not Missing")
	}
	if got := assessment.Assess(input).State; got != assessment.StatePlanned {
		t.Fatalf("state = %q, want planned", got)
	}
}

// TestDegradedHealthIsFailure proves Argo Degraded health translates to a
// delivery failure.
func TestDegradedHealthIsFailure(t *testing.T) {
	gatherer := healthyGatherer()
	gatherer.Delivery = fakeDelivery{at: now, facts: DeliveryFacts{ApplicationFound: true, SyncStatus: SyncSynced, HealthStatus: HealthDegraded}}

	got := gatherer.Assess(context.Background(), healthyRef())
	if got.State != assessment.StateFailed {
		t.Fatalf("state = %q, want failed", got.State)
	}
	if got.ReasonCode != assessment.ReasonDeliveryFailed {
		t.Fatalf("reason = %q, want %q", got.ReasonCode, assessment.ReasonDeliveryFailed)
	}
}

// TestOutOfSyncIsDrift proves an out-of-sync Application degrades the capability
// and routes to a Git proposal.
func TestOutOfSyncIsDrift(t *testing.T) {
	gatherer := healthyGatherer()
	gatherer.Delivery = fakeDelivery{at: now, facts: DeliveryFacts{ApplicationFound: true, SyncStatus: SyncOutOfSync, HealthStatus: HealthHealthy}}

	got := gatherer.Assess(context.Background(), healthyRef())
	if got.State != assessment.StateDegraded {
		t.Fatalf("state = %q, want degraded", got.State)
	}
	if got.ReasonCode != assessment.ReasonDeliveryDrifted {
		t.Fatalf("reason = %q, want %q", got.ReasonCode, assessment.ReasonDeliveryDrifted)
	}
}

// TestUnknownHealthProgressesNotHealthy proves a synced Application whose health
// is still Unknown reads as installing, never healthy.
func TestUnknownHealthProgressesNotHealthy(t *testing.T) {
	gatherer := healthyGatherer()
	gatherer.Delivery = fakeDelivery{at: now, facts: DeliveryFacts{ApplicationFound: true, SyncStatus: SyncSynced, HealthStatus: HealthUnknown}}

	got := gatherer.Assess(context.Background(), healthyRef())
	if got.State == assessment.StateHealthy {
		t.Fatalf("state = healthy, want not healthy while delivery health is unknown")
	}
	if got.State != assessment.StateInstalling {
		t.Fatalf("state = %q, want installing", got.State)
	}
}

// TestStaleEvidenceIsNotHealthy proves an observation older than the freshness
// budget keeps a capability from reading healthy.
func TestStaleEvidenceIsNotHealthy(t *testing.T) {
	gatherer := healthyGatherer()
	gatherer.Runtime = fakeRuntime{at: now.Add(-2 * time.Hour), facts: RuntimeFacts{WorkloadsFound: true, AllWorkloadsReady: true, AllPVCsBound: true, ProbesPassing: true}}

	input := gatherer.Gather(context.Background(), healthyRef())
	if !input.Runtime.At.Equal(now.Add(-2 * time.Hour)) {
		t.Fatalf("runtime observedAt = %v, want the source's refresh time", input.Runtime.At)
	}
	if got := assessment.Assess(input).State; got == assessment.StateHealthy {
		t.Fatalf("state = healthy, want not healthy for stale runtime evidence")
	}
}

func TestTranslatorsMissingOnError(t *testing.T) {
	readErr := errors.New("read failed")
	if ev := configurationEvidence(ConfigurationFacts{}, now, readErr); !ev.Missing {
		t.Error("configurationEvidence not Missing on error")
	}
	if ev := deliveryEvidence(DeliveryFacts{}, now, readErr); !ev.Missing {
		t.Error("deliveryEvidence not Missing on error")
	}
	if ev := runtimeEvidence(RuntimeFacts{}, now, readErr); !ev.Missing {
		t.Error("runtimeEvidence not Missing on error")
	}
	if ev := accessEvidence(AccessFacts{}, now, readErr); !ev.Missing {
		t.Error("accessEvidence not Missing on error")
	}
	if ev := protectionEvidence(ProtectionFacts{}, now, readErr); !ev.Missing {
		t.Error("protectionEvidence not Missing on error")
	}
}
