package test

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	mock "github.com/yanet-platform/yanet2/mock/go"
	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
)

////////////////////////////////////////////////////////////////////////////////

var defaultDeviceName string = "01:00.0"
var defaultPipelineName string = "pipeline0"
var defaultFunctionName string = "function0"
var defaultChainName string = "chain0"
var defaultConfigName string = "acl0"

////////////////////////////////////////////////////////////////////////////////

type TestConfig struct {
	mock  *mock.YanetMockConfig
	rules []acl.AclRule
}

type TestSetup struct {
	mock   *mock.YanetMock
	agent  *ffi.Agent
	module *acl.ModuleConfig
}

func SetupTest(config *TestConfig) (*TestSetup, error) {
	if config.mock == nil {
		config.mock = &mock.YanetMockConfig{
			CpMemory: 1 << 28,
			DpMemory: 1 << 26,
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

	// create mock

	mock, err := mock.NewYanetMock(config.mock)
	if err != nil {
		return nil, fmt.Errorf("failed to create new yanet mock: %w", err)
	}

	agent, err := mock.SharedMemory().
		AgentAttach("acl", 0, (1<<26))
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent: %w", err)
	}

	module, err := acl.NewModuleConfig(agent, defaultConfigName)
	if err != nil {
		return nil, fmt.Errorf("failed to create new acl module: %w", err)
	}

	err = module.Update(config.rules)
	if err != nil {
		return nil, fmt.Errorf("failed to update acl module: %w", err)
	}

	if err := agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}); err != nil {
		return nil, fmt.Errorf("failed to update dp modules: %w", err)
	}

	if err := setupCp(agent); err != nil {
		return nil, fmt.Errorf("failed to setup yanet mock: %w", err)
	}

	return &TestSetup{
		mock:   mock,
		agent:  agent,
		module: module,
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
								Type: "acl",
								Name: defaultConfigName,
							},
						},
					},
				},
			},
		}

		if err := agent.UpdateFunction(functionConfig); err != nil {
			return fmt.Errorf("failed to update functions: %w", err)
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
			return fmt.Errorf("failed to update devices: %w", err)
		}
	}

	return nil
}

func (ctx *TestSetup) Free() {
	// ctx.module.Close()
	ctx.agent.Close()
	ctx.mock.Free()
}
