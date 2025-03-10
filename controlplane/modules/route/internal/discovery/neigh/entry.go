package neigh

import (
	"net"
	"net/netip"
	"time"
)

// NeighbourEntry stores information about a neighbor with resolved hardware
// addresses.
type NeighbourEntry struct {
	// NextHop is the IP address of the next hop.
	NextHop netip.Addr
	// LinkAddr is the MAC address of the next hop.
	LinkAddr net.HardwareAddr
	// HardwareAddr is the MAC address of the local interface that observed
	// the neighbour.
	HardwareAddr net.HardwareAddr
	// UpdatedAt is the timestamp when this entry was last updated.
	UpdatedAt time.Time
	// State is the state of the neighbor entry.
	State NeighbourState
}
