package commonpb

import (
	"encoding/json"
	"fmt"
	"net/netip"
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

// MarshalJSON serializes addr as a human-readable IP address string.
func (m *IPAddress) MarshalJSON() ([]byte, error) {
	addr, err := m.ToAddr()
	if err != nil {
		return nil, err
	}
	return fmt.Appendf(nil, `{"addr":"%s"}`, addr.String()), nil
}

// UnmarshalJSON accepts addr as an IPv4 or IPv6 address string.
func (m *IPAddress) UnmarshalJSON(data []byte) error {
	var raw struct {
		Addr string `json:"addr"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Addr == "" {
		return fmt.Errorf("empty IP address is not allowed")
	}

	parsed, err := netip.ParseAddr(raw.Addr)
	if err != nil {
		return fmt.Errorf("failed to parse IP address: %w", err)
	}

	*m = *NewIPAddressFromAddr(parsed)
	return nil
}
