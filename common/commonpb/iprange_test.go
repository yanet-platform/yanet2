package commonpb

import (
	"encoding/json"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewIPRange_V4(t *testing.T) {
	start := netip.MustParseAddr("10.0.0.0")
	end := netip.MustParseAddr("10.0.0.255")

	r := NewIPRange(start, end)

	require.NotNil(t, r.Start)
	require.NotNil(t, r.End)
	require.Equal(t, []byte{10, 0, 0, 0}, r.Start.Addr)
	require.Equal(t, []byte{10, 0, 0, 255}, r.End.Addr)
}

func TestNewIPRange_V6(t *testing.T) {
	start := netip.MustParseAddr("2001:db8::")
	end := netip.MustParseAddr("2001:db8::ffff")

	r := NewIPRange(start, end)

	require.NotNil(t, r.Start)
	require.NotNil(t, r.End)
	require.Len(t, r.Start.Addr, 16)
	require.Len(t, r.End.Addr, 16)
}

func TestIPRange_ToRange_RoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		start netip.Addr
		end   netip.Addr
	}{
		{
			name:  "IPv4 range",
			start: netip.MustParseAddr("10.0.0.0"),
			end:   netip.MustParseAddr("10.0.0.255"),
		},
		{
			name:  "IPv4 single address",
			start: netip.MustParseAddr("192.168.1.1"),
			end:   netip.MustParseAddr("192.168.1.1"),
		},
		{
			name:  "IPv6 range",
			start: netip.MustParseAddr("2001:db8::"),
			end:   netip.MustParseAddr("2001:db8::ffff"),
		},
		{
			name:  "IPv6 single address",
			start: netip.MustParseAddr("::1"),
			end:   netip.MustParseAddr("::1"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewIPRange(tt.start, tt.end)
			gotStart, gotEnd, err := r.ToRange()
			require.NoError(t, err)
			require.Equal(t, tt.start, gotStart)
			require.Equal(t, tt.end, gotEnd)
		})
	}
}

func TestIPRange_ToRange_InvalidStart(t *testing.T) {
	r := &IPRange{
		Start: &IPAddress{Addr: []byte{1, 2, 3}},
		End:   NewIPAddressFromAddr(netip.MustParseAddr("10.0.0.255")),
	}
	_, _, err := r.ToRange()
	require.Error(t, err)
}

func TestIPRange_ToRange_InvalidEnd(t *testing.T) {
	r := &IPRange{
		Start: NewIPAddressFromAddr(netip.MustParseAddr("10.0.0.0")),
		End:   &IPAddress{Addr: []byte{1, 2, 3}},
	}
	_, _, err := r.ToRange()
	require.Error(t, err)
}

func TestIPRange_ToRange_FamilyMismatch(t *testing.T) {
	r := &IPRange{
		Start: NewIPAddressFromAddr(netip.MustParseAddr("10.0.0.0")),
		End:   NewIPAddressFromAddr(netip.MustParseAddr("2001:db8::ffff")),
	}
	_, _, err := r.ToRange()
	require.Error(t, err)
}

func TestIPRange_MarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		r       *IPRange
		want    string
		wantErr bool
	}{
		{
			name: "IPv4",
			r:    NewIPRange(netip.MustParseAddr("10.0.0.0"), netip.MustParseAddr("10.0.0.255")),
			want: `{"start":"10.0.0.0","end":"10.0.0.255"}`,
		},
		{
			name: "IPv6",
			r:    NewIPRange(netip.MustParseAddr("2001:db8::"), netip.MustParseAddr("2001:db8::ffff")),
			want: `{"start":"2001:db8::","end":"2001:db8::ffff"}`,
		},
		{
			name: "invalid start length",
			r: &IPRange{
				Start: &IPAddress{Addr: []byte{1, 2, 3}},
				End:   NewIPAddressFromAddr(netip.MustParseAddr("10.0.0.255")),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.r)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, string(got))
		})
	}
}

func TestIPRange_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantStart netip.Addr
		wantEnd   netip.Addr
		wantErr   bool
	}{
		{
			name:      "IPv4",
			input:     `{"start":"10.0.0.0","end":"10.0.0.255"}`,
			wantStart: netip.MustParseAddr("10.0.0.0"),
			wantEnd:   netip.MustParseAddr("10.0.0.255"),
		},
		{
			name:      "IPv6",
			input:     `{"start":"2001:db8::","end":"2001:db8::ffff"}`,
			wantStart: netip.MustParseAddr("2001:db8::"),
			wantEnd:   netip.MustParseAddr("2001:db8::ffff"),
		},
		{
			name:    "empty start",
			input:   `{"start":"","end":"10.0.0.255"}`,
			wantErr: true,
		},
		{
			name:    "empty end",
			input:   `{"start":"10.0.0.0","end":""}`,
			wantErr: true,
		},
		{
			name:    "malformed start",
			input:   `{"start":"not-an-ip","end":"10.0.0.255"}`,
			wantErr: true,
		},
		{
			name:    "malformed end",
			input:   `{"start":"10.0.0.0","end":"not-an-ip"}`,
			wantErr: true,
		},
		{
			name:    "family mismatch v4 start v6 end",
			input:   `{"start":"10.0.0.0","end":"2001:db8::ffff"}`,
			wantErr: true,
		},
		{
			name:    "invalid JSON",
			input:   `{"start":`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r IPRange
			err := json.Unmarshal([]byte(tt.input), &r)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			gotStart, gotEnd, err := r.ToRange()
			require.NoError(t, err)
			require.Equal(t, tt.wantStart, gotStart)
			require.Equal(t, tt.wantEnd, gotEnd)
		})
	}
}

func TestIPRange_JSONRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		start netip.Addr
		end   netip.Addr
	}{
		{
			name:  "IPv4",
			start: netip.MustParseAddr("10.0.0.0"),
			end:   netip.MustParseAddr("10.0.0.255"),
		},
		{
			name:  "IPv6",
			start: netip.MustParseAddr("2001:db8::"),
			end:   netip.MustParseAddr("2001:db8::ffff"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := NewIPRange(tt.start, tt.end)

			data, err := json.Marshal(original)
			require.NoError(t, err)

			var got IPRange
			require.NoError(t, json.Unmarshal(data, &got))

			gotStart, gotEnd, err := got.ToRange()
			require.NoError(t, err)
			require.Equal(t, tt.start, gotStart)
			require.Equal(t, tt.end, gotEnd)
		})
	}
}

func TestIPRange_AsLogValue(t *testing.T) {
	tests := []struct {
		name string
		r    *IPRange
		want string
	}{
		{
			name: "IPv4",
			r:    NewIPRange(netip.MustParseAddr("10.0.0.0"), netip.MustParseAddr("10.0.0.255")),
			want: "[10.0.0.0, 10.0.0.255]",
		},
		{
			name: "IPv6",
			r:    NewIPRange(netip.MustParseAddr("2001:db8::"), netip.MustParseAddr("2001:db8::ffff")),
			want: "[2001:db8::, 2001:db8::ffff]",
		},
		{
			name: "invalid start bytes",
			r: &IPRange{
				Start: &IPAddress{Addr: []byte{1, 2, 3}},
				End:   NewIPAddressFromAddr(netip.MustParseAddr("10.0.0.255")),
			},
			want: "invalid",
		},
		{
			name: "family mismatch",
			r: &IPRange{
				Start: NewIPAddressFromAddr(netip.MustParseAddr("10.0.0.0")),
				End:   NewIPAddressFromAddr(netip.MustParseAddr("2001:db8::ffff")),
			},
			want: "invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.r.AsLogValue())
		})
	}
}
