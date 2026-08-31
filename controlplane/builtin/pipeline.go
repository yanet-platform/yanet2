package builtin

import (
	"context"

	"github.com/c2h5oh/datasize"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/internal/agenterr"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
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

// PipelineOption configures the Pipeline constructor.
type PipelineOption func(*pipelineOptions)

type pipelineOptions struct {
	Log *zap.Logger
}

func newPipelineOptions() *pipelineOptions {
	return &pipelineOptions{
		Log: zap.NewNop(),
	}
}

// WithPipelineLog sets the logger for the pipeline service.
func WithPipelineLog(log *zap.Logger) PipelineOption {
	return func(o *pipelineOptions) {
		o.Log = log
	}
}

// NewPipeline creates a new Pipeline service.
func NewPipeline(instanceID uint32, shm *ffi.SharedMemory, options ...PipelineOption) *Pipeline {
	opts := newPipelineOptions()
	for _, o := range options {
		o(opts)
	}

	return &Pipeline{
		instanceID: instanceID,
		shm:        shm,
		log:        opts.Log,
	}
}

// Name returns the service name.
func (m *Pipeline) Name() string { return "pipeline" }

// Endpoint returns empty string indicating in-process service.
func (m *Pipeline) Endpoint() string { return "" }

// ServicesNames returns the gRPC service names served by this service.
func (m *Pipeline) ServicesNames() []string { return []string{"controlplane.ynpb.v1.PipelineService"} }

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
	reqId := request.GetId()
	if reqId == nil {
		return nil, status.Error(codes.InvalidArgument, "pipeline id is required")
	}

	dpConfig := m.shm.DPConfig(m.instanceID)

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

	return nil, status.Error(codes.NotFound, "not found")
}

// Update updates or inserts a pipeline.
func (m *Pipeline) Update(
	ctx context.Context,
	request *ynpb.UpdatePipelineRequest,
) (*ynpb.UpdatePipelineResponse, error) {
	reqPipeline := request.GetPipeline()
	if reqPipeline == nil {
		return nil, status.Error(codes.InvalidArgument, "pipeline is required")
	}

	reqPipelineId := reqPipeline.GetId()
	if reqPipelineId == nil {
		return nil, status.Error(codes.InvalidArgument, "pipeline id is required")
	}
	if reqPipelineId.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "pipeline name is required")
	}

	pipeline := ffi.PipelineConfig{
		Name:      reqPipelineId.Name,
		Functions: make([]string, len(reqPipeline.Functions)),
	}

	for idx, reqFunctionId := range reqPipeline.Functions {
		if reqFunctionId == nil {
			return nil, status.Error(codes.InvalidArgument, "function id is required")
		}
		pipeline.Functions[idx] = reqFunctionId.Name
	}

	agent, err := m.shm.AgentAttach(agentName, m.instanceID, defaultAgentMemory)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	defer agent.Close()

	if err := agent.UpdatePipeline(pipeline); err != nil {
		return nil, agenterr.ClassifyUpdate(err)
	}

	return &ynpb.UpdatePipelineResponse{}, nil
}

// Delete deletes the pipeline with the given ID.
func (m *Pipeline) Delete(
	ctx context.Context,
	request *ynpb.DeletePipelineRequest,
) (*ynpb.DeletePipelineResponse, error) {
	reqId := request.GetId()
	if reqId == nil {
		return nil, status.Error(codes.InvalidArgument, "pipeline id is required")
	}
	if reqId.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "pipeline name is required")
	}
	pipelineName := reqId.Name

	agent, err := m.shm.AgentAttach(agentName, m.instanceID, defaultAgentMemory)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	defer agent.Close()

	if err := agent.DeletePipeline(pipelineName); err != nil {
		return nil, agenterr.ClassifyDelete(err)
	}

	return &ynpb.DeletePipelineResponse{}, nil
}
