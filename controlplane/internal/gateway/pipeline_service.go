package gateway

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/ynpb"
)

const agentName = "pipeline"

// Pipeline agent is not persistent: it is created
// on every call of update/assign/delete
// Memory, allocated for pipeline agent, will be free after
// corresponding call is done. So, on every call we need to allocate
// memory for temporary operations only. For now, 1MB is
// sufficient.
const defaultAgentMemory = uint(1 << 20)

// TODO: docs.
type PipelineService struct {
	ynpb.UnimplementedPipelineServiceServer

	shm *ffi.SharedMemory
	log *zap.SugaredLogger
}

// TODO: docs.
func NewPipelineService(shm *ffi.SharedMemory, log *zap.SugaredLogger) *PipelineService {
	return &PipelineService{
		shm: shm,
		log: log,
	}
}

// TODO: docs.
func (m *PipelineService) Update(
	ctx context.Context,
	request *ynpb.UpdatePipelinesRequest,
) (*ynpb.UpdatePipelinesResponse, error) {
	instance := request.GetInstance()
	pipelines := request.GetPipelines()

	agent, err := m.shm.AgentAttach(agentName, instance, defaultAgentMemory)
	if err != nil {
		return nil, fmt.Errorf("failed to attach to agent %q: %w", agentName, err)
	}
	defer agent.Close()

	configs := make([]ffi.PipelineConfig, 0, len(pipelines))

	for _, pipelineConfig := range pipelines {
		cfg := ffi.PipelineConfig{
			Name: pipelineConfig.GetName(),
		}
		for _, functionName := range pipelineConfig.GetFunctions() {
			cfg.Functions = append(cfg.Functions, functionName)
		}

		configs = append(configs, cfg)
	}

	m.log.Infow("updating pipelines",
		zap.Uint32("instance", instance),
		zap.Any("configs", configs),
	)

	if err := agent.UpdatePipelines(configs); err != nil {
		return nil, fmt.Errorf("failed to update pipelines: %w", err)
	}

	m.log.Infow("updated pipelines",
		zap.Uint32("instance", instance),
		zap.Any("configs", configs),
	)

	return &ynpb.UpdatePipelinesResponse{}, nil
}

func (m *PipelineService) Delete(
	ctx context.Context,
	request *ynpb.DeletePipelineRequest,
) (*ynpb.DeletePipelineResponse, error) {
	instance := request.GetInstance()
	pipeline_name := request.GetPipelineName()

	agent, err := m.shm.AgentAttach(agentName, instance, defaultAgentMemory)
	if err != nil {
		return nil, fmt.Errorf("failed to attach to agent %q: %w", agentName, err)
	}
	defer agent.Close()

	if err := agent.DeletePipeline(pipeline_name); err != nil {
		return nil, fmt.Errorf("failed to delete pipeline: %w", err)
	}

	return &ynpb.DeletePipelineResponse{}, nil
}

// TODO: docs.
func NewDeviceService(shm *ffi.SharedMemory, log *zap.SugaredLogger) *DeviceService {
	return &DeviceService{
		shm: shm,
		log: log,
	}
}

// TODO: docs
type DeviceService struct {
	ynpb.UnimplementedDeviceServiceServer

	shm *ffi.SharedMemory
	log *zap.SugaredLogger
}

func (m *DeviceService) Update(
	ctx context.Context,
	request *ynpb.UpdateDevicesRequest,
) (*ynpb.UpdateDevicesResponse, error) {
	instance := request.GetInstance()
	devices := request.GetDevices()

	agent, err := m.shm.AgentAttach(agentName, instance, defaultAgentMemory)
	if err != nil {
		return nil, fmt.Errorf("failed to attach to agent %q: %w", agentName, err)
	}
	defer agent.Close()

	configs := make([]ffi.DeviceConfig, 0, len(devices))
	for _, deviceConfig := range devices {
		cfg := ffi.DeviceConfig{
			Name:   deviceConfig.GetName(),
			Input:  make([]ffi.DevicePipelineConfig, 0, len(deviceConfig.GetInput())),
			Output: make([]ffi.DevicePipelineConfig, 0, len(deviceConfig.GetOutput())),
		}

		for _, pipeline := range deviceConfig.GetInput() {
			cfg.Input = append(cfg.Input, ffi.DevicePipelineConfig{
				Name:   pipeline.GetName(),
				Weight: pipeline.GetWeight(),
			})
		}

		for _, pipeline := range deviceConfig.GetOutput() {
			cfg.Output = append(cfg.Output, ffi.DevicePipelineConfig{
				Name:   pipeline.GetName(),
				Weight: pipeline.GetWeight(),
			})
		}
		configs = append(configs, cfg)
	}

	if err := agent.UpdateDevices(configs); err != nil {
		return nil, fmt.Errorf("failed to assign pipelines to devices: %w", err)
	}

	m.log.Infow("assigned pipelines to devices",

		zap.Uint32("instance", instance),
		zap.Any("devices", devices),
	)

	return &ynpb.UpdateDevicesResponse{}, nil
}
