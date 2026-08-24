package commonpb_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/xnetip"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// Test_IPv6Network_RoundTrip asserts that contiguous, bi-contiguous, and
// hole-inside-a-half masks all survive the New/ToNetwork6 round trip.
func Test_IPv6Network_RoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		network string
	}{
		{name: "contiguous", network: "2001:db8::/32"},
		{name: "bi-contiguous", network: "2001:db8:1::/ffff:ffff:ffff:0:ffff::"},
		{name: "hole inside a half", network: "2001:db8::/ffff:0:ffff::"},
		{name: "host route", network: "2001:db8::1/128"},
		{name: "match all", network: "::/::"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			net, err := xnetip.ParseNetwork6(tt.network)
			require.NoError(t, err)

			got, err := commonpb.NewIPv6NetworkFrom6(net).ToNetwork6()
			require.NoError(t, err)
			require.Equal(t, net, got)
		})
	}
}

// Test_IPv6Network_ToNetwork6_MissingMask asserts that an absent mask
// submessage is rejected as malformed rather than treated as a wildcard.
func Test_IPv6Network_ToNetwork6_MissingMask(t *testing.T) {
	message := &commonpb.IPv6Network{
		Addr: commonpb.NewIPv6Address([16]byte{0x20, 0x01, 0x0d, 0xb8}),
	}

	_, err := message.ToNetwork6()
	require.Error(t, err)
}

// Test_IPv6Network_ToNetwork6_MissingAddr asserts that an absent address
// submessage is rejected as malformed.
func Test_IPv6Network_ToNetwork6_MissingAddr(t *testing.T) {
	_, err := (&commonpb.IPv6Network{}).ToNetwork6()
	require.Error(t, err)
}

// Test_IPv6Network_JSON asserts that the JSON form is a bare string: the
// CIDR form for a contiguous mask, the address/mask form otherwise.
func Test_IPv6Network_JSON(t *testing.T) {
	tests := []struct {
		name string
		text string
		json string
	}{
		{name: "contiguous renders CIDR", text: "2001:db8::/32", json: `"2001:db8::/32"`},
		{name: "bi-contiguous renders mask", text: "2001:db8:1::/ffff:ffff:ffff:0:ffff::", json: `"2001:db8:1::/ffff:ffff:ffff:0:ffff::"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var message commonpb.IPv6Network
			require.NoError(t, json.Unmarshal([]byte(`"`+tt.text+`"`), &message))

			data, err := json.Marshal(&message)
			require.NoError(t, err)
			require.Equal(t, tt.json, string(data))
		})
	}
}

// Test_IPv6Network_JSON_Rejects asserts that empty and IPv4 inputs are
// rejected on decode.
func Test_IPv6Network_JSON_Rejects(t *testing.T) {
	for _, input := range []string{`""`, `"192.0.2.0/24"`, `"garbage"`} {
		var message commonpb.IPv6Network
		require.Error(t, json.Unmarshal([]byte(input), &message), input)
	}
}
