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
	chains := request.GetChains()

	availableModuleNames := map[string]struct{}{}
	for _, mod := range m.shm.DPConfig(instance).Modules() {
		availableModuleNames[mod.Name()] = struct{}{}
	}

	// TODO: ensure requested module is in available.

	agent, err := m.shm.AgentAttach(agentName, instance, defaultAgentMemory)
	if err != nil {
		return nil, fmt.Errorf("failed to attach to agent %q: %w", agentName, err)
	}
	defer agent.Close()

	configs := make([]ffi.PipelineConfig, 0, len(chains))

	for _, pipelineConfig := range chains {
		cfg := ffi.PipelineConfig{
			Name: pipelineConfig.GetName(),
		}
		for _, node := range pipelineConfig.GetNodes() {
			moduleName := node.GetModuleName()
			configName := node.GetConfigName()

			cfg.Chain = append(cfg.Chain, ffi.PipelineModuleConfig{
				ModuleName: moduleName,
				ConfigName: configName,
			})
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

// Assign assigns pipelines to devices.
func (m *PipelineService) Assign(
	ctx context.Context,
	request *ynpb.AssignPipelinesRequest,
) (*ynpb.AssignPipelinesResponse, error) {
	instance := request.GetInstance()
	devices := request.GetDevices()

	agent, err := m.shm.AgentAttach(agentName, instance, defaultAgentMemory)
	if err != nil {
		return nil, fmt.Errorf("failed to attach to agent %q: %w", agentName, err)
	}
	defer agent.Close()

	devicePipelines := make(map[string][]ffi.DevicePipeline)
	for deviceID, pipelines := range devices {
		devicePipelinesList := make([]ffi.DevicePipeline, 0, len(pipelines.GetPipelines()))

		for _, pipeline := range pipelines.GetPipelines() {
			devicePipelinesList = append(devicePipelinesList, ffi.DevicePipeline{
				Name:   pipeline.GetPipelineName(),
				Weight: pipeline.GetPipelineWeight(),
			})
		}

		devicePipelines[deviceID] = devicePipelinesList
	}

	if err := agent.UpdateDevices(devicePipelines); err != nil {
		return nil, fmt.Errorf("failed to assign pipelines to devices: %w", err)
	}

	m.log.Infow("assigned pipelines to devices",
		zap.Uint32("instance", instance),
		zap.Any("devices", devices),
	)

	return &ynpb.AssignPipelinesResponse{}, nil
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
