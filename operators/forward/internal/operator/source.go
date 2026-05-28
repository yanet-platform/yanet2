package operator

import (
	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb"
)

// State is the desired payload pushed by each reconcile pass.
type State struct {
	Rules []*forwardpb.Rule
}

// staticSource is a StateSource that holds a fixed set of rules loaded
// once at construction time. Snapshot always returns the same slice.
// Wake is never signalled — the reconcile interval is the sole pacing
// mechanism.
type staticSource struct {
	rules []*forwardpb.Rule
	wake  chan struct{}
	log   *zap.Logger
}

// NewStaticSource constructs a staticSource holding the supplied rules.
// The rules slice is not copied; callers must not modify it after
// passing it in.
func NewStaticSource(rules []*forwardpb.Rule, options ...StaticSourceOption) operator.StateSource[State] {
	opts := newStaticSourceOptions()
	for _, o := range options {
		o(opts)
	}
	return &staticSource{
		rules: rules,
		wake:  make(chan struct{}),
		log:   opts.Log,
	}
}

// Snapshot returns the fixed rules as the current desired state.
func (m *staticSource) Snapshot() (State, bool) {
	return State{Rules: m.rules}, true
}

// Wake returns the channel the Reconciler monitors for eager wakeups.
// staticSource never signals it; the reconcile interval is the sole
// pacing mechanism.
func (m *staticSource) Wake() <-chan struct{} { return m.wake }

// Advance is a no-op: the rules are fixed for the lifetime of the source.
func (m *staticSource) Advance(_ State) {}
