package commonpb_test

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

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
