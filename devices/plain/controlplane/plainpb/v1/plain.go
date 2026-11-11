package plainpb

import (
	"fmt"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

func (m *UpdateDevicePlainRequest) Validate() error {
	if err := commonpb.ValidateDeviceName("name", m.GetName()); err != nil {
		return err
	}
	if err := m.GetDevice().Validate(); err != nil {
		return fmt.Errorf("device: %w", err)
	}
	return nil
}

func (m *ShowDevicePlainRequest) Validate() error {
	return commonpb.ValidateDeviceName("name", m.GetName())
}
