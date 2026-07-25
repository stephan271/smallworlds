package ephemeralcluster_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/ephemeralcluster"
)

func countingGuard(t *testing.T, limits ephemeralcluster.Limits, destroyErr error) (*ephemeralcluster.Guard, *atomic.Int32) {
	t.Helper()
	var destroys atomic.Int32
	guard, err := ephemeralcluster.NewGuard(limits, func(context.Context) error {
		destroys.Add(1)
		return destroyErr
	})
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	return guard, &destroys
}

func smallLimits() ephemeralcluster.Limits {
	return ephemeralcluster.Limits{MaxMonthlyEUR: 60, MaxDuration: time.Minute}
}

// An absent limit is not a permissive setting. A guard that cannot bound cost or
// time, or cannot clean up, reads like protection while providing none.
func TestNewGuardRefusesAnEnvelopeThatBoundsNothing(t *testing.T) {
	destroy := func(context.Context) error { return nil }
	for name, limits := range map[string]ephemeralcluster.Limits{
		"no cost limit":     {MaxDuration: time.Minute},
		"no time limit":     {MaxMonthlyEUR: 10},
		"negative cost":     {MaxMonthlyEUR: -1, MaxDuration: time.Minute},
		"negative duration": {MaxMonthlyEUR: 10, MaxDuration: -time.Minute},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ephemeralcluster.NewGuard(limits, destroy); !errors.Is(err, ephemeralcluster.ErrInvalidLimits) {
				t.Fatalf("err = %v, want ErrInvalidLimits", err)
			}
		})
	}
	if _, err := ephemeralcluster.NewGuard(smallLimits(), nil); !errors.Is(err, ephemeralcluster.ErrInvalidLimits) {
		t.Fatalf("err = %v, want a guard without a destroyer refused", err)
	}
	if err := ephemeralcluster.DefaultLimits().Validate(); err != nil {
		t.Fatalf("the default envelope is unusable: %v", err)
	}
}

// The cost cap is checked before approval, so a plan that is too expensive never
// becomes infrastructure at all.
func TestAdmitRefusesAPlanOverTheCostLimit(t *testing.T) {
	guard, destroys := countingGuard(t, smallLimits(), nil)
	if err := guard.Admit(37.68); err != nil {
		t.Fatalf("a plan within the limit was refused: %v", err)
	}
	if err := guard.Admit(120); !errors.Is(err, ephemeralcluster.ErrCostRefused) {
		t.Fatalf("err = %v, want ErrCostRefused", err)
	}
	// A plan with no stated cost cannot be checked, so it is refused rather than
	// treated as free.
	if err := guard.Admit(0); !errors.Is(err, ephemeralcluster.ErrCostRefused) {
		t.Fatalf("err = %v, want a costless plan refused", err)
	}
	if destroys.Load() != 0 {
		t.Fatal("admission must not destroy anything")
	}
}

// Cleanup on the happy path, and exactly once however many times it is asked
// for — a second destroy of a profile another run now owns would be worse than
// the leak it is trying to prevent.
func TestRunCleansUpExactlyOnceOnSuccess(t *testing.T) {
	guard, destroys := countingGuard(t, smallLimits(), nil)
	if err := guard.Run(context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !guard.CleanedUp() || destroys.Load() != 1 {
		t.Fatalf("destroys = %d, cleanedUp = %v, want exactly one cleanup", destroys.Load(), guard.CleanedUp())
	}
	if err := guard.Cleanup(context.Background()); err != nil {
		t.Fatalf("repeat cleanup: %v", err)
	}
	if destroys.Load() != 1 {
		t.Fatalf("destroys = %d, want cleanup to be idempotent", destroys.Load())
	}
}

// The work failing is the ordinary case for a test that is finding a bug. It
// must not also leak a server.
func TestRunCleansUpWhenTheWorkFails(t *testing.T) {
	guard, destroys := countingGuard(t, smallLimits(), nil)
	failure := errors.New("cluster never converged")
	err := guard.Run(context.Background(), func(context.Context) error { return failure })
	if !errors.Is(err, failure) {
		t.Fatalf("err = %v, want the work's own failure", err)
	}
	if destroys.Load() != 1 {
		t.Fatalf("destroys = %d, want cleanup after a failed run", destroys.Load())
	}
}

// A panicking test is exactly the one most likely to leak, because a naive
// deferred cleanup in the test body may never be reached.
func TestRunCleansUpAndRepanicsOnAPanic(t *testing.T) {
	guard, destroys := countingGuard(t, smallLimits(), nil)
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("the panic must not be swallowed")
		}
		if destroys.Load() != 1 {
			t.Fatalf("destroys = %d, want cleanup before the panic propagated", destroys.Load())
		}
	}()
	_ = guard.Run(context.Background(), func(context.Context) error { panic("assertion blew up") })
}

// Past the deadline the work is cancelled and the resources are destroyed
// anyway — and the cleanup gets a live context, because a run that timed out is
// precisely the one that most needs to be able to destroy what it made.
func TestRunEnforcesTheTimeLimitAndStillCleansUp(t *testing.T) {
	var cleanupContextLive bool
	guard, err := ephemeralcluster.NewGuard(ephemeralcluster.Limits{MaxMonthlyEUR: 60, MaxDuration: 20 * time.Millisecond}, func(ctx context.Context) error {
		cleanupContextLive = ctx.Err() == nil
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	runErr := guard.Run(context.Background(), func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(runErr, ephemeralcluster.ErrDeadlineExceeded) {
		t.Fatalf("err = %v, want ErrDeadlineExceeded", runErr)
	}
	if !guard.CleanedUp() {
		t.Fatal("a timed-out run must still be cleaned up")
	}
	if !cleanupContextLive {
		t.Fatal("cleanup was handed an already-cancelled context and could not have destroyed anything")
	}
}

// A leaked paid resource is never reported as some other failure: the whole
// point is that somebody notices.
func TestCleanupFailureIsReportedLoudlyAndNeverAbsorbed(t *testing.T) {
	guard, _ := countingGuard(t, smallLimits(), errors.New("provider returned 500"))
	err := guard.Run(context.Background(), func(context.Context) error { return nil })
	if !errors.Is(err, ephemeralcluster.ErrCleanupFailed) {
		t.Fatalf("err = %v, want ErrCleanupFailed", err)
	}

	// Even when the work itself failed, the leak is the error the caller sees.
	guard, _ = countingGuard(t, smallLimits(), errors.New("provider returned 500"))
	workFailure := errors.New("cluster never converged")
	err = guard.Run(context.Background(), func(context.Context) error { return workFailure })
	if !errors.Is(err, ephemeralcluster.ErrCleanupFailed) {
		t.Fatalf("err = %v, want the leak reported over the work's failure", err)
	}
	if err.Error() == "" || !containsAll(err.Error(), "cleanup failed", "never converged") {
		t.Fatalf("err = %q, want both the leak and the underlying failure stated", err)
	}
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		found := false
		for index := 0; index+len(part) <= len(value); index++ {
			if value[index:index+len(part)] == part {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
