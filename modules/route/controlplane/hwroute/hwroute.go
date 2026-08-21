// Package hwroute holds the route module's Layer 2 forwarding identity.
//
// It is a leaf package that depends on the standard library only, so that
// consumers that need just the identity type — the route operator, which
// talks to the gateway over gRPC and never touches shared memory — do not
// pull the cgo shared-memory stack behind the route control plane into
// their link.
package hwroute

import (
	"bytes"
	"cmp"
	"fmt"
	"net"
)

// HardwareRoute represents a route in the Layer 2 (L2) networking stack.
//
// This is the dataplane's forwarding identity: two nexthops agreeing on
// these three fields share one route slot regardless of what else the
// request says about them (e.g. counter name). Keying the route module's
// nexthop dedup on more would split one physical neighbour across slots,
// skewing ECMP.
type HardwareRoute struct {
	// SourceMAC is the MAC address of the local interface that observed
	// the neighbour.
	SourceMAC [6]byte
	// DestinationMAC is the MAC address of the next hop.
	DestinationMAC [6]byte
	// Device is the interface name.
	Device string
}

// String renders the route as "<source MAC> -> <destination MAC>"; the
// device is left out.
func (m HardwareRoute) String() string {
	return fmt.Sprintf("%s -> %s", net.HardwareAddr(m.SourceMAC[:]), net.HardwareAddr(m.DestinationMAC[:]))
}

// Compare compares two hardware routes lexicographically for deterministic
// sorting.
func (m HardwareRoute) Compare(other HardwareRoute) int {
	if c := bytes.Compare(m.SourceMAC[:], other.SourceMAC[:]); c != 0 {
		return c
	}
	if c := bytes.Compare(m.DestinationMAC[:], other.DestinationMAC[:]); c != 0 {
		return c
	}

	return cmp.Compare(m.Device, other.Device)
}
