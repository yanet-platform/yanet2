package commonpb_test

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/xnetip"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// Test_PrefixesFromNetworks_NamesFieldAndIndex verifies that a malformed network is reported
// under the caller's field name and its index.
func Test_PrefixesFromNetworks_NamesFieldAndIndex(t *testing.T) {
	valid, err := commonpb.NewIPv6PrefixFromPrefix(netip.MustParsePrefix("2001:db8::/32"))
	require.NoError(t, err)

	_, err = commonpb.PrefixesFromNetworks("prefixes6", []*commonpb.IPv6Prefix{valid, {}})
	require.ErrorContains(t, err, "prefixes6[1]: ")
}

// Test_IPNetwork_ToNetwork_RoundTrip verifies that networks of both families,
// non-contiguous masks included, survive the construction and decode round
// trip in the branch of their own family.
func Test_IPNetwork_ToNetwork_RoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		network string
		wantV4  bool
	}{
		{name: "IPv4 prefix", network: "192.0.2.0/24", wantV4: true},
		{name: "IPv4 non-contiguous mask", network: "192.0.2.0/255.0.255.0", wantV4: true},
		{name: "IPv6 prefix", network: "2001:db8::/32"},
		{name: "IPv6 non-contiguous mask", network: "2001:db8::/ffff:0:ffff::"},
		{name: "IPv4-mapped IPv6 stays IPv6", network: "::ffff:192.0.2.0/120"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			net := xnetip.MustParseNetwork(tc.network)

			message := commonpb.NewIPNetworkFrom(net)
			require.Equal(t, tc.wantV4, message.GetV4() != nil)
			require.Equal(t, !tc.wantV4, message.GetV6() != nil)

			got, err := message.ToNetwork()
			require.NoError(t, err)
			require.Equal(t, net, got)
		})
	}
}

// Test_IPNetwork_ToNetwork_Malformed verifies that an unset oneof and a
// malformed branch decode to an error rather than a zero network.
func Test_IPNetwork_ToNetwork_Malformed(t *testing.T) {
	cases := []struct {
		name    string
		message *commonpb.IPNetwork
	}{
		{name: "nil message"},
		{name: "unset oneof", message: &commonpb.IPNetwork{}},
		{
			name: "IPv4 branch with missing mask",
			message: &commonpb.IPNetwork{Network: &commonpb.IPNetwork_V4{V4: &commonpb.IPv4Network{
				Addr: commonpb.NewIPv4Address([4]byte{192, 0, 2, 0}),
			}}},
		},
		{
			name:    "IPv6 branch with missing address",
			message: &commonpb.IPNetwork{Network: &commonpb.IPNetwork_V6{V6: &commonpb.IPv6Network{}}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.message.ToNetwork()
			require.Error(t, err)
		})
	}
}
