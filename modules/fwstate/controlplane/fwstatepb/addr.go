package fwstatepb

import "net/netip"

// NetipAddr converts the Addr bytes to a netip.Addr.
// Returns a zero-value netip.Addr if the bytes are not a valid IPv4 or IPv6 address.
func (a *Addr) NetipAddr() netip.Addr {
	if a == nil {
		return netip.Addr{}
	}
	addr, ok := netip.AddrFromSlice(a.Bytes)
	if !ok {
		return netip.Addr{}
	}
	return addr
}

// NewAddr creates an Addr from a netip.Addr.
func NewAddr(addr netip.Addr) *Addr {
	return &Addr{Bytes: addr.AsSlice()}
}
