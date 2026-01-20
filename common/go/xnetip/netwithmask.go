package xnetip

import (
	"fmt"
	"net"
	"net/netip"
)

// NetWithMask represents an IP address with an arbitrary netmask.
// Unlike netip.Prefix, this supports non-contiguous masks (e.g., 255.255.0.255).
type NetWithMask struct {
	Addr netip.Addr
	Mask net.IPMask
}

// NewNetWithMask creates a NetWithMask from an address and mask.
// Returns an error if the mask length doesn't match the address type.
func NewNetWithMask(addr netip.Addr, mask net.IPMask) (NetWithMask, error) {
	expectedLen := net.IPv4len
	if addr.Is6() {
		expectedLen = net.IPv6len
	}

	if len(mask) != expectedLen {
		return NetWithMask{}, fmt.Errorf(
			"mask length %d doesn't match address type (expected %d)",
			len(mask), expectedLen,
		)
	}

	return NetWithMask{Addr: addr, Mask: mask}, nil
}

// FromPrefix creates a NetWithMask from a netip.Prefix.
// This is useful for backward compatibility with prefix-based masks.
func FromPrefix(prefix netip.Prefix) NetWithMask {
	return NetWithMask{
		Addr: prefix.Addr(),
		Mask: Mask(prefix),
	}
}

// ToPrefix attempts to convert NetWithMask to netip.Prefix.
// Returns an error if the mask is not a valid contiguous prefix mask.
func (n NetWithMask) ToPrefix() (netip.Prefix, error) {
	// Count leading ones in the mask
	bits := 0
	foundZero := false

	for _, b := range n.Mask {
		for i := 7; i >= 0; i-- {
			if (b & (1 << i)) != 0 {
				if foundZero {
					return netip.Prefix{}, fmt.Errorf(
						"mask is not a valid prefix (non-contiguous bits)",
					)
				}
				bits++
			} else {
				foundZero = true
			}
		}
	}

	prefix, err := n.Addr.Prefix(bits)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("failed to create prefix: %w", err)
	}

	return prefix, nil
}

// IsValid returns true if the NetWithMask is valid (non-zero address and mask).
func (n NetWithMask) IsValid() bool {
	return n.Addr.IsValid() && len(n.Mask) > 0
}

// String returns a string representation of the NetWithMask.
// Format: "addr/mask" where mask is in dotted decimal (IPv4) or hex (IPv6).
func (n NetWithMask) String() string {
	if !n.IsValid() {
		return "invalid"
	}

	// Try to convert to prefix for cleaner output
	if prefix, err := n.ToPrefix(); err == nil {
		return prefix.String()
	}

	// Otherwise show addr + mask
	return fmt.Sprintf("%s/%s", n.Addr, n.Mask)
}

// MaskBytes returns the mask as a byte slice.
func (n NetWithMask) MaskBytes() []byte {
	return []byte(n.Mask)
}
