package commonpb

import (
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/yanet-platform/xnetip"
)

// NewContiguousIPv6NetworkFromContiguous creates a ContiguousIPv6Network
// from an xnetip IPv6 CIDR block.
//
// The conversion is total: the block is masked by its own invariant. The
// inverse of ToContiguous.
func NewContiguousIPv6NetworkFromContiguous(net xnetip.Contiguous[xnetip.Network6]) *ContiguousIPv6Network {
	return &ContiguousIPv6Network{
		Addr:      NewIPv6Address(net.Network().Addr().As16()),
		PrefixLen: uint32(net.PrefixLen()),
	}
}

// NewContiguousIPv6NetworkFromPrefix creates a ContiguousIPv6Network from
// a netip.Prefix value, masking off any host bits.
//
// Returns an error if prefix is not a valid IPv6 prefix; the IPv4-mapped
// form counts as IPv6 here, consistently with IPv6Address.
func NewContiguousIPv6NetworkFromPrefix(prefix netip.Prefix) (*ContiguousIPv6Network, error) {
	net, ok := xnetip.ContiguousFromPrefix6(prefix)
	if !ok {
		return nil, fmt.Errorf("not a valid IPv6 prefix: %s", prefix)
	}
	return NewContiguousIPv6NetworkFromContiguous(net), nil
}

// ParseContiguousIPv6Network parses s as an IPv6 CIDR prefix, masking off
// any host bits.
func ParseContiguousIPv6Network(s string) (*ContiguousIPv6Network, error) {
	prefix, err := netip.ParsePrefix(s)
	if err != nil {
		return nil, fmt.Errorf("failed to parse IP network: %w", err)
	}
	return NewContiguousIPv6NetworkFromPrefix(prefix)
}

// ToContiguous converts the ContiguousIPv6Network to an xnetip IPv6 CIDR
// block.
//
// Returns an error if addr is absent or prefix_len exceeds 128; the
// latter wraps xnetip.ErrCIDROverflow. The returned block is masked, so
// host bits never leak out even if the message was constructed by hand.
// The inverse of NewContiguousIPv6NetworkFromContiguous.
func (m *ContiguousIPv6Network) ToContiguous() (xnetip.Contiguous[xnetip.Network6], error) {
	if m.GetAddr() == nil {
		return xnetip.Contiguous[xnetip.Network6]{}, fmt.Errorf("missing network address")
	}
	net, err := xnetip.ContiguousFromCIDR6(m.GetAddr().ToAddr(), int(m.GetPrefixLen()))
	if err != nil {
		return xnetip.Contiguous[xnetip.Network6]{}, fmt.Errorf("failed to build IP network: %w", err)
	}
	return net, nil
}

// ToPrefix converts the ContiguousIPv6Network back to a masked
// netip.Prefix value.
//
// Returns an error exactly when ToContiguous does.
func (m *ContiguousIPv6Network) ToPrefix() (netip.Prefix, error) {
	net, err := m.ToContiguous()
	if err != nil {
		return netip.Prefix{}, err
	}
	return net.Prefix(), nil
}

// AsLogValue implements xgrpc.ProtoLogValue for compact gRPC logging.
func (m *ContiguousIPv6Network) AsLogValue() any {
	prefix, err := m.ToPrefix()
	if err != nil {
		return "invalid"
	}

	return prefix.String()
}

// MarshalJSON serializes the network as a bare CIDR string, the same
// form the Rust side renders.
func (m *ContiguousIPv6Network) MarshalJSON() ([]byte, error) {
	prefix, err := m.ToPrefix()
	if err != nil {
		return nil, err
	}
	return json.Marshal(prefix.String())
}

// UnmarshalJSON accepts a bare IPv6 CIDR string.
func (m *ContiguousIPv6Network) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == "" {
		return fmt.Errorf("empty IP network is not allowed")
	}

	parsed, err := ParseContiguousIPv6Network(raw)
	if err != nil {
		return err
	}

	m.Addr = parsed.Addr
	m.PrefixLen = parsed.PrefixLen
	return nil
}
