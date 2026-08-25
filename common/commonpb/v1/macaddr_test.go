package commonpb_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// Test_MACAddress_MarshalJSON verifies that a well-formed address
// marshals as a bare lowercase colon-separated EUI-48 string.
func Test_MACAddress_MarshalJSON(t *testing.T) {
	tests := []struct {
		name string
		mac  *commonpb.MACAddress
		want string
	}{
		{
			name: "typical address",
			mac:  commonpb.NewMACAddressEUI48([6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9}),
			want: `"3a:ac:26:9b:5b:f9"`,
		},
		{
			name: "all zeros",
			mac:  commonpb.NewMACAddressEUI48([6]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00}),
			want: `"00:00:00:00:00:00"`,
		},
		{
			name: "all ones",
			mac:  commonpb.NewMACAddressEUI48([6]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}),
			want: `"ff:ff:ff:ff:ff:ff"`,
		},
		{
			name: "zero message",
			mac:  &commonpb.MACAddress{},
			want: `"00:00:00:00:00:00"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.mac)
			require.NoError(t, err)
			require.Equal(t, tt.want, string(got))
		})
	}
}

// Test_MACAddress_MarshalJSON_RejectsUpperBits verifies that set upper
// 16 bits are reported as an error instead of being truncated.
func Test_MACAddress_MarshalJSON_RejectsUpperBits(t *testing.T) {
	_, err := json.Marshal(&commonpb.MACAddress{Addr: 0x1_0000_0000_0000})
	require.Error(t, err)
}

// Test_MACAddress_UnmarshalJSON verifies that only the colon- and
// hyphen-separated EUI-48 layouts are accepted.
func Test_MACAddress_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		want    [6]byte
		wantErr bool
	}{
		{
			name: "colon-separated",
			json: `"3a:ac:26:9b:5b:f9"`,
			want: [6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9},
		},
		{
			name: "hyphen-separated",
			json: `"3a-ac-26-9b-5b-f9"`,
			want: [6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9},
		},
		{
			name: "uppercase hex",
			json: `"3A:AC:26:9B:5B:F9"`,
			want: [6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9},
		},
		{
			name:    "dot-separated is rejected",
			json:    `"3aac.269b.5bf9"`,
			wantErr: true,
		},
		{
			name:    "unseparated is rejected",
			json:    `"3aac269b5bf9"`,
			wantErr: true,
		},
		{
			name:    "mixed separators are rejected",
			json:    `"3a:ac-26:9b-5b:f9"`,
			wantErr: true,
		},
		{
			name:    "legacy object form is rejected",
			json:    `{"addr":"3a:ac:26:9b:5b:f9"}`,
			wantErr: true,
		},
		{
			name:    "empty string",
			json:    `""`,
			wantErr: true,
		},
		{
			name:    "not an address",
			json:    `"invalid"`,
			wantErr: true,
		},
		{
			name:    "truncated JSON",
			json:    `"3a:ac`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mac commonpb.MACAddress
			err := json.Unmarshal([]byte(tt.json), &mac)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, mac.EUI48())
		})
	}
}

// Test_MACAddress_JSONRoundTrip verifies that a marshalled address
// unmarshals to the same wire value.
func Test_MACAddress_JSONRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		mac  *commonpb.MACAddress
	}{
		{
			name: "typical address",
			mac:  commonpb.NewMACAddressEUI48([6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9}),
		},
		{
			name: "all zeros",
			mac:  commonpb.NewMACAddressEUI48([6]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00}),
		},
		{
			name: "all ones",
			mac:  commonpb.NewMACAddressEUI48([6]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.mac)
			require.NoError(t, err)

			var got commonpb.MACAddress
			require.NoError(t, json.Unmarshal(data, &got))
			require.Equal(t, tt.mac.Addr, got.Addr)
		})
	}
}

// Test_MACAddress_AsLogValue verifies that logging renders the EUI-48
// text and falls back to a literal marker for a malformed message.
func Test_MACAddress_AsLogValue(t *testing.T) {
	tests := []struct {
		name string
		mac  *commonpb.MACAddress
		want string
	}{
		{
			name: "typical address",
			mac:  commonpb.NewMACAddressEUI48([6]byte{0x7c, 0xc3, 0x85, 0x70, 0x5a, 0xd6}),
			want: "7c:c3:85:70:5a:d6",
		},
		{
			name: "zero message",
			mac:  &commonpb.MACAddress{},
			want: "00:00:00:00:00:00",
		},
		{
			name: "upper bits set",
			mac:  &commonpb.MACAddress{Addr: 0x1_0000_0000_0000},
			want: "invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.mac.AsLogValue())
		})
	}
}

// Test_MACAddress_EUI48 verifies that the wire value decodes to the
// octets in network order.
func Test_MACAddress_EUI48(t *testing.T) {
	mac := &commonpb.MACAddress{Addr: 0x00003aac269b5bf9}
	require.Equal(t, [6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9}, mac.EUI48())
}

// Test_NewMACAddressEUI48_WireValue verifies that the octets encode into
// the lower 48 bits of the wire value.
func Test_NewMACAddressEUI48_WireValue(t *testing.T) {
	mac := commonpb.NewMACAddressEUI48([6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9})
	require.Equal(t, uint64(0x00003aac269b5bf9), mac.Addr)
}
