package rib_test

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/operators/bird-adapter/internal/rib"
)

var (
	sourceV4 = netip.MustParseAddr("192.0.2.1")
	sourceV6 = netip.MustParseAddr("2001:db8:ffff::1")
)

// Test_ToPBMPLSRoute_TunnelFamily verifies that a tunnel's destination and
// source take the nexthop's family, whatever the prefix's family.
func Test_ToPBMPLSRoute_TunnelFamily(t *testing.T) {
	cases := []struct {
		name        string
		prefix      string
		nexthop     string
		destination string
		source      netip.Addr
	}{
		{
			name:        "IPv4 route over an IPv4 nexthop",
			prefix:      "10.0.0.0/8",
			nexthop:     "::ffff:198.51.100.1",
			destination: "198.51.100.1",
			source:      sourceV4,
		},
		{
			name:        "IPv4 route over an IPv6 nexthop",
			prefix:      "10.0.0.0/8",
			nexthop:     "2001:db8::1",
			destination: "2001:db8::1",
			source:      sourceV6,
		},
		{
			name:        "IPv6 route over an IPv4 nexthop",
			prefix:      "2001:db8:1::/48",
			nexthop:     "::ffff:198.51.100.1",
			destination: "198.51.100.1",
			source:      sourceV4,
		},
		{
			name:        "IPv6 route over an IPv6 nexthop",
			prefix:      "2001:db8:1::/48",
			nexthop:     "2001:db8::1",
			destination: "2001:db8::1",
			source:      sourceV6,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			route := rib.Route{
				Prefix:  netip.MustParsePrefix(tc.prefix),
				NextHop: netip.MustParseAddr(tc.nexthop),
			}

			rule, err := rib.ToPBMPLSRoute(&route, sourceV4, sourceV6)
			require.NoError(t, err)

			destination, err := rule.GetNexthop().GetDestinationIp().ToAddr()
			require.NoError(t, err)
			source, err := rule.GetNexthop().GetSourceIp().ToAddr()
			require.NoError(t, err)
			require.Equal(t, netip.MustParseAddr(tc.destination), destination)
			require.Equal(t, tc.source, source)
		})
	}
}

// Test_ToPBMPLSRoute_FirstWeightCommunity verifies that a route carrying two
// weight communities is sent with the first one's, the weight it is diffed by.
func Test_ToPBMPLSRoute_FirstWeightCommunity(t *testing.T) {
	route := rib.Route{
		Prefix:  netip.MustParsePrefix("2001:db8:1::/48"),
		NextHop: netip.MustParseAddr("2001:db8::1"),
		LargeCommunities: []rib.LargeCommunity{
			{ASN: 13238, Function: 1, Value: 5},
			{ASN: 13238, Function: 1, Value: 7},
		},
	}

	rule, err := rib.ToPBMPLSRoute(&route, sourceV4, sourceV6)
	require.NoError(t, err)
	require.Equal(t, uint64(5), rule.GetNexthop().GetWeight())
}
