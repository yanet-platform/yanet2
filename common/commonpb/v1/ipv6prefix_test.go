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

// Test_NewIPv6PrefixFromPrefix_Valid verifies that IPv6
// prefixes are encoded with the typed address halves and their length.
//
// The fixture values mirror the Rust tests so an encoding defect fails
// identically in both languages.
func Test_NewIPv6PrefixFromPrefix_Valid(t *testing.T) {
	tests := []struct {
		name    string
		prefix  string
		wantHi  uint64
		wantLo  uint64
		wantLen uint32
	}{
		{name: "default route", prefix: "::/0", wantHi: 0, wantLo: 0, wantLen: 0},
		{
			name:    "global unicast",
			prefix:  "2a02:6b8::/32",
			wantHi:  0x2a0206b800000000,
			wantLo:  0,
			wantLen: 32,
		},
		{
			name:    "host route",
			prefix:  "2a02:6b8:0:1::100/128",
			wantHi:  0x2a0206b800000001,
			wantLo:  0x0000000000000100,
			wantLen: 128,
		},
		{
			name:    "IPv4-mapped IPv6",
			prefix:  "::ffff:10.0.0.0/104",
			wantHi:  0,
			wantLo:  0x0000ffff0a000000,
			wantLen: 104,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			network, err := commonpb.NewIPv6PrefixFromPrefix(netip.MustParsePrefix(tt.prefix))
			require.NoError(t, err)
			require.Equal(t, tt.wantHi, network.Addr.Hi)
			require.Equal(t, tt.wantLo, network.Addr.Lo)
			require.Equal(t, tt.wantLen, network.PrefixLen)
		})
	}
}

// Test_NewIPv6PrefixFromPrefix_MasksHostBits verifies that
// host bits below the prefix length are cleared at construction.
func Test_NewIPv6PrefixFromPrefix_MasksHostBits(t *testing.T) {
	network, err := commonpb.NewIPv6PrefixFromPrefix(netip.MustParsePrefix("2a02:6b8:0:1::100/64"))
	require.NoError(t, err)
	require.Equal(t, uint64(0x2a0206b800000001), network.Addr.Hi)
	require.Equal(t, uint64(0), network.Addr.Lo)
	require.Equal(t, uint32(64), network.PrefixLen)
}

// Test_NewIPv6PrefixFromPrefix_RejectsNonIPv6 verifies that
// IPv4 prefixes and the invalid zero value return errors.
func Test_NewIPv6PrefixFromPrefix_RejectsNonIPv6(t *testing.T) {
	tests := []struct {
		name   string
		prefix netip.Prefix
	}{
		{name: "IPv4", prefix: netip.MustParsePrefix("10.0.0.0/8")},
		{name: "zero value", prefix: netip.Prefix{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := commonpb.NewIPv6PrefixFromPrefix(tt.prefix)
			require.Error(t, err)
		})
	}
}

// Test_NewIPv6PrefixFromContiguous_ZeroValue verifies that
// the zero CIDR block converts totally to the default route message.
func Test_NewIPv6PrefixFromContiguous_ZeroValue(t *testing.T) {
	network := commonpb.NewIPv6PrefixFromContiguous(xnetip.Contiguous[xnetip.Network6]{})
	require.NotNil(t, network.Addr)
	require.Equal(t, uint64(0), network.Addr.Hi)
	require.Equal(t, uint64(0), network.Addr.Lo)
	require.Equal(t, uint32(0), network.PrefixLen)
}

// Test_IPv6Prefix_ToPrefix_RoundTrip verifies that conversion
// to netip and back preserves the network exactly, mapped form included.
func Test_IPv6Prefix_ToPrefix_RoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
	}{
		{name: "default route", prefix: "::/0"},
		{name: "global unicast", prefix: "2a02:6b8::/32"},
		{name: "host route", prefix: "::1/128"},
		{name: "IPv4-mapped IPv6", prefix: "::ffff:10.0.0.0/104"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefix := netip.MustParsePrefix(tt.prefix)
			network, err := commonpb.NewIPv6PrefixFromPrefix(prefix)
			require.NoError(t, err)
			got, err := network.ToPrefix()
			require.NoError(t, err)
			require.Equal(t, prefix, got)
		})
	}
}

// Test_IPv6Prefix_ToPrefix_MasksHostBits verifies that a
// hand-built message with host bits set decodes to the masked network.
func Test_IPv6Prefix_ToPrefix_MasksHostBits(t *testing.T) {
	network := &commonpb.IPv6Prefix{
		Addr:      &commonpb.IPv6Address{Hi: 0x2a0206b800000001, Lo: 0x0000000000000100},
		PrefixLen: 64,
	}
	got, err := network.ToPrefix()
	require.NoError(t, err)
	require.Equal(t, netip.MustParsePrefix("2a02:6b8:0:1::/64"), got)
}

// Test_IPv6Prefix_ToPrefix_Rejects verifies that an absent
// address and an out-of-range prefix length return errors.
func Test_IPv6Prefix_ToPrefix_Rejects(t *testing.T) {
	tests := []struct {
		name    string
		network *commonpb.IPv6Prefix
	}{
		{
			name:    "absent addr",
			network: &commonpb.IPv6Prefix{PrefixLen: 64},
		},
		{
			name: "prefix length above 128",
			network: &commonpb.IPv6Prefix{
				Addr:      &commonpb.IPv6Address{Hi: 0x2a0206b800000000},
				PrefixLen: 129,
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

// Test_IPv6Prefix_WireRoundTrip verifies golden wire bytes
// shared with the Rust tests and that decode reproduces the network.
//
// The default route is not an empty message: the present-but-zero
// address encodes as two bytes, and decode relies on that presence to
// tell the default route from a malformed message. A half-zero address
// omits that half inside the nested message and decodes back
// losslessly.
func Test_IPv6Prefix_WireRoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		prefix    string
		wantBytes []byte
	}{
		{
			name:      "default route",
			prefix:    "::/0",
			wantBytes: []byte{0x0a, 0x00},
		},
		{
			name:   "global unicast",
			prefix: "2a02:6b8::/32",
			wantBytes: []byte{
				0x0a, 0x09,
				0x09, 0x00, 0x00, 0x00, 0x00, 0xb8, 0x06, 0x02, 0x2a,
				0x10, 0x20,
			},
		},
		{
			name:   "sixty-four boundary",
			prefix: "2a02:6b8:0:1::/64",
			wantBytes: []byte{
				0x0a, 0x09,
				0x09, 0x01, 0x00, 0x00, 0x00, 0xb8, 0x06, 0x02, 0x2a,
				0x10, 0x40,
			},
		},
		{
			name:   "host route",
			prefix: "2a02:6b8:0:1::100/128",
			wantBytes: []byte{
				0x0a, 0x12,
				0x09, 0x01, 0x00, 0x00, 0x00, 0xb8, 0x06, 0x02, 0x2a,
				0x11, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
				0x10, 0x80, 0x01,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original, err := commonpb.NewIPv6PrefixFromPrefix(netip.MustParsePrefix(tt.prefix))
			require.NoError(t, err)

			data, err := proto.Marshal(original)
			require.NoError(t, err)
			require.Equal(t, tt.wantBytes, data)

			var got commonpb.IPv6Prefix
			require.NoError(t, proto.Unmarshal(data, &got))
			prefix, err := got.ToPrefix()
			require.NoError(t, err)
			require.Equal(t, netip.MustParsePrefix(tt.prefix), prefix)
		})
	}
}

// Test_IPv6Prefix_MarshalJSON verifies that the network
// serializes as a bare CIDR string, the same form the Rust side emits.
func Test_IPv6Prefix_MarshalJSON(t *testing.T) {
	network, err := commonpb.NewIPv6PrefixFromPrefix(netip.MustParsePrefix("2a02:6b8::/32"))
	require.NoError(t, err)
	got, err := json.Marshal(network)
	require.NoError(t, err)
	require.Equal(t, `"2a02:6b8::/32"`, string(got))
}

// Test_IPv6Prefix_MarshalJSON_RejectsAbsentAddr verifies that
// a malformed message fails to serialize instead of inventing a network.
func Test_IPv6Prefix_MarshalJSON_RejectsAbsentAddr(t *testing.T) {
	network := &commonpb.IPv6Prefix{PrefixLen: 64}
	_, err := json.Marshal(network)
	require.Error(t, err)
}

// Test_IPv6Prefix_UnmarshalJSON verifies that only bare IPv6
// CIDR strings are accepted, with host bits masked off.
func Test_IPv6Prefix_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "global unicast", input: `"2a02:6b8::/32"`, want: "2a02:6b8::/32"},
		{name: "unmasked host bits", input: `"2a02:6b8:0:1::100/64"`, want: "2a02:6b8:0:1::/64"},
		{name: "IPv4-mapped IPv6", input: `"::ffff:10.0.0.0/104"`, want: "::ffff:10.0.0.0/104"},
		{name: "IPv4", input: `"10.0.0.0/8"`, wantErr: true},
		{name: "bare address", input: `"2a02:6b8::1"`, wantErr: true},
		{name: "empty string", input: `""`, wantErr: true},
		{name: "malformed network", input: `"not-a-network"`, wantErr: true},
		{name: "object form", input: `{"network":"2a02:6b8::/32"}`, wantErr: true},
		{name: "invalid JSON", input: `"2a02`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var network commonpb.IPv6Prefix
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

// Test_IPv6Prefix_JSONRoundTrip verifies that marshaling and
// unmarshaling reproduce the original message.
func Test_IPv6Prefix_JSONRoundTrip(t *testing.T) {
	original, err := commonpb.NewIPv6PrefixFromPrefix(netip.MustParsePrefix("2a02:6b8:0:1::/64"))
	require.NoError(t, err)

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var got commonpb.IPv6Prefix
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, original.Addr.Hi, got.Addr.Hi)
	require.Equal(t, original.Addr.Lo, got.Addr.Lo)
	require.Equal(t, original.PrefixLen, got.PrefixLen)
}

// Test_IPv6Prefix_AsLogValue verifies compact CIDR rendering
// for request logs and the invalid fallback for a malformed message.
func Test_IPv6Prefix_AsLogValue(t *testing.T) {
	network, err := commonpb.NewIPv6PrefixFromPrefix(netip.MustParsePrefix("2a02:6b8::/32"))
	require.NoError(t, err)
	require.Equal(t, "2a02:6b8::/32", network.AsLogValue())

	malformed := &commonpb.IPv6Prefix{PrefixLen: 64}
	require.Equal(t, "invalid", malformed.AsLogValue())
}
