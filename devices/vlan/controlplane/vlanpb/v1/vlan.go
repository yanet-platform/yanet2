package vlanpb

import (
	"errors"
	"fmt"
	"strings"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// maxVlanID leaves the reserved VID 4095 outside the accepted VLAN range.
const maxVlanID = 4094

func (m *UpdateDeviceVlanRequest) Validate() error {
	if err := validateDeviceName(m.GetName()); err != nil {
		return err
	}
	if err := m.GetDevice().Validate(); err != nil {
		return fmt.Errorf("device: %w", err)
	}
	if vlan := m.GetVlan(); vlan > maxVlanID {
		return fmt.Errorf("vlan %d must be in range 0..%d", vlan, maxVlanID)
	}
	return nil
}

func (m *ShowDeviceVlanRequest) Validate() error {
	return validateDeviceName(m.GetName())
}

func validateDeviceName(name string) error {
	if name == "" {
		return errors.New("name is required")
	}
	if strings.IndexByte(name, 0) != -1 {
		return errors.New("name must not contain NUL")
	}
	if len(name) >= commonpb.MaxDeviceNameLen {
		return fmt.Errorf("name must be shorter than %d bytes", commonpb.MaxDeviceNameLen)
	}
	return nil
}
