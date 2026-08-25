package commonpb_test

import (
	"encoding/json"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// TestIPPrefix_ToPrefix_RoundTrip verifies that both families survive the
// construction and decode round trip, with host bits masked off.
func TestIPPrefix_ToPrefix_RoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   string
		wantV4 bool
	}{
		{name: "IPv4", input: "10.0.0.1/24", want: "10.0.0.0/24", wantV4: true},
		{name: "IPv6", input: "2001:db8::1/32", want: "2001:db8::/32"},
		{name: "IPv4-mapped IPv6 stays IPv6", input: "::ffff:10.0.0.1/120", want: "::ffff:10.0.0.0/120"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := commonpb.NewIPPrefixFromPrefix(netip.MustParsePrefix(tt.input))
			require.NoError(t, err)

			require.Equal(t, tt.wantV4, m.GetV4() != nil)
			require.Equal(t, !tt.wantV4, m.GetV6() != nil)

			got, err := m.ToPrefix()
			require.NoError(t, err)
			require.Equal(t, netip.MustParsePrefix(tt.want), got)
		})
	}
}

// TestNewIPPrefixFromPrefix_Invalid asserts that an invalid prefix is
// rejected at construction.
//
// Failing at construction is the contract: an invalid input must not
// produce a message that only fails later.
func TestNewIPPrefixFromPrefix_Invalid(t *testing.T) {
	_, err := commonpb.NewIPPrefixFromPrefix(netip.Prefix{})
	require.Error(t, err)
}

// TestIPPrefix_ToPrefix_Malformed verifies that an unset oneof and a
// malformed branch decode to an error rather than a zero prefix.
func TestIPPrefix_ToPrefix_Malformed(t *testing.T) {
	tests := []struct {
		name string
		m    *commonpb.IPPrefix
	}{
		{name: "unset oneof", m: &commonpb.IPPrefix{}},
		{
			name: "IPv4 branch with missing address",
			m:    &commonpb.IPPrefix{Prefix: &commonpb.IPPrefix_V4{V4: &commonpb.IPv4Prefix{PrefixLen: 24}}},
		},
		{
			name: "IPv6 branch with overflowing prefix length",
			m: &commonpb.IPPrefix{
				Prefix: &commonpb.IPPrefix_V6{V6: &commonpb.IPv6Prefix{Addr: &commonpb.IPv6Address{}, PrefixLen: 129}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.m.ToPrefix()
			require.Error(t, err)
			require.Equal(t, "invalid", tt.m.AsLogValue())
		})
	}
}

// TestIPPrefix_AsLogValue verifies the compact log rendering.
func TestIPPrefix_AsLogValue(t *testing.T) {
	m, err := commonpb.NewIPPrefixFromPrefix(netip.MustParsePrefix("10.0.0.0/24"))
	require.NoError(t, err)
	require.Equal(t, "10.0.0.0/24", m.AsLogValue())
}

// TestIPPrefix_JSONRoundTrip verifies that JSON is a bare CIDR string in
// both directions, identically to the family-typed prefix messages.
func TestIPPrefix_JSONRoundTrip(t *testing.T) {
	for _, cidr := range []string{"10.0.0.0/24", "2001:db8::/32"} {
		t.Run(cidr, func(t *testing.T) {
			m, err := commonpb.NewIPPrefixFromPrefix(netip.MustParsePrefix(cidr))
			require.NoError(t, err)

			data, err := json.Marshal(m)
			require.NoError(t, err)
			require.Equal(t, `"`+cidr+`"`, string(data))

			var decoded commonpb.IPPrefix
			require.NoError(t, json.Unmarshal(data, &decoded))
			got, err := decoded.ToPrefix()
			require.NoError(t, err)
			require.Equal(t, netip.MustParsePrefix(cidr), got)
		})
	}
}

// TestIPPrefix_UnmarshalJSON_Errors verifies that malformed JSON input is
// rejected.
func TestIPPrefix_UnmarshalJSON_Errors(t *testing.T) {
	for _, input := range []string{`""`, `"not-a-cidr"`, `"10.0.0.0"`, `{"network": "10.0.0.0/24"}`} {
		t.Run(input, func(t *testing.T) {
			var decoded commonpb.IPPrefix
			require.Error(t, json.Unmarshal([]byte(input), &decoded))
		})
	}
}
