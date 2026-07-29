package consoleserve

import (
	"sort"

	"github.com/stephan271/smallworlds/operator-console/internal/assessment"
	"github.com/stephan271/smallworlds/operator-console/internal/capability"
	"github.com/stephan271/smallworlds/operator-console/internal/protection"
)

// This file translates the Cluster Capability catalog into the references the
// assessment engine judges. The catalog records a capability's *policy*
// ("private-gateway", "application-policy"); the engine needs its *expected
// reachability*, and those are not the same statement. A Platform Service with
// no Ingress at all is not "private" — it is internal, and any Ingress appearing
// on it is a finding rather than a success. Getting that distinction wrong would
// either report every backend capability as degraded for lacking a gateway route
// it never wanted, or quietly accept a public route on an operator interface.

// privateOperatorInterfaces are the capabilities that serve a web interface to
// Operators and must be reachable only through the Private Gateway (ADR 0012,
// ADR 0038). A public route on any of them is an access failure, not a warning.
var privateOperatorInterfaces = map[string]bool{
	"argocd-ingress":        true,
	"kube-prometheus-stack": true,
	"operator-console":      true,
}

// publiclyReachable are the capabilities that must stay reachable from the
// internet in the first release (ADR 0038). Headscale has to be reachable before
// a device can join the network it coordinates; Keycloak serves identity
// callbacks for public applications; the Member Dashboard and mail are for
// community members, not Operators.
var publiclyReachable = map[string]bool{
	"headscale": true,
	"keycloak":  true,
	"dashboard": true,
	"stalwart":  true,
}

// documentedCapabilities are those with a subsystem note in doc/. The
// documentation remediation route points an Operator at a real file, so a
// capability without one gets no documentation link rather than a broken one.
var documentedCapabilities = map[string]string{
	"dashboard":         "doc/tenant-dashboard.md",
	"forgejo":           "doc/tenant-forgejo.md",
	"hermes":            "doc/tenant-hermes.md",
	"immich":            "doc/tenant-immich.md",
	"keycloak":          "doc/tenant-keycloak.md",
	"nextcloud":         "doc/tenant-nextcloud.md",
	"stalwart":          "doc/tenant-stalwart.md",
	"plane":             "doc/plane-architecture.md",
	"operator-console":  "doc/tenant-operator-console.md",
	"velero":            "doc/storage-and-backup.md",
	"backup-replicator": "doc/storage-and-backup.md",
	"garage":            "doc/storage-and-backup.md",
}

// CapabilityRefs turns the catalog into the assessment engine's references.
func CapabilityRefs(catalog capability.Catalog) []assessment.CapabilityRef {
	stateful := statefulCapabilities()
	refs := make([]assessment.CapabilityRef, 0, len(catalog.Capabilities))
	for _, entry := range catalog.Capabilities {
		refs = append(refs, assessment.CapabilityRef{
			ID:       entry.ID,
			Exposure: exposureOf(entry),
			Stateful: stateful[entry.ID],
			// Every capability is delivered by the Argo CD Application of the same
			// name — that identity is what lets one id address the delivery facet,
			// the Argo deep link, and the overlay declaration alike.
			ArgoApplication:  entry.ID,
			GrafanaDashboard: entry.ID,
			DocsPath:         documentedCapabilities[entry.ID],
			SetupTask:        entry.ID,
		})
	}
	sort.SliceStable(refs, func(first, second int) bool { return refs[first].ID < refs[second].ID })
	return refs
}

func exposureOf(entry capability.Entry) assessment.Exposure {
	switch {
	case privateOperatorInterfaces[entry.ID]:
		return assessment.ExposurePrivate
	case publiclyReachable[entry.ID]:
		return assessment.ExposurePublic
	case entry.Category == capability.CommunityApplication:
		// Community Applications keep their existing public exposure in the
		// first release; privatizing them is a separate initiative (ADR 0038).
		return assessment.ExposurePublic
	default:
		return assessment.ExposureInternal
	}
}

// statefulCapabilities are those that own at least one declared dataset. That is
// exactly the set for which stale protection should degrade a capability that is
// otherwise serving traffic, so the protection inventory — not a second list —
// decides which capabilities are stateful.
func statefulCapabilities() map[string]bool {
	stateful := map[string]bool{}
	for _, dataset := range protection.DefaultInventory() {
		stateful[dataset.Capability] = true
	}
	return stateful
}
