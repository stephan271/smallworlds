// Package overlayadoption moves a cluster onto an overlay commit the Operator
// has reviewed and merged.
//
// It exists because that was the one step of a release update with no path
// through either console. Everything before it is supported — a signed release
// is verified, a Change Plan is reviewed, a proposal is opened, the Operator
// merges it — and then nothing carries the merged commit to the cluster. The
// root Application is pinned to the reviewed commit at installation time and
// written again never, so an update that has been merged in every sense still
// deploys the previous release until somebody patches Kubernetes by hand.
//
// The pin itself is not the problem, it is the point: HEAD would let a
// deployment change without anybody approving it. What was missing is the
// deliberate act of moving the pin, which is what this package is.
//
// Both consoles need it and for different reasons. The Launcher holds cluster
// access until administration is handed over; the cluster-side console holds it
// afterwards, and after the handover removes the temporary path the Launcher
// cannot reach the cluster at all. So the decision lives here and each server
// brings its own way of running a privileged command.
package overlayadoption

import (
	"fmt"
	"regexp"
	"strings"
)

// RootApplication is the app-of-apps the bootstrap installs and pins. Its name
// is part of the bootstrap contract (infrastructure/local/bootstrap-local-node.sh),
// not a configurable.
const (
	RootApplication = "smallworlds-root"
	Namespace       = "argocd"
)

// A full commit, as the bootstrap requires for the same reason: a branch or a
// tag can be moved under a cluster afterwards, and a short hash can become
// ambiguous in a repository that keeps growing.
var fullCommit = regexp.MustCompile(`^[0-9a-f]{40,64}$`)

func ValidateRevision(revision string) error {
	if !fullCommit.MatchString(strings.TrimSpace(revision)) {
		return fmt.Errorf("an overlay revision must be a full commit hash, reviewed and merged")
	}
	return nil
}

// PatchCommand renders the privileged command that repoints the root
// Application at a reviewed commit.
//
// A merge patch rather than a replacement: the root Application carries the
// installation's own identity — its repository, its project, its sync policy —
// and an update has no business rewriting any of that. It moves one field.
//
// Argo CD then fetches the new revision on its next refresh and applies what it
// finds. Nothing here waits for that: adopting is the act of saying which
// commit is approved, and observing what the cluster makes of it is a separate
// question with its own evidence.
func PatchCommand(revision string) (string, error) {
	if err := ValidateRevision(revision); err != nil {
		return "", err
	}
	// The revision is matched against a hex-only pattern above, so it cannot
	// carry anything a shell would interpret.
	return fmt.Sprintf(
		`k3s kubectl -n %s patch application %s --type merge -p '{"spec":{"source":{"targetRevision":"%s"}}}'`,
		Namespace, RootApplication, strings.TrimSpace(revision),
	), nil
}

// ReadRevisionCommand renders the command that reports which commit the cluster
// is currently pinned to, so an adoption can be confirmed from the cluster
// rather than assumed from the fact that a patch returned no error.
func ReadRevisionCommand() string {
	return fmt.Sprintf(
		`k3s kubectl -n %s get application %s -o jsonpath='{.spec.source.targetRevision}'`,
		Namespace, RootApplication,
	)
}
