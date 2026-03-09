package device

import (
	"github.com/yanet-platform/yanet2/common/filterpb"
)

type Device struct {
	Name string
}

type Devices []Device

func FromDevices(devices []*filterpb.Device) ([]Device, error) {
	result := make([]Device, len(devices))

	for idx := range devices {
		result[idx] = Device{
			Name: devices[idx].Name,
		}
	}

	return result, nil
}
