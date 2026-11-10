package operator

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/common/go/xbackoff"
)

// Actuator applies a desired state of type T to a single downstream
// target, typically a single gateway.
type Actuator[T any] interface {
	Apply(ctx context.Context, state T) error
	Close() error
}

// ReconcilerState is the current state of the reconcile loop.
type ReconcilerState int

const (
	// ReconcilerStateIdle indicates the loop is waiting for the first
	// desired state to be published.
	ReconcilerStateIdle ReconcilerState = iota
	// ReconcilerStateApplying indicates the loop is invoking the
	// Actuator (possibly retrying with backoff).
	ReconcilerStateApplying
	// ReconcilerStateSleeping indicates the loop is sleeping between
	// passes — either the steady-state interval after success or the
	// backoff delay after failure — cut short by a newer desired state or
	// context cancellation.
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

// NoopReconcilerMetricsObserver is the default observer wired into
// reconciler options when no metrics sink is attached.
type NoopReconcilerMetricsObserver struct{}

// OnReconcileCompleted intentionally discards completion events.
func (NoopReconcilerMetricsObserver) OnReconcileCompleted(error) {}

// OnBackoffScheduled intentionally discards scheduled backoff events.
func (NoopReconcilerMetricsObserver) OnBackoffScheduled(time.Duration) {}

// OnBackoffReset intentionally discards backoff reset events.
func (NoopReconcilerMetricsObserver) OnBackoffReset() {}

// OnStateChanged intentionally discards state transition events.
func (NoopReconcilerMetricsObserver) OnStateChanged(ReconcilerState) {}

type reconcilerOptions struct {
	// Interval separates successful reconciliation passes.
	Interval time.Duration
	// InitialBackoff is the first retry delay after a failed pass.
	InitialBackoff time.Duration
	// MaxBackoff caps the retry delay after repeated failures.
	MaxBackoff time.Duration
	// Metrics receives reconcile-loop lifecycle events.
	Metrics ReconcilerMetricsObserver
	// Log records reconcile-loop activity.
	Log *zap.Logger
}

func newReconcilerOptions() *reconcilerOptions {
	return &reconcilerOptions{
		Interval:       DefaultReconcileInterval,
		InitialBackoff: DefaultReconcileInitialBackoff,
		MaxBackoff:     DefaultReconcileMaxBackoff,
		Metrics:        NoopReconcilerMetricsObserver{},
		Log:            zap.NewNop(),
	}
}

// ReconcilerOption configures NewReconciler.
type ReconcilerOption func(*reconcilerOptions)

// WithReconcilerLog sets the logger used by the reconcile loop.
func WithReconcilerLog(log *zap.Logger) ReconcilerOption {
	return func(o *reconcilerOptions) {
		o.Log = log
	}
}

// WithReconcileInterval sets the steady-state period between
// successful reconcile passes.
func WithReconcileInterval(d time.Duration) ReconcilerOption {
	return func(o *reconcilerOptions) {
		o.Interval = d
	}
}

// WithReconcileBackoff sets the initial and maximum sleep durations
// for the exponential backoff applied after failed reconcile passes.
func WithReconcileBackoff(initial, max time.Duration) ReconcilerOption {
	return func(o *reconcilerOptions) {
		o.InitialBackoff = initial
		o.MaxBackoff = max
	}
}

// WithReconcilerMetrics attaches the metrics observer for the
// reconcile loop.
func WithReconcilerMetrics(metrics ReconcilerMetricsObserver) ReconcilerOption {
	return func(o *reconcilerOptions) {
		o.Metrics = metrics
	}
}

// Reconciler drives an Actuator from the latest desired state until its
// context is cancelled.
//
// The loop has three states observable through ReconcilerMetricsObserver:
//   - Idle (nothing published yet: block on ctx and the first publication).
//   - Applying (Apply runs to completion — retried with backoff on
//     failure, preempted by a newer desired state between retries).
//   - Sleeping (interval after success, backoff delay after failure —
//     preempted by a newer desired state or ctx).
//
// Apply is never cancelled mid-flight by a newer desired state —
// preemption happens between applies, not within them.
type Reconciler[T any] struct {
	actuator Actuator[T]
	latest   *Latest[T]
	backoff  *xbackoff.Backoff
	// sleeper waits between applies until the applied version is replaced.
	sleeper  *reconcilerSleeper
	interval time.Duration
	metrics  ReconcilerMetricsObserver
	log      *zap.Logger
}

// NewReconciler constructs a Reconciler that applies the published desired
// state through the supplied actuator.
func NewReconciler[T any](
	actuator Actuator[T],
	latest *Latest[T],
	options ...ReconcilerOption,
) *Reconciler[T] {
	opts := newReconcilerOptions()
	for _, o := range options {
		o(opts)
	}

	sleeper := &reconcilerSleeper{}
	backoff := xbackoff.New(opts.InitialBackoff,
		xbackoff.WithMax(opts.MaxBackoff),
		xbackoff.WithSleeper(sleeper),
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
		latest:   latest,
		backoff:  backoff,
		sleeper:  sleeper,
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

	for {
		version := m.latest.Load()
		m.sleeper.Wake = version.Replaced
		if version.Number == 0 {
			m.metrics.OnStateChanged(ReconcilerStateIdle)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-version.Replaced:
			}

			continue
		}

		err := m.backoff.RunContext(ctx, func() error {
			m.metrics.OnStateChanged(ReconcilerStateApplying)
			return m.actuator.Apply(ctx, version.Value)
		})
		switch {
		case err == nil:
			m.metrics.OnReconcileCompleted(nil)

			m.metrics.OnStateChanged(ReconcilerStateSleeping)
			if err := m.sleeper.Sleep(ctx, m.interval); err != nil {
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

// reconcilerSleeper interrupts a sleep once the applied version is
// replaced.
type reconcilerSleeper struct {
	// Wake is closed when the version being applied is replaced.
	Wake <-chan struct{}
}

func (m *reconcilerSleeper) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.Wake:
		return xbackoff.ErrInterrupted
	case <-t.C:
		return nil
	}
}
