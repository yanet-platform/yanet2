package operator

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xbackoff"
)

// Actuator applies a desired set of FIBs to one or more downstream
// targets (gateways).
type Actuator = operator.Actuator[[]FIB]

// SnapshotProvider supplies the current desired FIB set for one reconcile pass.
type SnapshotProvider interface {
	Snapshot() []FIB
}

// SnapshotFunc adapts a plain function to SnapshotProvider.
type SnapshotFunc func() []FIB

func (m SnapshotFunc) Snapshot() []FIB {
	return m()
}

// Reconciler is the route-operator reconcile loop.
type Reconciler struct {
	actuator Actuator
	snapshot SnapshotProvider
	backoff  *xbackoff.Backoff
	interval time.Duration

	wakeCh chan struct{}

	metrics ReconcilerMetricsObserver
	log     *zap.Logger
}

// NewReconciler constructs a Reconciler bound to the supplied actuator
// and snapshot source.
func NewReconciler(
	actuator Actuator,
	snapshot SnapshotProvider,
	options ...ReconcilerOption,
) *Reconciler {
	opts := newReconcilerOptions()
	for _, o := range options {
		o(opts)
	}

	wakeCh := make(chan struct{}, 1)

	backoff := xbackoff.New(opts.InitialBackoff,
		xbackoff.WithMax(opts.MaxBackoff),
		xbackoff.WithSleeper(reconcilerSleeper{wake: wakeCh}),
		xbackoff.WithOnRetry(func(_ int, d time.Duration, err error) {
			opts.Log.Warn("reconcile pass failed",
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

	return &Reconciler{
		actuator: actuator,
		snapshot: snapshot,
		backoff:  backoff,
		interval: opts.Interval,
		wakeCh:   wakeCh,
		metrics:  opts.Metrics,
		log:      opts.Log,
	}
}

// Wake nudges the reconcile loop out of its sleep so the next pass runs
// immediately. Safe to call from any goroutine.
func (m *Reconciler) Wake() {
	select {
	case m.wakeCh <- struct{}{}:
	default:
	}
}

// Run executes the reconcile loop until the supplied context is
// cancelled.
func (m *Reconciler) Run(ctx context.Context) error {
	m.log.Info("running reconciler loop")
	defer m.log.Info("stopped reconciler loop")

	sleeper := reconcilerSleeper{wake: m.wakeCh}
	for {
		m.metrics.OnStateChanged(ReconcilerStateApplying)
		fibs := m.snapshot.Snapshot()
		err := m.backoff.RunContext(ctx, func() error {
			return m.actuator.Apply(ctx, fibs)
		})
		switch {
		case err == nil:
			m.metrics.OnReconcileCompleted(nil)
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
