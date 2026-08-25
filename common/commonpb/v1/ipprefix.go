package commonpb

import (
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/yanet-platform/xnetip"
)

// NewIPPrefixFromContiguous creates a IPPrefix from
// an xnetip CIDR block.
//
// The conversion is total: the block is masked by its own invariant, so
// the message carries the base address in the block's family width (four
// bytes for IPv4, sixteen for IPv6) and the prefix length verbatim. The
// inverse of ToContiguous.
func NewIPPrefixFromContiguous(net xnetip.Contiguous[xnetip.Network]) *IPPrefix {
	return &IPPrefix{
		Addr:      NewIPAddressFromAddr(net.Network().Addr()),
		PrefixLen: uint32(net.PrefixLen()),
	}
}

// NewIPPrefixFromPrefix creates a IPPrefix from a
// netip.Prefix value, masking off any host bits.
//
// Returns an error if prefix is not valid.
func NewIPPrefixFromPrefix(prefix netip.Prefix) (*IPPrefix, error) {
	net, ok := xnetip.ContiguousFromPrefix(prefix)
	if !ok {
		return nil, fmt.Errorf("invalid prefix")
	}
	return NewIPPrefixFromContiguous(net), nil
}

// NetworksFromPrefixes creates IPPrefix messages from netip.Prefix
// values, masking off any host bits.
//
// Returns an error if any prefix is not valid.
func NetworksFromPrefixes(prefixes []netip.Prefix) ([]*IPPrefix, error) {
	return networksFromPrefixes(prefixes, NewIPPrefixFromPrefix)
}

// ToContiguous converts the IPPrefix to an xnetip CIDR block.
//
// Returns an error if addr is missing or malformed, or if prefix_len
// exceeds the address family's bit length; the latter wraps
// xnetip.ErrCIDROverflow. The returned block is masked, so host bits never
// leak out even if the message was constructed by hand, and the family
// follows the address width: a sixteen-byte IPv4-mapped address stays
// IPv6, consistently with IPAddress. The inverse of
// NewIPPrefixFromContiguous.
func (m *IPPrefix) ToContiguous() (xnetip.Contiguous[xnetip.Network], error) {
	addr, err := m.GetAddr().ToAddr()
	if err != nil {
		return xnetip.Contiguous[xnetip.Network]{}, fmt.Errorf("failed to parse network address: %w", err)
	}
	net, err := xnetip.ContiguousFromCIDR(addr, int(m.GetPrefixLen()))
	if err != nil {
		return xnetip.Contiguous[xnetip.Network]{}, fmt.Errorf("failed to build IP network: %w", err)
	}
	return net, nil
}

// ToPrefix converts the IPPrefix back to a netip.Prefix value.
//
// Returns an error if addr is malformed or if prefix_len exceeds the
// address family's bit length, exactly when ToContiguous does. The returned
// prefix is masked, so host bits never leak out even if the message was
// constructed by hand.
func (m *IPPrefix) ToPrefix() (netip.Prefix, error) {
	net, err := m.ToContiguous()
	if err != nil {
		return netip.Prefix{}, err
	}
	return net.Prefix(), nil
}

// AsLogValue implements xgrpc.ProtoLogValue for compact gRPC logging.
func (m *IPPrefix) AsLogValue() any {
	prefix, err := m.ToPrefix()
	if err != nil {
		return "invalid"
	}

	return prefix.String()
}

// ipPrefixJSON is the JSON wire shape shared by MarshalJSON and
// UnmarshalJSON.
type ipPrefixJSON struct {
	// Network is the CIDR string.
	Network string `json:"network"`
}

// MarshalJSON serializes the network as a human-readable CIDR string.
func (m *IPPrefix) MarshalJSON() ([]byte, error) {
	prefix, err := m.ToPrefix()
	if err != nil {
		return nil, err
	}
	return json.Marshal(ipPrefixJSON{Network: prefix.String()})
}

// UnmarshalJSON accepts the network as a CIDR string under the "network"
// key.
func (m *IPPrefix) UnmarshalJSON(data []byte) error {
	var raw ipPrefixJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Network == "" {
		return fmt.Errorf("empty IP network is not allowed")
	}

	prefix, err := netip.ParsePrefix(raw.Network)
	if err != nil {
		return fmt.Errorf("failed to parse IP network: %w", err)
	}
	parsed, err := NewIPPrefixFromPrefix(prefix)
	if err != nil {
		return err
	}

	m.Addr = parsed.Addr
	m.PrefixLen = parsed.PrefixLen
	return nil
}
