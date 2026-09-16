package dscppb

import (
	"errors"
	"fmt"
)

func (m *ShowConfigRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}

	return nil
}

func (m *AddPrefixesRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}

	return nil
}

func (m *RemovePrefixesRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}

	return nil
}

// Validate checks that the request names the config to delete.
func (m *DeleteConfigRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}

	return nil
}

func (m *SetDscpMarkingRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}

	if m.GetDscpConfig() == nil {
		return errors.New("dscp_config is required")
	}
	if err := m.GetDscpConfig().Validate(); err != nil {
		return fmt.Errorf("dscp_config: %w", err)
	}

	return nil
}

// Validate checks that the flag and mark values are within their valid
// ranges.
func (m *DscpConfig) Validate() error {
	if m.GetFlag() > 2 {
		return fmt.Errorf("flag %d must be in range 0..2", m.GetFlag())
	}
	if m.GetMark() > 63 {
		return fmt.Errorf("mark %d must be in range 0..63", m.GetMark())
	}

	return nil
}
