package gateway

import (
	"context"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/ynpb"
)

type InspectService struct {
	ynpb.UnimplementedInspectServiceServer

	shm *ffi.SharedMemory
}

func NewInspectService(shm *ffi.SharedMemory) *InspectService {
	return &InspectService{
		shm: shm,
	}
}

func (m *InspectService) Inspect(
	ctx context.Context,
	request *ynpb.InspectRequest,
) (*ynpb.InspectResponse, error) {
	instanceIndices := m.shm.InstanceIndices()

	response := &ynpb.InspectResponse{
		InstanceIndices: instanceIndices,
	}

	for _, instanceIdx := range instanceIndices {
		dpConfig := m.shm.DPConfig(instanceIdx)

		instanceInfo := &ynpb.InstanceInfo{
			NumaIdx:   m.numaIdx(dpConfig),
			DpModules: m.dpModules(dpConfig),
			CpConfigs: m.cpConfigs(dpConfig),
			Pipelines: m.pipelines(dpConfig),
			Agents:    m.agents(dpConfig),
			Devices:   m.devices(dpConfig),
		}

		response.InstanceInfo = append(response.InstanceInfo, instanceInfo)
	}

	return response, nil
}

func (m *InspectService) dpModules(dpConfig *ffi.DPConfig) []*ynpb.DPModuleInfo {
	modules := dpConfig.Modules()

	out := make([]*ynpb.DPModuleInfo, len(modules))
	for idx, module := range modules {
		out[idx] = &ynpb.DPModuleInfo{
			Name: module.Name(),
		}
	}

	return out
}

func (m *InspectService) cpConfigs(dpConfig *ffi.DPConfig) []*ynpb.CPConfigInfo {
	configs := dpConfig.CPConfigs()

	out := make([]*ynpb.CPConfigInfo, len(configs))
	for idx, config := range configs {
		out[idx] = &ynpb.CPConfigInfo{
			ModuleIdx:  config.ModuleIndex,
			Name:       config.ConfigName,
			Generation: config.Gen,
		}
	}

	return out
}

func (m *InspectService) pipelines(dpConfig *ffi.DPConfig) []*ynpb.PipelineInfo {
	pipelines := dpConfig.Pipelines()

	out := make([]*ynpb.PipelineInfo, len(pipelines))
	for idx, pipeline := range pipelines {
		pipelineInfo := &ynpb.PipelineInfo{
			Name:    pipeline.Name,
			Modules: make([]*ynpb.PipelineModuleInfo, len(pipeline.ModuleConfigs)),
		}

		for modIdx, configIdx := range pipeline.ModuleConfigs {
			pipelineInfo.Modules[modIdx] = &ynpb.PipelineModuleInfo{
				ConfigIndex: uint32(configIdx),
			}
		}

		out[idx] = pipelineInfo
	}

	return out
}

func (m *InspectService) agents(dpConfig *ffi.DPConfig) []*ynpb.AgentInfo {
	agents := dpConfig.Agents()

	out := make([]*ynpb.AgentInfo, len(agents))
	for idx, agent := range agents {
		agentInfo := &ynpb.AgentInfo{
			Name:      agent.Name,
			Instances: make([]*ynpb.AgentInstanceInfo, len(agent.Instances)),
		}

		for instanceIdx, instance := range agent.Instances {
			agentInfo.Instances[instanceIdx] = &ynpb.AgentInstanceInfo{
				Pid:         instance.PID,
				MemoryLimit: instance.MemoryLimit,
				Allocated:   instance.Allocated,
				Freed:       instance.Freed,
				Generation:  instance.Gen,
			}
		}

		out[idx] = agentInfo
	}

	return out
}

func (m *InspectService) devices(dpConfig *ffi.DPConfig) []*ynpb.DeviceInfo {
	devices := dpConfig.Devices()
	if len(devices) == 0 {
		return nil
	}

	out := make([]*ynpb.DeviceInfo, len(devices))
	for idx, device := range devices {
		deviceInfo := &ynpb.DeviceInfo{
			DeviceId:  uint32(device.DeviceID),
			Pipelines: make([]*ynpb.DevicePipelineInfo, len(device.Pipelines)),
		}

		for pipelineIdx, pipeline := range device.Pipelines {
			deviceInfo.Pipelines[pipelineIdx] = &ynpb.DevicePipelineInfo{
				PipelineIdx: pipeline.PipelineIndex,
				Weight:      pipeline.Weight,
			}
		}

		out[idx] = deviceInfo
	}

	return out
}

func (m *InspectService) numaIdx(dpConfig *ffi.DPConfig) uint32 {
	return dpConfig.NumaIdx()
}
