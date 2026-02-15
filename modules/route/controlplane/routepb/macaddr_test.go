package routepb

import (
	"encoding/json"
	"testing"
)

func TestFormatMACString(t *testing.T) {
	tests := []struct {
		name string
		addr [6]byte
		want string
	}{
		{
			name: "typical MAC",
			addr: [6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9},
			want: "3a:ac:26:9b:5b:f9",
		},
		{
			name: "all zeros",
			addr: [6]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
			want: "00:00:00:00:00:00",
		},
		{
			name: "all ones",
			addr: [6]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
			want: "ff:ff:ff:ff:ff:ff",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatMACString(tt.addr)
			if got != tt.want {
				t.Errorf("FormatMACString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseMACString(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    [6]byte
		wantErr bool
	}{
		{
			name:  "colon-separated lowercase",
			input: "3a:ac:26:9b:5b:f9",
			want:  [6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9},
		},
		{
			name:  "colon-separated uppercase",
			input: "3A:AC:26:9B:5B:F9",
			want:  [6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9},
		},
		{
			name:  "colon-separated mixed case",
			input: "3a:Ac:26:9B:5b:F9",
			want:  [6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9},
		},
		{
			name:  "hyphen-separated",
			input: "3a-ac-26-9b-5b-f9",
			want:  [6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9},
		},
		{
			name:  "hyphen-separated uppercase",
			input: "3A-AC-26-9B-5B-F9",
			want:  [6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9},
		},
		{
			name:  "dot-separated (Cisco)",
			input: "3aac.269b.5bf9",
			want:  [6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9},
		},
		{
			name:  "dot-separated uppercase (Cisco)",
			input: "3AAC.269B.5BF9",
			want:  [6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9},
		},
		{
			name:  "no separator",
			input: "3aac269b5bf9",
			want:  [6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9},
		},
		{
			name:  "no separator uppercase",
			input: "3AAC269B5BF9",
			want:  [6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9},
		},
		{
			name:  "all zeros colon",
			input: "00:00:00:00:00:00",
			want:  [6]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		},
		{
			name:  "all ones colon",
			input: "ff:ff:ff:ff:ff:ff",
			want:  [6]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		},
		{
			name:  "with leading/trailing whitespace",
			input: "  3a:ac:26:9b:5b:f9  ",
			want:  [6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9},
		},
		{
			name:    "invalid - too few octets (colon)",
			input:   "3a:ac:26:9b:5b",
			wantErr: true,
		},
		{
			name:    "invalid - too many octets (colon)",
			input:   "3a:ac:26:9b:5b:f9:00",
			wantErr: true,
		},
		{
			name:    "invalid - too few octets (hyphen)",
			input:   "3a-ac-26-9b-5b",
			wantErr: true,
		},
		{
			name:    "invalid - wrong number of groups (Cisco)",
			input:   "3aac.269b",
			wantErr: true,
		},
		{
			name:    "invalid - wrong group length (Cisco)",
			input:   "3aa.269b.5bf9",
			wantErr: true,
		},
		{
			name:    "invalid - too short (no separator)",
			input:   "3aac269b5bf",
			wantErr: true,
		},
		{
			name:    "invalid - too long (no separator)",
			input:   "3aac269b5bf900",
			wantErr: true,
		},
		{
			name:    "invalid - non-hex characters",
			input:   "3a:ac:26:9g:5b:f9",
			wantErr: true,
		},
		{
			name:    "invalid - empty string",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMACString(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseMACString() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				gotBytes := got.EUI48()
				if gotBytes != tt.want {
					t.Errorf("ParseMACString() = %v, want %v", gotBytes, tt.want)
				}
			}
		})
	}
}

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
			name: "no separator",
			json: `{"addr":"3aac269b5bf9"}`,
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
