package neigh

import (
	"net/netip"
	"time"

	route "github.com/yanet-platform/yanet2/modules/route/controlplane"
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

type HardwareRoute = route.HardwareRoute
