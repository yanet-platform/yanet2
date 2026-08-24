package commonpb_test

import (
	"encoding/json"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// Test_NewIPv6AddressFromAddr_Valid verifies that IPv6 input splits into
// big-endian u64 halves along the byte 0-7 / 8-15 boundary.
//
// The fixture values mirror the Rust tests so a half-swap or endianness
// defect fails identically in both languages.
func Test_NewIPv6AddressFromAddr_Valid(t *testing.T) {
	tests := []struct {
		name   string
		addr   string
		wantHi uint64
		wantLo uint64
	}{
		{name: "zero address", addr: "::", wantHi: 0, wantLo: 0},
		{name: "loopback", addr: "::1", wantHi: 0, wantLo: 1},
		{
			name:   "asymmetric halves",
			addr:   "2a02:6b8:0:1::100",
			wantHi: 0x2a0206b800000001,
			wantLo: 0x0000000000000100,
		},
		{
			name:   "IPv4-mapped IPv6",
			addr:   "::ffff:10.1.2.3",
			wantHi: 0,
			wantLo: 0x0000ffff0a010203,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip, err := commonpb.NewIPv6AddressFromAddr(netip.MustParseAddr(tt.addr))
			require.NoError(t, err)
			require.Equal(t, tt.wantHi, ip.Hi)
			require.Equal(t, tt.wantLo, ip.Lo)
		})
	}
}

// Test_NewIPv6AddressFromAddr_RejectsNonIPv6 verifies that native IPv4
// input, zoned addresses, and the invalid zero value return errors.
func Test_NewIPv6AddressFromAddr_RejectsNonIPv6(t *testing.T) {
	tests := []struct {
		name string
		addr netip.Addr
	}{
		{name: "native IPv4", addr: netip.MustParseAddr("10.1.2.3")},
		{name: "zoned link-local", addr: netip.MustParseAddr("fe80::1%eth0")},
		{name: "zero value", addr: netip.Addr{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := commonpb.NewIPv6AddressFromAddr(tt.addr)
			require.Error(t, err)
		})
	}
}

// Test_NewIPv6Address_NetworkByteOrder verifies that the first address
// byte becomes the most significant byte of the high half.
func Test_NewIPv6Address_NetworkByteOrder(t *testing.T) {
	raw := [16]byte{
		0x2a, 0x02, 0x06, 0xb8, 0x00, 0x00, 0x00, 0x01,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00,
	}
	ip := commonpb.NewIPv6Address(raw)
	require.Equal(t, uint64(0x2a0206b800000001), ip.Hi)
	require.Equal(t, uint64(0x0000000000000100), ip.Lo)
}

// Test_IPv6Address_ToAddr_RoundTrip verifies that conversion to netip
// and back preserves the address exactly, mapped form included.
func Test_IPv6Address_ToAddr_RoundTrip(t *testing.T) {
	tests := []struct {
		name string
		addr string
	}{
		{name: "zero address", addr: "::"},
		{name: "asymmetric halves", addr: "2a02:6b8:0:1::100"},
		{name: "IPv4-mapped IPv6", addr: "::ffff:10.1.2.3"},
		{name: "all ones", addr: "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := netip.MustParseAddr(tt.addr)
			ip, err := commonpb.NewIPv6AddressFromAddr(addr)
			require.NoError(t, err)
			require.Equal(t, addr, ip.ToAddr())
		})
	}
}

// Test_IPv6Address_ToAddr_ZeroMessage verifies that the conversion is
// total: the zero message is the zero address, not an error.
func Test_IPv6Address_ToAddr_ZeroMessage(t *testing.T) {
	ip := &commonpb.IPv6Address{}
	require.Equal(t, netip.MustParseAddr("::"), ip.ToAddr())
}

// Test_IPv6Address_WireBytes verifies the two-fixed64 wire encoding
// against a golden byte fixture.
//
// The fixture is shared with the Rust tests, so a half-swap or
// endianness defect fails identically in both languages.
func Test_IPv6Address_WireBytes(t *testing.T) {
	ip, err := commonpb.NewIPv6AddressFromAddr(netip.MustParseAddr("2a02:6b8:0:1::100"))
	require.NoError(t, err)
	got, err := proto.Marshal(ip)
	require.NoError(t, err)
	require.Equal(t, []byte{
		0x09, 0x01, 0x00, 0x00, 0x00, 0xb8, 0x06, 0x02, 0x2a,
		0x11, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	}, got)
}

// Test_IPv6Address_MarshalJSON verifies that the message serializes as
// a bare IPv6 address string, the same form the Rust side emits.
func Test_IPv6Address_MarshalJSON(t *testing.T) {
	tests := []struct {
		name string
		ip   *commonpb.IPv6Address
		want string
	}{
		{
			name: "asymmetric halves",
			ip:   &commonpb.IPv6Address{Hi: 0x2a0206b800000001, Lo: 0x0000000000000100},
			want: `"2a02:6b8:0:1::100"`,
		},
		{
			name: "IPv4-mapped IPv6",
			ip:   &commonpb.IPv6Address{Hi: 0, Lo: 0x0000ffff0a010203},
			want: `"::ffff:10.1.2.3"`,
		},
		{
			name: "loopback",
			ip:   &commonpb.IPv6Address{Hi: 0, Lo: 1},
			want: `"::1"`,
		},
		{
			name: "zero message",
			ip:   &commonpb.IPv6Address{},
			want: `"::"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.ip)
			require.NoError(t, err)
			require.Equal(t, tt.want, string(got))
		})
	}
}

// Test_IPv6Address_UnmarshalJSON verifies that only bare unzoned IPv6
// address strings are accepted, the mapped form included.
func Test_IPv6Address_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "IPv6", input: `"2a02:6b8:0:1::100"`, want: "2a02:6b8:0:1::100"},
		{name: "IPv4-mapped IPv6", input: `"::ffff:10.1.2.3"`, want: "::ffff:10.1.2.3"},
		{name: "native IPv4", input: `"10.1.2.3"`, wantErr: true},
		{name: "zoned link-local", input: `"fe80::1%eth0"`, wantErr: true},
		{name: "empty string", input: `""`, wantErr: true},
		{name: "malformed address", input: `"not-an-ip"`, wantErr: true},
		{name: "object form", input: `{"hi":1,"lo":2}`, wantErr: true},
		{name: "invalid JSON", input: `"2a02`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ip commonpb.IPv6Address
			err := json.Unmarshal([]byte(tt.input), &ip)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, netip.MustParseAddr(tt.want), ip.ToAddr())
		})
	}
}

// Test_IPv6Address_JSONRoundTrip verifies that marshaling and
// unmarshaling reproduce the original message, mapped form included.
func Test_IPv6Address_JSONRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		ip   *commonpb.IPv6Address
	}{
		{
			name: "asymmetric halves",
			ip:   &commonpb.IPv6Address{Hi: 0x2a0206b800000001, Lo: 0x0000000000000100},
		},
		{
			name: "IPv4-mapped IPv6",
			ip:   &commonpb.IPv6Address{Hi: 0, Lo: 0x0000ffff0a010203},
		},
		{
			name: "loopback",
			ip:   &commonpb.IPv6Address{Hi: 0, Lo: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.ip)
			require.NoError(t, err)

			var got commonpb.IPv6Address
			require.NoError(t, json.Unmarshal(data, &got))
			require.Equal(t, tt.ip.Hi, got.Hi)
			require.Equal(t, tt.ip.Lo, got.Lo)
		})
	}
}

// Test_IPv6Address_AsLogValue verifies compact address rendering for
// request logs.
func Test_IPv6Address_AsLogValue(t *testing.T) {
	ip := &commonpb.IPv6Address{Hi: 0x2a0206b800000001, Lo: 0x0000000000000100}
	require.Equal(t, "2a02:6b8:0:1::100", ip.AsLogValue())
}
