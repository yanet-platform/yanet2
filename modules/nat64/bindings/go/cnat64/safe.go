package cnat64

import (
	"fmt"
	"math"
	"net/netip"
)

// AddMapping adds a new IPv4-IPv6 address mapping
func (m *ModuleConfig) AddMapping(ipv4 netip.Addr, ipv6 netip.Addr, prefixIndex uint32) error {
	if !ipv4.Is4() {
		return fmt.Errorf("ipv4 %q is not an IPv4 address", ipv4)
	}
	if !ipv6.Is6() || ipv6.Is4In6() {
		return fmt.Errorf("ipv6 %q is not a pure IPv6 address", ipv6)
	}

	return m.addMapping(ipv4.As4(), ipv6.As16(), prefixIndex)
}

// AddPrefix adds a new NAT64 prefix
func (m *ModuleConfig) AddPrefix(prefix []byte) error {
	if len(prefix) != 12 {
		return fmt.Errorf("invalid prefix length: got %d, want 12", len(prefix))
	}

	var p [12]byte
	copy(p[:], prefix)

	return m.addPrefix(p)
}

// SetDropUnknown sets drop_unknown_prefix and drop_unknown_mapping flags
func (m *ModuleConfig) SetDropUnknown(dropUnknownPrefix bool, dropUnknownMapping bool) error {
	return m.setDropUnknown(dropUnknownPrefix, dropUnknownMapping)
}

// SetMTU sets IPv4/IPv6 MTU limits.
func (m *ModuleConfig) SetMTU(ipv4MTU uint32, ipv6MTU uint32) error {
	if ipv4MTU > math.MaxUint16 {
		return fmt.Errorf("invalid IPv4 MTU: got %d, max %d", ipv4MTU, math.MaxUint16)
	}
	if ipv6MTU > math.MaxUint16 {
		return fmt.Errorf("invalid IPv6 MTU: got %d, max %d", ipv6MTU, math.MaxUint16)
	}

	return m.setMTU(uint16(ipv4MTU), uint16(ipv6MTU))
}
