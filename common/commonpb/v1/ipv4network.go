package commonpb

import (
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/yanet-platform/xnetip"
)

// NewContiguousIPv4NetworkFromContiguous creates a ContiguousIPv4Network
// from an xnetip IPv4 CIDR block.
//
// The conversion is total: the block is masked by its own invariant. The
// inverse of ToContiguous.
func NewContiguousIPv4NetworkFromContiguous(net xnetip.Contiguous[xnetip.Network4]) *ContiguousIPv4Network {
	return &ContiguousIPv4Network{
		Addr:      NewIPv4Address(net.Network().Addr().As4()),
		PrefixLen: uint32(net.PrefixLen()),
	}
}

// NewContiguousIPv4NetworkFromPrefix creates a ContiguousIPv4Network from
// a netip.Prefix value, masking off any host bits.
//
// Returns an error if prefix is not a valid IPv4 prefix; the IPv4-mapped
// form counts as IPv6 here, consistently with IPv4Address.
func NewContiguousIPv4NetworkFromPrefix(prefix netip.Prefix) (*ContiguousIPv4Network, error) {
	net, ok := xnetip.ContiguousFromPrefix4(prefix)
	if !ok {
		return nil, fmt.Errorf("not a valid IPv4 prefix: %s", prefix)
	}
	return NewContiguousIPv4NetworkFromContiguous(net), nil
}

// ToContiguous converts the ContiguousIPv4Network to an xnetip IPv4 CIDR
// block.
//
// Returns an error if addr is absent or prefix_len exceeds 32; the
// latter wraps xnetip.ErrCIDROverflow. The returned block is masked, so
// host bits never leak out even if the message was constructed by hand.
// The inverse of NewContiguousIPv4NetworkFromContiguous.
func (m *ContiguousIPv4Network) ToContiguous() (xnetip.Contiguous[xnetip.Network4], error) {
	if m.GetAddr() == nil {
		return xnetip.Contiguous[xnetip.Network4]{}, fmt.Errorf("missing network address")
	}
	net, err := xnetip.ContiguousFromCIDR4(m.GetAddr().ToAddr(), int(m.GetPrefixLen()))
	if err != nil {
		return xnetip.Contiguous[xnetip.Network4]{}, fmt.Errorf("failed to build IP network: %w", err)
	}
	return net, nil
}

// ToPrefix converts the ContiguousIPv4Network back to a masked
// netip.Prefix value.
//
// Returns an error exactly when ToContiguous does.
func (m *ContiguousIPv4Network) ToPrefix() (netip.Prefix, error) {
	net, err := m.ToContiguous()
	if err != nil {
		return netip.Prefix{}, err
	}
	return net.Prefix(), nil
}

// AsLogValue implements xgrpc.ProtoLogValue for compact gRPC logging.
func (m *ContiguousIPv4Network) AsLogValue() any {
	prefix, err := m.ToPrefix()
	if err != nil {
		return "invalid"
	}

	return prefix.String()
}

// MarshalJSON serializes the network as a bare CIDR string, the same
// form the Rust side renders.
func (m *ContiguousIPv4Network) MarshalJSON() ([]byte, error) {
	prefix, err := m.ToPrefix()
	if err != nil {
		return nil, err
	}
	return json.Marshal(prefix.String())
}

// UnmarshalJSON accepts a bare IPv4 CIDR string.
func (m *ContiguousIPv4Network) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == "" {
		return fmt.Errorf("empty IP network is not allowed")
	}

	prefix, err := netip.ParsePrefix(raw)
	if err != nil {
		return fmt.Errorf("failed to parse IP network: %w", err)
	}
	parsed, err := NewContiguousIPv4NetworkFromPrefix(prefix)
	if err != nil {
		return err
	}

	m.Addr = parsed.Addr
	m.PrefixLen = parsed.PrefixLen
	return nil
}
