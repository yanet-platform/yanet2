package operator

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/common/go/xbackoff"
)

// StateSource produces the current desired state for the reconcile loop.
type StateSource[T any] interface {
	// Snapshot returns the current target along with an "ok" flag.
	//
	// When ok is false the source is idle: the reconcile loop transitions
	// to the Idle state and blocks on Wake or context cancellation rather
	// than invoking the Actuator.
	//
	// When ok is true the returned value is the state to apply on the next
	// pass: the source must hold it stable until either Advance is called
	// or a subsequent Snapshot replaces it.
	Snapshot() (T, bool)
	// Wake returns a buffered channel the source signals whenever new
	// state is available or whenever an in-flight reconcile pass should be
	// preempted.
	//
	// It is safe to call from any goroutine.
	Wake() <-chan struct{}
	// Advance is invoked by the reconcile loop after a successful Apply,
	// passing the same value Snapshot returned.
	//
	// Queue-style sources use it to pop the head element. Sources that publish
	// a single steady-state snapshot may treat it as a no-op.
	Advance(applied T)
}

// Actuator applies a desired state of type T to a single downstream
// target, typically a single gateway.
type Actuator[T any] interface {
	Apply(ctx context.Context, state T) error
	Close() error
}

// ReconcilerState is the current state of the reconcile loop.
type ReconcilerState int

const (
	// ReconcilerStateIdle indicates the loop is blocked on Wake or
	// context cancellation because the source reported no work.
	ReconcilerStateIdle ReconcilerState = iota
	// ReconcilerStateApplying indicates the loop is invoking the
	// Actuator (possibly retrying with backoff).
	ReconcilerStateApplying
	// ReconcilerStateSleeping indicates the loop is sleeping between
	// passes — either the steady-state interval after success or the
	// backoff delay after failure — preemptable by Wake or context
	// cancellation.
	ReconcilerStateSleeping
)

// ReconcilerMetricsObserver receives semantic events from the
// reconcile loop and translates them into metrics.
type ReconcilerMetricsObserver interface {
	OnReconcileCompleted(err error)
	OnBackoffScheduled(delay time.Duration)
	OnBackoffReset()
	OnStateChanged(state ReconcilerState)
}

// Reconciler drives an Actuator from a StateSource until its context
// is cancelled.
//
// The loop has three states observable through ReconcilerMetricsObserver:
//   - Idle (source returned ok=false: block on ctx and Wake).
//   - Applying (Apply runs to completion; retried with backoff on
//     failure, preemptable by Wake between retries).
//   - Sleeping (interval after success, backoff delay after failure;
//     preemptable by Wake or ctx).
//
// Apply is never cancelled mid-flight by a concurrent Wake —
// preemption happens between applies, not within them.
type Reconciler[T any] struct {
	actuator Actuator[T]
	source   StateSource[T]
	backoff  *xbackoff.Backoff
	interval time.Duration
	metrics  ReconcilerMetricsObserver
	log      *zap.Logger
}

// NewReconciler constructs a Reconciler bound to the supplied actuator
// and state source.
func NewReconciler[T any](
	actuator Actuator[T],
	source StateSource[T],
	options ...ReconcilerOption,
) *Reconciler[T] {
	opts := newReconcilerOptions()
	for _, o := range options {
		o(opts)
	}

	wake := source.Wake()
	backoff := xbackoff.New(opts.InitialBackoff,
		xbackoff.WithMax(opts.MaxBackoff),
		xbackoff.WithSleeper(reconcilerSleeper{wake: wake}),
		xbackoff.WithOnRetry(func(_ int, d time.Duration, err error) {
			opts.Log.Warn("reconcile failed",
				zap.Duration("backoff", d),
				zap.Error(err),
			)
			opts.Metrics.OnReconcileCompleted(err)
			opts.Metrics.OnBackoffScheduled(d)
			opts.Metrics.OnStateChanged(ReconcilerStateSleeping)
		}),
		xbackoff.WithOnReset(func() {
			opts.Metrics.OnBackoffReset()
		}),
	)

	return &Reconciler[T]{
		actuator: actuator,
		source:   source,
		backoff:  backoff,
		interval: opts.Interval,
		metrics:  opts.Metrics,
		log:      opts.Log,
	}
}

// Run executes the reconcile loop until the supplied context is
// cancelled.
func (m *Reconciler[T]) Run(ctx context.Context) error {
	m.log.Info("running reconciler loop")
	defer m.log.Info("stopped reconciler loop")

	wake := m.source.Wake()
	sleeper := reconcilerSleeper{wake: wake}
	for {
		target, ok := m.source.Snapshot()
		if !ok {
			m.metrics.OnStateChanged(ReconcilerStateIdle)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-wake:
			}

			continue
		}

		m.metrics.OnStateChanged(ReconcilerStateApplying)
		err := m.backoff.RunContext(ctx, func() error {
			return m.actuator.Apply(ctx, target)
		})
		switch {
		case err == nil:
			m.metrics.OnReconcileCompleted(nil)
			m.source.Advance(target)

			m.metrics.OnStateChanged(ReconcilerStateSleeping)
			if err := sleeper.Sleep(ctx, m.interval); err != nil {
				if errors.Is(err, xbackoff.ErrInterrupted) {
					continue
				}
				return err
			}
		case errors.Is(err, xbackoff.ErrInterrupted):
			continue
		default:
			return err
		}
	}
}

// reconcilerSleeper interrupts the backoff sleep on a wake signal.
type reconcilerSleeper struct {
	wake <-chan struct{}
}

func (m reconcilerSleeper) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.wake:
		return xbackoff.ErrInterrupted
	case <-t.C:
		return nil
	}
}
