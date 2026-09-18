package commonpb

import (
	"errors"
	"fmt"
	"net/netip"

	"github.com/yanet-platform/xnetip"
)

var errEmptyNetwork = errors.New("empty IP network is not allowed")

// NewIPNetworkFrom creates an IPNetwork from an xnetip network value of
// either family. The inverse of ToNetwork.
func NewIPNetworkFrom(net xnetip.Network) *IPNetwork {
	if net4, ok := net.IPv4(); ok {
		return &IPNetwork{Network: &IPNetwork_V4{V4: NewIPv4NetworkFrom4(net4)}}
	}

	net6, _ := net.IPv6()
	return &IPNetwork{Network: &IPNetwork_V6{V6: NewIPv6NetworkFrom6(net6)}}
}

// ToNetwork converts the IPNetwork to a normalized xnetip network value.
//
// Returns an error if the oneof is unset or the set branch is malformed.
// The inverse of NewIPNetworkFrom.
func (m *IPNetwork) ToNetwork() (xnetip.Network, error) {
	switch network := m.GetNetwork().(type) {
	case *IPNetwork_V4:
		net, err := network.V4.ToNetwork4()
		if err != nil {
			return xnetip.Network{}, err
		}
		return xnetip.NetworkFrom4(net), nil
	case *IPNetwork_V6:
		net, err := network.V6.ToNetwork6()
		if err != nil {
			return xnetip.Network{}, err
		}
		return xnetip.NetworkFrom6(net), nil
	default:
		return xnetip.Network{}, fmt.Errorf("missing IP network")
	}
}

// networksFromPrefixes converts netip.Prefix values to network messages
// through the supplied per-family constructor.
//
// Returns an error naming the offending index if any prefix does not fit
// the constructor's family.
func networksFromPrefixes[T any](prefixes []netip.Prefix, newNetwork func(netip.Prefix) (T, error)) ([]T, error) {
	networks := make([]T, 0, len(prefixes))
	for idx, prefix := range prefixes {
		network, err := newNetwork(prefix)
		if err != nil {
			return nil, fmt.Errorf("prefixes[%d]: %w", idx, err)
		}
		networks = append(networks, network)
	}

	return networks, nil
}

// PrefixesFromNetworks converts contiguous network messages of any one
// message type to masked netip.Prefix values.
//
// Returns an error naming the field and the offending index if any network is
// malformed.
func PrefixesFromNetworks[T interface{ ToPrefix() (netip.Prefix, error) }](
	field string,
	networks []T,
) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(networks))
	for idx, network := range networks {
		prefix, err := network.ToPrefix()
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", field, idx, err)
		}
		prefixes = append(prefixes, prefix)
	}

	return prefixes, nil
}
