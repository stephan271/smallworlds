package operatordevice

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrInvalidGuidance is returned when enrollment guidance cannot be derived from
// the given Deployment Mode or Private Network.
var ErrInvalidGuidance = errors.New("operator device enrollment guidance is invalid")

// DeploymentMode mirrors the cluster's deployment mode. It is defined here (not
// imported) so the domain stays a leaf package; the console maps its own
// capability.DeploymentMode onto these values.
type DeploymentMode string

const (
	// Hetzner and LocalPublic obtain publicly trusted (Let's Encrypt)
	// certificates, so an Operator Device needs no extra trust installation.
	Hetzner     DeploymentMode = "hetzner"
	LocalPublic DeploymentMode = "local-public"
	// LocalLAN is the LAN-only mode: operator interfaces are served under a
	// private base domain with a self-signed Cluster CA, so a joining device must
	// explicitly install the Cluster CA root to trust them over HTTPS.
	LocalLAN DeploymentMode = "local-lan"
)

// Valid reports whether the mode is a recognized Deployment Mode.
func (mode DeploymentMode) Valid() bool {
	switch mode {
	case Hetzner, LocalPublic, LocalLAN:
		return true
	default:
		return false
	}
}

// RequiresClusterCATrust reports whether a device joining in this mode must
// install the Cluster CA root. Only the LAN-only self-signed mode does; the
// publicly trusted modes do not.
func (mode DeploymentMode) RequiresClusterCATrust() bool {
	return mode == LocalLAN
}

// StepKind is a typed enrollment step the joining device follows in order. The
// console/UI localizes each kind; the domain owns only the ordering and which
// steps apply.
type StepKind string

const (
	// StepAcquireTailscaleClient obtains the official, verified Tailscale client
	// (never an undocumented prerequisite) — installation requires elevation.
	StepAcquireTailscaleClient StepKind = "acquire-tailscale-client"
	// StepJoinPrivateNetwork joins the LAN-only Private Network with the
	// single-use Enrollment Invitation — bringing the tailnet up requires
	// elevation.
	StepJoinPrivateNetwork StepKind = "join-private-network"
	// StepConfigurePrivateDNS points the device at the Private Network's MagicDNS
	// so operator hostnames resolve onto the Private Gateway.
	StepConfigurePrivateDNS StepKind = "configure-private-dns"
	// StepInstallClusterCA installs the Cluster CA root so operator interfaces are
	// trusted over HTTPS. It appears only where the Deployment Mode requires it,
	// and installing a system trust root requires elevation.
	StepInstallClusterCA StepKind = "install-cluster-ca"
	// StepVerifyGatewayAccess confirms the device reaches operator hostnames
	// through the Private Gateway and that they are absent from public routes.
	StepVerifyGatewayAccess StepKind = "verify-gateway-access"
)

// EnrollmentStep is one ordered instruction in the enrollment path.
type EnrollmentStep struct {
	Kind StepKind `json:"kind"`
	// ElevationRequired flags steps that need explicit administrator elevation,
	// so the requirement is disclosed up front rather than hit mid-flow.
	ElevationRequired bool `json:"elevationRequired"`
}

// Guidance is the derived, deterministic enrollment path for a device joining in
// a given Deployment Mode. It names the operator hostnames the device must reach
// through the Private Gateway, and includes the Cluster CA trust step only where
// the mode requires it.
type Guidance struct {
	Mode                   DeploymentMode   `json:"mode"`
	ClusterCATrustRequired bool             `json:"clusterCaTrustRequired"`
	GatewayHostname        string           `json:"gatewayHostname"`
	OperatorHostnames      []string         `json:"operatorHostnames"`
	Steps                  []EnrollmentStep `json:"steps"`
}

var safeGuidanceHost = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

// EnrollmentGuidance derives the ordered enrollment steps for a device joining
// through the Private Gateway. operatorHostnames are the interfaces the joined
// device must reach privately; they must all be subdomains of the gateway's base
// domain so the verification step cannot point a device at a public host.
func EnrollmentGuidance(mode DeploymentMode, gatewayHostname string, operatorHostnames []string) (Guidance, error) {
	if !mode.Valid() {
		return Guidance{}, fmt.Errorf("%w: deployment mode", ErrInvalidGuidance)
	}
	gatewayHostname = strings.ToLower(strings.TrimSpace(gatewayHostname))
	if !isHostname(gatewayHostname) {
		return Guidance{}, fmt.Errorf("%w: gateway hostname", ErrInvalidGuidance)
	}
	if len(operatorHostnames) == 0 {
		return Guidance{}, fmt.Errorf("%w: no operator hostnames", ErrInvalidGuidance)
	}
	hosts := make([]string, 0, len(operatorHostnames))
	seen := make(map[string]bool, len(operatorHostnames))
	for _, host := range operatorHostnames {
		normalized := strings.ToLower(strings.TrimSpace(host))
		if !isHostname(normalized) {
			return Guidance{}, fmt.Errorf("%w: operator hostname %q", ErrInvalidGuidance, host)
		}
		if seen[normalized] {
			return Guidance{}, fmt.Errorf("%w: duplicate operator hostname", ErrInvalidGuidance)
		}
		seen[normalized] = true
		hosts = append(hosts, normalized)
	}

	caTrust := mode.RequiresClusterCATrust()
	steps := []EnrollmentStep{
		{Kind: StepAcquireTailscaleClient, ElevationRequired: true},
		{Kind: StepJoinPrivateNetwork, ElevationRequired: true},
		{Kind: StepConfigurePrivateDNS, ElevationRequired: false},
	}
	if caTrust {
		steps = append(steps, EnrollmentStep{Kind: StepInstallClusterCA, ElevationRequired: true})
	}
	steps = append(steps, EnrollmentStep{Kind: StepVerifyGatewayAccess, ElevationRequired: false})

	return Guidance{
		Mode:                   mode,
		ClusterCATrustRequired: caTrust,
		GatewayHostname:        gatewayHostname,
		OperatorHostnames:      hosts,
		Steps:                  steps,
	}, nil
}

func isHostname(host string) bool {
	return safeGuidanceHost.MatchString(host) && !strings.Contains(host, "..")
}
