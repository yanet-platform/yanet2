package commonpb_test

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// TestPrefixesFromNetworks_TypedNetworks asserts that a family-typed
// network list decodes to masked netip.Prefix values.
func TestPrefixesFromNetworks_TypedNetworks(t *testing.T) {
	first, err := commonpb.NewIPv4PrefixFromPrefix(netip.MustParsePrefix("10.0.0.0/24"))
	require.NoError(t, err)
	second, err := commonpb.NewIPv4PrefixFromPrefix(netip.MustParsePrefix("10.0.1.1/24"))
	require.NoError(t, err)

	prefixes, err := commonpb.PrefixesFromNetworks([]*commonpb.IPv4Prefix{first, second})
	require.NoError(t, err)
	require.Equal(t, []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/24"),
		netip.MustParsePrefix("10.0.1.0/24"),
	}, prefixes)
}

// TestPrefixesFromNetworks_MalformedTypedNetwork asserts that a malformed
// family-typed network is rejected with the offending index named.
func TestPrefixesFromNetworks_MalformedTypedNetwork(t *testing.T) {
	_, err := commonpb.PrefixesFromNetworks([]*commonpb.IPv4Prefix{
		{Addr: &commonpb.IPv4Address{Addr: 0x0a000000}, PrefixLen: 33},
	})
	require.ErrorContains(t, err, "prefixes[0]")
}
