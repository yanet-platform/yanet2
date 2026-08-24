package commonpb_test

import (
	"encoding/json"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// Test_NewIPv4AddressFromAddr_Valid verifies that native IPv4 input is
// encoded as a big-endian u32.
//
// The fixture values mirror the Rust tests so a byte-order defect fails
// identically in both languages.
func Test_NewIPv4AddressFromAddr_Valid(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want uint32
	}{
		{name: "zero address", addr: "0.0.0.0", want: 0x00000000},
		{name: "broadcast", addr: "255.255.255.255", want: 0xffffffff},
		{name: "asymmetric octets", addr: "10.1.2.3", want: 0x0a010203},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip, err := commonpb.NewIPv4AddressFromAddr(netip.MustParseAddr(tt.addr))
			require.NoError(t, err)
			require.Equal(t, tt.want, ip.Addr)
		})
	}
}

// Test_NewIPv4AddressFromAddr_RejectsNonIPv4 verifies that IPv6 input
// and the invalid zero value return errors instead of being truncated.
//
// The IPv4-mapped form counts as IPv6 here: on the wire it belongs to
// the IPv6 family.
func Test_NewIPv4AddressFromAddr_RejectsNonIPv4(t *testing.T) {
	tests := []struct {
		name string
		addr netip.Addr
	}{
		{name: "IPv6", addr: netip.MustParseAddr("2001:db8::1")},
		{name: "IPv4-mapped IPv6", addr: netip.MustParseAddr("::ffff:10.1.2.3")},
		{name: "zero value", addr: netip.Addr{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := commonpb.NewIPv4AddressFromAddr(tt.addr)
			require.Error(t, err)
		})
	}
}

// Test_NewIPv4Address_NetworkByteOrder verifies that the first address
// byte becomes the most significant byte of the encoded value.
func Test_NewIPv4Address_NetworkByteOrder(t *testing.T) {
	ip := commonpb.NewIPv4Address([4]byte{10, 1, 2, 3})
	require.Equal(t, uint32(0x0a010203), ip.Addr)
}

// Test_IPv4Address_ToAddr_RoundTrip verifies that conversion to netip
// and back preserves the address exactly.
func Test_IPv4Address_ToAddr_RoundTrip(t *testing.T) {
	tests := []struct {
		name string
		addr string
	}{
		{name: "zero address", addr: "0.0.0.0"},
		{name: "broadcast", addr: "255.255.255.255"},
		{name: "asymmetric octets", addr: "10.1.2.3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := netip.MustParseAddr(tt.addr)
			ip, err := commonpb.NewIPv4AddressFromAddr(addr)
			require.NoError(t, err)
			require.Equal(t, addr, ip.ToAddr())
		})
	}
}

// Test_IPv4Address_ToAddr_ZeroMessage verifies that the conversion is
// total: the zero message is the zero address, not an error.
func Test_IPv4Address_ToAddr_ZeroMessage(t *testing.T) {
	ip := &commonpb.IPv4Address{}
	require.Equal(t, netip.MustParseAddr("0.0.0.0"), ip.ToAddr())
}

// Test_IPv4Address_WireBytes verifies the fixed32 wire encoding against
// a golden byte fixture.
//
// The fixture is shared with the Rust tests, so an endianness defect
// fails identically in both languages.
func Test_IPv4Address_WireBytes(t *testing.T) {
	ip := commonpb.NewIPv4Address([4]byte{10, 1, 2, 3})
	got, err := proto.Marshal(ip)
	require.NoError(t, err)
	require.Equal(t, []byte{0x0d, 0x03, 0x02, 0x01, 0x0a}, got)
}

// Test_IPv4Address_MarshalJSON verifies that the message serializes as
// a bare dotted-quad string, the same form the Rust side emits.
func Test_IPv4Address_MarshalJSON(t *testing.T) {
	tests := []struct {
		name string
		ip   *commonpb.IPv4Address
		want string
	}{
		{
			name: "asymmetric octets",
			ip:   commonpb.NewIPv4Address([4]byte{10, 1, 2, 3}),
			want: `"10.1.2.3"`,
		},
		{
			name: "zero message",
			ip:   &commonpb.IPv4Address{},
			want: `"0.0.0.0"`,
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

// Test_IPv4Address_UnmarshalJSON verifies that only bare native IPv4
// address strings are accepted.
func Test_IPv4Address_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "IPv4", input: `"10.1.2.3"`, want: "10.1.2.3"},
		{name: "IPv6", input: `"2001:db8::1"`, wantErr: true},
		{name: "IPv4-mapped IPv6", input: `"::ffff:10.1.2.3"`, wantErr: true},
		{name: "empty string", input: `""`, wantErr: true},
		{name: "malformed address", input: `"not-an-ip"`, wantErr: true},
		{name: "object form", input: `{"addr":"10.1.2.3"}`, wantErr: true},
		{name: "invalid JSON", input: `"10.1`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ip commonpb.IPv4Address
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

// Test_IPv4Address_JSONRoundTrip verifies that marshaling and
// unmarshaling reproduce the original message.
func Test_IPv4Address_JSONRoundTrip(t *testing.T) {
	original := commonpb.NewIPv4Address([4]byte{255, 255, 255, 255})

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var got commonpb.IPv4Address
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, original.Addr, got.Addr)
}

// Test_IPv4Address_AsLogValue verifies compact address rendering for
// request logs.
func Test_IPv4Address_AsLogValue(t *testing.T) {
	ip := commonpb.NewIPv4Address([4]byte{10, 1, 2, 3})
	require.Equal(t, "10.1.2.3", ip.AsLogValue())
}
