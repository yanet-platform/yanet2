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
	// UpdatedAt is the timestamp when this entry was last updated.
	UpdatedAt time.Time
	// State is the state of the neighbor entry.
	State NeighbourState
	// Source is the name of the table this entry belongs to.
	//
	// It is set during merge and is empty inside individual source caches.
	Source string
	// Priority determines which entry wins when the same IP exists in multiple
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
