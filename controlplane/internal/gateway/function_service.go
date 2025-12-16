package gateway

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/ynpb"
)

const functionAgentName = "function"

// Function agent is not persistent: it is created
// on every call of update/assign/delete
// Memory, allocated for function agent, will be free after
// corresponding call is done. So, on every call we need to allocate
// memory for temporary operations only. For now, 1MB is
// sufficient.
const functionAgentMemory = uint(1 << 20)

// FunctionService is a gRPC service for managing functions.
type FunctionService struct {
	ynpb.UnimplementedFunctionServiceServer

	instanceID uint32
	shm        *ffi.SharedMemory
	log        *zap.SugaredLogger
}

// NewFunctionService creates a new FunctionService.
func NewFunctionService(instanceID uint32, shm *ffi.SharedMemory, log *zap.SugaredLogger) *FunctionService {
	return &FunctionService{
		instanceID: instanceID,
		shm:        shm,
		log:        log,
	}
}

func (m *FunctionService) List(
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

func (m *FunctionService) Get(
	ctx context.Context,
	request *ynpb.GetFunctionRequest,
) (*ynpb.GetFunctionResponse, error) {
	dpConfig := m.shm.DPConfig(m.instanceID)

	reqId := request.Id

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

	return nil, fmt.Errorf("not found")

}

// Update updates or inserts a function.
func (m *FunctionService) Update(
	ctx context.Context,
	request *ynpb.UpdateFunctionRequest,
) (*ynpb.UpdateFunctionResponse, error) {
	reqFunction := request.Function

	agent, err := m.shm.AgentAttach(functionAgentName, m.instanceID, functionAgentMemory)
	if err != nil {
		return nil, fmt.Errorf("failed to attach to agent %q: %w", functionAgentName, err)
	}
	defer agent.Close()

	function := ffi.FunctionConfig{
		Name: reqFunction.Id.Name,
	}
	for _, reqFunctionChain := range reqFunction.Chains {
		reqChain := reqFunctionChain.Chain
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

	if err := agent.UpdateFunction(function); err != nil {
		return nil, fmt.Errorf("failed to update function: %w", err)
	}

	return &ynpb.UpdateFunctionResponse{}, nil
}

func (m *FunctionService) Delete(
	ctx context.Context,
	request *ynpb.DeleteFunctionRequest,
) (*ynpb.DeleteFunctionResponse, error) {
	functionName := request.Id.Name

	agent, err := m.shm.AgentAttach(functionAgentName, m.instanceID, functionAgentMemory)
	if err != nil {
		return nil, fmt.Errorf("failed to attach to agent %q: %w", functionAgentName, err)
	}
	defer agent.Close()

	if err := agent.DeleteFunction(functionName); err != nil {
		return nil, fmt.Errorf("failed to delete function: %w", err)
	}

	return &ynpb.DeleteFunctionResponse{}, nil
}
