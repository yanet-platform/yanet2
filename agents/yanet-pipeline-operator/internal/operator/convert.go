package operator

import (
	"fmt"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/controlplane/ynpb"
	"github.com/yanet-platform/yanet2/devices/plain/controlplane/plainpb"
	"github.com/yanet-platform/yanet2/devices/vlan/controlplane/vlanpb"
)

func pipelineToProto(p PipelineConfig) *ynpb.Pipeline {
	functions := make([]*commonpb.FunctionId, len(p.Functions))
	for idx, name := range p.Functions {
		functions[idx] = &commonpb.FunctionId{Name: name}
	}

	return &ynpb.Pipeline{
		Id:        &commonpb.PipelineId{Name: p.Name},
		Functions: functions,
	}
}

func plainDeviceToProto(d DeviceConfig) *plainpb.UpdateDevicePlainRequest {
	return &plainpb.UpdateDevicePlainRequest{
		Name: d.Name,
		Device: &commonpb.Device{
			Input:  devicePipelineToProto(d.Input),
			Output: devicePipelineToProto(d.Output),
		},
	}
}

func vlanDeviceToProto(d VLANDeviceConfig) *vlanpb.UpdateDeviceVlanRequest {
	return &vlanpb.UpdateDeviceVlanRequest{
		Name: d.Name,
		Vlan: d.VLAN,
		Device: &commonpb.Device{
			Input:  devicePipelineToProto(d.Input),
			Output: devicePipelineToProto(d.Output),
		},
	}
}

func devicePipelineToProto(refs []PipelineRefConfig) []*commonpb.DevicePipeline {
	out := make([]*commonpb.DevicePipeline, len(refs))
	for idx, r := range refs {
		out[idx] = &commonpb.DevicePipeline{
			Name:   r.Name,
			Weight: r.Weight,
		}
	}

	return out
}

// devicePipelineRefStrings renders pipeline refs as "name(weight=N)"
// strings for human-readable log output.
func devicePipelineRefStrings(refs []PipelineRefConfig) []string {
	out := make([]string, len(refs))
	for idx, r := range refs {
		out[idx] = fmt.Sprintf("%s(weight=%d)", r.Name, r.Weight)
	}

	return out
}
