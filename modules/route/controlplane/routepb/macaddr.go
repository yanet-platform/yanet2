package routepb

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// NewMACAddressEUI48 creates a MACAddress from a 6-byte EUI-48
// address.
func NewMACAddressEUI48(addr [6]byte) *MACAddress {
	buf := [8]byte{}
	copy(buf[:], addr[:])

	return &MACAddress{
		Addr: binary.BigEndian.Uint64(buf[:]),
	}
}

// EUI48 extracts a 6-byte EUI-48 address from the MACAddress.
func (m *MACAddress) EUI48() [6]byte {
	buf := [8]byte{}
	binary.BigEndian.PutUint64(buf[:], m.GetAddr())

	return [6]byte(buf[:6])
}

// FormatMACString formats a MAC address as a colon-separated string.
func FormatMACString(eui48 [6]byte) string {
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x",
		eui48[0], eui48[1], eui48[2], eui48[3], eui48[4], eui48[5])
}

// ParseMACString parses a MAC address string.
//
// Supported formats:
// - Colon-separated: "xx:xx:xx:xx:xx:xx" (IEEE standard)
// - Hyphen-separated: "xx-xx-xx-xx-xx-xx" (Microsoft/Windows)
// - Dot-separated: "xxxx.xxxx.xxxx" (Cisco)
// - No separator: "xxxxxxxxxxxx" (12 hex digits)
func ParseMACString(s string) (*MACAddress, error) {
	s = strings.TrimSpace(s)

	var eui48 [6]byte

	if strings.Contains(s, ":") || strings.Contains(s, "-") {
		separator := ":"
		if strings.Contains(s, "-") {
			separator = "-"
		}
		parts := strings.Split(s, separator)
		if len(parts) != 6 {
			return nil, fmt.Errorf("invalid MAC address format: expected 6 octets, got %d", len(parts))
		}
		for idx, part := range parts {
			b, err := strconv.ParseUint(part, 16, 8)
			if err != nil {
				return nil, fmt.Errorf("invalid octet at position %d: %w", idx, err)
			}
			eui48[idx] = byte(b)
		}
	} else if strings.Contains(s, ".") {
		parts := strings.Split(s, ".")
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid Cisco MAC address format: expected 3 groups, got %d", len(parts))
		}
		for idx, part := range parts {
			if len(part) != 4 {
				return nil, fmt.Errorf("invalid Cisco MAC address format: group %d must be 4 hex digits", idx)
			}
			val, err := strconv.ParseUint(part, 16, 16)
			if err != nil {
				return nil, fmt.Errorf("invalid hex in group %d: %w", idx, err)
			}
			eui48[idx*2] = byte(val >> 8)
			eui48[idx*2+1] = byte(val & 0xff)
		}
	} else {
		if len(s) != 12 {
			return nil, fmt.Errorf("invalid MAC address format: expected 12 hex digits, got %d", len(s))
		}
		for idx := range 6 {
			b, err := strconv.ParseUint(s[idx*2:idx*2+2], 16, 8)
			if err != nil {
				return nil, fmt.Errorf("invalid hex at position %d: %w", idx, err)
			}
			eui48[idx] = byte(b)
		}
	}

	return NewMACAddressEUI48(eui48), nil
}

// MarshalJSON serializes addr as a human-readable MAC address string.
func (m *MACAddress) MarshalJSON() ([]byte, error) {
	return fmt.Appendf(nil, `{"addr":"%s"}`, FormatMACString(m.EUI48())), nil
}

// UnmarshalJSON accepts addr as a MAC address string in various EUI-48 formats.
func (m *MACAddress) UnmarshalJSON(data []byte) error {
	var raw struct {
		Addr string `json:"addr"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Addr == "" {
		return fmt.Errorf("empty MAC address is not allowed")
	}

	parsed, err := ParseMACString(raw.Addr)
	if err != nil {
		return err
	}
	m.Addr = parsed.Addr
	return nil
}
