package operator

import "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/desired"

// State is the immutable startup interface configuration.
type State = desired.State

// Source retains the managed topology and coalesces neighbour event wakes.
type Source struct {
	state State
	wake  chan struct{}
}

// NewSource takes a defensive copy of the startup configuration.
func NewSource(state State) *Source {
	return &Source{state: state.Clone(), wake: make(chan struct{}, 1)}
}

// Snapshot supplies an owned copy of the managed topology for each collection.
func (m *Source) Snapshot() (State, bool) {
	return m.state.Clone(), true
}

// Wake interrupts the wait between full neighbour collections.
func (m *Source) Wake() <-chan struct{} {
	return m.wake
}

// Notify retains one pending wake without blocking event reception.
func (m *Source) Notify() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// Advance preserves the startup configuration after every pass.
func (m *Source) Advance(State) {}
