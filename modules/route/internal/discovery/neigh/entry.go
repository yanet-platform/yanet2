package neigh

import (
	"fmt"
	"net"
	"net/netip"
	"time"
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
}

// HardwareRoute is a hashable pair of MAC addresses.
type HardwareRoute struct {
	// SourceMAC is the MAC address of the local interface that observed
	// the neighbour.
	SourceMAC [6]byte
	// DestinationMAC is the MAC address of the next hop.
	DestinationMAC [6]byte
	// Device name
	Device string
}

func (m HardwareRoute) String() string {
	return fmt.Sprintf("%s -> %s", net.HardwareAddr(m.SourceMAC[:]), net.HardwareAddr(m.DestinationMAC[:]))
}
