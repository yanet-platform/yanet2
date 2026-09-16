package nat64pb

import (
	"errors"
	"fmt"
)

// MaxMTU is the largest MTU accepted by the NAT64 dataplane.
const MaxMTU = 1<<16 - 1

func (m *ShowConfigRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}

	return nil
}

func (m *AddPrefixRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}
	if m.GetPrefix() == nil {
		return errors.New("prefix is required")
	}

	return nil
}

func (m *RemovePrefixRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}
	if m.GetPrefix() == nil {
		return errors.New("prefix is required")
	}

	return nil
}

func (m *AddMappingRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}
	if m.GetIpv4() == nil {
		return errors.New("ipv4 is required")
	}
	if m.GetIpv6() == nil {
		return errors.New("ipv6 is required")
	}

	return nil
}

func (m *RemoveMappingRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}
	if m.GetIpv4() == nil {
		return errors.New("ipv4 is required")
	}

	return nil
}

func (m *SetMTURequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}
	if m.GetMtu() == nil {
		return errors.New("mtu is required")
	}
	if err := m.GetMtu().Validate(); err != nil {
		return fmt.Errorf("mtu: %w", err)
	}

	return nil
}

func (m *MTUConfig) Validate() error {
	if m.GetIpv4Mtu() > MaxMTU {
		return fmt.Errorf(
			"ipv4_mtu %d must be in range 0..%d",
			m.GetIpv4Mtu(),
			MaxMTU,
		)
	}
	if m.GetIpv6Mtu() > MaxMTU {
		return fmt.Errorf(
			"ipv6_mtu %d must be in range 0..%d",
			m.GetIpv6Mtu(),
			MaxMTU,
		)
	}

	return nil
}

func (m *SetDropUnknownRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}

	return nil
}

func (m *DeleteConfigRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}

	return nil
}
