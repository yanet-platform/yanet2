package test

import (
	"fmt"

	"github.com/c2h5oh/datasize"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	mock "github.com/yanet-platform/yanet2/mock/go"
	balancer2 "github.com/yanet-platform/yanet2/modules/balancer2/controlplane"
)

////////////////////////////////////////////////////////////////////////////////

var (
	defaultDeviceName   string = "01:00.0"
	defaultPipelineName string = "pipeline0"
	defaultFunctionName string = "function0"
	defaultChainName    string = "chain0"
	defaultConfigName   string = "balancer0"
)

////////////////////////////////////////////////////////////////////////////////

type TestConfig struct {
	mock             *mock.YanetMockConfig
	balancer         *balancer2.ConfigParams
	sessionsCapacity uint64
}

type TestSetup struct {
	mock     *mock.YanetMock
	agent    *ffi.Agent
	balancer *balancer2.ModuleConfig
	sessions *balancer2.SessionsState
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
	mock, err := mock.NewYanetMock(config.mock)
	if err != nil {
		return nil, fmt.Errorf("failed to create new yanet mock: %w", err)
	}

	agent, err := mock.SharedMemory().
		AgentAttach("balancer2", 0, config.mock.GetAgentsMemory())
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent: %w", err)
	}

	sessions, err := balancer2.NewSessionsState(defaultConfigName, agent, config.sessionsCapacity)
	if err != nil {
		return nil, fmt.Errorf("failed to create sessions state: %w", err)
	}

	module, err := balancer2.NewModuleConfig(defaultConfigName, agent, config.balancer, sessions)
	if err != nil {
		return nil, fmt.Errorf("failed to create new balancer module: %w", err)
	}

	if err := setupCp(agent); err != nil {
		return nil, fmt.Errorf("failed to setup yanet mock: %w", err)
	}

	return &TestSetup{
		mock:     mock,
		agent:    agent,
		balancer: module,
		sessions: sessions,
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
								Type: "balancer2",
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
	ctx.balancer.Free()
	ctx.sessions.Free()
	ctx.agent.Close()
	ctx.mock.Free()
}
