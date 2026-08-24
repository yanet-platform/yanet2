package commonpb

import (
	"encoding/json"
	"fmt"

	"github.com/yanet-platform/xnetip"
)

// NewIPv6NetworkFrom6 creates an IPv6Network from an xnetip
// network value. The inverse of ToNetwork6.
func NewIPv6NetworkFrom6(net xnetip.Network6) *IPv6Network {
	return &IPv6Network{
		Addr: NewIPv6Address(net.Addr().As16()),
		Mask: NewIPv6Address(net.Mask().As16()),
	}
}

// ToNetwork6 converts the IPv6Network to a normalized xnetip network
// value, rejecting an absent addr or mask. The inverse of NewIPv6NetworkFrom6.
func (m *IPv6Network) ToNetwork6() (xnetip.Network6, error) {
	if m.GetAddr() == nil {
		return xnetip.Network6{}, fmt.Errorf("missing network address")
	}
	if m.GetMask() == nil {
		return xnetip.Network6{}, fmt.Errorf("missing network mask")
	}

	net, err := xnetip.Network6From(m.GetAddr().ToAddr(), m.GetMask().ToAddr())
	if err != nil {
		return xnetip.Network6{}, fmt.Errorf("failed to build IP network: %w", err)
	}
	return net, nil
}

// AsLogValue implements xgrpc.ProtoLogValue for compact gRPC logging.
func (m *IPv6Network) AsLogValue() any {
	net, err := m.ToNetwork6()
	if err != nil {
		return "invalid"
	}

	return net.String()
}

// MarshalJSON serializes the network as a bare string: the CIDR form
// when the mask is contiguous, the address/mask form otherwise.
func (m *IPv6Network) MarshalJSON() ([]byte, error) {
	net, err := m.ToNetwork6()
	if err != nil {
		return nil, err
	}
	return json.Marshal(net.String())
}

// UnmarshalJSON accepts a bare string in any form xnetip parsing accepts:
// CIDR, explicit address/mask, or a bare host address.
func (m *IPv6Network) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == "" {
		return fmt.Errorf("empty IP network is not allowed")
	}

	net, err := xnetip.ParseNetwork6(raw)
	if err != nil {
		return fmt.Errorf("failed to parse IP network: %w", err)
	}

	parsed := NewIPv6NetworkFrom6(net)
	m.Addr = parsed.Addr
	m.Mask = parsed.Mask
	return nil
}
