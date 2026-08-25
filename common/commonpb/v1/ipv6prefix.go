package commonpb

import (
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/yanet-platform/xnetip"
)

// NewIPv6PrefixFromContiguous creates a IPv6Prefix
// from an xnetip IPv6 CIDR block.
//
// The conversion is total: the block is masked by its own invariant. The
// inverse of ToContiguous.
func NewIPv6PrefixFromContiguous(net xnetip.Contiguous[xnetip.Network6]) *IPv6Prefix {
	return &IPv6Prefix{
		Addr:      NewIPv6Address(net.Network().Addr().As16()),
		PrefixLen: uint32(net.PrefixLen()),
	}
}

// NewIPv6PrefixFromPrefix creates a IPv6Prefix from
// a netip.Prefix value, masking off any host bits.
//
// Returns an error if prefix is not a valid IPv6 prefix; the IPv4-mapped
// form counts as IPv6 here, consistently with IPv6Address.
func NewIPv6PrefixFromPrefix(prefix netip.Prefix) (*IPv6Prefix, error) {
	net, ok := xnetip.ContiguousFromPrefix6(prefix)
	if !ok {
		return nil, fmt.Errorf("not a valid IPv6 prefix: %s", prefix)
	}
	return NewIPv6PrefixFromContiguous(net), nil
}

// NewIPv6PrefixesFromPrefixes creates IPv6Prefix messages from IPv6
// netip.Prefix values, masking off any host bits.
//
// Returns an error if any prefix is invalid or not IPv6.
func NewIPv6PrefixesFromPrefixes(prefixes []netip.Prefix) ([]*IPv6Prefix, error) {
	return networksFromPrefixes(prefixes, NewIPv6PrefixFromPrefix)
}

// ToContiguous converts the IPv6Prefix to an xnetip IPv6 CIDR
// block.
//
// Returns an error if addr is absent or prefix_len exceeds 128; the
// latter wraps xnetip.ErrCIDROverflow. The returned block is masked, so
// host bits never leak out even if the message was constructed by hand.
// The inverse of NewIPv6PrefixFromContiguous.
func (m *IPv6Prefix) ToContiguous() (xnetip.Contiguous[xnetip.Network6], error) {
	if m.GetAddr() == nil {
		return xnetip.Contiguous[xnetip.Network6]{}, fmt.Errorf("missing network address")
	}
	net, err := xnetip.ContiguousFromCIDR6(m.GetAddr().ToAddr(), int(m.GetPrefixLen()))
	if err != nil {
		return xnetip.Contiguous[xnetip.Network6]{}, fmt.Errorf("failed to build IP network: %w", err)
	}
	return net, nil
}

// ToPrefix converts the IPv6Prefix back to a masked
// netip.Prefix value.
//
// Returns an error exactly when ToContiguous does.
func (m *IPv6Prefix) ToPrefix() (netip.Prefix, error) {
	net, err := m.ToContiguous()
	if err != nil {
		return netip.Prefix{}, err
	}
	return net.Prefix(), nil
}

// AsLogValue implements xgrpc.ProtoLogValue for compact gRPC logging.
func (m *IPv6Prefix) AsLogValue() any {
	prefix, err := m.ToPrefix()
	if err != nil {
		return "invalid"
	}

	return prefix.String()
}

// MarshalJSON serializes the network as a bare CIDR string.
func (m *IPv6Prefix) MarshalJSON() ([]byte, error) {
	prefix, err := m.ToPrefix()
	if err != nil {
		return nil, err
	}
	return json.Marshal(prefix.String())
}

// UnmarshalJSON accepts a bare IPv6 CIDR string.
func (m *IPv6Prefix) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == "" {
		return fmt.Errorf("failed to parse IP network: %w", errEmptyNetwork)
	}

	prefix, err := netip.ParsePrefix(raw)
	if err != nil {
		return fmt.Errorf("failed to parse IP network: %w", err)
	}
	parsed, err := NewIPv6PrefixFromPrefix(prefix)
	if err != nil {
		return err
	}

	m.Addr = parsed.Addr
	m.PrefixLen = parsed.PrefixLen
	return nil
}
