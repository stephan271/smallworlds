// Package addcapability plans adding one currently-disabled optional Community
// Application to a running cluster (PRD user stories 32, 43–44, 83, 95). It is
// pure and deterministic: the same catalog, observed capability states, live
// capacity, and overlay target always yield the same offers and Change Plan.
//
// The package enforces the repository's delivery contract: adding a capability
// is a *Git proposal* against the operator's overlay, never a direct mutation of
// live Kubernetes resources. The rendered artifact is therefore catalog-derived
// Desired Configuration — the exact files the proposal would add — so what the
// operator reviews equals what is committed. Removal and disabling are out of
// scope for the first release and this package deliberately offers neither.
package addcapability

import (
	"sort"

	"github.com/stephan271/smallworlds/operator-console/internal/assessment"
	"github.com/stephan271/smallworlds/operator-console/internal/capability"
)

// present reports whether a capability is already part of the cluster — anything
// other than an explicit disabled state (or an absent observation, which is not
// evidence that the capability is running). Only a present dependency can be
// relied on; a disabled one must be pulled into the same proposal.
func present(state assessment.CapabilityState) bool {
	return state != "" && state != assessment.StateDisabled
}

// Offer is one addable Community Application: a currently-disabled optional
// capability the operator may propose to add, with the catalog facts that shape
// its planning (its resource footprint, exposure, protection, whether it holds
// persistent data, and which of its dependencies are still disabled and would be
// pulled into the same proposal).
type Offer struct {
	ID                   string               `json:"id"`
	DisplayKey           string               `json:"displayKey"`
	Resources            capability.Resources `json:"resources"`
	Exposure             string               `json:"exposure"`
	Protection           string               `json:"protection"`
	Stateful             bool                 `json:"stateful"`
	Dependencies         []string             `json:"dependencies"`
	DisabledDependencies []string             `json:"disabledDependencies"`
}

// Offers lists the Community Applications the operator may add now: optional (not
// required), categorized as a Community Application, observed as disabled, and
// supported in the cluster's deployment mode. Platform services and any already
// present capability are never offered, and there is no remove/disable offer.
func Offers(catalog capability.Catalog, states map[string]assessment.CapabilityState, mode capability.DeploymentMode) ([]Offer, error) {
	if err := catalog.Validate(); err != nil {
		return nil, err
	}
	offers := make([]Offer, 0)
	for _, entry := range catalog.Capabilities {
		if entry.Category != capability.CommunityApplication || entry.Required {
			continue
		}
		if states[entry.ID] != assessment.StateDisabled {
			continue
		}
		if !supportsMode(entry, mode) {
			continue
		}
		offers = append(offers, newOffer(entry, states))
	}
	sort.Slice(offers, func(i, j int) bool { return offers[i].ID < offers[j].ID })
	return offers, nil
}

func newOffer(entry capability.Entry, states map[string]assessment.CapabilityState) Offer {
	offer := Offer{
		ID:           entry.ID,
		DisplayKey:   entry.DisplayKey,
		Resources:    entry.Resources,
		Exposure:     entry.Exposure,
		Protection:   entry.Protection,
		Stateful:     entry.Resources.StorageGi > 0,
		Dependencies: append([]string(nil), entry.Dependencies...),
	}
	for _, dependency := range entry.Dependencies {
		if !present(states[dependency]) {
			offer.DisabledDependencies = append(offer.DisabledDependencies, dependency)
		}
	}
	sort.Strings(offer.DisabledDependencies)
	return offer
}

func supportsMode(entry capability.Entry, mode capability.DeploymentMode) bool {
	for _, supported := range entry.SupportedDeploymentModes {
		if supported == mode {
			return true
		}
	}
	return false
}
