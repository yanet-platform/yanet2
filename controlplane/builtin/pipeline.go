package builtin

import (
	"context"
	"fmt"

	"github.com/c2h5oh/datasize"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/ynpb"
)

const agentName = "pipeline"

// Pipeline agent is not persistent: it is created
// on every call of update/assign/delete.
// Memory, allocated for pipeline agent, will be free after
// corresponding call is done. So, on every call we need to allocate
// memory for temporary operations only. For now, 1MB is
// sufficient.
const defaultAgentMemory = datasize.MB

// Pipeline is an in-process gRPC service for managing pipelines.
type Pipeline struct {
	ynpb.UnimplementedPipelineServiceServer

	instanceID uint32
	shm        *ffi.SharedMemory
	log        *zap.Logger
}

// NewPipeline creates a new Pipeline service.
func NewPipeline(instanceID uint32, shm *ffi.SharedMemory, log *zap.Logger) *Pipeline {
	return &Pipeline{
		instanceID: instanceID,
		shm:        shm,
		log:        log,
	}
}

// Name returns the service name.
func (m *Pipeline) Name() string { return "pipeline" }

// Endpoint returns empty string indicating in-process service.
func (m *Pipeline) Endpoint() string { return "" }

// ServicesNames returns the gRPC service names served by this service.
func (m *Pipeline) ServicesNames() []string { return []string{"ynpb.PipelineService"} }

// RegisterService registers the service on the given gRPC server.
func (m *Pipeline) RegisterService(server *grpc.Server) {
	ynpb.RegisterPipelineServiceServer(server, m)
}

// List returns all pipeline IDs.
func (m *Pipeline) List(
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

// Get returns the pipeline with the given ID.
func (m *Pipeline) Get(
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
func (m *Pipeline) Update(
	ctx context.Context,
	request *ynpb.UpdatePipelineRequest,
) (*ynpb.UpdatePipelineResponse, error) {
	agent, err := m.shm.AgentReattach(agentName, m.instanceID, defaultAgentMemory)
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

// Delete deletes the pipeline with the given ID.
func (m *Pipeline) Delete(
	ctx context.Context,
	request *ynpb.DeletePipelineRequest,
) (*ynpb.DeletePipelineResponse, error) {
	pipelineName := request.GetId().GetName()

	agent, err := m.shm.AgentReattach(agentName, m.instanceID, defaultAgentMemory)
	if err != nil {
		return nil, fmt.Errorf("failed to attach to agent %q: %w", agentName, err)
	}
	defer agent.Close()

	if err := agent.DeletePipeline(pipelineName); err != nil {
		return nil, fmt.Errorf("failed to delete pipeline: %w", err)
	}

	return &ynpb.DeletePipelineResponse{}, nil
}
