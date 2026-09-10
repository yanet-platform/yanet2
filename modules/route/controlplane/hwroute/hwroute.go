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
	"strings"
)

// DeviceNameMaxLen reserves the terminator in the dataplane's 80-byte name.
const DeviceNameMaxLen = 79

// ValidateDevice preserves logical device identity across the C ABI boundary.
//
// An empty name retains the legacy unscoped forwarding contract.
func ValidateDevice(device string) error {
	if len(device) > DeviceNameMaxLen {
		return fmt.Errorf("device name exceeds %d bytes", DeviceNameMaxLen)
	}
	if strings.ContainsAny(device, "\x00 \t\n\r\v\f") {
		return fmt.Errorf("device name contains whitespace or a NUL byte")
	}
	return nil
}

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

// ParseMAC converts a nonzero Ethernet address into forwarding identity.
func ParseMAC(address net.HardwareAddr) ([6]byte, bool) {
	if len(address) != 6 {
		return [6]byte{}, false
	}
	value := [6]byte(address)
	return value, value != [6]byte{}
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
