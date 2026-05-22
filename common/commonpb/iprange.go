package commonpb

import (
	"encoding/json"
	"fmt"
	"net/netip"
)

// NewIPRange creates an IPRange from two netip.Addr values.
func NewIPRange(start, end netip.Addr) *IPRange {
	return &IPRange{
		Start: NewIPAddressFromAddr(start),
		End:   NewIPAddressFromAddr(end),
	}
}

// ToRange converts the IPRange back to a pair of netip.Addr values.
//
// Returns an error if either endpoint fails to parse or if the endpoints
// belong to different address families.
func (m *IPRange) ToRange() (netip.Addr, netip.Addr, error) {
	start, err := m.GetStart().ToAddr()
	if err != nil {
		return netip.Addr{}, netip.Addr{}, fmt.Errorf("failed to parse start address: %w", err)
	}

	end, err := m.GetEnd().ToAddr()
	if err != nil {
		return netip.Addr{}, netip.Addr{}, fmt.Errorf("failed to parse end address: %w", err)
	}

	if start.Is4() != end.Is4() {
		return netip.Addr{}, netip.Addr{}, fmt.Errorf("address family mismatch: start is IPv%d, end is IPv%d",
			familyNum(start), familyNum(end))
	}

	return start, end, nil
}

// familyNum returns 4 for IPv4 and 6 for IPv6.
func familyNum(addr netip.Addr) int {
	if addr.Is4() {
		return 4
	}
	return 6
}

// AsLogValue implements xgrpc.ProtoLogValue for compact gRPC logging.
func (m *IPRange) AsLogValue() any {
	start, end, err := m.ToRange()
	if err != nil {
		return "invalid"
	}

	return fmt.Sprintf("[%s, %s]", start.String(), end.String())
}

// MarshalJSON serializes the range as human-readable IP address strings.
func (m *IPRange) MarshalJSON() ([]byte, error) {
	start, end, err := m.ToRange()
	if err != nil {
		return nil, err
	}
	return fmt.Appendf(nil, `{"start":"%s","end":"%s"}`, start.String(), end.String()), nil
}

// UnmarshalJSON accepts start and end as IPv4 or IPv6 address strings.
func (m *IPRange) UnmarshalJSON(data []byte) error {
	var raw struct {
		Start string `json:"start"`
		End   string `json:"end"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Start == "" {
		return fmt.Errorf("empty start address is not allowed")
	}
	if raw.End == "" {
		return fmt.Errorf("empty end address is not allowed")
	}

	start, err := netip.ParseAddr(raw.Start)
	if err != nil {
		return fmt.Errorf("failed to parse start address: %w", err)
	}

	end, err := netip.ParseAddr(raw.End)
	if err != nil {
		return fmt.Errorf("failed to parse end address: %w", err)
	}

	if start.Is4() != end.Is4() {
		return fmt.Errorf(
			"address family mismatch: start is IPv%d, end is IPv%d",
			familyNum(start),
			familyNum(end),
		)
	}

	*m = *NewIPRange(start, end)
	return nil
}
