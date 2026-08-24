package commonpb_test

import (
	"encoding/json"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/xnetip"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// Test_NewIPv4PrefixFromPrefix_Valid verifies that IPv4
// prefixes are encoded with the typed address and their length.
//
// The fixture values mirror the Rust tests so an encoding defect fails
// identically in both languages.
func Test_NewIPv4PrefixFromPrefix_Valid(t *testing.T) {
	tests := []struct {
		name     string
		prefix   string
		wantAddr uint32
		wantLen  uint32
	}{
		{name: "default route", prefix: "0.0.0.0/0", wantAddr: 0, wantLen: 0},
		{name: "private eight", prefix: "10.0.0.0/8", wantAddr: 0x0a000000, wantLen: 8},
		{name: "host route", prefix: "10.1.2.3/32", wantAddr: 0x0a010203, wantLen: 32},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			network, err := commonpb.NewIPv4PrefixFromPrefix(netip.MustParsePrefix(tt.prefix))
			require.NoError(t, err)
			require.Equal(t, tt.wantAddr, network.Addr.Addr)
			require.Equal(t, tt.wantLen, network.PrefixLen)
		})
	}
}

// Test_NewIPv4PrefixFromPrefix_MasksHostBits verifies that
// host bits below the prefix length are cleared at construction.
func Test_NewIPv4PrefixFromPrefix_MasksHostBits(t *testing.T) {
	network, err := commonpb.NewIPv4PrefixFromPrefix(netip.MustParsePrefix("10.1.2.3/24"))
	require.NoError(t, err)
	require.Equal(t, uint32(0x0a010200), network.Addr.Addr)
	require.Equal(t, uint32(24), network.PrefixLen)
}

// Test_NewIPv4PrefixFromPrefix_RejectsNonIPv4 verifies that
// non-IPv4 prefixes and the invalid zero value return errors.
//
// The IPv4-mapped form counts as IPv6 here, consistently with the typed
// address message.
func Test_NewIPv4PrefixFromPrefix_RejectsNonIPv4(t *testing.T) {
	tests := []struct {
		name   string
		prefix netip.Prefix
	}{
		{name: "IPv6", prefix: netip.MustParsePrefix("2a02:6b8::/32")},
		{name: "IPv4-mapped IPv6", prefix: netip.MustParsePrefix("::ffff:10.0.0.0/104")},
		{name: "zero value", prefix: netip.Prefix{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := commonpb.NewIPv4PrefixFromPrefix(tt.prefix)
			require.Error(t, err)
		})
	}
}

// Test_IPv4Prefix_ToPrefix_RoundTrip verifies that conversion
// to netip and back preserves the network exactly.
func Test_IPv4Prefix_ToPrefix_RoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
	}{
		{name: "default route", prefix: "0.0.0.0/0"},
		{name: "private eight", prefix: "10.0.0.0/8"},
		{name: "subnet", prefix: "192.168.1.0/24"},
		{name: "host route", prefix: "10.1.2.3/32"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefix := netip.MustParsePrefix(tt.prefix)
			network, err := commonpb.NewIPv4PrefixFromPrefix(prefix)
			require.NoError(t, err)
			got, err := network.ToPrefix()
			require.NoError(t, err)
			require.Equal(t, prefix, got)
		})
	}
}

// Test_IPv4Prefix_ToPrefix_MasksHostBits verifies that a
// hand-built message with host bits set decodes to the masked network.
func Test_IPv4Prefix_ToPrefix_MasksHostBits(t *testing.T) {
	network := &commonpb.IPv4Prefix{
		Addr:      &commonpb.IPv4Address{Addr: 0x0a010203},
		PrefixLen: 24,
	}
	got, err := network.ToPrefix()
	require.NoError(t, err)
	require.Equal(t, netip.MustParsePrefix("10.1.2.0/24"), got)
}

// Test_IPv4Prefix_ToPrefix_Rejects verifies that an absent
// address and an out-of-range prefix length return errors.
func Test_IPv4Prefix_ToPrefix_Rejects(t *testing.T) {
	tests := []struct {
		name    string
		network *commonpb.IPv4Prefix
	}{
		{
			name:    "absent addr",
			network: &commonpb.IPv4Prefix{PrefixLen: 24},
		},
		{
			name: "prefix length above 32",
			network: &commonpb.IPv4Prefix{
				Addr:      &commonpb.IPv4Address{Addr: 0x0a000000},
				PrefixLen: 33,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.network.ToPrefix()
			require.Error(t, err)
		})
	}
}

// Test_NewIPv4PrefixFromContiguous_ZeroValue verifies that
// the zero CIDR block converts totally to the default route message.
func Test_NewIPv4PrefixFromContiguous_ZeroValue(t *testing.T) {
	network := commonpb.NewIPv4PrefixFromContiguous(xnetip.Contiguous[xnetip.Network4]{})
	require.NotNil(t, network.Addr)
	require.Equal(t, uint32(0), network.Addr.Addr)
	require.Equal(t, uint32(0), network.PrefixLen)
}

// Test_IPv4Prefix_WireRoundTrip verifies golden wire bytes
// shared with the Rust tests and that decode reproduces the network.
//
// The default route is not an empty message: the present-but-zero
// address encodes as two bytes, and decode relies on that presence to
// tell the default route from a malformed message.
func Test_IPv4Prefix_WireRoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		prefix    string
		wantBytes []byte
	}{
		{
			name:      "default route",
			prefix:    "0.0.0.0/0",
			wantBytes: []byte{0x0a, 0x00},
		},
		{
			name:      "host route",
			prefix:    "10.1.2.3/32",
			wantBytes: []byte{0x0a, 0x05, 0x0d, 0x03, 0x02, 0x01, 0x0a, 0x10, 0x20},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original, err := commonpb.NewIPv4PrefixFromPrefix(netip.MustParsePrefix(tt.prefix))
			require.NoError(t, err)

			data, err := proto.Marshal(original)
			require.NoError(t, err)
			require.Equal(t, tt.wantBytes, data)

			var got commonpb.IPv4Prefix
			require.NoError(t, proto.Unmarshal(data, &got))
			prefix, err := got.ToPrefix()
			require.NoError(t, err)
			require.Equal(t, netip.MustParsePrefix(tt.prefix), prefix)
		})
	}
}

// Test_IPv4Prefix_MarshalJSON verifies that the network
// serializes as a bare CIDR string, the same form the Rust side emits.
func Test_IPv4Prefix_MarshalJSON(t *testing.T) {
	network, err := commonpb.NewIPv4PrefixFromPrefix(netip.MustParsePrefix("10.1.2.0/24"))
	require.NoError(t, err)
	got, err := json.Marshal(network)
	require.NoError(t, err)
	require.Equal(t, `"10.1.2.0/24"`, string(got))
}

// Test_IPv4Prefix_MarshalJSON_RejectsAbsentAddr verifies that
// a malformed message fails to serialize instead of inventing a network.
func Test_IPv4Prefix_MarshalJSON_RejectsAbsentAddr(t *testing.T) {
	network := &commonpb.IPv4Prefix{PrefixLen: 24}
	_, err := json.Marshal(network)
	require.Error(t, err)
}

// Test_IPv4Prefix_UnmarshalJSON verifies that only bare IPv4
// CIDR strings are accepted, with host bits masked off.
func Test_IPv4Prefix_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "subnet", input: `"10.1.2.0/24"`, want: "10.1.2.0/24"},
		{name: "unmasked host bits", input: `"10.1.2.3/24"`, want: "10.1.2.0/24"},
		{name: "IPv6", input: `"2a02:6b8::/32"`, wantErr: true},
		{name: "IPv4-mapped IPv6", input: `"::ffff:10.0.0.0/104"`, wantErr: true},
		{name: "bare address", input: `"10.1.2.3"`, wantErr: true},
		{name: "empty string", input: `""`, wantErr: true},
		{name: "malformed network", input: `"not-a-network"`, wantErr: true},
		{name: "object form", input: `{"network":"10.1.2.0/24"}`, wantErr: true},
		{name: "invalid JSON", input: `"10.1`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var network commonpb.IPv4Prefix
			err := json.Unmarshal([]byte(tt.input), &network)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			got, err := network.ToPrefix()
			require.NoError(t, err)
			require.Equal(t, netip.MustParsePrefix(tt.want), got)
		})
	}
}

// Test_IPv4Prefix_JSONRoundTrip verifies that marshaling and
// unmarshaling reproduce the original message.
func Test_IPv4Prefix_JSONRoundTrip(t *testing.T) {
	original, err := commonpb.NewIPv4PrefixFromPrefix(netip.MustParsePrefix("192.168.1.0/24"))
	require.NoError(t, err)

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var got commonpb.IPv4Prefix
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, original.Addr.Addr, got.Addr.Addr)
	require.Equal(t, original.PrefixLen, got.PrefixLen)
}

// Test_IPv4Prefix_AsLogValue verifies compact CIDR rendering
// for request logs and the invalid fallback for a malformed message.
func Test_IPv4Prefix_AsLogValue(t *testing.T) {
	network, err := commonpb.NewIPv4PrefixFromPrefix(netip.MustParsePrefix("10.1.2.0/24"))
	require.NoError(t, err)
	require.Equal(t, "10.1.2.0/24", network.AsLogValue())

	malformed := &commonpb.IPv4Prefix{PrefixLen: 24}
	require.Equal(t, "invalid", malformed.AsLogValue())
}
