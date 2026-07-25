// Package ephemeralcluster owns the safety envelope around a test that
// provisions real, paid infrastructure and then throws it away.
//
// Such a test is the only way to know the Hetzner journey actually works, and it
// is also the only thing in this repository that can quietly cost a community
// money. Two failures matter, and they are not symmetric:
//
//   - Creating more than intended is bad but bounded — the cost cap refuses a
//     plan before anything exists.
//   - Failing to destroy what was created is unbounded. A leaked server bills
//     forever, and nobody notices, because the test that leaked it passed.
//
// So cleanup here is not a deferred call that might not run. It runs on success,
// on failure, on panic, and on timeout; it runs exactly once; and a cleanup that
// itself fails is loud, because the alternative is a green test and a running
// server.
package ephemeralcluster

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrInvalidLimits is returned when the safety envelope is not usable.
	ErrInvalidLimits = errors.New("ephemeral cluster limits are invalid")
	// ErrCostRefused is returned when a plan would cost more than the cap. It is
	// returned before anything is created.
	ErrCostRefused = errors.New("ephemeral cluster plan exceeds the cost limit")
	// ErrDeadlineExceeded is returned when the run outlived its time limit. The
	// resources are still destroyed.
	ErrDeadlineExceeded = errors.New("ephemeral cluster run exceeded its time limit")
	// ErrCleanupFailed is returned when the resources could not be destroyed.
	// It is never merged into another error: a leaked paid resource must not be
	// reported as some other failure.
	ErrCleanupFailed = errors.New("ephemeral cluster cleanup failed; resources may still exist and still bill")
)

// Limits is the envelope a run may not leave.
type Limits struct {
	// MaxMonthlyEUR caps the recurring cost of the plan the test is allowed to
	// approve. It is a monthly figure because that is what the plan states; the
	// resources live for minutes.
	MaxMonthlyEUR float64
	// MaxDuration bounds the whole run. Past it the work is cancelled and the
	// resources are destroyed, whatever state they reached.
	MaxDuration time.Duration
}

// DefaultLimits is a deliberately small envelope: enough for one recommended
// node for the length of a slow bootstrap, and no more.
func DefaultLimits() Limits {
	return Limits{MaxMonthlyEUR: 60, MaxDuration: 45 * time.Minute}
}

// Validate rejects an envelope that does not actually bound anything. An
// unlimited cap or an unlimited duration is not a permissive setting; it is an
// absent one, and it is refused rather than honoured.
func (limits Limits) Validate() error {
	if limits.MaxMonthlyEUR <= 0 {
		return fmt.Errorf("%w: cost limit must be positive", ErrInvalidLimits)
	}
	if limits.MaxDuration <= 0 {
		return fmt.Errorf("%w: time limit must be positive", ErrInvalidLimits)
	}
	return nil
}

// Destroyer removes everything a run created. It must be safe to call against a
// run that got part-way, or not started at all.
type Destroyer func(ctx context.Context) error

// Guard enforces the envelope for one run.
type Guard struct {
	limits  Limits
	destroy Destroyer

	once        sync.Once
	cleanedUp   bool
	cleanupErr  error
	cleanupTime time.Duration
}

// NewGuard builds a guard. Both a usable envelope and a destroyer are required:
// a guard that cannot clean up is worse than no guard, because it reads like
// protection.
func NewGuard(limits Limits, destroy Destroyer) (*Guard, error) {
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	if destroy == nil {
		return nil, fmt.Errorf("%w: no destroyer", ErrInvalidLimits)
	}
	return &Guard{limits: limits, destroy: destroy}, nil
}

// Admit reports whether a plan's estimated recurring cost is within the cap. It
// is checked before approval, so a plan that is too expensive never becomes
// infrastructure.
func (guard *Guard) Admit(monthlyEUR float64) error {
	if monthlyEUR <= 0 {
		return fmt.Errorf("%w: plan states no cost, so it cannot be checked", ErrCostRefused)
	}
	if monthlyEUR > guard.limits.MaxMonthlyEUR {
		return fmt.Errorf("%w: %.2f EUR/month exceeds the %.2f EUR/month limit", ErrCostRefused, monthlyEUR, guard.limits.MaxMonthlyEUR)
	}
	return nil
}

// Run executes the work inside the envelope and always cleans up afterwards.
//
// A panic in the work is cleaned up after and then re-raised, so a crashing test
// cannot leak a server. The cleanup gets its own context, not the timed-out one,
// because a run that exceeded its deadline is exactly the run that most needs to
// be able to destroy what it made.
func (guard *Guard) Run(ctx context.Context, work func(context.Context) error) (err error) {
	workCtx, cancel := context.WithTimeout(ctx, guard.limits.MaxDuration)
	defer cancel()

	started := time.Now()
	defer func() {
		panicked := recover()
		cleanupErr := guard.Cleanup(context.WithoutCancel(ctx))
		if panicked != nil {
			// The panic is the more important signal; cleanup already happened.
			panic(panicked)
		}
		if cleanupErr != nil {
			// Reported separately even when the work failed: a leak is not a
			// detail of some other failure.
			if err != nil {
				err = fmt.Errorf("%w (after: %v)", cleanupErr, err)
			} else {
				err = cleanupErr
			}
		}
	}()

	err = work(workCtx)
	if workCtx.Err() != nil && time.Since(started) >= guard.limits.MaxDuration {
		return fmt.Errorf("%w: %s", ErrDeadlineExceeded, guard.limits.MaxDuration)
	}
	return err
}

// Cleanup destroys the run's resources. It is idempotent: repeated calls return
// the first result rather than destroying twice or reporting a second failure.
func (guard *Guard) Cleanup(ctx context.Context) error {
	guard.once.Do(func() {
		started := time.Now()
		if err := guard.destroy(ctx); err != nil {
			guard.cleanupErr = fmt.Errorf("%w: %v", ErrCleanupFailed, err)
		}
		guard.cleanedUp, guard.cleanupTime = true, time.Since(started)
	})
	return guard.cleanupErr
}

// CleanedUp reports whether cleanup has run. A test asserts this to prove the
// envelope held, rather than trusting that it did.
func (guard *Guard) CleanedUp() bool { return guard.cleanedUp }

// Limits returns the envelope in force, so a report can state what it was.
func (guard *Guard) Limits() Limits { return guard.limits }
