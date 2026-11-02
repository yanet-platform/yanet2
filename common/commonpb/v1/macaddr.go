package commonpb

import (
	"encoding/json"
	"fmt"

	"github.com/yanet-platform/xnetip"
)

// eui48TextLen is the length of the separated EUI-48 text form,
// "xx:xx:xx:xx:xx:xx".
const eui48TextLen = 17

// NewMACAddressEUI48 creates a MACAddress from a 6-byte EUI-48
// address.
func NewMACAddressEUI48(addr [6]byte) *MACAddress {
	return &MACAddress{
		Addr: xnetip.MacAddrFrom6(addr).Uint64(),
	}
}

// EUI48 extracts the lower 48 bits in network order, treating a nil
// address as zero.
func (m *MACAddress) EUI48() [6]byte {
	address, _ := xnetip.MacAddrFromUint64(m.GetAddr() & 0xffffffffffff)
	return address.As6()
}

// eui48Text renders the address as six colon-separated lowercase hex
// octets, rejecting set upper 16 bits instead of truncating them.
func (m *MACAddress) eui48Text() (string, error) {
	address, ok := xnetip.MacAddrFromUint64(m.GetAddr())
	if !ok {
		return "", fmt.Errorf("upper 16 bits are set for MAC address")
	}

	return address.String(), nil
}

// AsLogValue implements xgrpc.ProtoLogValue for compact gRPC logging.
func (m *MACAddress) AsLogValue() any {
	text, err := m.eui48Text()
	if err != nil {
		return "invalid"
	}

	return text
}

// MarshalJSON serializes the address as a bare EUI-48 string such as
// "3a:ac:26:9b:5b:f9".
func (m *MACAddress) MarshalJSON() ([]byte, error) {
	text, err := m.eui48Text()
	if err != nil {
		return nil, err
	}
	return json.Marshal(text)
}

// UnmarshalJSON accepts a bare EUI-48 string: colon- or hyphen-separated
// hex octets in either letter case. Other layouts are rejected.
func (m *MACAddress) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == "" {
		return fmt.Errorf("empty MAC address is not allowed")
	}
	if len(raw) != eui48TextLen {
		return fmt.Errorf("invalid MAC address format: expected EUI-48 xx:xx:xx:xx:xx:xx, got %q", raw)
	}

	parsed, err := xnetip.ParseMacAddr(raw)
	if err != nil {
		return err
	}

	*m = MACAddress{Addr: parsed.Uint64()}
	return nil
}
