package builtin

import (
	"context"
	"errors"
	"sync"

	"github.com/c2h5oh/datasize"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

const functionAgentName = "function"

// A token reservation: attaching refuses a zero size, and an arena that can
// hand back no block at all is reported as fully occupied.
//
// An update or a delete clones the touched generation from the global
// configuration pool, so no call ever allocates from the agent's own arena.
// The arena outlives its call — detaching releases nothing — and is
// reclaimed only when the next attach under the same name supersedes it.
const functionAgentMemory = 4 * datasize.KB

// Function is an in-process gRPC service for managing functions.
type Function struct {
	ynpb.UnimplementedFunctionServiceServer

	// One attach-through-mutation lifetime at a time under the fixed agent
	// name, because a concurrent attach reclaims the agent still in use.
	mu sync.Mutex

	instanceID uint32
	shm        *ffi.SharedMemory
	log        *zap.Logger
}

// FunctionOption configures the Function constructor.
type FunctionOption func(*functionOptions)

type functionOptions struct {
	Log *zap.Logger
}

func newFunctionOptions() *functionOptions {
	return &functionOptions{
		Log: zap.NewNop(),
	}
}

// WithFunctionLog sets the logger for the function service.
func WithFunctionLog(log *zap.Logger) FunctionOption {
	return func(o *functionOptions) {
		o.Log = log
	}
}

// NewFunction creates a new Function service.
func NewFunction(instanceID uint32, shm *ffi.SharedMemory, options ...FunctionOption) *Function {
	opts := newFunctionOptions()
	for _, o := range options {
		o(opts)
	}

	return &Function{
		instanceID: instanceID,
		shm:        shm,
		log:        opts.Log,
	}
}

// Name returns the service name.
func (m *Function) Name() string { return "function" }

// Endpoint returns empty string indicating in-process service.
func (m *Function) Endpoint() string { return "" }

// ServicesNames returns the gRPC service names served by this service.
func (m *Function) ServicesNames() []string { return []string{"controlplane.ynpb.v1.FunctionService"} }

// RegisterService registers the service on the given gRPC server.
func (m *Function) RegisterService(server *grpc.Server) {
	ynpb.RegisterFunctionServiceServer(server, m)
}

// List returns all function IDs.
func (m *Function) List(
	ctx context.Context,
	request *ynpb.ListFunctionsRequest,
) (*ynpb.ListFunctionsResponse, error) {
	dpConfig := m.shm.DPConfig(m.instanceID)

	functions := dpConfig.Functions()

	response := &ynpb.ListFunctionsResponse{
		Ids: make([]*commonpb.FunctionId, len(functions)),
	}
	for idx, function := range functions {
		response.Ids[idx] = &commonpb.FunctionId{
			Name: function.Name,
		}
	}

	return response, nil
}

// Get returns the function with the given ID.
func (m *Function) Get(
	ctx context.Context,
	request *ynpb.GetFunctionRequest,
) (*ynpb.GetFunctionResponse, error) {
	reqId := request.GetId()
	if reqId == nil {
		return nil, status.Error(codes.InvalidArgument, "function id is required")
	}

	dpConfig := m.shm.DPConfig(m.instanceID)

	functions := dpConfig.Functions()
	for _, function := range functions {
		if reqId.Name == function.Name {
			respChains := make([]*ynpb.FunctionChain, len(function.Chains))
			for idx, chain := range function.Chains {
				respModules := make([]*commonpb.ModuleId, len(chain.Modules))
				for idx, module := range chain.Modules {
					respModules[idx] = &commonpb.ModuleId{
						Type: module.Type,
						Name: module.Name,
					}
				}

				respChain := &ynpb.Chain{
					Name:    chain.Name,
					Modules: respModules,
				}

				respChains[idx] = &ynpb.FunctionChain{
					Chain:  respChain,
					Weight: chain.Weight,
				}
			}

			respFunction := ynpb.Function{
				Id: &commonpb.FunctionId{
					Name: function.Name,
				},
				Chains: respChains,
			}

			return &ynpb.GetFunctionResponse{
				Function: &respFunction,
			}, nil
		}
	}

	return nil, status.Error(codes.NotFound, "not found")
}

// Update updates or inserts a function.
func (m *Function) Update(
	ctx context.Context,
	request *ynpb.UpdateFunctionRequest,
) (*ynpb.UpdateFunctionResponse, error) {
	reqFunction := request.GetFunction()
	if err := reqFunction.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	function := ffi.FunctionConfig{
		Name: reqFunction.Id.Name,
	}
	for _, reqFunctionChain := range reqFunction.Chains {
		reqChain := reqFunctionChain.GetChain()

		modules := make([]ffi.ChainModuleConfig, 0, len(reqChain.Modules))
		for _, reqChainModule := range reqChain.Modules {
			modules = append(modules, ffi.ChainModuleConfig{
				Type: reqChainModule.Type,
				Name: reqChainModule.Name,
			})
		}
		chain := ffi.ChainConfig{
			Name:    reqChain.Name,
			Modules: modules,
		}

		functionChain := ffi.FunctionChainConfig{
			Weight: reqFunctionChain.Weight,
			Chain:  chain,
		}
		function.Chains = append(function.Chains, functionChain)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	agent, err := m.shm.AgentAttach(functionAgentName, m.instanceID, functionAgentMemory)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	defer agent.Close()

	if err := agent.UpdateFunction(function); err != nil {
		if errors.Is(err, ffi.ErrFailedPrecondition) {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &ynpb.UpdateFunctionResponse{}, nil
}

// Delete deletes the function with the given name.
func (m *Function) Delete(
	ctx context.Context,
	request *ynpb.DeleteFunctionRequest,
) (*ynpb.DeleteFunctionResponse, error) {
	reqId := request.GetId()
	if reqId == nil {
		return nil, status.Error(codes.InvalidArgument, "function id is required")
	}
	if reqId.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "function name is required")
	}
	functionName := reqId.Name

	m.mu.Lock()
	defer m.mu.Unlock()

	agent, err := m.shm.AgentAttach(functionAgentName, m.instanceID, functionAgentMemory)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	defer agent.Close()

	if err := agent.DeleteFunction(functionName); err != nil {
		switch {
		case errors.Is(err, ffi.ErrNotFound):
			return nil, status.Error(codes.NotFound, err.Error())
		case errors.Is(err, ffi.ErrFailedPrecondition):
			// The live configuration still runs the function.
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &ynpb.DeleteFunctionResponse{}, nil
}
