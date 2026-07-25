package temporaryaccess_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/temporaryaccess"
)

var at = time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)

// A routable address narrows the path to exactly one host. Widening to the
// Operator's subnet would admit their whole ISP allocation, which is barely a
// narrowing at all.
func TestDeriveScopeNarrowsToASingleOperatorHost(t *testing.T) {
	for address, want := range map[string]string{
		"198.51.100.7":       "198.51.100.7/32",
		"2001:db8::1":        "2001:db8::1/128",
		"::ffff:203.0.113.5": "203.0.113.5/32",
	} {
		scope := temporaryaccess.DeriveScope(address)
		if !scope.Scoped || len(scope.Sources) != 1 || scope.Sources[0] != want {
			t.Fatalf("%s: scope = %+v, want a single host %s", address, scope, want)
		}
		if scope.ReasonKey != temporaryaccess.ReasonScopedToOperator {
			t.Fatalf("%s: reason = %q", address, scope.ReasonKey)
		}
	}
}

// Each of these would produce a scope that admits the wrong people — nobody, or
// strangers plus a moving target. The path stays open and says why, because an
// Operator locked out of a fresh cluster has no second path in.
func TestDeriveScopeRefusesToNarrowWhenNarrowingWouldMislead(t *testing.T) {
	cases := map[string]struct {
		address string
		reason  string
	}{
		"nothing observed":  {"", temporaryaccess.ReasonAddressUnobserved},
		"not an address":    {"not-an-address", temporaryaccess.ReasonAddressUnobserved},
		"private network":   {"192.168.178.52", temporaryaccess.ReasonAddressNotRoutable},
		"private class A":   {"10.1.2.3", temporaryaccess.ReasonAddressNotRoutable},
		"loopback":          {"127.0.0.1", temporaryaccess.ReasonAddressNotRoutable},
		"link local":        {"169.254.1.1", temporaryaccess.ReasonAddressNotRoutable},
		"carrier-grade NAT": {"100.70.1.1", temporaryaccess.ReasonAddressShared},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			scope := temporaryaccess.DeriveScope(testCase.address)
			if scope.Scoped || len(scope.Sources) != 0 {
				t.Fatalf("scope = %+v, want an unscoped path", scope)
			}
			if scope.ReasonKey != testCase.reason {
				t.Fatalf("reason = %q, want %q", scope.ReasonKey, testCase.reason)
			}
		})
	}
}

// This is the criterion the package exists for: the path is removed only after
// the handoff has been verified.
func TestCloseRequiresAVerifiedHandoff(t *testing.T) {
	state := temporaryaccess.Open("198.51.100.7", at)
	if _, err := state.Close(false, at); !errors.Is(err, temporaryaccess.ErrClosureNotPermitted) {
		t.Fatalf("err = %v, want closure refused before verification", err)
	}
	closed, err := state.Close(true, at)
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if closed.Open || closed.ClosedAt.IsZero() {
		t.Fatalf("state = %+v, want a closed path with a recorded time", closed)
	}
	if err := closed.Validate(); err != nil {
		t.Fatalf("closed state is invalid: %v", err)
	}

	// A retried close must not turn a completed handoff into a failure.
	again, err := closed.Close(true, at.Add(time.Minute))
	if err != nil || again.ClosedAt != closed.ClosedAt {
		t.Fatalf("state = %+v, err = %v, want an idempotent close", again, err)
	}
	// Even a retry cannot bypass the gate.
	if _, err := closed.Close(false, at); !errors.Is(err, temporaryaccess.ErrClosureNotPermitted) {
		t.Fatal("an unverified close was permitted on an already-closed path")
	}
}

// An Operator whose address becomes observable partway through gets the narrower
// path without restarting the journey — but narrowing must never reopen a path
// that was already closed.
func TestNarrowTightensAnOpenPathAndNeverReopensAClosedOne(t *testing.T) {
	state := temporaryaccess.Open("", at)
	if state.Scope.Scoped {
		t.Fatal("an unobserved address must not produce a scoped path")
	}
	narrowed, err := state.Narrow("198.51.100.7", at)
	if err != nil {
		t.Fatalf("Narrow: %v", err)
	}
	if !narrowed.Scope.Scoped || narrowed.Scope.Sources[0] != "198.51.100.7/32" {
		t.Fatalf("state = %+v, want the path narrowed to the observed address", narrowed)
	}
	if narrowed.OpenedAt != state.OpenedAt {
		t.Fatal("narrowing must not change when the path was opened")
	}

	closed, err := narrowed.Close(true, at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := closed.Narrow("203.0.113.1", at); !errors.Is(err, temporaryaccess.ErrInvalidState) {
		t.Fatalf("err = %v, want narrowing a closed path refused", err)
	}
}

// A record that could not have come from this type's own operations is refused,
// so a corrupted store cannot claim a closed path was never opened or that an
// unscoped path admits a range.
func TestValidateRejectsInconsistentRecords(t *testing.T) {
	for name, state := range map[string]temporaryaccess.State{
		"no reason": {Open: true},
		"scoped without sources": {
			Open: true, Scope: temporaryaccess.Scope{Scoped: true, ReasonKey: temporaryaccess.ReasonScopedToOperator},
		},
		"unscoped with sources": {
			Open: true, Scope: temporaryaccess.Scope{Sources: []string{"0.0.0.0/0"}, ReasonKey: temporaryaccess.ReasonAddressUnobserved},
		},
		"malformed source": {
			Open: true, Scope: temporaryaccess.Scope{Scoped: true, Sources: []string{"198.51.100.7"}, ReasonKey: temporaryaccess.ReasonScopedToOperator},
		},
		"closed without a time": {
			Scope: temporaryaccess.Scope{ReasonKey: temporaryaccess.ReasonAddressUnobserved},
		},
		"open yet closed": {
			Open: true, ClosedAt: at, Scope: temporaryaccess.Scope{ReasonKey: temporaryaccess.ReasonAddressUnobserved},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := state.Validate(); !errors.Is(err, temporaryaccess.ErrInvalidState) {
				t.Fatalf("err = %v, want ErrInvalidState", err)
			}
		})
	}

	if err := temporaryaccess.Open("198.51.100.7", at).Validate(); err != nil {
		t.Fatalf("a freshly opened scoped path is invalid: %v", err)
	}
	if err := temporaryaccess.Open("", at).Validate(); err != nil {
		t.Fatalf("a freshly opened unscoped path is invalid: %v", err)
	}
}

// The default observer guesses nothing. An incorrect scope is exactly the
// failure this package exists to avoid, so no address is better than a wrong one.
func TestDefaultObserverGuessesNothing(t *testing.T) {
	address, err := temporaryaccess.UnobservedAddress{}.ObserveOperatorAddress("node.example.org")
	if err != nil || address != "" {
		t.Fatalf("address = %q, err = %v, want no observation and no error", address, err)
	}
	if scope := temporaryaccess.DeriveScope(address); scope.Scoped {
		t.Fatal("the default observer must not produce a scoped path")
	}
}
