package commonpb

import (
	"errors"
	"fmt"
	"net/netip"
)

var errEmptyNetwork = errors.New("empty IP network is not allowed")

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
// Returns an error naming the offending index if any network is
// malformed.
func PrefixesFromNetworks[T interface{ ToPrefix() (netip.Prefix, error) }](networks []T) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(networks))
	for idx, network := range networks {
		prefix, err := network.ToPrefix()
		if err != nil {
			return nil, fmt.Errorf("prefixes[%d]: %w", idx, err)
		}
		prefixes = append(prefixes, prefix)
	}

	return prefixes, nil
}
