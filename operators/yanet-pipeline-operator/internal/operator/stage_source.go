package operator

import (
	"sync"

	"go.uber.org/zap"
)

// QueueMetricsObserver receives queue-level events from the StageQueueSource.
type QueueMetricsObserver interface {
	OnQueueChanged(depth int)
	OnStageAdvanced()
}

type noopQueueMetricsObserver struct{}

func (noopQueueMetricsObserver) OnQueueChanged(int) {}
func (noopQueueMetricsObserver) OnStageAdvanced()   {}

type stageQueueOptions struct {
	Metrics QueueMetricsObserver
	Log     *zap.Logger
}

func newStageQueueOptions() *stageQueueOptions {
	return &stageQueueOptions{
		Metrics: noopQueueMetricsObserver{},
		Log:     zap.NewNop(),
	}
}

// StageQueueOption configures NewStageQueueSource.
type StageQueueOption func(*stageQueueOptions)

// WithStageQueueLog sets the logger for the queue source.
func WithStageQueueLog(log *zap.Logger) StageQueueOption {
	return func(o *stageQueueOptions) {
		o.Log = log
	}
}

// WithStageQueueMetrics attaches the queue-specific metrics observer.
func WithStageQueueMetrics(metrics QueueMetricsObserver) StageQueueOption {
	return func(o *stageQueueOptions) {
		o.Metrics = metrics
	}
}

// StageQueueSource is the operator.StateSource[*StageConfig] used by
// the pipeline operator.
//
// It wraps an ordered queue of target stages.
//
// The head is the active reconcile target. On a successful Apply the head is
// dropped unless it is the only element, in which case it is retained as the
// steady-state target.
//
// SetStages and Switch atomically replace the queue and wake the reconcile
// loop, including during apply-backoff where the wake produces an
// xbackoff.ErrInterrupted that the loop converts into a fresh Snapshot.
type StageQueueSource struct {
	mu     sync.Mutex
	stages []*StageConfig
	wakeCh chan struct{}

	metrics QueueMetricsObserver
	log     *zap.Logger
}

// NewStageQueueSource constructs an empty StageQueueSource.
func NewStageQueueSource(options ...StageQueueOption) *StageQueueSource {
	opts := newStageQueueOptions()
	for _, o := range options {
		o(opts)
	}

	return &StageQueueSource{
		wakeCh:  make(chan struct{}, 1),
		metrics: opts.Metrics,
		log:     opts.Log,
	}
}

// SetStages atomically replaces the queue of target stages and wakes
// the reconcile loop.
//
// An empty or nil slice returns the source to the idle state.
func (m *StageQueueSource) SetStages(stages []*StageConfig) {
	depth := m.replaceStages(stages)

	m.metrics.OnQueueChanged(depth)
}

// replaceStages swaps the queue under the lock and signals the
// reconcile loop.
func (m *StageQueueSource) replaceStages(stages []*StageConfig) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.stages = stages

	select {
	case m.wakeCh <- struct{}{}:
	default:
	}

	return len(m.stages)
}

// Switch replaces the queue with a single target stage and wakes the
// reconcile loop.
func (m *StageQueueSource) Switch(stage *StageConfig) {
	m.SetStages([]*StageConfig{stage})
}

// Snapshot returns the current queue head and drains any pending wake
// signal.
//
// Draining under the same lock SetStages holds means a wake signal
// that pre-dates the snapshot is consumed alongside the target it
// announced, so the next sleep will not trip on it. A wake signal
// arriving after this call is preserved by SetStages re-filling the
// buffered slot.
func (m *StageQueueSource) Snapshot() (*StageConfig, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	select {
	case <-m.wakeCh:
	default:
	}

	if len(m.stages) == 0 {
		return nil, false
	}

	return m.stages[0], true
}

// Wake returns the buffered channel signalled whenever the queue
// changes.
func (m *StageQueueSource) Wake() <-chan struct{} {
	return m.wakeCh
}

// Advance drops the just-applied head from the queue, exposing the
// next stage. The tail stage is preserved as the steady-state target.
func (m *StageQueueSource) Advance(applied *StageConfig) {
	advanced, depth := m.popHead(applied)
	if !advanced {
		return
	}

	m.metrics.OnStageAdvanced()
	m.metrics.OnQueueChanged(depth)
}

// popHead drops the just-applied head if it is still at the front of
// the queue.
//
// If the queue was concurrently replaced by SetStages while Apply was
// in flight, the stage we just applied may no longer be at the head;
// in that case the queue is left alone so the new head wins on the
// next iteration.
//
// The single-element queue is also preserved as the steady-state target.
func (m *StageQueueSource) popHead(applied *StageConfig) (bool, int) {
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
