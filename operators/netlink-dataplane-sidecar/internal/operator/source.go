package operator

import "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"

// State is the immutable interface configuration restored on each pass.
type State = netplan.State

// Source retains startup configuration and coalesces reconciliation wakes.
type Source struct {
	state State
	wake  chan struct{}
}

// NewSource takes a defensive copy of the startup configuration.
func NewSource(state State) *Source {
	return &Source{state: state.Clone(), wake: make(chan struct{}, 1)}
}

// Snapshot requests periodic restoration of the original configuration.
func (m *Source) Snapshot() (State, bool) {
	return m.state.Clone(), true
}

// Wake returns the coalescing notification channel.
func (m *Source) Wake() <-chan struct{} {
	return m.wake
}

// Notify requests a pass without blocking the event subscriber.
func (m *Source) Notify() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// Advance preserves the startup configuration after every pass.
func (m *Source) Advance(State) {}
