package operator

import (
	"context"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v5"
	"go.uber.org/zap"
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
	backoff  backoff.ExponentialBackOff
	interval time.Duration

	mu     sync.Mutex
	stages []*StageConfig

	wakeCh chan struct{}

	log *zap.Logger
}

// NewReconciler constructs a Reconciler bound to the given Actuator.
func NewReconciler(actuator Actuator, options ...ReconcilerOption) *Reconciler {
	opts := newReconcilerOptions()
	for _, o := range options {
		o(opts)
	}

	backoff := backoff.ExponentialBackOff{
		InitialInterval:     opts.InitialBackoff,
		RandomizationFactor: backoff.DefaultRandomizationFactor,
		Multiplier:          backoff.DefaultMultiplier,
		MaxInterval:         opts.MaxBackoff,
	}

	return &Reconciler{
		actuator: actuator,
		backoff:  backoff,
		interval: opts.Interval,
		log:      opts.Log,
		wakeCh:   make(chan struct{}, 1),
	}
}

// SetStages atomically replaces the queue of target stages and wakes Run if
// it is sleeping.
//
// An empty or nil slice returns the reconciler to the idle state.
func (m *Reconciler) SetStages(stages []*StageConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.stages = stages

	select {
	case m.wakeCh <- struct{}{}:
	default:
	}
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

	m.backoff.Reset()

	for {
		target := m.snapshot()
		if target == nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-m.wakeCh:
			}

			continue
		}

		var d time.Duration
		err := m.actuator.Apply(ctx, target)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			m.log.Warn("reconcile failed",
				zap.String("stage", target.Name),
				zap.Error(err),
			)
			d = m.backoff.NextBackOff()
		} else {
			m.advance(target)
			d = m.interval
			m.backoff.Reset()
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-m.wakeCh:
		case <-time.After(d):
		}
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
// If the queue was concurrently replaced by SetStages while Apply was
// in flight, the stage we just applied may no longer be at the head;
// in that case we leave the queue alone so the new head wins on the
// next iteration.
func (m *Reconciler) advance(applied *StageConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.stages) == 0 || m.stages[0] != applied {
		return
	}

	if len(m.stages) == 1 {
		return
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
}
