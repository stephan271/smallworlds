package assessment

import "time"

// Stable reason codes. They are the interface contract for the browser, which
// maps them to localized messages; they must never change meaning once shipped.
const (
	ReasonHealthy          = "healthy"
	ReasonAwaitingDelivery = "awaiting-delivery"

	// Configuration facet.
	ReasonConfigurationUnknown  = "configuration-evidence-unknown"
	ReasonCapabilityDisabled    = "capability-disabled"
	ReasonDependencyUnmet       = "dependency-unmet"
	ReasonRequiredValuesMissing = "required-values-missing"
	ReasonNotDeclaredInGit      = "not-declared-in-git"

	// Delivery facet.
	ReasonDeliveryUnknown     = "delivery-evidence-unknown"
	ReasonDeliveryFailed      = "delivery-failed"
	ReasonDeliveryProgressing = "delivery-progressing"
	ReasonDeliveryPending     = "delivery-pending"
	ReasonDeliveryDrifted     = "delivery-drifted"
	ReasonDeliveryOutOfSync   = "delivery-out-of-sync"

	// Runtime facet.
	ReasonRuntimeUnknown           = "runtime-evidence-unknown"
	ReasonRuntimeJobsFailed        = "runtime-jobs-failed"
	ReasonRuntimePVCUnbound        = "runtime-pvc-unbound"
	ReasonRuntimeWorkloadsNotReady = "runtime-workloads-not-ready"
	ReasonRuntimeProbesFailing     = "runtime-probes-failing"

	// Access facet.
	ReasonAccessUnknown                = "access-evidence-unknown"
	ReasonAccessDNSUnresolved          = "access-dns-unresolved"
	ReasonAccessCertNotReady           = "access-certificate-not-ready"
	ReasonAccessPublicUnreachable      = "access-public-unreachable"
	ReasonAccessGatewayUnreachable     = "access-gateway-unreachable"
	ReasonAccessPrivateExposedPublicly = "access-private-exposed-publicly"
	ReasonAccessInternalExposed        = "access-internal-exposed"

	// Protection facet.
	ReasonProtectionNotApplicable             = "protection-not-applicable"
	ReasonProtectionUnknown                   = "protection-evidence-unknown"
	ReasonProtectionCoverageGap               = "protection-coverage-gap"
	ReasonProtectionNoLocalRecoveryPoint      = "protection-no-local-recovery-point"
	ReasonProtectionLocalRecoveryPointStale   = "protection-local-recovery-point-stale"
	ReasonProtectionNoOffsiteRecoveryPoint    = "protection-no-offsite-recovery-point"
	ReasonProtectionOffsiteRecoveryPointStale = "protection-offsite-recovery-point-stale"
	ReasonProtectionRetentionUnmet            = "protection-retention-unmet"
)

// ConfigurationEvidence covers selection, required values, dependency blockers,
// and the Git declaration.
type ConfigurationEvidence struct {
	Observation
	Selected          bool
	RequiredValuesMet bool
	UnmetDependencies []string
	DeclaredInGit     bool
}

// DeliveryEvidence covers Argo CD sync, health, operation phase, drift, and the
// last reconciliation time.
type DeliveryEvidence struct {
	Observation
	ArgoSynced       bool
	ArgoHealthy      bool
	Progressing      bool
	Drifted          bool
	Failed           bool
	LastReconciledAt time.Time
}

// RuntimeEvidence covers controller readiness, failed Jobs, PVC binding, and
// endpoint/application probes.
type RuntimeEvidence struct {
	Observation
	WorkloadsReady bool
	Starting       bool
	FailedJobs     int
	PVCsBound      bool
	ProbesPassing  bool
}

// AccessEvidence covers DNS resolution, certificate readiness, and observed
// public/Private Gateway reachability. It is interpreted against the
// capability's declared exposure.
type AccessEvidence struct {
	Observation
	DNSResolves      bool
	CertificateReady bool
	PublicReachable  bool
	GatewayReachable bool
}

// ProtectionEvidence covers dataset coverage, the latest local and offsite
// Recovery Points, retention satisfaction, and the last Restore Drill.
type ProtectionEvidence struct {
	Observation
	DatasetsCovered        bool
	LocalRecoveryPointAt   time.Time
	OffsiteRecoveryPointAt time.Time
	RetentionSatisfied     bool
	RestoreDrillAt         time.Time
}

func setupRemediation(ref CapabilityRef) *Remediation {
	return &Remediation{Kind: RemediateSetupJourney, Reference: ref.SetupTask}
}

func argoRemediation(ref CapabilityRef) *Remediation {
	return &Remediation{Kind: RemediateArgoCD, Reference: ref.ArgoApplication}
}

func grafanaRemediation(ref CapabilityRef) *Remediation {
	return &Remediation{Kind: RemediateGrafana, Reference: ref.GrafanaDashboard}
}

func gitRemediation(ref CapabilityRef) *Remediation {
	return &Remediation{Kind: RemediateGitProposal, Reference: ref.ID}
}

func assessConfiguration(input Input) Facet {
	evidence := input.Configuration
	facet := Facet{Kind: FacetConfiguration, ObservedAt: evidence.At}

	if evidence.Missing {
		facet.State = FacetUnknown
		facet.ReasonCode = ReasonConfigurationUnknown
		facet.Remediation = setupRemediation(input.Capability)
		return facet
	}
	facet.Stale = evidence.stale(input.Now, input.Freshness)
	if !evidence.Selected {
		facet.State = FacetNotApplicable
		facet.ReasonCode = ReasonCapabilityDisabled
		return facet
	}
	if len(evidence.UnmetDependencies) > 0 {
		facet.State = FacetBlocked
		facet.ReasonCode = ReasonDependencyUnmet
		facet.Remediation = setupRemediation(input.Capability)
		return facet
	}
	if !evidence.RequiredValuesMet {
		facet.State = FacetBlocked
		facet.ReasonCode = ReasonRequiredValuesMissing
		facet.Remediation = setupRemediation(input.Capability)
		return facet
	}
	if !evidence.DeclaredInGit {
		facet.State = FacetBlocked
		facet.ReasonCode = ReasonNotDeclaredInGit
		facet.Remediation = gitRemediation(input.Capability)
		return facet
	}
	facet.State = FacetSatisfied
	facet.ReasonCode = ReasonHealthy
	return facet
}

func assessDelivery(input Input) Facet {
	evidence := input.Delivery
	facet := Facet{Kind: FacetDelivery, ObservedAt: evidence.At}

	if !input.Configuration.Missing && !input.Configuration.Selected {
		facet.State = FacetNotApplicable
		facet.ReasonCode = ReasonCapabilityDisabled
		return facet
	}
	if evidence.Missing {
		facet.State = FacetUnknown
		facet.ReasonCode = ReasonDeliveryUnknown
		facet.Remediation = argoRemediation(input.Capability)
		return facet
	}
	facet.Stale = evidence.stale(input.Now, input.Freshness)
	switch {
	case evidence.Failed:
		facet.State = FacetFailed
		facet.ReasonCode = ReasonDeliveryFailed
		facet.Remediation = argoRemediation(input.Capability)
	case evidence.Drifted:
		facet.State = FacetDegraded
		facet.ReasonCode = ReasonDeliveryDrifted
		facet.Remediation = gitRemediation(input.Capability)
	case evidence.Progressing:
		facet.State = FacetProgressing
		facet.ReasonCode = ReasonDeliveryProgressing
		facet.Remediation = argoRemediation(input.Capability)
	case !evidence.ArgoSynced && !evidence.ArgoHealthy:
		// Declared, but Argo CD has not yet acted on the resources.
		facet.State = FacetPending
		facet.ReasonCode = ReasonDeliveryPending
		facet.Remediation = argoRemediation(input.Capability)
	case !evidence.ArgoSynced:
		facet.State = FacetDegraded
		facet.ReasonCode = ReasonDeliveryOutOfSync
		facet.Remediation = argoRemediation(input.Capability)
	default:
		facet.State = FacetSatisfied
		facet.ReasonCode = ReasonHealthy
	}
	return facet
}

func assessRuntime(input Input) Facet {
	evidence := input.Runtime
	facet := Facet{Kind: FacetRuntime, ObservedAt: evidence.At}

	if !input.Configuration.Missing && !input.Configuration.Selected {
		facet.State = FacetNotApplicable
		facet.ReasonCode = ReasonCapabilityDisabled
		return facet
	}
	if evidence.Missing {
		facet.State = FacetUnknown
		facet.ReasonCode = ReasonRuntimeUnknown
		facet.Remediation = grafanaRemediation(input.Capability)
		return facet
	}
	facet.Stale = evidence.stale(input.Now, input.Freshness)
	switch {
	case evidence.FailedJobs > 0:
		facet.State = FacetFailed
		facet.ReasonCode = ReasonRuntimeJobsFailed
		facet.Remediation = grafanaRemediation(input.Capability)
	case !evidence.PVCsBound:
		facet.State = FacetDegraded
		facet.ReasonCode = ReasonRuntimePVCUnbound
		facet.Remediation = grafanaRemediation(input.Capability)
	case evidence.Starting && !evidence.WorkloadsReady:
		facet.State = FacetProgressing
		facet.ReasonCode = ReasonRuntimeWorkloadsNotReady
		facet.Remediation = grafanaRemediation(input.Capability)
	case !evidence.WorkloadsReady:
		facet.State = FacetDegraded
		facet.ReasonCode = ReasonRuntimeWorkloadsNotReady
		facet.Remediation = grafanaRemediation(input.Capability)
	case !evidence.ProbesPassing:
		facet.State = FacetDegraded
		facet.ReasonCode = ReasonRuntimeProbesFailing
		facet.Remediation = grafanaRemediation(input.Capability)
	default:
		facet.State = FacetSatisfied
		facet.ReasonCode = ReasonHealthy
	}
	return facet
}

// assessAccess judges reachability against the capability's declared exposure,
// so public and private reachability are not evaluated identically: a private
// capability reachable through public ingress is a failure, not a success.
func assessAccess(input Input) Facet {
	evidence := input.Access
	ref := input.Capability
	facet := Facet{Kind: FacetAccess, ObservedAt: evidence.At}

	if !input.Configuration.Missing && !input.Configuration.Selected {
		facet.State = FacetNotApplicable
		facet.ReasonCode = ReasonCapabilityDisabled
		return facet
	}
	if evidence.Missing {
		facet.State = FacetUnknown
		facet.ReasonCode = ReasonAccessUnknown
		facet.Remediation = setupRemediation(ref)
		return facet
	}
	facet.Stale = evidence.stale(input.Now, input.Freshness)

	// Exposure violations are judged first: a private or internal capability
	// reachable from the public internet is a security failure regardless of DNS
	// or certificate readiness.
	if ref.Exposure == ExposurePrivate && evidence.PublicReachable {
		facet.State = FacetFailed
		facet.ReasonCode = ReasonAccessPrivateExposedPublicly
		facet.Remediation = setupRemediation(ref)
		return facet
	}
	if ref.Exposure == ExposureInternal && evidence.PublicReachable {
		facet.State = FacetFailed
		facet.ReasonCode = ReasonAccessInternalExposed
		facet.Remediation = setupRemediation(ref)
		return facet
	}

	if !evidence.DNSResolves {
		facet.State = FacetDegraded
		facet.ReasonCode = ReasonAccessDNSUnresolved
		facet.Remediation = setupRemediation(ref)
		return facet
	}
	if !evidence.CertificateReady {
		facet.State = FacetDegraded
		facet.ReasonCode = ReasonAccessCertNotReady
		facet.Remediation = setupRemediation(ref)
		return facet
	}
	switch ref.Exposure {
	case ExposurePublic:
		if !evidence.PublicReachable {
			facet.State = FacetDegraded
			facet.ReasonCode = ReasonAccessPublicUnreachable
			facet.Remediation = setupRemediation(ref)
			return facet
		}
	case ExposurePrivate:
		if !evidence.GatewayReachable {
			facet.State = FacetDegraded
			facet.ReasonCode = ReasonAccessGatewayUnreachable
			facet.Remediation = setupRemediation(ref)
			return facet
		}
	}
	facet.State = FacetSatisfied
	facet.ReasonCode = ReasonHealthy
	return facet
}

// assessProtection degrades a stateful capability when its backup protection is
// missing or stale, even while the workload serves traffic. It does not apply
// to stateless capabilities.
func assessProtection(input Input) Facet {
	evidence := input.Protection
	ref := input.Capability
	facet := Facet{Kind: FacetProtection, ObservedAt: evidence.At}

	if !ref.Stateful {
		facet.State = FacetNotApplicable
		facet.ReasonCode = ReasonProtectionNotApplicable
		return facet
	}
	if !input.Configuration.Missing && !input.Configuration.Selected {
		facet.State = FacetNotApplicable
		facet.ReasonCode = ReasonCapabilityDisabled
		return facet
	}
	if evidence.Missing {
		facet.State = FacetUnknown
		facet.ReasonCode = ReasonProtectionUnknown
		facet.Remediation = setupRemediation(ref)
		return facet
	}
	facet.Stale = evidence.stale(input.Now, input.Freshness)

	if !evidence.DatasetsCovered {
		facet.State = FacetDegraded
		facet.ReasonCode = ReasonProtectionCoverageGap
		facet.Remediation = setupRemediation(ref)
		return facet
	}
	if evidence.LocalRecoveryPointAt.IsZero() {
		facet.State = FacetDegraded
		facet.ReasonCode = ReasonProtectionNoLocalRecoveryPoint
		facet.Remediation = setupRemediation(ref)
		return facet
	}
	if recoveryStale(evidence.LocalRecoveryPointAt, input.Now, input.Freshness) {
		facet.State = FacetDegraded
		facet.ReasonCode = ReasonProtectionLocalRecoveryPointStale
		facet.Stale = true
		facet.Remediation = setupRemediation(ref)
		return facet
	}
	if evidence.OffsiteRecoveryPointAt.IsZero() {
		facet.State = FacetDegraded
		facet.ReasonCode = ReasonProtectionNoOffsiteRecoveryPoint
		facet.Remediation = setupRemediation(ref)
		return facet
	}
	if recoveryStale(evidence.OffsiteRecoveryPointAt, input.Now, input.Freshness) {
		facet.State = FacetDegraded
		facet.ReasonCode = ReasonProtectionOffsiteRecoveryPointStale
		facet.Stale = true
		facet.Remediation = setupRemediation(ref)
		return facet
	}
	if !evidence.RetentionSatisfied {
		facet.State = FacetDegraded
		facet.ReasonCode = ReasonProtectionRetentionUnmet
		facet.Remediation = setupRemediation(ref)
		return facet
	}
	facet.State = FacetSatisfied
	facet.ReasonCode = ReasonHealthy
	return facet
}

// recoveryStale reports whether a Recovery Point is older than the recovery-point
// freshness budget. A zero budget disables the check.
func recoveryStale(at time.Time, now time.Time, freshness Freshness) bool {
	if at.IsZero() || freshness.RecoveryPoint <= 0 {
		return false
	}
	return now.Sub(at) > freshness.RecoveryPoint
}
