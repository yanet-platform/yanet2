package trafgenpb

import (
	"fmt"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

func (m *UpdateDeviceRequest) Validate() error {
	if err := commonpb.ValidateDeviceName("name", m.GetName()); err != nil {
		return err
	}
	if err := m.GetDevice().Validate(); err != nil {
		return fmt.Errorf("device: %w", err)
	}
	return nil
}

func (m *ShowConfigRequest) Validate() error {
	return commonpb.ValidateDeviceName("name", m.GetName())
}

func (m *ShowPacketsRequest) Validate() error {
	return commonpb.ValidateDeviceName("name", m.GetName())
}

func (m *UploadPcapRequest) Validate() error {
	return commonpb.ValidateDeviceName("name", m.GetName())
}

func (m *SetRateRequest) Validate() error {
	return commonpb.ValidateDeviceName("name", m.GetName())
}
