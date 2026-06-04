package commonpb

import (
	"encoding/json"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewIPAddressFromAddr_V4(t *testing.T) {
	addr := netip.MustParseAddr("10.0.0.1")
	ip := NewIPAddressFromAddr(addr)
	require.Equal(t, []byte{10, 0, 0, 1}, ip.Addr)
}

func TestNewIPAddressFromAddr_V6(t *testing.T) {
	addr := netip.MustParseAddr("2001:db8::1")
	ip := NewIPAddressFromAddr(addr)
	require.Len(t, ip.Addr, 16)
}

func TestNewIPAddressFromAddr_Zero(t *testing.T) {
	ip := NewIPAddressFromAddr(netip.Addr{})
	require.Empty(t, ip.Addr)
}

func TestNewIPAddressV4(t *testing.T) {
	raw := [4]byte{192, 168, 1, 1}
	ip := NewIPAddressV4(raw)
	require.Equal(t, []byte{192, 168, 1, 1}, ip.Addr)
}

func TestNewIPAddressV6(t *testing.T) {
	raw := [16]byte{
		0x20, 0x01, 0x0d, 0xb8,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x01,
	}
	ip := NewIPAddressV6(raw)
	require.Len(t, ip.Addr, 16)
	require.Equal(t, raw[:], ip.Addr)
}

func TestIPAddress_ToAddr_RoundTrip(t *testing.T) {
	tests := []struct {
		name string
		addr netip.Addr
	}{
		{
			name: "IPv4",
			addr: netip.MustParseAddr("10.0.0.1"),
		},
		{
			name: "IPv4 broadcast",
			addr: netip.MustParseAddr("255.255.255.255"),
		},
		{
			name: "IPv6",
			addr: netip.MustParseAddr("2001:db8::1"),
		},
		{
			name: "IPv6 loopback",
			addr: netip.MustParseAddr("::1"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := NewIPAddressFromAddr(tt.addr)
			got, err := ip.ToAddr()
			require.NoError(t, err)
			require.Equal(t, tt.addr, got)
		})
	}
}

func TestIPAddress_ToAddr_V4Constructor(t *testing.T) {
	raw := [4]byte{172, 16, 0, 1}
	ip := NewIPAddressV4(raw)
	got, err := ip.ToAddr()
	require.NoError(t, err)
	require.Equal(t, netip.AddrFrom4(raw), got)
}

func TestIPAddress_ToAddr_V6Constructor(t *testing.T) {
	raw := [16]byte{
		0xfe, 0x80, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x01,
	}
	ip := NewIPAddressV6(raw)
	got, err := ip.ToAddr()
	require.NoError(t, err)
	require.Equal(t, netip.AddrFrom16(raw), got)
}

func TestIPAddress_ToAddr_InvalidLength(t *testing.T) {
	tests := []struct {
		name   string
		length int
	}{
		{name: "zero", length: 0},
		{name: "one byte", length: 1},
		{name: "three bytes", length: 3},
		{name: "five bytes", length: 5},
		{name: "fifteen bytes", length: 15},
		{name: "seventeen bytes", length: 17},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := &IPAddress{Addr: make([]byte, tt.length)}
			_, err := ip.ToAddr()
			require.Error(t, err)
		})
	}
}

func TestIPAddress_MarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		ip      *IPAddress
		want    string
		wantErr bool
	}{
		{
			name: "IPv4",
			ip:   NewIPAddressFromAddr(netip.MustParseAddr("10.0.0.1")),
			want: `{"addr":"10.0.0.1"}`,
		},
		{
			name: "IPv6",
			ip:   NewIPAddressFromAddr(netip.MustParseAddr("2001:db8::1")),
			want: `{"addr":"2001:db8::1"}`,
		},
		{
			name:    "invalid length",
			ip:      &IPAddress{Addr: []byte{1, 2, 3}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.ip)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, string(got))
		})
	}
}

func TestIPAddress_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    netip.Addr
		wantErr bool
	}{
		{
			name:  "IPv4",
			input: `{"addr":"10.0.0.1"}`,
			want:  netip.MustParseAddr("10.0.0.1"),
		},
		{
			name:  "IPv6",
			input: `{"addr":"2001:db8::1"}`,
			want:  netip.MustParseAddr("2001:db8::1"),
		},
		{
			name:    "empty string",
			input:   `{"addr":""}`,
			wantErr: true,
		},
		{
			name:    "malformed address",
			input:   `{"addr":"not-an-ip"}`,
			wantErr: true,
		},
		{
			name:    "invalid JSON",
			input:   `{"addr":`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ip IPAddress
			err := json.Unmarshal([]byte(tt.input), &ip)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			got, err := ip.ToAddr()
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestIPAddress_JSONRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		addr netip.Addr
	}{
		{name: "IPv4", addr: netip.MustParseAddr("10.0.0.1")},
		{name: "IPv6", addr: netip.MustParseAddr("2001:db8::1")},
		{name: "IPv6 loopback", addr: netip.MustParseAddr("::1")},
		{name: "IPv4 zeros", addr: netip.MustParseAddr("0.0.0.0")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := NewIPAddressFromAddr(tt.addr)

			data, err := json.Marshal(original)
			require.NoError(t, err)

			var got IPAddress
			require.NoError(t, json.Unmarshal(data, &got))

			gotAddr, err := got.ToAddr()
			require.NoError(t, err)
			require.Equal(t, tt.addr, gotAddr)
		})
	}
}

func TestIPAddress_AsLogValue(t *testing.T) {
	tests := []struct {
		name string
		ip   *IPAddress
		want string
	}{
		{
			name: "IPv4",
			ip:   NewIPAddressFromAddr(netip.MustParseAddr("10.0.0.1")),
			want: "10.0.0.1",
		},
		{
			name: "IPv6",
			ip:   NewIPAddressFromAddr(netip.MustParseAddr("2001:db8::1")),
			want: "2001:db8::1",
		},
		{
			name: "invalid bytes",
			ip:   &IPAddress{Addr: []byte{1, 2, 3}},
			want: "invalid",
		},
		{
			name: "empty bytes",
			ip:   &IPAddress{},
			want: "invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.ip.AsLogValue())
		})
	}
}
