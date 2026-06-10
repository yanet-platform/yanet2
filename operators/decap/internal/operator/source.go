package operator

import (
	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/common/go/operator"
)

// ModuleConfig holds the desired prefix set for one decap module config.
type ModuleConfig struct {
	Name     string
	Prefixes []string
}

// State is the desired payload pushed by each reconcile pass.
type State struct {
	Modules []ModuleConfig
}

// staticSource is a StateSource that holds a fixed set of module configs
// loaded once at construction time. Snapshot always returns the same
// slice. Wake is never signalled — the reconcile interval is the sole
// pacing mechanism.
type staticSource struct {
	modules []ModuleConfig
	wake    chan struct{}
	log     *zap.Logger
}

// NewStaticSource constructs a staticSource holding the supplied module
// configs. The slice is not copied; callers must not modify it after
// passing it in.
func NewStaticSource(modules []ModuleConfig, options ...StaticSourceOption) operator.StateSource[State] {
	opts := newStaticSourceOptions()
	for _, o := range options {
		o(opts)
	}
	return &staticSource{
		modules: modules,
		wake:    make(chan struct{}),
		log:     opts.Log,
	}
}

// Snapshot returns the fixed module configs as the current desired state.
func (m *staticSource) Snapshot() (State, bool) {
	return State{Modules: m.modules}, true
}

// Wake returns the channel the Reconciler monitors for eager wakeups.
//
// staticSource never signals it; the reconcile interval is the sole
// pacing mechanism.
func (m *staticSource) Wake() <-chan struct{} { return m.wake }

// Advance is a no-op: the module configs are fixed for the lifetime of
// the source.
func (m *staticSource) Advance(_ State) {}
