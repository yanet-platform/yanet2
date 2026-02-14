package routepb

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
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

// MarshalJSON serializes addr as a JSON string to avoid precision
// loss in JavaScript (uint64 exceeds Number.MAX_SAFE_INTEGER).
func (m *MACAddress) MarshalJSON() ([]byte, error) {
	return fmt.Appendf(nil, `{"addr":"%d"}`, m.Addr), nil
}

// UnmarshalJSON accepts addr as either a JSON number or a JSON
// string so that JavaScript clients that cannot represent uint64
// without precision loss can send the value as a string.
func (m *MACAddress) UnmarshalJSON(data []byte) error {
	var raw struct {
		Addr json.RawMessage `json:"addr"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw.Addr) == 0 {
		return nil
	}

	s := string(raw.Addr)
	// JSON string: "3a:ac:26:9b:5b:f9"
	if len(s) >= 2 && s[0] == '"' {
		unquoted := s[1 : len(s)-1]
		v, err := strconv.ParseUint(unquoted, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid addr string: %w", err)
		}
		m.Addr = v
		return nil
	}

	// JSON number: 0x3aac269b5bf90000
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid addr number: %w", err)
	}

	m.Addr = v
	return nil
}
