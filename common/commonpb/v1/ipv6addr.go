package commonpb

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/netip"
)

// NewIPv6AddressFromAddr creates an IPv6Address from a netip.Addr value.
//
// The input must be an IPv6 value; the IPv4-mapped form is accepted as
// IPv6. Zoned addresses are rejected rather than silently stripped: a
// zone is host-local and belongs as a sibling field on the owning
// message.
func NewIPv6AddressFromAddr(addr netip.Addr) (*IPv6Address, error) {
	if addr.Zone() != "" {
		return nil, fmt.Errorf("zoned IPv6 address is not allowed: %s", addr)
	}
	if !addr.Is6() {
		return nil, fmt.Errorf("not an IPv6 address: %s", addr)
	}
	return NewIPv6Address(addr.As16()), nil
}

// NewIPv6Address creates an IPv6Address from a 16-byte address in
// network byte order.
func NewIPv6Address(addr [16]byte) *IPv6Address {
	return &IPv6Address{
		Hi: binary.BigEndian.Uint64(addr[:8]),
		Lo: binary.BigEndian.Uint64(addr[8:]),
	}
}

// ToAddr converts the IPv6Address to a netip.Addr value.
//
// Every value is a valid address, so the conversion is total: the zero
// message maps to the zero address. Whether an address is present at all
// is the owning field's concern, not this message's.
func (m *IPv6Address) ToAddr() netip.Addr {
	var raw [16]byte
	binary.BigEndian.PutUint64(raw[:8], m.GetHi())
	binary.BigEndian.PutUint64(raw[8:], m.GetLo())
	return netip.AddrFrom16(raw)
}

// AsLogValue implements xgrpc.ProtoLogValue for compact gRPC logging.
func (m *IPv6Address) AsLogValue() any {
	return m.ToAddr().String()
}

// MarshalJSON serializes the message as a bare IPv6 address string, the
// same form the Rust side renders.
func (m *IPv6Address) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.ToAddr().String())
}

// UnmarshalJSON accepts a bare IPv6 address string.
func (m *IPv6Address) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == "" {
		return fmt.Errorf("empty IP address is not allowed")
	}

	parsed, err := netip.ParseAddr(raw)
	if err != nil {
		return fmt.Errorf("failed to parse IP address: %w", err)
	}

	addr, err := NewIPv6AddressFromAddr(parsed)
	if err != nil {
		return err
	}

	m.Hi = addr.Hi
	m.Lo = addr.Lo
	return nil
}
