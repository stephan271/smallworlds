// Package assessment implements the pure Capability Assessment engine of the
// in-cluster Operator Console. Given evidence gathered by observers, it derives
// one headline Capability State for a Cluster Capability from five facets —
// configuration, delivery, runtime, access, and protection — and retains the
// underlying reason codes, evidence timestamps, staleness, and one remediation
// route per non-satisfied facet.
//
// The engine is deliberately pure and table-tested (ADR 0020): observers gather
// evidence; the engine decides presentation state. It holds no clock, network,
// or Kubernetes handle. Four invariants shape the rules:
//
//   - An Argo CD "Healthy" value, or a ready workload, is evidence — never
//     sufficient on its own for a healthy headline (runtime must also be ready).
//   - Unknown or stale evidence is never flattened into healthy.
//   - A capability's declared exposure changes how access evidence is judged:
//     a private capability reachable through public ingress is a failure, not a
//     success.
//   - Stale backup protection degrades a stateful capability even while its
//     workload is serving traffic.
package assessment

import "time"

// CapabilityState is the headline lifecycle condition of a Cluster Capability.
type CapabilityState string

const (
	StatePlanned    CapabilityState = "planned"
	StateBlocked    CapabilityState = "blocked"
	StateInstalling CapabilityState = "installing"
	StateHealthy    CapabilityState = "healthy"
	StateDegraded   CapabilityState = "degraded"
	StateFailed     CapabilityState = "failed"
	StateDisabled   CapabilityState = "disabled"
)

// FacetKind names one of the five evidence facets behind a headline state.
type FacetKind string

const (
	FacetConfiguration FacetKind = "configuration"
	FacetDelivery      FacetKind = "delivery"
	FacetRuntime       FacetKind = "runtime"
	FacetAccess        FacetKind = "access"
	FacetProtection    FacetKind = "protection"
)

// facetOrder fixes the presentation order of facets in an assessment.
var facetOrder = []FacetKind{FacetConfiguration, FacetDelivery, FacetRuntime, FacetAccess, FacetProtection}

// FacetState is the condition of a single facet.
type FacetState string

const (
	// FacetSatisfied means the facet's evidence is fresh and healthy.
	FacetSatisfied FacetState = "satisfied"
	// FacetPending means the facet is declared but no action has been observed
	// yet (for example, Argo CD has not created the resources).
	FacetPending FacetState = "pending"
	// FacetProgressing means the facet is actively reconciling or starting up.
	FacetProgressing FacetState = "progressing"
	// FacetDegraded means the facet is functioning below its healthy contract.
	FacetDegraded FacetState = "degraded"
	// FacetFailed means the facet is in an error condition.
	FacetFailed FacetState = "failed"
	// FacetBlocked means a precondition (dependency or required value) is unmet.
	FacetBlocked FacetState = "blocked"
	// FacetUnknown means no evidence was collected, or the evidence is stale.
	FacetUnknown FacetState = "unknown"
	// FacetNotApplicable means the facet does not apply to this capability
	// (for example, protection for a stateless capability).
	FacetNotApplicable FacetState = "not-applicable"
)

// Exposure is a capability's declared reachability, which changes how the
// access facet is evaluated.
type Exposure string

const (
	// ExposurePublic capabilities are expected to be reachable via public ingress.
	ExposurePublic Exposure = "public"
	// ExposurePrivate capabilities are reachable only through the Private Gateway;
	// public reachability is a failure.
	ExposurePrivate Exposure = "private"
	// ExposureInternal capabilities have no ingress at all; any ingress is a failure.
	ExposureInternal Exposure = "internal"
)

// RemediationKind is the category of the single next route offered for a
// non-satisfied facet.
type RemediationKind string

const (
	RemediateSetupJourney  RemediationKind = "setup-journey"
	RemediateGitProposal   RemediationKind = "git-proposal"
	RemediateRuntimeAction RemediationKind = "runtime-action"
	RemediateDocumentation RemediationKind = "documentation"
	RemediateGrafana       RemediationKind = "grafana"
	RemediateArgoCD        RemediationKind = "argocd"
)

// Remediation is one relevant next route for an unhealthy facet. Reference is a
// non-secret identifier whose meaning depends on Kind (a Setup Journey task key,
// a documentation path, an Argo application name, a Grafana dashboard id, or a
// bounded Runtime Action id).
type Remediation struct {
	Kind      RemediationKind `json:"kind"`
	Reference string          `json:"reference,omitempty"`
}

// Facet is the assessed condition of one evidence facet, retaining the reason
// code, the evidence timestamp, whether that evidence is stale, and the single
// remediation route when the facet is not satisfied.
type Facet struct {
	Kind        FacetKind    `json:"kind"`
	State       FacetState   `json:"state"`
	ReasonCode  string       `json:"reasonCode"`
	ObservedAt  time.Time    `json:"observedAt,omitempty"`
	Stale       bool         `json:"stale"`
	Remediation *Remediation `json:"remediation,omitempty"`
}

// CapabilityRef identifies the capability under assessment and carries the
// declarative properties that change how its evidence is interpreted and where
// its remediation routes point. Fields mirror the Cluster Capability catalog.
type CapabilityRef struct {
	ID               string   `json:"id"`
	Exposure         Exposure `json:"exposure"`
	Stateful         bool     `json:"stateful"`
	ArgoApplication  string   `json:"argoApplication,omitempty"`
	GrafanaDashboard string   `json:"grafanaDashboard,omitempty"`
	DocsPath         string   `json:"docsPath,omitempty"`
	SetupTask        string   `json:"setupTask,omitempty"`
}

// Freshness bounds how old evidence may be before it is treated as stale.
type Freshness struct {
	// Evidence is the maximum age of an observation before the facet it backs is
	// treated as unknown rather than current.
	Evidence time.Duration
	// RecoveryPoint is the maximum age of a local or offsite Recovery Point
	// before protection is considered stale.
	RecoveryPoint time.Duration
}

// Observation is embedded in every facet's evidence. At records when the
// evidence was gathered; Missing is true when no evidence could be collected at
// all, which yields an unknown facet.
type Observation struct {
	At      time.Time
	Missing bool
}

// stale reports whether an observation is older than the evidence freshness
// budget. A zero budget disables the staleness check.
func (o Observation) stale(now time.Time, freshness Freshness) bool {
	if o.Missing || o.At.IsZero() || freshness.Evidence <= 0 {
		return false
	}
	return now.Sub(o.At) > freshness.Evidence
}

// Input is the complete evidence for one capability assessment.
type Input struct {
	Capability    CapabilityRef
	Now           time.Time
	Freshness     Freshness
	Configuration ConfigurationEvidence
	Delivery      DeliveryEvidence
	Runtime       RuntimeEvidence
	Access        AccessEvidence
	Protection    ProtectionEvidence
}

// CapabilityAssessment is the derived headline state plus its explaining facets.
type CapabilityAssessment struct {
	CapabilityID string          `json:"capabilityId"`
	State        CapabilityState `json:"state"`
	ReasonCode   string          `json:"reasonCode"`
	Facets       []Facet         `json:"facets"`
	ObservedAt   time.Time       `json:"observedAt,omitempty"`
}

// Assess derives the headline Capability State and its five facets from the
// supplied evidence. It is pure: identical input yields identical output.
func Assess(input Input) CapabilityAssessment {
	byKind := map[FacetKind]Facet{
		FacetConfiguration: assessConfiguration(input),
		FacetDelivery:      assessDelivery(input),
		FacetRuntime:       assessRuntime(input),
		FacetAccess:        assessAccess(input),
		FacetProtection:    assessProtection(input),
	}

	facets := make([]Facet, 0, len(facetOrder))
	var latest time.Time
	for _, kind := range facetOrder {
		facet := byKind[kind]
		facets = append(facets, facet)
		if facet.ObservedAt.After(latest) {
			latest = facet.ObservedAt
		}
	}

	state, reason := headline(byKind)
	return CapabilityAssessment{
		CapabilityID: input.Capability.ID,
		State:        state,
		ReasonCode:   reason,
		Facets:       facets,
		ObservedAt:   latest,
	}
}

// headline aggregates the five facets into one Capability State. The precedence
// is deliberate: a disabled or blocked capability short-circuits before any
// runtime reading; an outright failure outranks progress; and a healthy
// headline is only reached when every applicable facet is satisfied and fresh —
// so an Argo-Healthy-but-runtime-pending capability, an unknown/stale facet, or
// stale protection on a serving stateful capability can never read as healthy.
func headline(facets map[FacetKind]Facet) (CapabilityState, string) {
	configuration := facets[FacetConfiguration]

	if configuration.State == FacetNotApplicable {
		return StateDisabled, configuration.ReasonCode
	}
	if configuration.State == FacetBlocked {
		return StateBlocked, configuration.ReasonCode
	}
	if facet, ok := firstInOrder(facets, func(f Facet) bool { return f.State == FacetFailed }); ok {
		return StateFailed, facet.ReasonCode
	}
	if facet, ok := firstInOrder(facets, func(f Facet) bool { return f.State == FacetProgressing }); ok {
		return StateInstalling, facet.ReasonCode
	}

	delivery := facets[FacetDelivery]
	runtime := facets[FacetRuntime]
	if configuration.State == FacetSatisfied && delivery.State == FacetPending &&
		(runtime.State == FacetPending || runtime.State == FacetUnknown) {
		return StatePlanned, ReasonAwaitingDelivery
	}

	if facet, ok := firstInOrder(facets, isDegrading); ok {
		return StateDegraded, facet.ReasonCode
	}
	return StateHealthy, ReasonHealthy
}

// isDegrading reports whether an applicable facet keeps a capability from being
// healthy: any non-satisfied applicable facet, or a satisfied-but-stale one.
func isDegrading(facet Facet) bool {
	if facet.State == FacetNotApplicable {
		return false
	}
	return facet.State != FacetSatisfied || facet.Stale
}

// firstInOrder returns the first facet, in fixed presentation order, matching
// the predicate, so the headline reason is stable and explainable.
func firstInOrder(facets map[FacetKind]Facet, match func(Facet) bool) (Facet, bool) {
	for _, kind := range facetOrder {
		if facet := facets[kind]; match(facet) {
			return facet, true
		}
	}
	return Facet{}, false
}
