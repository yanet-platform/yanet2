package operator

import (
	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/common/go/operator"
	decappb "github.com/yanet-platform/yanet2/modules/decap/controlplane/decappb/v1"
)

// State is the desired payload pushed by each reconcile pass.
type State struct {
	// Modules holds one update request per decap module config, sent as is.
	Modules []*decappb.UpdateConfigRequest
}

// staticSource is a StateSource that holds a fixed set of module configs
// loaded once at construction time. Snapshot always returns the same
// slice. Wake is never signalled — the reconcile interval is the sole
// pacing mechanism.
type staticSource struct {
	modules []*decappb.UpdateConfigRequest
	wake    chan struct{}
	log     *zap.Logger
}

// NewStaticSource constructs a staticSource holding the supplied module
// configs, which callers must not modify afterwards.
//
// Neither the slice nor the requests it holds are copied, and every
// reconcile pass sends the same requests.
func NewStaticSource(
	modules []*decappb.UpdateConfigRequest,
	options ...StaticSourceOption,
) operator.StateSource[State] {
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
// staticSource never signals it — the reconcile interval is the sole
// pacing mechanism.
func (m *staticSource) Wake() <-chan struct{} { return m.wake }

// Advance is a no-op: the module configs are fixed for the lifetime of
// the source.
func (m *staticSource) Advance(_ State) {}
