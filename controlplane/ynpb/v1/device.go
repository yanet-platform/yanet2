package ynpb

import commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"

func (m *DeleteDeviceRequest) Validate() error {
	return commonpb.ValidateDeviceName("name", m.GetName())
}
