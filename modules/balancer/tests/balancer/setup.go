package balancer

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	mock "github.com/yanet-platform/yanet2/mock/go"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancer"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"go.uber.org/zap"
)

////////////////////////////////////////////////////////////////////////////////

var defaultDeviceName string = "01:00.0"
var defaultPipelineName string = "pipeline0"
var defaultFunctionName string = "function0"
var defaultChainName string = "chain0"
var defaultConfigName string = "balancer0"

////////////////////////////////////////////////////////////////////////////////

type TestConfig struct {
	mock        *mock.YanetMockConfig
	balancer    *balancerpb.ModuleConfig
	stateConfig *balancerpb.ModuleStateConfig
}

type TestSetup struct {
	mock     *mock.YanetMock
	agent    *ffi.Agent
	balancer *balancer.Balancer
}

func SetupTest(config *TestConfig) (*TestSetup, error) {
	if config.mock == nil {
		config.mock = &mock.YanetMockConfig{
			CpMemory: 1 << 29,
			DpMemory: 1 << 27,
			Workers:  1,
			Devices: []mock.YanetMockDeviceConfig{
				{
					Id:   0,
					Name: defaultDeviceName,
				},
			},
		}
	}
	if config.mock.CpMemory < (1 << 27) {
		return nil, fmt.Errorf("need at least 128MB for the controlplane")
	}

	if config.balancer == nil {
		config.balancer = &balancerpb.ModuleConfig{}
	}

	if config.stateConfig == nil {
		config.stateConfig = &balancerpb.ModuleStateConfig{
			SessionTableCapacity:      128,
			SessionTableMaxLoadFactor: 0.75,
		}
	}

	// create mock

	mockInstance, err := mock.NewYanetMock(config.mock)
	if err != nil {
		return nil, fmt.Errorf("failed to create new yanet mock: %w", err)
	}

	agent, err := mockInstance.SharedMemory().
		AgentAttach("balancer", 0, uint(config.mock.CpMemory)-(1<<26))
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent: %w", err)
	}

	// Create logger for balancer
	logger, _ := zap.NewDevelopment()
	sugaredLogger := logger.Sugar()

	balancerInstance, err := balancer.NewBalancerFromProto(
		*agent,
		defaultConfigName,
		config.balancer,
		config.stateConfig,
		sugaredLogger,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to create new balancer module instance: %w",
			err,
		)
	}

	if err := setupCp(agent); err != nil {
		return nil, fmt.Errorf("failed to setup yanet mock: %w", err)
	}

	return &TestSetup{
		mock:     mockInstance,
		agent:    agent,
		balancer: balancerInstance,
	}, nil
}

func setupCp(agent *ffi.Agent) error {
	{
		functionConfig := ffi.FunctionConfig{
			Name: defaultFunctionName,
			Chains: []ffi.FunctionChainConfig{
				{
					Weight: 1,
					Chain: ffi.ChainConfig{
						Name: defaultChainName,
						Modules: []ffi.ChainModuleConfig{
							{
								Type: "balancer",
								Name: defaultConfigName,
							},
						},
					},
				},
			},
		}

		if err := agent.UpdateFunction(functionConfig); err != nil {
			return fmt.Errorf("failed to update function: %w", err)
		}
	}

	// update pipelines
	{
		inputPipelineConfig := ffi.PipelineConfig{
			Name:      defaultPipelineName,
			Functions: []string{defaultFunctionName},
		}

		dummyPipelineConfig := ffi.PipelineConfig{
			Name:      "dummy",
			Functions: []string{},
		}

		if err := agent.UpdatePipeline(inputPipelineConfig); err != nil {
			return fmt.Errorf("failed to update pipeline: %w", err)
		}

		if err := agent.UpdatePipeline(dummyPipelineConfig); err != nil {
			return fmt.Errorf("failed to update pipeline: %w", err)
		}
	}

	// update devices
	{
		deviceConfig := ffi.DeviceConfig{
			Name: defaultDeviceName,
			Input: []ffi.DevicePipelineConfig{
				{
					Name:   defaultPipelineName,
					Weight: 1,
				},
			},
			Output: []ffi.DevicePipelineConfig{
				{
					Name:   "dummy",
					Weight: 1,
				},
			},
		}

		if err := agent.UpdatePlainDevices([]ffi.DeviceConfig{deviceConfig}); err != nil {
			return fmt.Errorf("failed to update pipelines: %w", err)
		}
	}

	return nil
}

func (ctx *TestSetup) Free() {
	ctx.balancer.Free()
	ctx.agent.Close()
	ctx.mock.Free()
}
