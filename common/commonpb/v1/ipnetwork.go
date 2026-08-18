package commonpb

import (
	"encoding/json"
	"fmt"
	"net/netip"
)

// NewContiguousIPNetworkFromPrefix creates a ContiguousIPNetwork from a
// netip.Prefix value, masking off any host bits.
//
// Returns an error if prefix is not valid.
func NewContiguousIPNetworkFromPrefix(prefix netip.Prefix) (*ContiguousIPNetwork, error) {
	if !prefix.IsValid() {
		return nil, fmt.Errorf("invalid prefix")
	}
	masked := prefix.Masked()
	return &ContiguousIPNetwork{
		Addr:      NewIPAddressFromAddr(masked.Addr()),
		PrefixLen: uint32(masked.Bits()),
	}, nil
}

// ParseContiguousIPNetwork parses s as a CIDR prefix, masking off any host
// bits.
func ParseContiguousIPNetwork(s string) (*ContiguousIPNetwork, error) {
	prefix, err := netip.ParsePrefix(s)
	if err != nil {
		return nil, fmt.Errorf("failed to parse IP network: %w", err)
	}
	return NewContiguousIPNetworkFromPrefix(prefix)
}

// NetworksFromPrefixes creates ContiguousIPNetwork messages from netip.Prefix
// values, masking off any host bits.
//
// Returns an error if any prefix is not valid.
func NetworksFromPrefixes(prefixes []netip.Prefix) ([]*ContiguousIPNetwork, error) {
	networks := make([]*ContiguousIPNetwork, 0, len(prefixes))
	for idx, prefix := range prefixes {
		network, err := NewContiguousIPNetworkFromPrefix(prefix)
		if err != nil {
			return nil, fmt.Errorf("prefixes[%d]: %w", idx, err)
		}
		networks = append(networks, network)
	}

	return networks, nil
}

// PrefixesFromNetworks converts ContiguousIPNetwork messages to masked
// netip.Prefix values.
//
// Returns an error if any network is malformed.
func PrefixesFromNetworks(networks []*ContiguousIPNetwork) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(networks))
	for idx, network := range networks {
		prefix, err := network.ToPrefix()
		if err != nil {
			return nil, fmt.Errorf("prefixes[%d]: %w", idx, err)
		}
		prefixes = append(prefixes, prefix)
	}

	return prefixes, nil
}

// ToPrefix converts the ContiguousIPNetwork back to a netip.Prefix value.
//
// Returns an error if addr is malformed or if prefix_len exceeds the
// address family's bit length. The returned prefix is masked, so host bits
// never leak out even if the message was constructed by hand.
func (m *ContiguousIPNetwork) ToPrefix() (netip.Prefix, error) {
	addr, err := m.GetAddr().ToAddr()
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("failed to parse network address: %w", err)
	}
	if m.GetPrefixLen() > uint32(addr.BitLen()) {
		return netip.Prefix{}, fmt.Errorf(
			"prefix length %d exceeds IPv%d address bit length %d",
			m.GetPrefixLen(),
			familyNum(addr),
			addr.BitLen(),
		)
	}
	return netip.PrefixFrom(addr, int(m.GetPrefixLen())).Masked(), nil
}

// AsLogValue implements xgrpc.ProtoLogValue for compact gRPC logging.
func (m *ContiguousIPNetwork) AsLogValue() any {
	prefix, err := m.ToPrefix()
	if err != nil {
		return "invalid"
	}

	return prefix.String()
}

// contiguousIPNetworkJSON is the JSON wire shape shared by MarshalJSON and
// UnmarshalJSON.
type contiguousIPNetworkJSON struct {
	Network string `json:"network"`
}

// MarshalJSON serializes the network as a human-readable CIDR string.
func (m *ContiguousIPNetwork) MarshalJSON() ([]byte, error) {
	prefix, err := m.ToPrefix()
	if err != nil {
		return nil, err
	}
	return json.Marshal(contiguousIPNetworkJSON{Network: prefix.String()})
}

// UnmarshalJSON accepts the network as a CIDR string under the "network"
// key.
func (m *ContiguousIPNetwork) UnmarshalJSON(data []byte) error {
	var raw contiguousIPNetworkJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Network == "" {
		return fmt.Errorf("empty IP network is not allowed")
	}

	parsed, err := ParseContiguousIPNetwork(raw.Network)
	if err != nil {
		return err
	}

	m.Addr = parsed.Addr
	m.PrefixLen = parsed.PrefixLen
	return nil
}
