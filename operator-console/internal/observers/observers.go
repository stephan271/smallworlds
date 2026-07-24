// Package observers is the evidence-gathering seam of the in-cluster Operator
// Console. Observers read live cluster state — Argo CD delivery, Kubernetes
// runtime, expected access, and backup protection — and translate it into the
// facet evidence the pure assessment engine judges. Per ADR 0020 the split is
// strict: observers gather evidence and never decide presentation state; the
// assessment package owns every truth-table.
//
// This package defines provider-neutral raw fact structs, the source interfaces
// where production cluster readers plug in, and the pure translators from facts
// to assessment evidence — including the two distinctions that keep the headline
// honest: a source that cannot read is Missing evidence (unknown, never healthy),
// while a resource that simply does not exist yet is Pending (declared, awaiting
// delivery). A Gatherer composes the five sources for one capability into an
// assessment.Input and runs the engine.
//
// The production readers themselves (raw-HTTP Kubernetes/Argo CD reads, DNS/TLS
// reachability probes) land in a follow-up, mirroring how the live handoff
// verifier followed the pure verification core; tests here inject deterministic
// sources.
package observers

import (
	"context"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/assessment"
)

// Argo CD status vocabulary, matching the values Argo reports on an Application.
const (
	SyncSynced    = "Synced"
	SyncOutOfSync = "OutOfSync"

	HealthHealthy     = "Healthy"
	HealthProgressing = "Progressing"
	HealthDegraded    = "Degraded"
	HealthMissing     = "Missing"
	HealthUnknown     = "Unknown"
	HealthSuspended   = "Suspended"

	PhaseRunning   = "Running"
	PhaseSucceeded = "Succeeded"
	PhaseFailed    = "Failed"
	PhaseError     = "Error"
)

// ConfigurationFacts is the observed configuration of a capability: whether it
// is selected, its required values are present, which declared dependencies are
// unmet, and whether it is declared in the GitOps Overlay.
type ConfigurationFacts struct {
	Selected          bool
	RequiredValuesMet bool
	UnmetDependencies []string
	DeclaredInGit     bool
}

// DeliveryFacts is the observed Argo CD state for a capability's Application.
// ApplicationFound is false when Argo has no Application for the capability yet
// (declared in Git but not reconciled), which the translator maps to Pending.
type DeliveryFacts struct {
	ApplicationFound bool
	SyncStatus       string
	HealthStatus     string
	OperationPhase   string
	LastReconciledAt time.Time
}

// RuntimeFacts is the observed Kubernetes runtime for a capability's workloads.
type RuntimeFacts struct {
	WorkloadsFound      bool
	AllWorkloadsReady   bool
	AnyWorkloadStarting bool
	FailedJobs          int
	AllPVCsBound        bool
	ProbesPassing       bool
}

// AccessFacts is the observed reachability for a capability's endpoint. The
// assessment interprets these against the capability's declared exposure.
type AccessFacts struct {
	DNSResolves      bool
	CertificateReady bool
	PublicReachable  bool
	GatewayReachable bool
}

// ProtectionFacts is the observed backup protection for a capability's datasets.
type ProtectionFacts struct {
	DatasetsCovered        bool
	LocalRecoveryPointAt   time.Time
	OffsiteRecoveryPointAt time.Time
	RetentionSatisfied     bool
	RestoreDrillAt         time.Time
}

// The source interfaces are the seam where production cluster readers plug in.
// Each returns the observed facts plus the time the reader last refreshed them
// (so the assessment can judge staleness) and an error when the read failed,
// which the translators turn into Missing (unknown) evidence.

// ConfigurationSource observes a capability's configuration state.
type ConfigurationSource interface {
	ObserveConfiguration(ctx context.Context, capabilityID string) (ConfigurationFacts, time.Time, error)
}

// DeliverySource observes a capability's Argo CD Application.
type DeliverySource interface {
	ObserveDelivery(ctx context.Context, capabilityID string) (DeliveryFacts, time.Time, error)
}

// RuntimeSource observes a capability's Kubernetes workloads.
type RuntimeSource interface {
	ObserveRuntime(ctx context.Context, capabilityID string) (RuntimeFacts, time.Time, error)
}

// AccessSource observes a capability's endpoint reachability.
type AccessSource interface {
	ObserveAccess(ctx context.Context, capabilityID string) (AccessFacts, time.Time, error)
}

// ProtectionSource observes a capability's backup protection.
type ProtectionSource interface {
	ObserveProtection(ctx context.Context, capabilityID string) (ProtectionFacts, time.Time, error)
}

// configurationEvidence translates configuration facts into assessment evidence.
func configurationEvidence(facts ConfigurationFacts, observedAt time.Time, err error) assessment.ConfigurationEvidence {
	if err != nil {
		return assessment.ConfigurationEvidence{Observation: assessment.Observation{Missing: true}}
	}
	return assessment.ConfigurationEvidence{
		Observation:       assessment.Observation{At: observedAt},
		Selected:          facts.Selected,
		RequiredValuesMet: facts.RequiredValuesMet,
		UnmetDependencies: facts.UnmetDependencies,
		DeclaredInGit:     facts.DeclaredInGit,
	}
}

// deliveryEvidence translates Argo CD facts into assessment evidence. A read
// error is Missing; an Application that does not exist yet is Pending (all flags
// false, which the engine reads as awaiting delivery); Degraded health or a
// failed/errored operation is a delivery failure; and Progressing, Missing, or
// Unknown health while otherwise synced keeps the capability from reading
// healthy until the runtime facet confirms readiness.
func deliveryEvidence(facts DeliveryFacts, observedAt time.Time, err error) assessment.DeliveryEvidence {
	if err != nil {
		return assessment.DeliveryEvidence{Observation: assessment.Observation{Missing: true}}
	}
	if !facts.ApplicationFound {
		return assessment.DeliveryEvidence{Observation: assessment.Observation{At: observedAt}}
	}
	return assessment.DeliveryEvidence{
		Observation:      assessment.Observation{At: observedAt},
		ArgoSynced:       facts.SyncStatus == SyncSynced,
		ArgoHealthy:      facts.HealthStatus == HealthHealthy,
		Progressing:      isDeliveryProgressing(facts),
		Drifted:          facts.SyncStatus == SyncOutOfSync,
		Failed:           facts.HealthStatus == HealthDegraded || facts.OperationPhase == PhaseFailed || facts.OperationPhase == PhaseError,
		LastReconciledAt: facts.LastReconciledAt,
	}
}

func isDeliveryProgressing(facts DeliveryFacts) bool {
	switch facts.HealthStatus {
	case HealthProgressing, HealthMissing, HealthUnknown:
		return true
	}
	return facts.OperationPhase == PhaseRunning
}

// runtimeEvidence translates Kubernetes runtime facts into assessment evidence.
// Workloads that have not appeared yet are reported not ready rather than
// fabricated as ready.
func runtimeEvidence(facts RuntimeFacts, observedAt time.Time, err error) assessment.RuntimeEvidence {
	if err != nil {
		return assessment.RuntimeEvidence{Observation: assessment.Observation{Missing: true}}
	}
	return assessment.RuntimeEvidence{
		Observation:    assessment.Observation{At: observedAt},
		WorkloadsReady: facts.WorkloadsFound && facts.AllWorkloadsReady,
		Starting:       facts.AnyWorkloadStarting,
		FailedJobs:     facts.FailedJobs,
		PVCsBound:      facts.AllPVCsBound,
		ProbesPassing:  facts.ProbesPassing,
	}
}

// accessEvidence translates reachability facts into assessment evidence.
func accessEvidence(facts AccessFacts, observedAt time.Time, err error) assessment.AccessEvidence {
	if err != nil {
		return assessment.AccessEvidence{Observation: assessment.Observation{Missing: true}}
	}
	return assessment.AccessEvidence{
		Observation:      assessment.Observation{At: observedAt},
		DNSResolves:      facts.DNSResolves,
		CertificateReady: facts.CertificateReady,
		PublicReachable:  facts.PublicReachable,
		GatewayReachable: facts.GatewayReachable,
	}
}

// protectionEvidence translates backup protection facts into assessment evidence.
func protectionEvidence(facts ProtectionFacts, observedAt time.Time, err error) assessment.ProtectionEvidence {
	if err != nil {
		return assessment.ProtectionEvidence{Observation: assessment.Observation{Missing: true}}
	}
	return assessment.ProtectionEvidence{
		Observation:            assessment.Observation{At: observedAt},
		DatasetsCovered:        facts.DatasetsCovered,
		LocalRecoveryPointAt:   facts.LocalRecoveryPointAt,
		OffsiteRecoveryPointAt: facts.OffsiteRecoveryPointAt,
		RetentionSatisfied:     facts.RetentionSatisfied,
		RestoreDrillAt:         facts.RestoreDrillAt,
	}
}

// Gatherer composes the five evidence sources for capabilities into a complete
// assessment.Input. Any source left nil yields Missing (unknown) evidence for
// that facet, so a console that has not wired an observer never reports a false
// healthy. Clock supplies the assessment's "now"; when nil, time.Now is used.
type Gatherer struct {
	Configuration ConfigurationSource
	Delivery      DeliverySource
	Runtime       RuntimeSource
	Access        AccessSource
	Protection    ProtectionSource
	Freshness     assessment.Freshness
	Clock         func() time.Time
}

func (gatherer Gatherer) now() time.Time {
	if gatherer.Clock != nil {
		return gatherer.Clock()
	}
	return time.Now().UTC()
}

// Gather reads every wired source for the capability and assembles the evidence
// into an assessment.Input. It does not decide state; call assessment.Assess (or
// Gatherer.Assess) on the result.
func (gatherer Gatherer) Gather(ctx context.Context, ref assessment.CapabilityRef) assessment.Input {
	input := assessment.Input{
		Capability: ref,
		Now:        gatherer.now(),
		Freshness:  gatherer.Freshness,
	}

	if gatherer.Configuration != nil {
		facts, at, err := gatherer.Configuration.ObserveConfiguration(ctx, ref.ID)
		input.Configuration = configurationEvidence(facts, at, err)
	} else {
		input.Configuration = assessment.ConfigurationEvidence{Observation: assessment.Observation{Missing: true}}
	}
	if gatherer.Delivery != nil {
		facts, at, err := gatherer.Delivery.ObserveDelivery(ctx, ref.ID)
		input.Delivery = deliveryEvidence(facts, at, err)
	} else {
		input.Delivery = assessment.DeliveryEvidence{Observation: assessment.Observation{Missing: true}}
	}
	if gatherer.Runtime != nil {
		facts, at, err := gatherer.Runtime.ObserveRuntime(ctx, ref.ID)
		input.Runtime = runtimeEvidence(facts, at, err)
	} else {
		input.Runtime = assessment.RuntimeEvidence{Observation: assessment.Observation{Missing: true}}
	}
	if gatherer.Access != nil {
		facts, at, err := gatherer.Access.ObserveAccess(ctx, ref.ID)
		input.Access = accessEvidence(facts, at, err)
	} else {
		input.Access = assessment.AccessEvidence{Observation: assessment.Observation{Missing: true}}
	}
	if gatherer.Protection != nil {
		facts, at, err := gatherer.Protection.ObserveProtection(ctx, ref.ID)
		input.Protection = protectionEvidence(facts, at, err)
	} else {
		input.Protection = assessment.ProtectionEvidence{Observation: assessment.Observation{Missing: true}}
	}
	return input
}

// Assess gathers evidence for the capability and runs the assessment engine.
func (gatherer Gatherer) Assess(ctx context.Context, ref assessment.CapabilityRef) assessment.CapabilityAssessment {
	return assessment.Assess(gatherer.Gather(ctx, ref))
}
