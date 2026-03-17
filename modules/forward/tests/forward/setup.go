package test

import (
	"fmt"

	"github.com/c2h5oh/datasize"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	mock "github.com/yanet-platform/yanet2/mock/go"
	forwardffi "github.com/yanet-platform/yanet2/modules/forward/internal/ffi"
)

var defaultDeviceName string = "01:00.0"
var defaultPipelineName string = "pipeline0"
var defaultFunctionName string = "function0"
var defaultChainName string = "chain0"
var defaultConfigName string = "forward0"

type TestConfig struct {
	mock  *mock.YanetMockConfig
	rules []forwardffi.ForwardRule
}

type TestSetup struct {
	mock   *mock.YanetMock
	agent  *ffi.Agent
	module *forwardffi.ModuleConfig
}

func SetupTest(config *TestConfig) (*TestSetup, error) {
	if config.mock == nil {
		config.mock = &mock.YanetMockConfig{
			AgentsMemory: datasize.MB * 64,
			Workers:      1,
			Devices: []mock.YanetMockDeviceConfig{
				{
					ID:   0,
					Name: defaultDeviceName,
				},
			},
		}
	}
	m, err := mock.NewYanetMock(config.mock)
	if err != nil {
		return nil, fmt.Errorf("failed to create new yanet mock: %w", err)
	}

	agent, err := m.SharedMemory().
		AgentReattach("forward", 0, config.mock.GetAgentsMemory())
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent: %w", err)
	}

	module, err := forwardffi.NewModuleConfig(agent, defaultConfigName)
	if err != nil {
		return nil, fmt.Errorf("failed to create new forward module: %w", err)
	}

	err = module.Update(config.rules)
	if err != nil {
		return nil, fmt.Errorf("failed to update forward module: %w", err)
	}

	if err := agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}); err != nil {
		return nil, fmt.Errorf("failed to update dp modules: %w", err)
	}

	if err := setupCp(agent); err != nil {
		return nil, fmt.Errorf("failed to setup yanet mock: %w", err)
	}

	return &TestSetup{
		mock:   m,
		agent:  agent,
		module: module,
	}, nil
}

func setupCp(agent *ffi.Agent) error {
	functionConfig := ffi.FunctionConfig{
		Name: defaultFunctionName,
		Chains: []ffi.FunctionChainConfig{
			{
				Weight: 1,
				Chain: ffi.ChainConfig{
					Name: defaultChainName,
					Modules: []ffi.ChainModuleConfig{
						{
							Type: "forward",
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

	return nil
}

func (m *TestSetup) Free() {
	m.agent.Close()
	m.mock.Free()
}
