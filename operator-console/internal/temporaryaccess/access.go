// Package temporaryaccess owns the temporary public administration path — SSH
// and the Kubernetes API — that exists only between provisioning a cluster and
// handing it over to private administration.
//
// Two rules shape it, and they pull in opposite directions.
//
// The path should be as narrow as possible: an SSH port open to the internet on
// a fresh node is the single most attacked surface an installation has. So it is
// scoped to the Operator's own address whenever that address can be observed and
// is actually usable as a scope.
//
// But narrowing it wrongly is worse than not narrowing it. An Operator behind a
// carrier-grade NAT, a mobile connection, or a dynamic residential address can
// lose that address mid-setup — and the path they would have used to recover is
// the one just closed to them. So "where feasible" is a real condition, decided
// here and stated explicitly, rather than a default that quietly locks people
// out.
package temporaryaccess

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"
)

// ErrInvalidState is returned when a temporary access record is inconsistent.
var ErrInvalidState = errors.New("temporary access state is invalid")

// ErrClosureNotPermitted is returned when closing the path is attempted before
// the handoff has been verified.
var ErrClosureNotPermitted = errors.New("temporary access may not be closed before the handoff is verified")

// Reason keys explaining a scope decision. They are stable and translatable, and
// each says what an Operator can do about it.
const (
	// ReasonScopedToOperator is the good case: the path admits one address.
	ReasonScopedToOperator = "scoped-to-operator-address"
	// ReasonAddressUnobserved means the launcher has not yet learned the
	// Operator's public address — there is nothing to scope to.
	ReasonAddressUnobserved = "operator-address-not-observed"
	// ReasonAddressNotRoutable means the observed address is private, loopback,
	// or otherwise not the address the provider will see, so scoping to it would
	// admit nobody at all.
	ReasonAddressNotRoutable = "operator-address-not-publicly-routable"
	// ReasonAddressShared means the address is carrier-grade NAT space: it is
	// shared with strangers and it changes, so scoping to it is both weaker than
	// it looks and liable to lock the Operator out.
	ReasonAddressShared = "operator-address-carrier-grade-nat"
)

// Scope is how the temporary administration path is restricted.
type Scope struct {
	// Scoped is true only when Sources actually narrows the path.
	Scoped bool `json:"scoped"`
	// Sources are the CIDR ranges admitted to SSH and the Kubernetes API. Empty
	// means the path is open to the internet.
	Sources []string `json:"sources,omitempty"`
	// ReasonKey states why the path is scoped or why it could not be.
	ReasonKey string `json:"reasonKey"`
}

// carrierGradeNAT is 100.64.0.0/10 (RFC 6598). An address here is shared with
// other customers of the same carrier and is reassigned without warning.
var carrierGradeNAT = netip.MustParsePrefix("100.64.0.0/10")

// DeriveScope decides whether the Operator's observed public address can narrow
// the temporary path.
//
// An empty address is not an error: not knowing where the Operator is coming
// from is the ordinary case before anything has observed them, and it yields an
// open path with the reason stated rather than a refusal.
func DeriveScope(observedAddress string) Scope {
	address := strings.TrimSpace(observedAddress)
	if address == "" {
		return Scope{ReasonKey: ReasonAddressUnobserved}
	}
	parsed, err := netip.ParseAddr(address)
	if err != nil {
		return Scope{ReasonKey: ReasonAddressUnobserved}
	}
	parsed = parsed.Unmap()
	switch {
	case !parsed.IsGlobalUnicast() || parsed.IsPrivate() || parsed.IsLoopback() || parsed.IsLinkLocalUnicast():
		// The provider sees the address the Operator's traffic arrives from, not
		// this one. Scoping to it would admit nobody.
		return Scope{ReasonKey: ReasonAddressNotRoutable}
	case parsed.Is4() && carrierGradeNAT.Contains(parsed):
		return Scope{ReasonKey: ReasonAddressShared}
	}
	// A single-host prefix: /32 for IPv4, /128 for IPv6. Widening to the
	// Operator's whole subnet would admit their entire ISP allocation.
	bits := parsed.BitLen()
	return Scope{
		Scoped:    true,
		Sources:   []string{netip.PrefixFrom(parsed, bits).String()},
		ReasonKey: ReasonScopedToOperator,
	}
}

// State is the durable record of the temporary path for one Cluster Profile.
type State struct {
	// Open is false once the path has been closed. Closing is deliberately
	// one-way within a run: re-opening is a new, separately approved decision.
	Open  bool  `json:"open"`
	Scope Scope `json:"scope"`
	// ObservedAddress is the Operator address the scope was derived from, kept
	// so a later re-scope can tell "unchanged" from "moved".
	ObservedAddress string    `json:"observedAddress,omitempty"`
	OpenedAt        time.Time `json:"openedAt,omitempty"`
	ClosedAt        time.Time `json:"closedAt,omitempty"`
}

// Open records the temporary path as open under a scope derived from the
// observed Operator address.
func Open(observedAddress string, at time.Time) State {
	return State{
		Open:            true,
		Scope:           DeriveScope(observedAddress),
		ObservedAddress: strings.TrimSpace(observedAddress),
		OpenedAt:        at.UTC(),
	}
}

// Narrow re-derives the scope from a newly observed address without reopening a
// closed path. An Operator whose address becomes observable partway through the
// journey gets the narrower path without having to start over.
func (state State) Narrow(observedAddress string, at time.Time) (State, error) {
	if !state.Open {
		return State{}, fmt.Errorf("%w: the temporary path is already closed", ErrInvalidState)
	}
	narrowed := state
	narrowed.Scope = DeriveScope(observedAddress)
	narrowed.ObservedAddress = strings.TrimSpace(observedAddress)
	if narrowed.OpenedAt.IsZero() {
		narrowed.OpenedAt = at.UTC()
	}
	return narrowed, nil
}

// Close removes the temporary path. It refuses unless the handoff verification
// permits closure — that gate is the whole reason this type exists, because
// closing the path with private access unverified leaves an Operator with no way
// into the cluster at all.
func (state State) Close(verificationPermitsClosure bool, at time.Time) (State, error) {
	if !verificationPermitsClosure {
		return State{}, ErrClosureNotPermitted
	}
	if !state.Open {
		// Already closed: closing again is a no-op rather than an error, so a
		// retried request cannot turn a completed handoff into a failure.
		return state, nil
	}
	closed := state
	closed.Open = false
	closed.ClosedAt = at.UTC()
	return closed, nil
}

// Validate rejects a record that could not have arisen from this type's own
// operations, so a corrupted or hand-edited record cannot claim a closed path
// was never opened.
func (state State) Validate() error {
	if state.Scope.ReasonKey == "" {
		return fmt.Errorf("%w: scope reason", ErrInvalidState)
	}
	if state.Scope.Scoped == (len(state.Scope.Sources) == 0) {
		return fmt.Errorf("%w: a scoped path must name its sources and an open one must not", ErrInvalidState)
	}
	for _, source := range state.Scope.Sources {
		if _, _, err := net.ParseCIDR(source); err != nil {
			return fmt.Errorf("%w: source %q", ErrInvalidState, source)
		}
	}
	if !state.Open && state.ClosedAt.IsZero() {
		return fmt.Errorf("%w: a closed path must record when it closed", ErrInvalidState)
	}
	if state.Open && !state.ClosedAt.IsZero() {
		return fmt.Errorf("%w: an open path cannot have closed", ErrInvalidState)
	}
	return nil
}

// AddressObserver reports the public address the Operator's launcher reaches the
// cluster from.
//
// It is an interface because there is no way to learn this locally: the launcher
// runs on the Operator's own machine behind whatever NAT they have. The
// production implementation asks the provisioned node what source address it saw
// the launcher connect from — the one observation that involves no third party
// and no guessing.
type AddressObserver interface {
	ObserveOperatorAddress(host string) (string, error)
}

// UnobservedAddress is the default: it observes nothing, so the path stays open
// and says so. It is deliberately not a guess — an incorrect scope is the
// failure this package exists to avoid.
type UnobservedAddress struct{}

// ObserveOperatorAddress reports no address.
func (UnobservedAddress) ObserveOperatorAddress(string) (string, error) { return "", nil }
