package gateway

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/common/commonpb"
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

// PipelineService is a gRPC service for managing pipelines.
type PipelineService struct {
	ynpb.UnimplementedPipelineServiceServer

	instanceID uint32
	shm        *ffi.SharedMemory
	log        *zap.SugaredLogger
}

// NewPipelineService creates a new PipelineService.
func NewPipelineService(instanceID uint32, shm *ffi.SharedMemory, log *zap.SugaredLogger) *PipelineService {
	return &PipelineService{
		instanceID: instanceID,
		shm:        shm,
		log:        log,
	}
}

func (m *PipelineService) List(
	ctx context.Context,
	request *ynpb.ListPipelinesRequest,
) (*ynpb.ListPipelinesResponse, error) {
	dpConfig := m.shm.DPConfig(m.instanceID)

	pipelines := dpConfig.Pipelines()

	response := &ynpb.ListPipelinesResponse{
		Ids: make([]*commonpb.PipelineId, len(pipelines)),
	}
	for idx, pipeline := range pipelines {
		response.Ids[idx] = &commonpb.PipelineId{
			Name: pipeline.Name,
		}
	}

	return response, nil
}

func (m *PipelineService) Get(
	ctx context.Context,
	request *ynpb.GetPipelineRequest,
) (*ynpb.GetPipelineResponse, error) {
	dpConfig := m.shm.DPConfig(m.instanceID)

	reqId := request.Id

	pipelines := dpConfig.Pipelines()
	for _, pipeline := range pipelines {
		if reqId.Name == pipeline.Name {
			respFunctions := make([]*commonpb.FunctionId, len(pipeline.Functions))
			for idx, function := range pipeline.Functions {
				respFunctions[idx] = &commonpb.FunctionId{
					Name: function,
				}
			}

			respPipeline := ynpb.Pipeline{
				Id: &commonpb.PipelineId{
					Name: pipeline.Name,
				},
				Functions: respFunctions,
			}

			return &ynpb.GetPipelineResponse{
				Pipeline: &respPipeline,
			}, nil
		}
	}

	return nil, fmt.Errorf("not found")

}

// Update updates or inserts a pipeline.
func (m *PipelineService) Update(
	ctx context.Context,
	request *ynpb.UpdatePipelineRequest,
) (*ynpb.UpdatePipelineResponse, error) {
	agent, err := m.shm.AgentAttach(agentName, m.instanceID, defaultAgentMemory)
	if err != nil {
		return nil, fmt.Errorf("failed to attach to agent %q: %w", agentName, err)
	}
	defer agent.Close()

	reqPipeline := request.Pipeline

	pipeline := ffi.PipelineConfig{
		Name:      reqPipeline.Id.Name,
		Functions: make([]string, len(reqPipeline.Functions)),
	}

	for idx, reqFunctionId := range reqPipeline.Functions {
		pipeline.Functions[idx] = reqFunctionId.Name
	}

	if err := agent.UpdatePipeline(pipeline); err != nil {
		return nil, fmt.Errorf("failed to update function: %w", err)
	}

	return &ynpb.UpdatePipelineResponse{}, nil
}

func (m *PipelineService) Delete(
	ctx context.Context,
	request *ynpb.DeletePipelineRequest,
) (*ynpb.DeletePipelineResponse, error) {
	pipelineName := request.GetId().GetName()

	agent, err := m.shm.AgentAttach(agentName, m.instanceID, defaultAgentMemory)
	if err != nil {
		return nil, fmt.Errorf("failed to attach to agent %q: %w", agentName, err)
	}
	defer agent.Close()

	if err := agent.DeletePipeline(pipelineName); err != nil {
		return nil, fmt.Errorf("failed to delete pipeline: %w", err)
	}

	return &ynpb.DeletePipelineResponse{}, nil
}
