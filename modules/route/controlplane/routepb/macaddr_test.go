package routepb

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMACAddress_MarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		mac     *MACAddress
		want    string
		wantErr bool
	}{
		{
			name: "typical MAC",
			mac:  NewMACAddressEUI48([6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9}),
			want: `{"addr":"3a:ac:26:9b:5b:f9"}`,
		},
		{
			name: "all zeros",
			mac:  NewMACAddressEUI48([6]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00}),
			want: `{"addr":"00:00:00:00:00:00"}`,
		},
		{
			name: "all ones",
			mac:  NewMACAddressEUI48([6]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}),
			want: `{"addr":"ff:ff:ff:ff:ff:ff"}`,
		},
		{
			name: "zero value MAC",
			mac:  &MACAddress{Addr: 0},
			want: `{"addr":"00:00:00:00:00:00"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.mac)
			if (err != nil) != tt.wantErr {
				t.Errorf("MarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if string(got) != tt.want {
				t.Errorf("MarshalJSON() = %v, want %v", string(got), tt.want)
			}
		})
	}
}

func TestMACAddress_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		want    [6]byte
		wantErr bool
	}{
		{
			name: "colon-separated",
			json: `{"addr":"3a:ac:26:9b:5b:f9"}`,
			want: [6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9},
		},
		{
			name: "hyphen-separated",
			json: `{"addr":"3a-ac-26-9b-5b-f9"}`,
			want: [6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9},
		},
		{
			name: "dot-separated (Cisco)",
			json: `{"addr":"3aac.269b.5bf9"}`,
			want: [6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9},
		},
		{
			name: "uppercase",
			json: `{"addr":"3A:AC:26:9B:5B:F9"}`,
			want: [6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9},
		},
		{
			name:    "empty string",
			json:    `{"addr":""}`,
			wantErr: true,
		},
		{
			name:    "invalid format",
			json:    `{"addr":"invalid"}`,
			wantErr: true,
		},
		{
			name:    "invalid JSON",
			json:    `{"addr":`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mac MACAddress
			err := json.Unmarshal([]byte(tt.json), &mac)
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				got := mac.EUI48()
				if got != tt.want {
					t.Errorf("UnmarshalJSON() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestMACAddress_RoundTrip(t *testing.T) {
	tests := []struct {
		name string
		mac  *MACAddress
	}{
		{
			name: "typical MAC",
			mac:  NewMACAddressEUI48([6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9}),
		},
		{
			name: "all zeros",
			mac:  NewMACAddressEUI48([6]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00}),
		},
		{
			name: "all ones",
			mac:  NewMACAddressEUI48([6]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.mac)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}

			var got MACAddress
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}

			if got.Addr != tt.mac.Addr {
				t.Errorf("Round trip failed: got %v, want %v", got.Addr, tt.mac.Addr)
			}
		})
	}
}

func TestMACAddress_FromProto(t *testing.T) {
	mac := &MACAddress{
		Addr: 0x00003aac269b5bf9,
	}

	assert.Equal(t, [6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9}, mac.EUI48())
}

func TestMACAddress_ToProto(t *testing.T) {
	mac := NewMACAddressEUI48([6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9})
	assert.Equal(t, uint64(0x00003aac269b5bf9), mac.Addr)
}
