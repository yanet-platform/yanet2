package commonpb_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/xnetip"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// Test_NewBiContiguousIPv6NetworkFromBiContiguous_Valid verifies that
// networks encode as the address plus one run length per mask half.
//
// The fixture values mirror the Rust tests so an encoding defect fails
// identically in both languages.
func Test_NewBiContiguousIPv6NetworkFromBiContiguous_Valid(t *testing.T) {
	tests := []struct {
		name    string
		network string
		wantHi  uint64
		wantLo  uint64
		wantHL  uint32
		wantLL  uint32
	}{
		{name: "default route", network: "::/0"},
		{
			name:    "globally contiguous subnet",
			network: "2001:db8::/32",
			wantHi:  0x20010db800000000,
			wantHL:  32,
		},
		{
			name:    "host route",
			network: "2001:db8::1/128",
			wantHi:  0x20010db800000000,
			wantLo:  1,
			wantHL:  64,
			wantLL:  64,
		},
		{
			name:    "hole at the /64 boundary",
			network: "2001:db8:ff00::1:2:0:0/ffff:ffff:ff00:0:ffff:ffff:0:0",
			wantHi:  0x20010db8ff000000,
			wantLo:  0x0001000200000000,
			wantHL:  40,
			wantLL:  32,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			network := commonpb.NewBiContiguousIPv6NetworkFromBiContiguous(
				xnetip.MustParseBiContiguous(tt.network),
			)
			require.Equal(t, tt.wantHi, network.Addr.Hi)
			require.Equal(t, tt.wantLo, network.Addr.Lo)
			require.Equal(t, tt.wantHL, network.HiPrefixLen)
			require.Equal(t, tt.wantLL, network.LoPrefixLen)
		})
	}
}

// Test_NewBiContiguousIPv6NetworkFromBiContiguous_ZeroValue verifies
// that the zero network converts totally to the wildcard message.
func Test_NewBiContiguousIPv6NetworkFromBiContiguous_ZeroValue(t *testing.T) {
	network := commonpb.NewBiContiguousIPv6NetworkFromBiContiguous(xnetip.BiContiguous{})
	require.NotNil(t, network.Addr)
	require.Equal(t, uint64(0), network.Addr.Hi)
	require.Equal(t, uint64(0), network.Addr.Lo)
	require.Equal(t, uint32(0), network.HiPrefixLen)
	require.Equal(t, uint32(0), network.LoPrefixLen)
}

// Test_BiContiguousIPv6Network_ToBiContiguous_RoundTrip verifies that
// conversion to the domain type and back preserves the network exactly.
func Test_BiContiguousIPv6Network_ToBiContiguous_RoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		network string
		want    string
	}{
		{name: "default route", network: "::/0", want: "::/0"},
		{name: "globally contiguous subnet", network: "2001:db8::/32", want: "2001:db8::/32"},
		{name: "host route", network: "2001:db8::1/128", want: "2001:db8::1/128"},
		{
			name:    "hole at the /64 boundary",
			network: "2001:db8:ff00::1:2:0:0/ffff:ffff:ff00:0:ffff:ffff:0:0",
			want:    "2001:db8:ff00:0:1:2::/ffff:ffff:ff00:0:ffff:ffff::",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			network, err := commonpb.ParseBiContiguousIPv6Network(tt.network)
			require.NoError(t, err)
			got, err := network.ToBiContiguous()
			require.NoError(t, err)
			require.Equal(t, tt.want, got.String())
		})
	}
}

// Test_BiContiguousIPv6Network_AllPrefixPairs_RoundTrip verifies that
// every length pair from 0 through 64 survives the round trip.
//
// This is the class bijectivity the encoding is chosen for: no pair in
// range is invalid, and nothing outside the class is expressible.
func Test_BiContiguousIPv6Network_AllPrefixPairs_RoundTrip(t *testing.T) {
	allOnes := &commonpb.IPv6Address{Hi: ^uint64(0), Lo: ^uint64(0)}
	for hiLen := range uint32(65) {
		for loLen := range uint32(65) {
			network := &commonpb.BiContiguousIPv6Network{
				Addr:        allOnes,
				HiPrefixLen: hiLen,
				LoPrefixLen: loLen,
			}
			net, err := network.ToBiContiguous()
			require.NoError(t, err)
			back := commonpb.NewBiContiguousIPv6NetworkFromBiContiguous(net)
			require.Equal(t, hiLen, back.HiPrefixLen)
			require.Equal(t, loLen, back.LoPrefixLen)
		}
	}
}

// Test_BiContiguousIPv6Network_ToBiContiguous_MasksHostBits verifies
// that bits outside the mask decode to the masked network.
func Test_BiContiguousIPv6Network_ToBiContiguous_MasksHostBits(t *testing.T) {
	network := &commonpb.BiContiguousIPv6Network{
		Addr:        &commonpb.IPv6Address{Hi: 0x20010db8ffffffff, Lo: ^uint64(0)},
		HiPrefixLen: 32,
		LoPrefixLen: 16,
	}
	got, err := network.ToBiContiguous()
	require.NoError(t, err)
	require.Equal(t, "2001:db8:0:0:ffff::/ffff:ffff:0:0:ffff::", got.String())
}

// Test_BiContiguousIPv6Network_ToBiContiguous_Rejects verifies that an
// absent address and an out-of-range half length return errors.
func Test_BiContiguousIPv6Network_ToBiContiguous_Rejects(t *testing.T) {
	validAddr := &commonpb.IPv6Address{Hi: 0x20010db800000000}
	tests := []struct {
		name    string
		network *commonpb.BiContiguousIPv6Network
	}{
		{
			name:    "absent addr",
			network: &commonpb.BiContiguousIPv6Network{HiPrefixLen: 32},
		},
		{
			name: "high length above 64",
			network: &commonpb.BiContiguousIPv6Network{
				Addr:        validAddr,
				HiPrefixLen: 65,
			},
		},
		{
			name: "low length above 64",
			network: &commonpb.BiContiguousIPv6Network{
				Addr:        validAddr,
				LoPrefixLen: 65,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.network.ToBiContiguous()
			require.Error(t, err)
		})
	}
}

// Test_BiContiguousIPv6Network_WireRoundTrip verifies golden wire bytes
// shared with the Rust tests and that decode reproduces the network.
//
// The wildcard is not an empty message: the present-but-zero address
// encodes as two bytes, and decode relies on that presence to tell the
// wildcard from a malformed message.
func Test_BiContiguousIPv6Network_WireRoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		network   string
		wantBytes []byte
	}{
		{
			name:      "default route",
			network:   "::/0",
			wantBytes: []byte{0x0a, 0x00},
		},
		{
			name:    "hole at the /64 boundary",
			network: "2001:db8:ff00::1:2:0:0/ffff:ffff:ff00:0:ffff:ffff:0:0",
			wantBytes: []byte{
				0x0a, 0x12,
				0x09, 0x00, 0x00, 0x00, 0xff, 0xb8, 0x0d, 0x01, 0x20,
				0x11, 0x00, 0x00, 0x00, 0x00, 0x02, 0x00, 0x01, 0x00,
				0x10, 0x28,
				0x18, 0x20,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original, err := commonpb.ParseBiContiguousIPv6Network(tt.network)
			require.NoError(t, err)

			data, err := proto.Marshal(original)
			require.NoError(t, err)
			require.Equal(t, tt.wantBytes, data)

			var got commonpb.BiContiguousIPv6Network
			require.NoError(t, proto.Unmarshal(data, &got))
			net, err := got.ToBiContiguous()
			require.NoError(t, err)
			want, err := original.ToBiContiguous()
			require.NoError(t, err)
			require.Equal(t, want, net)
		})
	}
}

// Test_ParseBiContiguousIPv6Network_AcceptedForms verifies that the
// CIDR, explicit mask, and bare host forms parse canonically.
func Test_ParseBiContiguousIPv6Network_AcceptedForms(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "CIDR form", input: "2001:db8::/32", want: "2001:db8::/32"},
		{
			name:  "explicit mask form",
			input: "2001:db8:ff00::1:2:0:0/ffff:ffff:ff00:0:ffff:ffff:0:0",
			want:  "2001:db8:ff00:0:1:2::/ffff:ffff:ff00:0:ffff:ffff::",
		},
		{name: "bare address is a host route", input: "2001:db8::1", want: "2001:db8::1/128"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			network, err := commonpb.ParseBiContiguousIPv6Network(tt.input)
			require.NoError(t, err)
			net, err := network.ToBiContiguous()
			require.NoError(t, err)
			require.Equal(t, tt.want, net.String())
		})
	}
}

// Test_ParseBiContiguousIPv6Network_Invalid verifies that masks with an
// interior hole, IPv4 input, and junk are rejected.
func Test_ParseBiContiguousIPv6Network_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "interior hole in the high half", input: "2001:db8::/ffff:0:ffff::"},
		{
			name:  "interior hole in the low half",
			input: "2001:db8::/ffff:ffff:ffff:ffff:ffff:0:ffff:0",
		},
		{name: "IPv4 CIDR", input: "10.0.0.0/24"},
		{name: "not a network", input: "not-a-net"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := commonpb.ParseBiContiguousIPv6Network(tt.input)
			require.Error(t, err)
		})
	}
}

// Test_BiContiguousIPv6Network_MarshalJSON verifies that the network
// serializes as a bare string, the same form the Rust side emits.
//
// The CIDR form appears when the mask is globally contiguous, the
// address/mask form otherwise.
func Test_BiContiguousIPv6Network_MarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		network string
		want    string
	}{
		{
			name:    "globally contiguous subnet",
			network: "2001:db8::/32",
			want:    `"2001:db8::/32"`,
		},
		{
			name:    "hole at the /64 boundary",
			network: "2001:db8:ff00::1:2:0:0/ffff:ffff:ff00:0:ffff:ffff:0:0",
			want:    `"2001:db8:ff00:0:1:2::/ffff:ffff:ff00:0:ffff:ffff::"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			network, err := commonpb.ParseBiContiguousIPv6Network(tt.network)
			require.NoError(t, err)
			got, err := json.Marshal(network)
			require.NoError(t, err)
			require.Equal(t, tt.want, string(got))
		})
	}
}

// Test_BiContiguousIPv6Network_MarshalJSON_RejectsAbsentAddr verifies
// that a malformed message fails to serialize.
func Test_BiContiguousIPv6Network_MarshalJSON_RejectsAbsentAddr(t *testing.T) {
	network := &commonpb.BiContiguousIPv6Network{HiPrefixLen: 32}
	_, err := json.Marshal(network)
	require.Error(t, err)
}

// Test_BiContiguousIPv6Network_UnmarshalJSON verifies that bare strings
// in every accepted parse form are decoded and the rest are rejected.
func Test_BiContiguousIPv6Network_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "CIDR form", input: `"2001:db8::/32"`, want: "2001:db8::/32"},
		{
			name:  "explicit mask form",
			input: `"2001:db8:ff00::1:2:0:0/ffff:ffff:ff00:0:ffff:ffff:0:0"`,
			want:  "2001:db8:ff00:0:1:2::/ffff:ffff:ff00:0:ffff:ffff::",
		},
		{name: "interior hole", input: `"2001:db8::/ffff:0:ffff::"`, wantErr: true},
		{name: "IPv4 CIDR", input: `"10.0.0.0/24"`, wantErr: true},
		{name: "empty string", input: `""`, wantErr: true},
		{name: "object form", input: `{"network":"2001:db8::/32"}`, wantErr: true},
		{name: "invalid JSON", input: `"2001`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var network commonpb.BiContiguousIPv6Network
			err := json.Unmarshal([]byte(tt.input), &network)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			net, err := network.ToBiContiguous()
			require.NoError(t, err)
			require.Equal(t, tt.want, net.String())
		})
	}
}

// Test_BiContiguousIPv6Network_JSONRoundTrip verifies that marshaling
// and unmarshaling reproduce the original message for both text forms.
func Test_BiContiguousIPv6Network_JSONRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		network string
	}{
		{name: "globally contiguous subnet", network: "2001:db8::/32"},
		{
			name:    "hole at the /64 boundary",
			network: "2001:db8:ff00::1:2:0:0/ffff:ffff:ff00:0:ffff:ffff:0:0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original, err := commonpb.ParseBiContiguousIPv6Network(tt.network)
			require.NoError(t, err)

			data, err := json.Marshal(original)
			require.NoError(t, err)

			var got commonpb.BiContiguousIPv6Network
			require.NoError(t, json.Unmarshal(data, &got))
			require.Equal(t, original.Addr.Hi, got.Addr.Hi)
			require.Equal(t, original.Addr.Lo, got.Addr.Lo)
			require.Equal(t, original.HiPrefixLen, got.HiPrefixLen)
			require.Equal(t, original.LoPrefixLen, got.LoPrefixLen)
		})
	}
}

// Test_BiContiguousIPv6Network_AsLogValue verifies compact rendering for
// request logs and the invalid fallback for a malformed message.
func Test_BiContiguousIPv6Network_AsLogValue(t *testing.T) {
	network, err := commonpb.ParseBiContiguousIPv6Network("2001:db8::/32")
	require.NoError(t, err)
	require.Equal(t, "2001:db8::/32", network.AsLogValue())

	malformed := &commonpb.BiContiguousIPv6Network{HiPrefixLen: 32}
	require.Equal(t, "invalid", malformed.AsLogValue())
}
