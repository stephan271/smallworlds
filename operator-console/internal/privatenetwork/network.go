// Package privatenetwork owns the Private Network shape behind every private
// administration handoff: a Headscale coordination server and MagicDNS-style
// resolution of stable operator hostnames onto a single Private Gateway.
//
// Two shapes exist, and the difference between them is exactly one thing — where
// coordination is reachable. A LAN-only installation has no public address at
// all, so coordination is private and the whole tailnet is reachable only from
// the local network. A publicly addressed installation (Hetzner, or a local node
// exposed to the internet) must publish coordination so an Operator can join
// their device from anywhere; that is what makes remote administration possible
// at all.
//
// What does *not* differ, in either shape, is the property the handoff depends
// on: operator interfaces resolve only through the tailnet's DNS onto the
// Private Gateway, never through a public record and never through a permanent
// hosts-file entry. A publicly reachable coordination endpoint is not a publicly
// reachable console.
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
	"sort"
	"strings"
)

// ErrInvalidReference is returned when a Private Network reference fails
// validation.
var ErrInvalidReference = errors.New("private network reference is invalid")

// Shape is how a Private Network's coordination endpoint is reached.
type Shape string

const (
	// LANOnly keeps Headscale coordination on the local network. Nothing about
	// the installation is reachable from the internet.
	LANOnly Shape = "lan-only"
	// PublicCoordination publishes the Headscale coordination endpoint under the
	// installation's public domain, so an Operator Device can join the tailnet
	// from outside the local network. Operator interfaces stay private.
	PublicCoordination Shape = "public-coordination"
)

// Valid reports whether a shape is one this package models.
func (shape Shape) Valid() bool { return shape == LANOnly || shape == PublicCoordination }

const (
	// exposurePrivate and exposurePublic record where coordination is reachable.
	// The value is derived from the shape and validated against it, so a
	// reference cannot claim to be LAN-only while publishing coordination.
	exposurePrivate = "private"
	exposurePublic  = "public"
	// resolution records that operator hostnames resolve through tailnet MagicDNS
	// rather than any permanent hosts-file entry.
	resolution = "magic-dns"
	namespace  = "smallworlds"
	// coordinationLabel is the subdomain coordination is published at in the
	// public shape. It matches the `vpn` record the installation's DNS already
	// maintains, so coordination needs no record of its own.
	coordinationLabel = "vpn"
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

// Reference is the secret-free description of a profile's Private Network. It is
// safe to persist and to bind into a Change Plan; it contains no Headscale key
// material.
type Reference struct {
	Shape                Shape              `json:"shape"`
	CoordinationExposure string             `json:"coordinationExposure"`
	Resolution           string             `json:"resolution"`
	BaseDomain           string             `json:"baseDomain"`
	Namespace            string             `json:"namespace"`
	CoordinationHost     string             `json:"coordinationHost"`
	GatewayHostname      string             `json:"gatewayHostname"`
	OperatorEndpoints    []OperatorEndpoint `json:"operatorEndpoints"`
	// PublicDomain is the installation's publicly resolvable domain, set only in
	// the public-coordination shape. Coordination is published under it.
	PublicDomain string `json:"publicDomain,omitempty"`
	// PublishedHostnames are the names that genuinely have public DNS records —
	// the community's own services. No operator hostname may be one of them, and
	// Validate enforces that: an operator interface sharing a name with a
	// published record is an operator interface with a public route.
	PublishedHostnames []string `json:"publishedHostnames,omitempty"`
}

// Input is what a Private Network is derived from.
type Input struct {
	Shape     Shape
	ProfileID string
	// BaseDomain is where the operator hostnames live. It is resolved only
	// through the tailnet, in both shapes.
	BaseDomain string
	// PublicDomain is required in the public-coordination shape and rejected in
	// the LAN-only one, where no public name exists to publish under.
	PublicDomain string
	// PublishedHostnames are the installation's public DNS records, used to prove
	// no operator hostname collides with one.
	PublishedHostnames []string
}

// Plan deterministically derives a profile's Private Network reference. Every
// operator hostname is derived from the base domain and targets the single
// stable Private Gateway hostname; only the coordination endpoint differs
// between the two shapes.
func Plan(input Input) (Reference, error) {
	if !input.Shape.Valid() {
		return Reference{}, fmt.Errorf("%w: shape", ErrInvalidReference)
	}
	if !safeProfileID.MatchString(input.ProfileID) {
		return Reference{}, fmt.Errorf("%w: profile", ErrInvalidReference)
	}
	baseDomain := strings.ToLower(strings.TrimSpace(input.BaseDomain))
	if !safeDomain.MatchString(baseDomain) || strings.Contains(baseDomain, "..") || !strings.Contains(baseDomain, ".") {
		return Reference{}, fmt.Errorf("%w: base domain", ErrInvalidReference)
	}
	gateway := "gateway." + baseDomain
	reference := Reference{
		Shape:           input.Shape,
		Resolution:      resolution,
		BaseDomain:      baseDomain,
		Namespace:       namespace,
		GatewayHostname: gateway,
	}
	switch input.Shape {
	case LANOnly:
		if strings.TrimSpace(input.PublicDomain) != "" {
			return Reference{}, fmt.Errorf("%w: a LAN-only network publishes no public domain", ErrInvalidReference)
		}
		reference.CoordinationExposure = exposurePrivate
		reference.CoordinationHost = "headscale." + baseDomain
	case PublicCoordination:
		publicDomain := strings.ToLower(strings.TrimSpace(input.PublicDomain))
		if !safeDomain.MatchString(publicDomain) || strings.Contains(publicDomain, "..") || !strings.Contains(publicDomain, ".") {
			return Reference{}, fmt.Errorf("%w: public domain", ErrInvalidReference)
		}
		reference.CoordinationExposure = exposurePublic
		reference.PublicDomain = publicDomain
		reference.CoordinationHost = coordinationLabel + "." + publicDomain
	}
	reference.PublishedHostnames = normalizeHosts(input.PublishedHostnames)
	for _, service := range operatorServices {
		reference.OperatorEndpoints = append(reference.OperatorEndpoints, OperatorEndpoint{Name: service, FQDN: service + "." + baseDomain, Target: gateway})
	}
	if err := reference.Validate(); err != nil {
		return Reference{}, err
	}
	return reference, nil
}

func normalizeHosts(hosts []string) []string {
	if len(hosts) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(hosts))
	seen := make(map[string]bool, len(hosts))
	for _, host := range hosts {
		value := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized
}

// Validate enforces the invariants every shape shares — MagicDNS resolution and
// every operator hostname resolving to the Private Gateway — plus the ones that
// distinguish the shapes, so a reference can never claim to be LAN-only while
// publishing coordination, or claim public coordination without a public domain
// to publish it under.
func (reference Reference) Validate() error {
	if !reference.Shape.Valid() {
		return fmt.Errorf("%w: shape", ErrInvalidReference)
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
	if err := reference.validateCoordination(); err != nil {
		return err
	}
	if err := reference.validateNoPublicOperatorRoute(); err != nil {
		return err
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

// validateCoordination pins the coordination endpoint to its shape: private and
// under the operator base domain when LAN-only, public and under the public
// domain when publicly coordinated.
func (reference Reference) validateCoordination() error {
	switch reference.Shape {
	case LANOnly:
		if reference.CoordinationExposure != exposurePrivate {
			return fmt.Errorf("%w: coordination must be private in the LAN-only shape", ErrInvalidReference)
		}
		if reference.PublicDomain != "" {
			return fmt.Errorf("%w: a LAN-only network has no public domain", ErrInvalidReference)
		}
		if !isSubdomainOf(reference.CoordinationHost, reference.BaseDomain) {
			return fmt.Errorf("%w: coordination host", ErrInvalidReference)
		}
	case PublicCoordination:
		if reference.CoordinationExposure != exposurePublic {
			return fmt.Errorf("%w: coordination must be public in the public-coordination shape", ErrInvalidReference)
		}
		if !safeDomain.MatchString(reference.PublicDomain) || strings.Contains(reference.PublicDomain, "..") || !strings.Contains(reference.PublicDomain, ".") {
			return fmt.Errorf("%w: public domain", ErrInvalidReference)
		}
		if !isSubdomainOf(reference.CoordinationHost, reference.PublicDomain) {
			return fmt.Errorf("%w: coordination host must be published under the public domain", ErrInvalidReference)
		}
	}
	return nil
}

// validateNoPublicOperatorRoute is the invariant that keeps a publicly addressed
// installation from acquiring a public console. Publishing coordination is
// necessary — an Operator has to be able to join the tailnet from outside — but
// an operator interface that shares a name with a published DNS record has a
// public route to it, which is precisely what must not exist.
func (reference Reference) validateNoPublicOperatorRoute() error {
	if len(reference.PublishedHostnames) == 0 {
		return nil
	}
	published := make(map[string]bool, len(reference.PublishedHostnames))
	for _, host := range reference.PublishedHostnames {
		published[host] = true
	}
	for _, endpoint := range reference.OperatorEndpoints {
		if published[endpoint.FQDN] {
			return fmt.Errorf("%w: operator hostname %q also has a public DNS record", ErrInvalidReference, endpoint.FQDN)
		}
	}
	if published[reference.GatewayHostname] {
		return fmt.Errorf("%w: the Private Gateway must not have a public DNS record", ErrInvalidReference)
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
