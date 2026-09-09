package neigh

import (
	"net/netip"
	"time"

	"github.com/yanet-platform/yanet2/modules/route/controlplane/hwroute"
)

// NeighbourEntry stores information about a neighbor with resolved hardware
// addresses.
type NeighbourEntry struct {
	// NextHop is the IP address of the next hop.
	NextHop netip.Addr
	// HardwareRoute represents a route in the Layer 2 (L2) networking stack.
	HardwareRoute HardwareRoute
	// Ifindex belongs to the publisher namespace; zero leaves scope unspecified.
	Ifindex uint32
	// UpdatedAt is the timestamp when this entry was last updated.
	UpdatedAt time.Time
	// State is the state of the neighbor entry.
	State NeighbourState
	// Source is the name of the table this entry belongs to.
	//
	// It is set during merge and is empty inside individual source caches.
	Source string
	// Priority determines which entry wins when the same IP/device pair exists in multiple
	// tables.
	//
	// Lower value means higher priority.
	Priority uint32
}

// HardwareRoute is the dataplane's Layer 2 forwarding identity carried by
// a neighbour entry.
//
// It aliases the route module's leaf type so that both sides share one
// definition without the operator linking the route control plane.
type HardwareRoute = hwroute.HardwareRoute

// Key preserves interface scope for equal next-hop addresses.
type Key struct {
	NextHop netip.Addr
	Device  string
}

// NewKey normalizes both IPv4 representations to one neighbour identity.
func NewKey(nextHop netip.Addr, device string) Key {
	return Key{NextHop: nextHop.Unmap(), Device: device}
}

// Key returns the canonical identity of the neighbour entry.
func (m NeighbourEntry) Key() Key {
	return NewKey(m.NextHop, m.HardwareRoute.Device)
}
