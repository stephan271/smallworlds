// Package handoffassessment composes the final Setup Journey assessment for the
// LAN-only private administration handoff. It reports the completion of every
// prior step, always states the LAN-only limitations, and — once the handoff is
// complete — provides the in-cluster Operator Console handoff URL.
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

// LANOnlyLimitations are always surfaced in the final assessment so the Operator
// understands what a LAN-only deployment does and does not provide.
var LANOnlyLimitations = []string{
	"Operator interfaces are reachable only from devices joined to the private network; there is no remote administration over the public internet.",
	"Each Operator Device must install the Cluster CA root to trust operator interfaces over HTTPS.",
	"No router port is opened and no public DNS is published; the cluster is not reachable from outside the private network.",
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

// Evaluate composes the ordered steps, the always-present LAN-only limitations,
// and — only when every step is complete — the in-cluster console handoff URL.
func Evaluate(inputs Inputs) (Assessment, error) {
	steps := []Step{
		{Name: StepClusterCATrust, Complete: inputs.DeviceTrustInstalled},
		{Name: StepPrivateNetwork, Complete: inputs.PrivateNetworkReady},
		{Name: StepLauncherEnrolled, Complete: inputs.LauncherEnrolled},
		{Name: StepGatewayIdentity, Complete: inputs.GatewayIdentityReady},
		{Name: StepGatewayAccessEnforced, Complete: inputs.GatewayAccessEnforced},
		{Name: StepHandoffVerified, Complete: inputs.HandoffVerified},
		{Name: StepTemporaryAccessClosed, Complete: inputs.TemporaryAccessClosed},
		{Name: StepFirstOwnerRegistered, Complete: inputs.OwnerRegistered && inputs.BootstrapGrantDisabled},
	}
	complete := true
	for _, step := range steps {
		if !step.Complete {
			complete = false
		}
	}
	assessment := Assessment{Steps: steps, Complete: complete, Limitations: append([]string(nil), LANOnlyLimitations...)}
	if complete {
		host := strings.ToLower(strings.TrimSpace(inputs.ConsoleHost))
		if !safeHost.MatchString(host) || strings.Contains(host, "..") || !strings.Contains(host, ".") {
			return Assessment{}, fmt.Errorf("%w: %q", ErrInvalidConsoleHost, inputs.ConsoleHost)
		}
		assessment.ConsoleHandoffURL = "https://" + host
	}
	return assessment, nil
}
