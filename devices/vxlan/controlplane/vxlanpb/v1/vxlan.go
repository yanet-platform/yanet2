package vxlanpb

import (
	"fmt"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// maxVni is the highest VXLAN network identifier the 24-bit VNI field of
// the tunnel header can carry.
const maxVni = 0xFFFFFF

func (m *UpdateDeviceVxlanRequest) Validate() error {
	if err := commonpb.ValidateDeviceName("name", m.GetName()); err != nil {
		return err
	}
	if err := m.GetDevice().Validate(); err != nil {
		return fmt.Errorf("device: %w", err)
	}
	if vni := m.GetVni(); vni > maxVni {
		return fmt.Errorf("vni %d must be in range 0..%d", vni, maxVni)
	}
	if port := m.GetDstPort(); port == 0 || port > 0xFFFF {
		return fmt.Errorf("dst_port %d must be in range 1..65535", port)
	}
	return nil
}

func (m *GetDeviceVxlanRequest) Validate() error {
	return commonpb.ValidateDeviceName("name", m.GetName())
}
