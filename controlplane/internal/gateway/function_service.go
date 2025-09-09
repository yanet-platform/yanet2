package gateway

import (
	"context"
	"fmt"

	"go.uber.org/zap"

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

// TODO: docs.
type FunctionService struct {
	ynpb.UnimplementedFunctionServiceServer

	shm *ffi.SharedMemory
	log *zap.SugaredLogger
}

// TODO: docs.
func NewFunctionService(shm *ffi.SharedMemory, log *zap.SugaredLogger) *FunctionService {
	return &FunctionService{
		shm: shm,
		log: log,
	}
}

// TODO: docs.
func (m *FunctionService) Update(
	ctx context.Context,
	request *ynpb.UpdateFunctionsRequest,
) (*ynpb.UpdateFunctionsResponse, error) {
	instance := request.GetInstance()
	functions := request.GetFunctions()

	agent, err := m.shm.AgentAttach(functionAgentName, instance, functionAgentMemory)
	if err != nil {
		return nil, fmt.Errorf("failed to attach to agent %q: %w", functionAgentName, err)
	}
	defer agent.Close()

	configs := make([]ffi.FunctionConfig, 0, len(functions))

	for _, functionConfig := range functions {
		cfg := ffi.FunctionConfig{
			Name: functionConfig.GetName(),
		}
		for _, funcChain := range functionConfig.GetChains() {
			chain := funcChain.GetChain()
			chainModules := make([]ffi.ChainModuleConfig, 0, len(chain.GetModules()))
			for _, chainModule := range chain.GetModules() {
				chainModules = append(chainModules, ffi.ChainModuleConfig{
					Type: chainModule.GetType(),
					Name: chainModule.GetName(),
				})
			}
			chainCfg := ffi.ChainConfig{
				Name:    chain.GetName(),
				Modules: chainModules,
			}

			funcChainCfg := ffi.FunctionChainConfig{
				Weight: funcChain.GetWeight(),
				Chain:  chainCfg,
			}
			cfg.Chains = append(cfg.Chains, funcChainCfg)
		}

		configs = append(configs, cfg)
	}

	m.log.Infow("updating functions",
		zap.Uint32("instance", instance),
		zap.Any("configs", configs),
	)

	if err := agent.UpdateFunctions(configs); err != nil {
		return nil, fmt.Errorf("failed to update functions: %w", err)
	}

	m.log.Infow("updated functions",
		zap.Uint32("instance", instance),
		zap.Any("configs", configs),
	)

	return &ynpb.UpdateFunctionsResponse{}, nil
}

func (m *FunctionService) Delete(
	ctx context.Context,
	request *ynpb.DeleteFunctionRequest,
) (*ynpb.DeleteFunctionResponse, error) {
	instance := request.GetInstance()
	function_name := request.GetFunctionName()

	agent, err := m.shm.AgentAttach(functionAgentName, instance, functionAgentMemory)
	if err != nil {
		return nil, fmt.Errorf("failed to attach to agent %q: %w", functionAgentName, err)
	}
	defer agent.Close()

	if err := agent.DeleteFunction(function_name); err != nil {
		return nil, fmt.Errorf("failed to delete function: %w", err)
	}

	return &ynpb.DeleteFunctionResponse{}, nil
}
