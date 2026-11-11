package vlanpb

import (
	"fmt"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// maxVlanID leaves the reserved VID 4095 outside the accepted VLAN range.
const maxVlanID = 4094

func (m *UpdateDeviceVlanRequest) Validate() error {
	if err := commonpb.ValidateDeviceName("name", m.GetName()); err != nil {
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
	return commonpb.ValidateDeviceName("name", m.GetName())
}
