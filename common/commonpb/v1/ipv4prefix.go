package commonpb

import (
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/yanet-platform/xnetip"
)

// NewIPv4PrefixFromContiguous creates a IPv4Prefix
// from an xnetip IPv4 CIDR block.
//
// The conversion is total: the block is masked by its own invariant. The
// inverse of ToContiguous.
func NewIPv4PrefixFromContiguous(net xnetip.Contiguous[xnetip.Network4]) *IPv4Prefix {
	return &IPv4Prefix{
		Addr:      NewIPv4Address(net.Network().Addr().As4()),
		PrefixLen: uint32(net.PrefixLen()),
	}
}

// NewIPv4PrefixFromPrefix creates a IPv4Prefix from
// a netip.Prefix value, masking off any host bits.
//
// Returns an error if prefix is not a valid IPv4 prefix; the IPv4-mapped
// form counts as IPv6 here, consistently with IPv4Address.
func NewIPv4PrefixFromPrefix(prefix netip.Prefix) (*IPv4Prefix, error) {
	net, ok := xnetip.ContiguousFromPrefix4(prefix)
	if !ok {
		return nil, fmt.Errorf("not a valid IPv4 prefix: %s", prefix)
	}
	return NewIPv4PrefixFromContiguous(net), nil
}

// NewIPv4PrefixesFromPrefixes creates IPv4Prefix messages from IPv4
// netip.Prefix values, masking off any host bits.
//
// Returns an error if any prefix is invalid or not IPv4.
func NewIPv4PrefixesFromPrefixes(prefixes []netip.Prefix) ([]*IPv4Prefix, error) {
	return networksFromPrefixes(prefixes, NewIPv4PrefixFromPrefix)
}

// ToContiguous converts the IPv4Prefix to an xnetip IPv4 CIDR
// block.
//
// Returns an error if addr is absent or prefix_len exceeds 32; the
// latter wraps xnetip.ErrCIDROverflow. The returned block is masked, so
// host bits never leak out even if the message was constructed by hand.
// The inverse of NewIPv4PrefixFromContiguous.
func (m *IPv4Prefix) ToContiguous() (xnetip.Contiguous[xnetip.Network4], error) {
	if m.GetAddr() == nil {
		return xnetip.Contiguous[xnetip.Network4]{}, fmt.Errorf("missing network address")
	}
	net, err := xnetip.ContiguousFromCIDR4(m.GetAddr().ToAddr(), int(m.GetPrefixLen()))
	if err != nil {
		return xnetip.Contiguous[xnetip.Network4]{}, fmt.Errorf("failed to build IP network: %w", err)
	}
	return net, nil
}

// ToPrefix converts the IPv4Prefix back to a masked
// netip.Prefix value.
//
// Returns an error exactly when ToContiguous does.
func (m *IPv4Prefix) ToPrefix() (netip.Prefix, error) {
	net, err := m.ToContiguous()
	if err != nil {
		return netip.Prefix{}, err
	}
	return net.Prefix(), nil
}

// AsLogValue implements xgrpc.ProtoLogValue for compact gRPC logging.
func (m *IPv4Prefix) AsLogValue() any {
	prefix, err := m.ToPrefix()
	if err != nil {
		return "invalid"
	}

	return prefix.String()
}

// MarshalJSON serializes the network as a bare CIDR string, the same
// form the Rust side renders.
func (m *IPv4Prefix) MarshalJSON() ([]byte, error) {
	prefix, err := m.ToPrefix()
	if err != nil {
		return nil, err
	}
	return json.Marshal(prefix.String())
}

// UnmarshalJSON accepts a bare IPv4 CIDR string.
func (m *IPv4Prefix) UnmarshalJSON(data []byte) error {
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
	parsed, err := NewIPv4PrefixFromPrefix(prefix)
	if err != nil {
		return err
	}

	m.Addr = parsed.Addr
	m.PrefixLen = parsed.PrefixLen
	return nil
}
