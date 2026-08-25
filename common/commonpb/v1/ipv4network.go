package commonpb

import (
	"encoding/json"
	"fmt"

	"github.com/yanet-platform/xnetip"
)

// NewIPv4NetworkFrom4 creates an IPv4Network from an xnetip
// network value. The inverse of ToNetwork4.
func NewIPv4NetworkFrom4(net xnetip.Network4) *IPv4Network {
	return &IPv4Network{
		Addr: NewIPv4Address(net.Addr().As4()),
		Mask: NewIPv4Address(net.Mask().As4()),
	}
}

// ToNetwork4 converts the IPv4Network to a normalized xnetip network
// value, rejecting an absent addr or mask. The inverse of NewIPv4NetworkFrom4.
func (m *IPv4Network) ToNetwork4() (xnetip.Network4, error) {
	if m.GetAddr() == nil {
		return xnetip.Network4{}, fmt.Errorf("missing network address")
	}
	if m.GetMask() == nil {
		return xnetip.Network4{}, fmt.Errorf("missing network mask")
	}

	net, err := xnetip.Network4From(m.GetAddr().ToAddr(), m.GetMask().ToAddr())
	if err != nil {
		return xnetip.Network4{}, fmt.Errorf("failed to build IP network: %w", err)
	}
	return net, nil
}

// AsLogValue implements xgrpc.ProtoLogValue for compact gRPC logging.
func (m *IPv4Network) AsLogValue() any {
	net, err := m.ToNetwork4()
	if err != nil {
		return "invalid"
	}

	return net.String()
}

// MarshalJSON serializes the network as a bare string: the CIDR form
// when the mask is contiguous, the address/mask form otherwise.
func (m *IPv4Network) MarshalJSON() ([]byte, error) {
	net, err := m.ToNetwork4()
	if err != nil {
		return nil, err
	}
	return json.Marshal(net.String())
}

// UnmarshalJSON accepts a bare string in any form xnetip parsing accepts:
// CIDR, explicit address/mask, or a bare host address.
func (m *IPv4Network) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == "" {
		return fmt.Errorf("failed to parse IP network: %w", errEmptyNetwork)
	}

	net, err := xnetip.ParseNetwork4(raw)
	if err != nil {
		return fmt.Errorf("failed to parse IP network: %w", err)
	}

	parsed := NewIPv4NetworkFrom4(net)
	m.Addr = parsed.Addr
	m.Mask = parsed.Mask
	return nil
}
