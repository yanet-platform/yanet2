package commonpb_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/xnetip"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// Test_IPv4Network_RoundTrip asserts that contiguous and non-contiguous
// masks survive the New/ToNetwork4 round trip normalized.
func Test_IPv4Network_RoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		network string
	}{
		{name: "contiguous", network: "192.0.2.0/255.255.255.0"},
		{name: "non-contiguous", network: "192.0.2.0/255.0.255.0"},
		{name: "host route", network: "192.0.2.1/255.255.255.255"},
		{name: "match all", network: "0.0.0.0/0.0.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			net, err := xnetip.ParseNetwork4(tt.network)
			require.NoError(t, err)

			got, err := commonpb.NewIPv4NetworkFrom4(net).ToNetwork4()
			require.NoError(t, err)
			require.Equal(t, net, got)
		})
	}
}

// Test_IPv4Network_ToNetwork4_NormalizesAddr asserts that address bits
// outside a hand-constructed message's mask are cleared on decode.
func Test_IPv4Network_ToNetwork4_NormalizesAddr(t *testing.T) {
	message := &commonpb.IPv4Network{
		Addr: commonpb.NewIPv4Address([4]byte{192, 0, 2, 1}),
		Mask: commonpb.NewIPv4Address([4]byte{255, 255, 255, 0}),
	}

	net, err := message.ToNetwork4()
	require.NoError(t, err)
	require.Equal(t, "192.0.2.0/24", net.String())
}

// Test_IPv4Network_ToNetwork4_MissingAddr asserts that an absent address
// submessage is rejected as malformed.
func Test_IPv4Network_ToNetwork4_MissingAddr(t *testing.T) {
	_, err := (&commonpb.IPv4Network{Mask: commonpb.NewIPv4Address([4]byte{255, 255, 255, 0})}).ToNetwork4()
	require.Error(t, err)
}

// Test_IPv4Network_ToNetwork4_MissingMask asserts that an absent mask
// submessage is rejected as malformed rather than treated as a wildcard.
func Test_IPv4Network_ToNetwork4_MissingMask(t *testing.T) {
	_, err := (&commonpb.IPv4Network{Addr: commonpb.NewIPv4Address([4]byte{192, 0, 2, 1})}).ToNetwork4()
	require.Error(t, err)
}

// Test_IPv4Network_JSON asserts that the JSON form is a bare string: the
// CIDR form for a contiguous mask, the address/mask form otherwise.
func Test_IPv4Network_JSON(t *testing.T) {
	tests := []struct {
		name string
		text string
		json string
	}{
		{name: "contiguous renders CIDR", text: "192.0.2.0/24", json: `"192.0.2.0/24"`},
		{name: "non-contiguous renders mask", text: "192.0.2.0/255.0.255.0", json: `"192.0.2.0/255.0.255.0"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var message commonpb.IPv4Network
			require.NoError(t, json.Unmarshal([]byte(`"`+tt.text+`"`), &message))

			data, err := json.Marshal(&message)
			require.NoError(t, err)
			require.Equal(t, tt.json, string(data))
		})
	}
}

// Test_IPv4Network_JSON_Rejects asserts that empty and IPv6 inputs are
// rejected on decode.
func Test_IPv4Network_JSON_Rejects(t *testing.T) {
	for _, input := range []string{`""`, `"2001:db8::/32"`, `"garbage"`} {
		var message commonpb.IPv4Network
		require.Error(t, json.Unmarshal([]byte(input), &message), input)
	}
}
