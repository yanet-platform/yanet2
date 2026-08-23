package commonpb_test

import (
	"encoding/json"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/xnetip"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// TestContiguousIPNetwork_ToPrefix_RoundTrip asserts that a prefix that is
// already masked survives a New/ToPrefix round trip for both families.
func TestContiguousIPNetwork_ToPrefix_RoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		prefix netip.Prefix
	}{
		{name: "IPv4", prefix: netip.MustParsePrefix("10.0.0.0/24")},
		{name: "IPv4 host route", prefix: netip.MustParsePrefix("10.0.0.1/32")},
		{name: "IPv4 default route", prefix: netip.MustParsePrefix("0.0.0.0/0")},
		{name: "IPv6", prefix: netip.MustParsePrefix("2001:db8::/32")},
		{name: "IPv6 host route", prefix: netip.MustParsePrefix("2001:db8::1/128")},
		{name: "IPv6 default route", prefix: netip.MustParsePrefix("::/0")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := commonpb.NewContiguousIPNetworkFromPrefix(tt.prefix)
			require.NoError(t, err)
			got, err := m.ToPrefix()
			require.NoError(t, err)
			require.Equal(t, tt.prefix, got)
		})
	}
}

// TestContiguousIPNetwork_HostBitsNormalized asserts that construction and
// decoding both mask host bits below prefix_len, so the normalization is
// documented behavior rather than an accident of one direction.
//
// It checks the raw wire bytes (m.GetAddr().GetAddr()) directly rather than
// only round-tripping through ToPrefix, because ToPrefix masks on the way
// out too: a constructor that forgot to mask would still pass a
// ToPrefix-only assertion.
func TestContiguousIPNetwork_HostBitsNormalized(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantIn    string
		wantBytes []byte
	}{
		{name: "IPv4", input: "10.0.0.1/24", wantIn: "10.0.0.0/24", wantBytes: []byte{10, 0, 0, 0}},
		{
			name:      "IPv6",
			input:     "2001:db8::1/32",
			wantIn:    "2001:db8::/32",
			wantBytes: []byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := netip.MustParsePrefix(tt.input)
			want := netip.MustParsePrefix(tt.wantIn)

			m, err := commonpb.NewContiguousIPNetworkFromPrefix(in)
			require.NoError(t, err)
			require.Equal(t, tt.wantBytes, m.GetAddr().GetAddr())
			got, err := m.ToPrefix()
			require.NoError(t, err)
			require.Equal(t, want, got)

			parsed, err := commonpb.ParseContiguousIPNetwork(tt.input)
			require.NoError(t, err)
			require.Equal(t, tt.wantBytes, parsed.GetAddr().GetAddr())
			got, err = parsed.ToPrefix()
			require.NoError(t, err)
			require.Equal(t, want, got)
		})
	}

	decodeTests := []struct {
		name      string
		addr      []byte
		prefixLen uint32
		wantIn    string
	}{
		{name: "IPv4 hand-built", addr: []byte{10, 0, 0, 1}, prefixLen: 24, wantIn: "10.0.0.0/24"},
		{
			name:      "IPv6 hand-built",
			addr:      []byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
			prefixLen: 32,
			wantIn:    "2001:db8::/32",
		},
	}

	for _, tt := range decodeTests {
		t.Run(tt.name, func(t *testing.T) {
			want := netip.MustParsePrefix(tt.wantIn)
			m := &commonpb.ContiguousIPNetwork{
				Addr:      &commonpb.IPAddress{Addr: tt.addr},
				PrefixLen: tt.prefixLen,
			}
			got, err := m.ToPrefix()
			require.NoError(t, err)
			require.Equal(t, want, got)
		})
	}
}

// TestContiguousIPNetwork_ToPrefix_PrefixLenOverflow asserts that a
// prefix_len exceeding the address family's bit length is rejected rather
// than silently truncated or wrapped.
func TestContiguousIPNetwork_ToPrefix_PrefixLenOverflow(t *testing.T) {
	tests := []struct {
		name      string
		addr      []byte
		prefixLen uint32
	}{
		{name: "IPv4 overflow", addr: []byte{10, 0, 0, 0}, prefixLen: 33},
		{name: "IPv6 overflow", addr: make([]byte, 16), prefixLen: 129},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &commonpb.ContiguousIPNetwork{
				Addr:      &commonpb.IPAddress{Addr: tt.addr},
				PrefixLen: tt.prefixLen,
			}
			_, err := m.ToPrefix()
			require.Error(t, err)
		})
	}
}

// TestContiguousIPNetwork_ToPrefix_MalformedAddr asserts that an addr byte
// length other than 4 or 16 is rejected.
func TestContiguousIPNetwork_ToPrefix_MalformedAddr(t *testing.T) {
	m := &commonpb.ContiguousIPNetwork{
		Addr:      &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5}},
		PrefixLen: 24,
	}
	_, err := m.ToPrefix()
	require.Error(t, err)
}

// TestNewContiguousIPNetworkFromPrefix_Invalid asserts that constructing
// from an invalid netip.Prefix is rejected at construction time rather than
// producing a message that fails later.
func TestNewContiguousIPNetworkFromPrefix_Invalid(t *testing.T) {
	_, err := commonpb.NewContiguousIPNetworkFromPrefix(netip.Prefix{})
	require.Error(t, err)
}

// TestParseContiguousIPNetwork_Invalid asserts that a malformed CIDR
// string is rejected.
func TestParseContiguousIPNetwork_Invalid(t *testing.T) {
	_, err := commonpb.ParseContiguousIPNetwork("not-a-cidr")
	require.Error(t, err)
}

// TestContiguousIPNetwork_SliceConversions verifies that slice conversion
// masks host bits and preserves IPv4 and IPv6 prefixes in both directions.
func TestContiguousIPNetwork_SliceConversions(t *testing.T) {
	prefixes := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.1/24"),
		netip.MustParsePrefix("2001:db8::1/32"),
	}
	want := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/24"),
		netip.MustParsePrefix("2001:db8::/32"),
	}

	networks, err := commonpb.NetworksFromPrefixes(prefixes)
	require.NoError(t, err)

	got, err := commonpb.PrefixesFromNetworks(networks)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// TestContiguousIPNetwork_SliceConversions_Invalid verifies that conversion
// errors identify the failing prefix position.
func TestContiguousIPNetwork_SliceConversions_Invalid(t *testing.T) {
	_, err := commonpb.NetworksFromPrefixes([]netip.Prefix{{}})
	require.ErrorContains(t, err, "prefixes[0]")

	_, err = commonpb.PrefixesFromNetworks([]*commonpb.ContiguousIPNetwork{{
		Addr: &commonpb.IPAddress{Addr: []byte{10, 0, 0}},
	}})
	require.ErrorContains(t, err, "prefixes[0]")
}

// TestContiguousIPNetwork_AsLogValue asserts the log-friendly string form,
// including the "invalid" fallback for an undecodable message.
func TestContiguousIPNetwork_AsLogValue(t *testing.T) {
	ipv4, err := commonpb.NewContiguousIPNetworkFromPrefix(netip.MustParsePrefix("10.0.0.0/24"))
	require.NoError(t, err)
	ipv6, err := commonpb.NewContiguousIPNetworkFromPrefix(netip.MustParsePrefix("2001:db8::/32"))
	require.NoError(t, err)

	tests := []struct {
		name string
		m    *commonpb.ContiguousIPNetwork
		want string
	}{
		{name: "IPv4", m: ipv4, want: "10.0.0.0/24"},
		{name: "IPv6", m: ipv6, want: "2001:db8::/32"},
		{
			name: "invalid",
			m:    &commonpb.ContiguousIPNetwork{Addr: &commonpb.IPAddress{Addr: []byte{1, 2, 3}}},
			want: "invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.m.AsLogValue())
		})
	}
}

// TestContiguousIPNetwork_JSONRoundTrip asserts marshal/unmarshal round
// trips through the "network" CIDR string for both families.
func TestContiguousIPNetwork_JSONRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		prefix netip.Prefix
		want   string
	}{
		{name: "IPv4", prefix: netip.MustParsePrefix("10.0.0.0/24"), want: `{"network":"10.0.0.0/24"}`},
		{name: "IPv6", prefix: netip.MustParsePrefix("2001:db8::/32"), want: `{"network":"2001:db8::/32"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original, err := commonpb.NewContiguousIPNetworkFromPrefix(tt.prefix)
			require.NoError(t, err)

			data, err := json.Marshal(original)
			require.NoError(t, err)
			require.Equal(t, tt.want, string(data))

			var got commonpb.ContiguousIPNetwork
			require.NoError(t, json.Unmarshal(data, &got))
			require.Equal(t, tt.prefix.Addr().AsSlice(), got.GetAddr().GetAddr())

			gotPrefix, err := got.ToPrefix()
			require.NoError(t, err)
			require.Equal(t, tt.prefix, gotPrefix)
		})
	}
}

// TestContiguousIPNetwork_UnmarshalJSON_Errors asserts that an empty or
// malformed "network" value is rejected.
func TestContiguousIPNetwork_UnmarshalJSON_Errors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "empty string", input: `{"network":""}`},
		{name: "malformed CIDR", input: `{"network":"not-a-cidr"}`},
		{name: "invalid JSON", input: `{"network":`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m commonpb.ContiguousIPNetwork
			err := json.Unmarshal([]byte(tt.input), &m)
			require.Error(t, err)
		})
	}
}

// Test_ContiguousIPNetwork_ToContiguous_RoundTrip verifies that a CIDR block
// of either family round-trips normalized, with a family-width wire address.
func Test_ContiguousIPNetwork_ToContiguous_RoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		net       xnetip.Contiguous[xnetip.Network]
		wantBytes []byte
		wantBits  uint32
	}{
		{
			name:      "IPv4 default route",
			net:       xnetip.MustParseContiguous("0.0.0.0/0"),
			wantBytes: []byte{0, 0, 0, 0},
			wantBits:  0,
		},
		{
			name:      "IPv4 subnet",
			net:       xnetip.MustParseContiguous("10.0.0.0/24"),
			wantBytes: []byte{10, 0, 0, 0},
			wantBits:  24,
		},
		{
			name:      "IPv4 host route",
			net:       xnetip.MustParseContiguous("10.0.0.1/32"),
			wantBytes: []byte{10, 0, 0, 1},
			wantBits:  32,
		},
		{
			name:      "IPv6 default route",
			net:       xnetip.MustParseContiguous("::/0"),
			wantBytes: make([]byte, 16),
			wantBits:  0,
		},
		{
			name:      "zero block is the IPv6 default route",
			net:       xnetip.Contiguous[xnetip.Network]{},
			wantBytes: make([]byte, 16),
			wantBits:  0,
		},
		{
			name:      "IPv6 subnet",
			net:       xnetip.MustParseContiguous("2001:db8::/32"),
			wantBytes: []byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			wantBits:  32,
		},
		{
			name:      "IPv6 host route",
			net:       xnetip.MustParseContiguous("2001:db8::1/128"),
			wantBytes: []byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
			wantBits:  128,
		},
		{
			name:      "IPv4-mapped IPv6 stays sixteen bytes",
			net:       xnetip.MustParseContiguous("::ffff:10.0.0.0/120"),
			wantBytes: []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 10, 0, 0, 0},
			wantBits:  120,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := commonpb.NewContiguousIPNetworkFromContiguous(tt.net)
			require.Equal(t, tt.wantBytes, m.GetAddr().GetAddr())
			require.Equal(t, tt.wantBits, m.GetPrefixLen())

			got, err := m.ToContiguous()
			require.NoError(t, err)
			require.Equal(t, tt.net, got)
		})
	}
}

// Test_ContiguousIPNetwork_ToContiguous_HostBitsNormalized verifies that a
// hand-built message with host bits set decodes to the masked block.
func Test_ContiguousIPNetwork_ToContiguous_HostBitsNormalized(t *testing.T) {
	tests := []struct {
		name      string
		addr      []byte
		prefixLen uint32
		want      string
	}{
		{name: "IPv4", addr: []byte{10, 0, 0, 1}, prefixLen: 24, want: "10.0.0.0/24"},
		{
			name:      "IPv6",
			addr:      []byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
			prefixLen: 32,
			want:      "2001:db8::/32",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &commonpb.ContiguousIPNetwork{
				Addr:      &commonpb.IPAddress{Addr: tt.addr},
				PrefixLen: tt.prefixLen,
			}
			got, err := m.ToContiguous()
			require.NoError(t, err)
			require.Equal(t, xnetip.MustParseContiguous(tt.want), got)
		})
	}
}

// Test_ContiguousIPNetwork_ToContiguous_Errors verifies that a bad address or
// an overlong prefix length is rejected, never decoded to the zero block.
func Test_ContiguousIPNetwork_ToContiguous_Errors(t *testing.T) {
	tests := []struct {
		name      string
		m         *commonpb.ContiguousIPNetwork
		wantErrIs error
	}{
		{name: "zero message", m: &commonpb.ContiguousIPNetwork{}},
		{name: "nil address", m: &commonpb.ContiguousIPNetwork{PrefixLen: 24}},
		{
			name: "empty address",
			m: &commonpb.ContiguousIPNetwork{
				Addr:      &commonpb.IPAddress{},
				PrefixLen: 24,
			},
		},
		{
			name: "three byte address",
			m: &commonpb.ContiguousIPNetwork{
				Addr:      &commonpb.IPAddress{Addr: []byte{10, 0, 0}},
				PrefixLen: 24,
			},
		},
		{
			name: "five byte address",
			m: &commonpb.ContiguousIPNetwork{
				Addr:      &commonpb.IPAddress{Addr: []byte{1, 2, 3, 4, 5}},
				PrefixLen: 24,
			},
		},
		{
			name: "IPv4 prefix length above 32",
			m: &commonpb.ContiguousIPNetwork{
				Addr:      &commonpb.IPAddress{Addr: []byte{10, 0, 0, 0}},
				PrefixLen: 33,
			},
			wantErrIs: xnetip.ErrCIDROverflow,
		},
		{
			name: "IPv6 prefix length above 128",
			m: &commonpb.ContiguousIPNetwork{
				Addr:      &commonpb.IPAddress{Addr: make([]byte, 16)},
				PrefixLen: 129,
			},
			wantErrIs: xnetip.ErrCIDROverflow,
		},
		{
			name: "IPv4 prefix length at uint32 maximum",
			m: &commonpb.ContiguousIPNetwork{
				Addr:      &commonpb.IPAddress{Addr: []byte{10, 0, 0, 0}},
				PrefixLen: ^uint32(0),
			},
			wantErrIs: xnetip.ErrCIDROverflow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.m.ToContiguous()
			require.Error(t, err)
			if tt.wantErrIs != nil {
				require.ErrorIs(t, err, tt.wantErrIs)
			}
			require.Equal(t, xnetip.Contiguous[xnetip.Network]{}, got)
		})
	}
}

// Test_ContiguousIPNetwork_ToContiguous_FamilyExtraction verifies that the
// decoded block unwraps into the family of its wire address width only.
func Test_ContiguousIPNetwork_ToContiguous_FamilyExtraction(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantIPv4 bool
	}{
		{name: "IPv4 message unwraps to Network4", input: "10.0.0.0/24", wantIPv4: true},
		{name: "IPv6 message unwraps to Network6", input: "2001:db8::/32", wantIPv4: false},
		{
			name:     "IPv4-mapped IPv6 message unwraps to Network6",
			input:    "::ffff:10.0.0.0/120",
			wantIPv4: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := commonpb.ParseContiguousIPNetwork(tt.input)
			require.NoError(t, err)
			net, err := m.ToContiguous()
			require.NoError(t, err)
			want := netip.MustParsePrefix(tt.input)

			net4, ok4 := xnetip.ContiguousIPv4(net)
			net6, ok6 := xnetip.ContiguousIPv6(net)
			require.Equal(t, tt.wantIPv4, ok4)
			require.Equal(t, !tt.wantIPv4, ok6)
			if tt.wantIPv4 {
				require.Equal(t, want, net4.Prefix())
			} else {
				require.Equal(t, want, net6.Prefix())
			}
		})
	}
}

// Test_ContiguousIPNetwork_ToPrefix_MatchesToContiguous verifies that the
// prefix view and the block view of one message name the same network.
func Test_ContiguousIPNetwork_ToPrefix_MatchesToContiguous(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "IPv4", input: "10.0.0.1/24"},
		{name: "IPv6", input: "2001:db8::1/32"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := commonpb.ParseContiguousIPNetwork(tt.input)
			require.NoError(t, err)

			prefix, err := m.ToPrefix()
			require.NoError(t, err)
			net, err := m.ToContiguous()
			require.NoError(t, err)
			require.Equal(t, prefix, net.Prefix())
		})
	}
}
