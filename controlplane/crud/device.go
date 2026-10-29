package crud

import (
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// DeviceProto renders a live device registry entry as the device
// message a read serves back.
func DeviceProto(info ffi.DeviceInfo) *commonpb.Device {
	device := &commonpb.Device{
		Input:  make([]*commonpb.DevicePipeline, len(info.InputPipelines)),
		Output: make([]*commonpb.DevicePipeline, len(info.OutputPipelines)),
	}
	for idx, pipeline := range info.InputPipelines {
		device.Input[idx] = &commonpb.DevicePipeline{Name: pipeline.Name, Weight: pipeline.Weight}
	}
	for idx, pipeline := range info.OutputPipelines {
		device.Output[idx] = &commonpb.DevicePipeline{Name: pipeline.Name, Weight: pipeline.Weight}
	}
	return device
}
