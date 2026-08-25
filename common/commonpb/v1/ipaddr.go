package commonpb

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
)

var (
	errEmptyAddress = errors.New("empty IP address is not allowed")
	errZonedAddress = errors.New("zoned IPv6 address is not allowed")
)

// NewIPAddressFromAddr creates an IPAddress from a netip.Addr value.
//
// If addr is the zero value, the returned message has empty addr bytes.
func NewIPAddressFromAddr(addr netip.Addr) *IPAddress {
	if !addr.IsValid() {
		return &IPAddress{}
	}
	if addr.Is4() {
		raw := addr.As4()
		return &IPAddress{Addr: raw[:]}
	}
	raw := addr.As16()
	return &IPAddress{Addr: raw[:]}
}

// NewIPAddressV4 creates an IPAddress from a 4-byte IPv4 address in
// network byte order.
func NewIPAddressV4(addr [4]byte) *IPAddress {
	return &IPAddress{Addr: addr[:]}
}

// NewIPAddressV6 creates an IPAddress from a 16-byte IPv6 address in
// network byte order.
func NewIPAddressV6(addr [16]byte) *IPAddress {
	return &IPAddress{Addr: addr[:]}
}

// ToAddr converts the IPAddress back to a netip.Addr value.
// Returns an error if the byte length is not exactly 4 or 16.
func (m *IPAddress) ToAddr() (netip.Addr, error) {
	switch len(m.GetAddr()) {
	case 4:
		return netip.AddrFrom4([4]byte(m.GetAddr())), nil
	case 16:
		return netip.AddrFrom16([16]byte(m.GetAddr())), nil
	default:
		return netip.Addr{}, fmt.Errorf("invalid IP address length: %d", len(m.GetAddr()))
	}
}

// AsLogValue implements xgrpc.ProtoLogValue for compact gRPC logging.
func (m *IPAddress) AsLogValue() any {
	addr, err := m.ToAddr()
	if err != nil {
		return "invalid"
	}

	return addr.String()
}

// MarshalJSON serializes the message as a bare IP address string.
func (m *IPAddress) MarshalJSON() ([]byte, error) {
	addr, err := m.ToAddr()
	if err != nil {
		return nil, err
	}
	return json.Marshal(addr.String())
}

// UnmarshalJSON accepts a bare IPv4 or IPv6 address string.
//
// A zoned address is rejected.
func (m *IPAddress) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	parsed, err := parseAddr(raw)
	if err != nil {
		return fmt.Errorf("failed to parse IP address: %w", err)
	}

	*m = *NewIPAddressFromAddr(parsed)
	return nil
}

// parseAddr parses a bare address string for the JSON decoders, rejecting
// an empty string and a zoned address.
func parseAddr(raw string) (netip.Addr, error) {
	if raw == "" {
		return netip.Addr{}, errEmptyAddress
	}

	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return netip.Addr{}, err
	}
	if addr.Zone() != "" {
		return netip.Addr{}, fmt.Errorf("%w: %s", errZonedAddress, raw)
	}

	return addr, nil
}
