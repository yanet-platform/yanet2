package commonpb

import (
	"encoding/json"
	"fmt"
	"net/netip"
)

// NewIPPrefixFromPrefix creates an IPPrefix from a netip.Prefix value,
// masking off any host bits.
//
// The set oneof branch follows the address family. An IPv4-mapped IPv6
// address counts as IPv6, consistently with the family-typed messages.
// Returns an error if prefix is not valid.
func NewIPPrefixFromPrefix(prefix netip.Prefix) (*IPPrefix, error) {
	switch {
	case prefix.Addr().Is4():
		v4, err := NewIPv4PrefixFromPrefix(prefix)
		if err != nil {
			return nil, err
		}
		return &IPPrefix{Prefix: &IPPrefix_V4{V4: v4}}, nil
	case prefix.Addr().Is6():
		v6, err := NewIPv6PrefixFromPrefix(prefix)
		if err != nil {
			return nil, err
		}
		return &IPPrefix{Prefix: &IPPrefix_V6{V6: v6}}, nil
	default:
		return nil, fmt.Errorf("not a valid IP prefix: %s", prefix)
	}
}

// ToPrefix converts the IPPrefix back to a netip.Prefix value.
//
// Returns an error if the oneof is unset or the set branch is malformed.
// The returned prefix is masked, so host bits never leak out even if the
// message was constructed by hand.
func (m *IPPrefix) ToPrefix() (netip.Prefix, error) {
	switch prefix := m.GetPrefix().(type) {
	case *IPPrefix_V4:
		net, err := prefix.V4.ToContiguous()
		if err != nil {
			return netip.Prefix{}, err
		}
		return net.Prefix(), nil
	case *IPPrefix_V6:
		net, err := prefix.V6.ToContiguous()
		if err != nil {
			return netip.Prefix{}, err
		}
		return net.Prefix(), nil
	default:
		return netip.Prefix{}, fmt.Errorf("missing network prefix")
	}
}

// AsLogValue implements xgrpc.ProtoLogValue for compact gRPC logging.
func (m *IPPrefix) AsLogValue() any {
	prefix, err := m.ToPrefix()
	if err != nil {
		return "invalid"
	}

	return prefix.String()
}

// MarshalJSON serializes the prefix as a bare CIDR string, identically to
// the family-typed prefix messages.
func (m *IPPrefix) MarshalJSON() ([]byte, error) {
	prefix, err := m.ToPrefix()
	if err != nil {
		return nil, err
	}
	return json.Marshal(prefix.String())
}

// UnmarshalJSON accepts a bare CIDR string, inferring the family from the
// text.
func (m *IPPrefix) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == "" {
		return fmt.Errorf("failed to parse IP network: %w", errEmptyNetwork)
	}

	prefix, err := netip.ParsePrefix(raw)
	if err != nil {
		return fmt.Errorf("failed to parse IP network: %w", err)
	}
	parsed, err := NewIPPrefixFromPrefix(prefix)
	if err != nil {
		return err
	}

	m.Prefix = parsed.Prefix
	return nil
}
