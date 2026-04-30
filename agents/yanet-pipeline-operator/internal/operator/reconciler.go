package operator

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/common/go/xbackoff"
)

// Actuator applies a desired stage configuration.
type Actuator interface {
	Apply(ctx context.Context, stage *StageConfig) error
	Close() error
}

// Reconciler drives the system through an ordered queue of target
// StageConfigs.
//
// The head of the queue is the active reconcile target. After a
// successful Apply the head is dropped, exposing the next stage —
// unless the queue has length one, in which case the tail stage is
// retained as the steady-state target and applied on the configured
// interval.
type Reconciler struct {
	actuator Actuator
	backoff  *xbackoff.Backoff
	interval time.Duration

	mu     sync.Mutex
	stages []*StageConfig

	wakeCh chan struct{}

	metrics ReconcilerMetricsObserver
	log     *zap.Logger
}

// NewReconciler constructs a Reconciler bound to the given Actuator.
func NewReconciler(actuator Actuator, options ...ReconcilerOption) *Reconciler {
	opts := newReconcilerOptions()
	for _, o := range options {
		o(opts)
	}

	wakeCh := make(chan struct{}, 1)

	backoff := xbackoff.New(opts.InitialBackoff,
		xbackoff.WithMax(opts.MaxBackoff),
		xbackoff.WithSleeper(reconcilerSleeper{wake: wakeCh}),
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

	return &Reconciler{
		actuator: actuator,
		backoff:  backoff,
		interval: opts.Interval,
		wakeCh:   wakeCh,
		metrics:  opts.Metrics,
		log:      opts.Log,
	}
}

// SetStages atomically replaces the queue of target stages and wakes Run if
// it is sleeping.
//
// An empty or nil slice returns the reconciler to the idle state.
func (m *Reconciler) SetStages(stages []*StageConfig) {
	depth := m.replaceStages(stages)

	m.metrics.OnQueueChanged(depth)
}

// replaceStages swaps the queue under the lock and signals Run. It
// returns the resulting queue depth so the caller can publish metrics
// outside the lock.
func (m *Reconciler) replaceStages(stages []*StageConfig) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.stages = stages

	select {
	case m.wakeCh <- struct{}{}:
	default:
	}

	return len(m.stages)
}

// Switch replaces the queue with a single target stage and wakes Run
// if it is sleeping.
//
// It is a thin wrapper over SetStages — calling it from any source
// (config bootstrap, gRPC API) atomically discards whatever queue was being
// walked.
func (m *Reconciler) Switch(stage *StageConfig) {
	m.SetStages([]*StageConfig{stage})
}

// Run executes the reconcile loop until specified context is cancelled.
//
// The loop has three states:
//   - Idle (queue empty — block on ctx and wake).
//   - Applying (Apply runs to completion).
//   - Sleeping (interval after success, backoff after failure;
//     preemptable by SetStages/Switch or ctx).
//
// Apply is never cancelled mid-flight by a concurrent SetStages —
// preemption happens between applies, not within them.
func (m *Reconciler) Run(ctx context.Context) error {
	m.log.Info("running reconciler loop")
	defer m.log.Info("stopped reconciler loop")

	sleeper := reconcilerSleeper{wake: m.wakeCh}
	for {
		target := m.snapshot()
		if target == nil {
			m.metrics.OnStateChanged(ReconcilerStateIdle)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-m.wakeCh:
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
			m.advance(target)

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

// snapshot returns the current queue head and drains any pending wake
// signal.
//
// Draining under the same lock SetStages holds means a wake signal
// that pre-dates the snapshot is consumed alongside the target it
// announced, so the next sleep won't trip on it.
//
// A wake signal arriving after this call is preserved by SetStages
// re-filling the buffered slot.
func (m *Reconciler) snapshot() *StageConfig {
	m.mu.Lock()
	defer m.mu.Unlock()

	select {
	case <-m.wakeCh:
	default:
	}

	if len(m.stages) == 0 {
		return nil
	}

	return m.stages[0]
}

// advance drops the just-applied head from the queue, exposing the
// next stage. The tail stage is preserved as the steady-state target.
//
// The mutation runs under the lock in popHead; metric observers are
// invoked here, after the lock has been released, so they never
// contend with concurrent reconciler state changes.
func (m *Reconciler) advance(applied *StageConfig) {
	advanced, depth := m.popHead(applied)
	if !advanced {
		return
	}

	m.metrics.OnStageAdvanced()
	m.metrics.OnQueueChanged(depth)
}

// popHead drops the just-applied head if it is still at the front of
// the queue. It runs entirely under the lock and returns whether a
// transition happened along with the resulting queue depth so the
// caller can publish metrics outside the lock.
//
// If the queue was concurrently replaced by SetStages while Apply was
// in flight, the stage we just applied may no longer be at the head;
// in that case the queue is left alone so the new head wins on the
// next iteration. The single-element queue is also preserved as the
// steady-state target.
func (m *Reconciler) popHead(applied *StageConfig) (bool, int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.stages) == 0 || m.stages[0] != applied {
		return false, 0
	}
	if len(m.stages) == 1 {
		return false, 0
	}

	next := m.stages[1]
	m.stages = m.stages[1:]
	m.log.Info("advanced to next stage",
		zap.String("from", applied.Name),
		zap.String("to", next.Name),
	)

	select {
	case m.wakeCh <- struct{}{}:
	default:
	}

	return true, len(m.stages)
}
