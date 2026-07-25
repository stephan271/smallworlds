// Package handoffassessment composes the final Setup Journey assessment for the
// private administration handoff. It reports the completion of every prior step,
// always states the limitations that actually apply to the installation's
// Deployment Mode, and — once the handoff is complete — provides the in-cluster
// Operator Console handoff URL.
package handoffassessment

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrInvalidConsoleHost is returned when a complete handoff carries an unusable
// console hostname.
var ErrInvalidConsoleHost = errors.New("console handoff hostname is invalid")

// ErrInvalidMode is returned for an unrecognised installation mode. It is not
// defaulted: guessing the mode would state the wrong limitations, and the
// limitations are the part an Operator acts on.
var ErrInvalidMode = errors.New("handoff assessment deployment mode is invalid")

const (
	StepClusterCATrust        = "cluster-ca-trust-installed"
	StepPrivateNetwork        = "private-network"
	StepLauncherEnrolled      = "launcher-enrolled"
	StepGatewayIdentity       = "gateway-identity"
	StepGatewayAccessEnforced = "gateway-access-enforced"
	StepHandoffVerified       = "handoff-verified"
	StepTemporaryAccessClosed = "temporary-access-closed"
	StepFirstOwnerRegistered  = "first-owner-registered"
)

// Mode is the installation shape the assessment is composed for. It decides
// which steps apply and which limitations are stated.
type Mode string

const (
	// LANOnly has no public address at all.
	LANOnly Mode = "lan-only"
	// PubliclyAddressed covers Hetzner and internet-exposed local installations:
	// the community's services are public, the operator interfaces are not.
	PubliclyAddressed Mode = "publicly-addressed"
)

// Valid reports whether the mode is one this package composes for.
func (mode Mode) Valid() bool { return mode == LANOnly || mode == PubliclyAddressed }

// LANOnlyLimitations are surfaced for a LAN-only installation so the Operator
// understands what it does and does not provide.
var LANOnlyLimitations = []string{
	"Operator interfaces are reachable only from devices joined to the private network; there is no remote administration over the public internet.",
	"Each Operator Device must install the Cluster CA root to trust operator interfaces over HTTPS.",
	"No router port is opened and no public DNS is published; the cluster is not reachable from outside the private network.",
}

// PubliclyAddressedLimitations are surfaced for an installation with a public
// address. They are deliberately not reassurances: a publicly addressed
// installation carries obligations a LAN-only one does not, and the Operator is
// told about them at the moment they take over routine administration.
var PubliclyAddressedLimitations = []string{
	"Operator interfaces (Operator Console, Grafana, Argo CD) have no public route; they are reachable only from devices joined to the private network, even though the community's own services are public.",
	"Joining the private network is possible from anywhere, because the coordination endpoint is published under the public domain — losing every enrolled device therefore means losing routine administrative access until recovery.",
	"The community's services are exposed to the internet and their certificates are renewed automatically; the domain must stay delegated to the provider's nameservers or renewal will fail.",
	"The provisioned server, its volume, and its Primary IP continue to incur provider charges until they are explicitly decommissioned.",
}

var safeHost = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

// Inputs is the completion state of the prior tracers for one profile.
type Inputs struct {
	DeviceTrustInstalled   bool
	PrivateNetworkReady    bool
	LauncherEnrolled       bool
	GatewayIdentityReady   bool
	GatewayAccessEnforced  bool
	HandoffVerified        bool
	TemporaryAccessClosed  bool
	OwnerRegistered        bool
	BootstrapGrantDisabled bool
	ConsoleHost            string
}

// Limitations returns the caveats that apply to an installation of this mode.
func Limitations(mode Mode) []string {
	if mode == LANOnly {
		return append([]string(nil), LANOnlyLimitations...)
	}
	return append([]string(nil), PubliclyAddressedLimitations...)
}

// Step is one named completion result in the final assessment.
type Step struct {
	Name     string `json:"name"`
	Complete bool   `json:"complete"`
}

// Assessment is the final Setup Journey result.
type Assessment struct {
	Steps             []Step   `json:"steps"`
	Complete          bool     `json:"complete"`
	Limitations       []string `json:"limitations"`
	ConsoleHandoffURL string   `json:"consoleHandoffUrl,omitempty"`
}

// Evaluate composes the ordered steps, the limitations that apply to the mode,
// and — only when every step is complete — the in-cluster console handoff URL.
//
// The Cluster CA trust step appears only for a LAN-only installation. A
// publicly addressed one has no private root to install, so listing the step
// would leave the assessment permanently incomplete for a handoff that is in
// fact finished.
func Evaluate(mode Mode, inputs Inputs) (Assessment, error) {
	if !mode.Valid() {
		return Assessment{}, fmt.Errorf("%w: deployment mode %q", ErrInvalidMode, mode)
	}
	steps := make([]Step, 0, 8)
	if mode == LANOnly {
		steps = append(steps, Step{Name: StepClusterCATrust, Complete: inputs.DeviceTrustInstalled})
	}
	steps = append(steps,
		Step{Name: StepPrivateNetwork, Complete: inputs.PrivateNetworkReady},
		Step{Name: StepLauncherEnrolled, Complete: inputs.LauncherEnrolled},
		Step{Name: StepGatewayIdentity, Complete: inputs.GatewayIdentityReady},
		Step{Name: StepGatewayAccessEnforced, Complete: inputs.GatewayAccessEnforced},
		Step{Name: StepHandoffVerified, Complete: inputs.HandoffVerified},
		Step{Name: StepTemporaryAccessClosed, Complete: inputs.TemporaryAccessClosed},
		Step{Name: StepFirstOwnerRegistered, Complete: inputs.OwnerRegistered && inputs.BootstrapGrantDisabled},
	)
	complete := true
	for _, step := range steps {
		if !step.Complete {
			complete = false
		}
	}
	assessment := Assessment{Steps: steps, Complete: complete, Limitations: Limitations(mode)}
	if complete {
		host := strings.ToLower(strings.TrimSpace(inputs.ConsoleHost))
		if !safeHost.MatchString(host) || strings.Contains(host, "..") || !strings.Contains(host, ".") {
			return Assessment{}, fmt.Errorf("%w: %q", ErrInvalidConsoleHost, inputs.ConsoleHost)
		}
		assessment.ConsoleHandoffURL = "https://" + host
	}
	return assessment, nil
}
