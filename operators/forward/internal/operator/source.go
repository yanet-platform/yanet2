package operator

import (
	"fmt"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb"
)

// State is the desired payload pushed by each reconcile pass.
type State struct {
	Rules []*forwardpb.Rule
}

// pollingSource is a StateSource that re-reads the rules YAML on every
// Snapshot call. Wake is never signalled — the Reconciler interval is
// the sole pacing mechanism.
type pollingSource struct {
	path string
	last []*forwardpb.Rule
	wake chan struct{}
	log  *zap.Logger
}

// NewPollingSource constructs a pollingSource and performs an initial
// load of the rules file, returning an error if the file cannot be read.
func NewPollingSource(path string, log *zap.Logger) (*pollingSource, error) {
	rules, err := loadForwardRules(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load initial rules from %q: %w", path, err)
	}
	return &pollingSource{
		path: path,
		last: rules,
		wake: make(chan struct{}),
		log:  log,
	}, nil
}

// Snapshot re-reads the rules file and returns the current desired state.
// On read error the cached snapshot is returned so the last known-good
// state continues to be applied.
func (m *pollingSource) Snapshot() (State, bool) {
	rules, err := loadForwardRules(m.path)
	if err != nil {
		m.log.Warn("failed to reload rules, using cached snapshot",
			zap.String("path", m.path),
			zap.Error(err),
		)
		return State{Rules: m.last}, true
	}
	m.last = rules
	return State{Rules: rules}, true
}

// Wake returns the channel the Reconciler monitors for eager wakeups.
// pollingSource never signals it; the reconcile interval is the sole
// pacing mechanism.
func (m *pollingSource) Wake() <-chan struct{} { return m.wake }

// Advance is a no-op for a polling source: the next Snapshot call
// always re-reads from disk.
func (m *pollingSource) Advance(_ State) {}
