// Package privatenetwork owns the LAN-only Private Network shape for the Local
// private administration handoff: a privately reachable Headscale coordination
// server and MagicDNS-style resolution of stable operator hostnames onto the
// Private Gateway. It deliberately encodes only the LAN-only shape — the
// coordination endpoint is never public and the launcher never writes permanent
// hosts-file entries; operator hostnames resolve through the tailnet's DNS.
package privatenetwork

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrInvalidReference is returned when a Private Network reference fails
// validation.
var ErrInvalidReference = errors.New("private network reference is invalid")

const (
	// Shape is the only Deployment Mode this package models. Internet-exposed and
	// Hetzner modes expose Headscale coordination publicly and are handled
	// elsewhere.
	Shape = "lan-only"
	// coordinationExposure records that the Headscale coordination server is
	// reachable only on the private network in the LAN-only shape.
	coordinationExposure = "private"
	// resolution records that operator hostnames resolve through tailnet MagicDNS
	// rather than any permanent hosts-file entry.
	resolution = "magic-dns"
	namespace  = "smallworlds"
)

// operatorServices are the operator interfaces that must resolve to the Private
// Gateway. The order is stable so the derived reference is deterministic.
var operatorServices = []string{"console", "grafana", "argocd"}

var safeProfileID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var safeDomain = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
var safeLabel = regexp.MustCompile(`^[a-z0-9-]{1,63}$`)

// OperatorEndpoint is a stable operator hostname that resolves onto the Private
// Gateway through the private tailnet DNS.
type OperatorEndpoint struct {
	Name   string `json:"name"`
	FQDN   string `json:"fqdn"`
	Target string `json:"target"`
}

// Reference is the secret-free description of a profile's LAN-only Private
// Network. It is safe to persist and to bind into a Change Plan; it contains no
// Headscale key material.
type Reference struct {
	Shape                string             `json:"shape"`
	CoordinationExposure string             `json:"coordinationExposure"`
	Resolution           string             `json:"resolution"`
	BaseDomain           string             `json:"baseDomain"`
	Namespace            string             `json:"namespace"`
	CoordinationHost     string             `json:"coordinationHost"`
	GatewayHostname      string             `json:"gatewayHostname"`
	OperatorEndpoints    []OperatorEndpoint `json:"operatorEndpoints"`
}

// Plan deterministically derives the LAN-only Private Network reference for a
// profile under a private base domain. Every operator hostname is derived from
// the base domain and targets the single stable Private Gateway hostname.
func Plan(profileID, baseDomain string) (Reference, error) {
	if !safeProfileID.MatchString(profileID) {
		return Reference{}, fmt.Errorf("%w: profile", ErrInvalidReference)
	}
	baseDomain = strings.ToLower(strings.TrimSpace(baseDomain))
	if !safeDomain.MatchString(baseDomain) || strings.Contains(baseDomain, "..") || !strings.Contains(baseDomain, ".") {
		return Reference{}, fmt.Errorf("%w: base domain", ErrInvalidReference)
	}
	gateway := "gateway." + baseDomain
	reference := Reference{
		Shape:                Shape,
		CoordinationExposure: coordinationExposure,
		Resolution:           resolution,
		BaseDomain:           baseDomain,
		Namespace:            namespace,
		CoordinationHost:     "headscale." + baseDomain,
		GatewayHostname:      gateway,
	}
	for _, service := range operatorServices {
		reference.OperatorEndpoints = append(reference.OperatorEndpoints, OperatorEndpoint{Name: service, FQDN: service + "." + baseDomain, Target: gateway})
	}
	if err := reference.Validate(); err != nil {
		return Reference{}, err
	}
	return reference, nil
}

// Validate enforces the LAN-only invariants: private coordination, MagicDNS
// resolution, and every operator hostname resolving to the Private Gateway.
func (reference Reference) Validate() error {
	if reference.Shape != Shape {
		return fmt.Errorf("%w: shape", ErrInvalidReference)
	}
	if reference.CoordinationExposure != coordinationExposure {
		return fmt.Errorf("%w: coordination must be private in the LAN-only shape", ErrInvalidReference)
	}
	if reference.Resolution != resolution {
		return fmt.Errorf("%w: resolution", ErrInvalidReference)
	}
	if !safeDomain.MatchString(reference.BaseDomain) || strings.Contains(reference.BaseDomain, "..") || !strings.Contains(reference.BaseDomain, ".") {
		return fmt.Errorf("%w: base domain", ErrInvalidReference)
	}
	if !safeLabel.MatchString(reference.Namespace) {
		return fmt.Errorf("%w: namespace", ErrInvalidReference)
	}
	if !isSubdomainOf(reference.CoordinationHost, reference.BaseDomain) {
		return fmt.Errorf("%w: coordination host", ErrInvalidReference)
	}
	if !isSubdomainOf(reference.GatewayHostname, reference.BaseDomain) {
		return fmt.Errorf("%w: gateway hostname", ErrInvalidReference)
	}
	if len(reference.OperatorEndpoints) != len(operatorServices) {
		return fmt.Errorf("%w: operator endpoints", ErrInvalidReference)
	}
	seen := make(map[string]bool, len(reference.OperatorEndpoints))
	for index, endpoint := range reference.OperatorEndpoints {
		if endpoint.Name != operatorServices[index] {
			return fmt.Errorf("%w: operator endpoint order", ErrInvalidReference)
		}
		if endpoint.FQDN != endpoint.Name+"."+reference.BaseDomain {
			return fmt.Errorf("%w: operator endpoint hostname", ErrInvalidReference)
		}
		if endpoint.Target != reference.GatewayHostname {
			return fmt.Errorf("%w: operator endpoint must target the Private Gateway", ErrInvalidReference)
		}
		if seen[endpoint.FQDN] {
			return fmt.Errorf("%w: duplicate operator endpoint", ErrInvalidReference)
		}
		seen[endpoint.FQDN] = true
	}
	return nil
}

// Marshal returns the canonical secret-free JSON for a validated reference.
func (reference Reference) Marshal() (string, error) {
	if err := reference.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(reference)
	if err != nil {
		return "", fmt.Errorf("marshal private network reference: %w", err)
	}
	return string(encoded), nil
}

// ParseReference decodes and validates a stored reference.
func ParseReference(encoded string) (Reference, error) {
	var reference Reference
	decoder := json.NewDecoder(strings.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reference); err != nil {
		return Reference{}, fmt.Errorf("%w: json", ErrInvalidReference)
	}
	if err := reference.Validate(); err != nil {
		return Reference{}, err
	}
	return reference, nil
}

// Digest returns a stable content digest binding the network shape and its
// hostnames into a plan.
func (reference Reference) Digest() (string, error) {
	encoded, err := reference.Marshal()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(encoded))
	return hex.EncodeToString(sum[:]), nil
}

// GenerateCoordinationSecret returns fresh random Headscale coordination secret
// material for Launcher Vault custody. It never appears in a Reference.
func GenerateCoordinationSecret() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate coordination secret: %w", err)
	}
	return base64.RawStdEncoding.EncodeToString(buffer), nil
}

func isSubdomainOf(host, baseDomain string) bool {
	if !safeDomain.MatchString(host) || strings.Contains(host, "..") {
		return false
	}
	return strings.HasSuffix(host, "."+baseDomain) && host != baseDomain
}
