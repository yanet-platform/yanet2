package commonpb

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
)

// NewMACAddressEUI48 creates a MACAddress from a 6-byte EUI-48
// address.
func NewMACAddressEUI48(addr [6]byte) *MACAddress {
	buf := [8]byte{}
	copy(buf[2:], addr[:])

	return &MACAddress{
		Addr: binary.BigEndian.Uint64(buf[:]),
	}
}

// EUI48 extracts a 6-byte EUI-48 address from the MACAddress.
func (m *MACAddress) EUI48() [6]byte {
	buf := [8]byte{}
	binary.BigEndian.PutUint64(buf[:], m.GetAddr())

	return [6]byte(buf[2:])
}

// AsLogValue implements xgrpc.ProtoLogValue for compact gRPC logging.
func (m *MACAddress) AsLogValue() any {
	eui48 := m.EUI48()
	return net.HardwareAddr(eui48[:]).String()
}

// macAddressJSON is the JSON wire shape shared by MarshalJSON and
// UnmarshalJSON.
type macAddressJSON struct {
	Addr string `json:"addr"`
}

// MarshalJSON serializes addr as a human-readable MAC address string.
func (m *MACAddress) MarshalJSON() ([]byte, error) {
	eui48 := m.EUI48()
	return json.Marshal(macAddressJSON{Addr: net.HardwareAddr(eui48[:]).String()})
}

// UnmarshalJSON accepts addr as a MAC address string in various EUI-48
// formats.
func (m *MACAddress) UnmarshalJSON(data []byte) error {
	var raw macAddressJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Addr == "" {
		return fmt.Errorf("empty MAC address is not allowed")
	}

	parsed, err := net.ParseMAC(raw.Addr)
	if err != nil {
		return err
	}
	if len(parsed) != 6 {
		return fmt.Errorf("invalid MAC address format: expected 6 octets, got %d", len(parsed))
	}

	*m = *NewMACAddressEUI48([6]byte(parsed))
	return nil
}
