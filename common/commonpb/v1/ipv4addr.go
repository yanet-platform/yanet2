package commonpb

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/netip"
)

// NewIPv4AddressFromAddr creates an IPv4Address from a netip.Addr value.
//
// The input must be a native IPv4 value: IPv6 input is rejected,
// including the IPv4-mapped form, which belongs to the IPv6 family on
// the wire. Callers holding a mapped address unmap it first.
func NewIPv4AddressFromAddr(addr netip.Addr) (*IPv4Address, error) {
	if !addr.Is4() {
		return nil, fmt.Errorf("not an IPv4 address: %s", addr)
	}
	return NewIPv4Address(addr.As4()), nil
}

// NewIPv4Address creates an IPv4Address from a 4-byte address in network
// byte order.
func NewIPv4Address(addr [4]byte) *IPv4Address {
	return &IPv4Address{Addr: binary.BigEndian.Uint32(addr[:])}
}

// ToAddr converts the IPv4Address to a netip.Addr value.
//
// Every value is a valid address, so the conversion is total: the zero
// message maps to the zero address. Whether an address is present at all
// is the owning field's concern, not this message's.
func (m *IPv4Address) ToAddr() netip.Addr {
	var raw [4]byte
	binary.BigEndian.PutUint32(raw[:], m.GetAddr())
	return netip.AddrFrom4(raw)
}

// AsLogValue implements xgrpc.ProtoLogValue for compact gRPC logging.
func (m *IPv4Address) AsLogValue() any {
	return m.ToAddr().String()
}

// MarshalJSON serializes the message as a bare dotted-quad address
// string.
func (m *IPv4Address) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.ToAddr().String())
}

// UnmarshalJSON accepts a bare native IPv4 address string.
func (m *IPv4Address) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	parsed, err := parseAddr(raw)
	if err != nil {
		return fmt.Errorf("failed to parse IP address: %w", err)
	}

	addr, err := NewIPv4AddressFromAddr(parsed)
	if err != nil {
		return err
	}

	m.Addr = addr.Addr
	return nil
}
