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
// It wraps an ordered queue of target stages; the head is the active
// reconcile target. The queue drops the head only once no gateway is
// applying it and its latest apply succeeded on every gateway, so the
// gateways move through the stages in lock-step. The last stage stays as
// the steady-state target.
//
// SetStages and Switch atomically replace the queue and wake the reconcile
// loop.
type StageQueueSource struct {
	mu     sync.Mutex
	stages []*StageConfig
	// gateways are the names every head must reach before the queue moves on.
	gateways []string
	// pending holds the gateways that are applying the head or whose latest
	// apply of it did not succeed.
	pending map[string]struct{}
	wakeCh  chan struct{}

	metrics QueueMetricsObserver
	log     *zap.Logger
}

// NewStageQueueSource constructs an empty StageQueueSource whose stages
// must reach every one of the named gateways.
func NewStageQueueSource(gateways []string, options ...StageQueueOption) *StageQueueSource {
	opts := newStageQueueOptions()
	for _, o := range options {
		o(opts)
	}

	return &StageQueueSource{
		gateways: gateways,
		wakeCh:   make(chan struct{}, 1),
		metrics:  opts.Metrics,
		log:      opts.Log,
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
//
// The new head waits for every gateway, even one that applied the same
// stage before the swap.
func (m *StageQueueSource) replaceStages(stages []*StageConfig) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.stages = stages
	m.awaitEveryGatewayLocked()

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

// Advance does nothing: the queue moves on once every gateway applied the
// head, see OnStageApplied.
func (m *StageQueueSource) Advance(*StageConfig) {}

// OnStageApplying makes the head wait for the gateway until its apply of
// the head ends.
func (m *StageQueueSource) OnStageApplying(gateway string, stage *StageConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.isHeadLocked(stage) {
		m.pending[gateway] = struct{}{}
	}
}

// OnStageApplied records how one gateway's apply of the stage ended, and
// drops the head once no gateway is applying it and its latest apply
// succeeded on every gateway, unless it is the last stage.
//
// A failed apply leaves the head waiting for that gateway. A report for a
// stage that is no longer the head, because the queue moved on or was
// replaced meanwhile, changes nothing.
func (m *StageQueueSource) OnStageApplied(gateway string, stage *StageConfig, err error) {
	advanced, depth := m.record(gateway, stage, err)
	if !advanced {
		return
	}

	m.metrics.OnStageAdvanced()
	m.metrics.OnQueueChanged(depth)
}

// record updates the gateways the head waits for and pops the head when none
// is left, unless it is the last stage.
func (m *StageQueueSource) record(gateway string, stage *StageConfig, err error) (bool, int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.isHeadLocked(stage) {
		return false, 0
	}
	if err != nil {
		m.pending[gateway] = struct{}{}
		return false, 0
	}
	delete(m.pending, gateway)
	if len(m.pending) > 0 || len(m.stages) == 1 {
		return false, 0
	}

	next := m.stages[1]
	m.stages = m.stages[1:]
	m.awaitEveryGatewayLocked()
	m.log.Info("advanced to next stage",
		zap.String("from", stage.Name),
		zap.String("to", next.Name),
	)

	select {
	case m.wakeCh <- struct{}{}:
	default:
	}

	return true, len(m.stages)
}

// isHeadLocked reports whether the stage is the head of the queue.
//
// The caller holds the lock.
func (m *StageQueueSource) isHeadLocked(stage *StageConfig) bool {
	return len(m.stages) > 0 && m.stages[0] == stage
}

// awaitEveryGatewayLocked makes the head wait for every gateway again.
//
// The caller holds the lock.
func (m *StageQueueSource) awaitEveryGatewayLocked() {
	m.pending = map[string]struct{}{}
	for _, gateway := range m.gateways {
		m.pending[gateway] = struct{}{}
	}
}
