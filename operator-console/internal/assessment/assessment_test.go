package assessment

import (
	"testing"
	"time"
)

// now is a fixed reference time for deterministic tests.
var now = time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)

var defaultFreshness = Freshness{Evidence: time.Hour, RecoveryPoint: 24 * time.Hour}

// healthyStatefulPrivate builds an Input whose every facet is fresh and healthy
// for a stateful, private capability. Tests mutate one facet at a time.
func healthyStatefulPrivate() Input {
	return Input{
		Capability: CapabilityRef{
			ID:       "nextcloud",
			Exposure: ExposurePrivate,
			Stateful: true,
		},
		Now:       now,
		Freshness: defaultFreshness,
		Configuration: ConfigurationEvidence{
			Observation:       Observation{At: now},
			Selected:          true,
			RequiredValuesMet: true,
			DeclaredInGit:     true,
		},
		Delivery: DeliveryEvidence{
			Observation:      Observation{At: now},
			ArgoSynced:       true,
			ArgoHealthy:      true,
			LastReconciledAt: now,
		},
		Runtime: RuntimeEvidence{
			Observation:    Observation{At: now},
			WorkloadsReady: true,
			PVCsBound:      true,
			ProbesPassing:  true,
		},
		Access: AccessEvidence{
			Observation:      Observation{At: now},
			DNSResolves:      true,
			CertificateReady: true,
			GatewayReachable: true,
		},
		Protection: ProtectionEvidence{
			Observation:            Observation{At: now},
			DatasetsCovered:        true,
			LocalRecoveryPointAt:   now.Add(-time.Hour),
			OffsiteRecoveryPointAt: now.Add(-2 * time.Hour),
			RetentionSatisfied:     true,
			RestoreDrillAt:         now.Add(-48 * time.Hour),
		},
	}
}

func TestAssessHealthy(t *testing.T) {
	got := Assess(healthyStatefulPrivate())
	if got.State != StateHealthy {
		t.Fatalf("state = %q, want %q", got.State, StateHealthy)
	}
	if got.ReasonCode != ReasonHealthy {
		t.Fatalf("reason = %q, want %q", got.ReasonCode, ReasonHealthy)
	}
	if len(got.Facets) != 5 {
		t.Fatalf("facets = %d, want 5", len(got.Facets))
	}
	for _, facet := range got.Facets {
		if facet.State != FacetSatisfied {
			t.Errorf("facet %s = %q, want satisfied", facet.Kind, facet.State)
		}
	}
	if !got.ObservedAt.Equal(now) {
		t.Errorf("observedAt = %v, want %v", got.ObservedAt, now)
	}
}

// TestArgoHealthyIsNotSufficient proves an Argo-Healthy delivery with a runtime
// that is not yet ready never reads as a healthy capability (criterion 4).
func TestArgoHealthyIsNotSufficient(t *testing.T) {
	input := healthyStatefulPrivate()
	input.Runtime.WorkloadsReady = false
	input.Runtime.Starting = true

	got := Assess(input)
	if got.State == StateHealthy {
		t.Fatalf("state = healthy, want not healthy while runtime is not ready")
	}
	if got.State != StateInstalling {
		t.Fatalf("state = %q, want %q", got.State, StateInstalling)
	}
}

// TestUnknownEvidenceNeverHealthy proves stale or missing evidence is never
// flattened into a healthy headline (criterion 4).
func TestUnknownEvidenceNeverHealthy(t *testing.T) {
	t.Run("missing runtime", func(t *testing.T) {
		input := healthyStatefulPrivate()
		input.Runtime = RuntimeEvidence{Observation: Observation{Missing: true}}
		got := Assess(input)
		if got.State == StateHealthy {
			t.Fatalf("state = healthy, want degraded for missing runtime evidence")
		}
		if got.State != StateDegraded {
			t.Fatalf("state = %q, want %q", got.State, StateDegraded)
		}
	})

	t.Run("stale runtime observation", func(t *testing.T) {
		input := healthyStatefulPrivate()
		input.Runtime.At = now.Add(-2 * time.Hour) // older than the 1h budget
		got := Assess(input)
		if got.State == StateHealthy {
			t.Fatalf("state = healthy, want degraded for stale runtime evidence")
		}
		var runtime Facet
		for _, facet := range got.Facets {
			if facet.Kind == FacetRuntime {
				runtime = facet
			}
		}
		if !runtime.Stale {
			t.Fatalf("runtime facet not marked stale")
		}
	})
}

// TestExposureChangesAccessEvaluation proves public and private reachability are
// judged against the declared exposure (criterion 5).
func TestExposureChangesAccessEvaluation(t *testing.T) {
	t.Run("private exposed publicly fails", func(t *testing.T) {
		input := healthyStatefulPrivate()
		input.Access.PublicReachable = true
		got := Assess(input)
		if got.State != StateFailed {
			t.Fatalf("state = %q, want %q", got.State, StateFailed)
		}
		if got.ReasonCode != ReasonAccessPrivateExposedPublicly {
			t.Fatalf("reason = %q, want %q", got.ReasonCode, ReasonAccessPrivateExposedPublicly)
		}
	})

	t.Run("public reachable is satisfied", func(t *testing.T) {
		input := healthyStatefulPrivate()
		input.Capability.Exposure = ExposurePublic
		input.Access.PublicReachable = true
		input.Access.GatewayReachable = false
		got := Assess(input)
		if got.State != StateHealthy {
			t.Fatalf("state = %q, want %q", got.State, StateHealthy)
		}
	})

	t.Run("public unreachable degrades", func(t *testing.T) {
		input := healthyStatefulPrivate()
		input.Capability.Exposure = ExposurePublic
		input.Access.PublicReachable = false
		got := Assess(input)
		if got.State != StateDegraded {
			t.Fatalf("state = %q, want %q", got.State, StateDegraded)
		}
		if got.ReasonCode != ReasonAccessPublicUnreachable {
			t.Fatalf("reason = %q, want %q", got.ReasonCode, ReasonAccessPublicUnreachable)
		}
	})
}

// TestStaleProtectionDegradesServingCapability proves stale backup protection
// degrades a stateful capability even while its workload is serving (criterion 5).
func TestStaleProtectionDegradesServingCapability(t *testing.T) {
	input := healthyStatefulPrivate()
	input.Protection.OffsiteRecoveryPointAt = now.Add(-48 * time.Hour) // older than 24h budget

	got := Assess(input)
	if got.State != StateDegraded {
		t.Fatalf("state = %q, want %q", got.State, StateDegraded)
	}
	if got.ReasonCode != ReasonProtectionOffsiteRecoveryPointStale {
		t.Fatalf("reason = %q, want %q", got.ReasonCode, ReasonProtectionOffsiteRecoveryPointStale)
	}
}

// TestProtectionNotApplicableForStateless proves a stateless capability is not
// degraded for lacking backups.
func TestProtectionNotApplicableForStateless(t *testing.T) {
	input := healthyStatefulPrivate()
	input.Capability.Stateful = false
	input.Protection = ProtectionEvidence{Observation: Observation{Missing: true}}

	got := Assess(input)
	if got.State != StateHealthy {
		t.Fatalf("state = %q, want %q", got.State, StateHealthy)
	}
	for _, facet := range got.Facets {
		if facet.Kind == FacetProtection && facet.State != FacetNotApplicable {
			t.Fatalf("protection facet = %q, want not-applicable", facet.State)
		}
	}
}

// TestRemediationRoutePerUnhealthyFacet proves each non-satisfied facet offers
// exactly one relevant next route (criterion 6).
func TestRemediationRoutePerUnhealthyFacet(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Input)
		kind   FacetKind
		want   RemediationKind
	}{
		{
			name:   "unmet dependency routes to setup",
			mutate: func(in *Input) { in.Configuration.UnmetDependencies = []string{"keycloak"} },
			kind:   FacetConfiguration,
			want:   RemediateSetupJourney,
		},
		{
			name:   "delivery failure routes to argocd",
			mutate: func(in *Input) { in.Delivery.Failed = true },
			kind:   FacetDelivery,
			want:   RemediateArgoCD,
		},
		{
			name:   "drift routes to a git proposal",
			mutate: func(in *Input) { in.Delivery.Drifted = true },
			kind:   FacetDelivery,
			want:   RemediateGitProposal,
		},
		{
			name:   "runtime probe failure routes to grafana",
			mutate: func(in *Input) { in.Runtime.ProbesPassing = false },
			kind:   FacetRuntime,
			want:   RemediateGrafana,
		},
		{
			name:   "stale protection routes to setup",
			mutate: func(in *Input) { in.Protection.LocalRecoveryPointAt = now.Add(-48 * time.Hour) },
			kind:   FacetProtection,
			want:   RemediateSetupJourney,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := healthyStatefulPrivate()
			test.mutate(&input)
			got := Assess(input)
			var facet Facet
			for _, f := range got.Facets {
				if f.Kind == test.kind {
					facet = f
				}
			}
			if facet.State == FacetSatisfied {
				t.Fatalf("facet %s unexpectedly satisfied", test.kind)
			}
			if facet.Remediation == nil {
				t.Fatalf("facet %s has no remediation route", test.kind)
			}
			if facet.Remediation.Kind != test.want {
				t.Fatalf("remediation = %q, want %q", facet.Remediation.Kind, test.want)
			}
		})
	}
}

func TestHeadlinePrecedence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Input)
		want   CapabilityState
	}{
		{
			name:   "disabled when not selected",
			mutate: func(in *Input) { in.Configuration.Selected = false },
			want:   StateDisabled,
		},
		{
			name:   "blocked on missing required values",
			mutate: func(in *Input) { in.Configuration.RequiredValuesMet = false },
			want:   StateBlocked,
		},
		{
			name: "planned when declared but nothing delivered",
			mutate: func(in *Input) {
				in.Delivery = DeliveryEvidence{Observation: Observation{At: now}}
				in.Runtime = RuntimeEvidence{Observation: Observation{Missing: true}}
				in.Access = AccessEvidence{Observation: Observation{Missing: true}}
				in.Protection = ProtectionEvidence{Observation: Observation{Missing: true}}
			},
			want: StatePlanned,
		},
		{
			name:   "installing while delivery progresses",
			mutate: func(in *Input) { in.Delivery.Progressing = true },
			want:   StateInstalling,
		},
		{
			name:   "failed outranks progressing",
			mutate: func(in *Input) { in.Delivery.Progressing = true; in.Runtime.FailedJobs = 1 },
			want:   StateFailed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := healthyStatefulPrivate()
			test.mutate(&input)
			if got := Assess(input).State; got != test.want {
				t.Fatalf("state = %q, want %q", got, test.want)
			}
		})
	}
}
