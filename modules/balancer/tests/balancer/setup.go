package balancer

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	mock "github.com/yanet-platform/yanet2/mock/go"
	balancer "github.com/yanet-platform/yanet2/modules/balancer/controlplane"
)

////////////////////////////////////////////////////////////////////////////////

var defaultDeviceName string = "01:00.0"
var defaultPipelineName string = "pipeline0"
var defaultFunctionName string = "function0"
var defaultChainName string = "chain0"
var defaultConfigName string = "balancer0"

////////////////////////////////////////////////////////////////////////////////

type TestConfig struct {
	mock             *mock.YanetMockConfig
	balancer         *balancer.ModuleInstanceConfig
	timeouts         *balancer.SessionsTimeouts
	sessionTableSize int
}

type TestSetup struct {
	mock     *mock.YanetMock
	agent    *ffi.Agent
	balancer *balancer.ModuleInstance
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

	if config.balancer == nil {
		config.balancer = &balancer.ModuleInstanceConfig{}
	}

	if config.timeouts == nil {
		config.timeouts = &balancer.SessionsTimeouts{
			TcpSynAck: 30,
			TcpSyn:    30,
			TcpFin:    30,
			Tcp:       30,
			Udp:       30,
		}
	}

	sessionTableSize := 128
	if config.sessionTableSize != 0 {
		sessionTableSize = config.sessionTableSize
	}

	// create mock

	mock, err := mock.NewYanetMock(config.mock)
	if err != nil {
		return nil, fmt.Errorf("failed to create new yanet mock: %w", err)
	}

	agent, err := mock.SharedMemory().
		AgentAttach("balancer", 0, uint(config.mock.CpMemory)-(1<<26))
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent: %w", err)
	}

	balancer, err := balancer.NewModuleInstance(
		agent,
		defaultConfigName,
		config.balancer,
		uint64(sessionTableSize),
		config.timeouts,
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
		mock:     mock,
		agent:    agent,
		balancer: balancer,
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

		if err := agent.UpdateFunctions([]ffi.FunctionConfig{functionConfig}); err != nil {
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

		if err := agent.UpdatePipelines([]ffi.PipelineConfig{inputPipelineConfig, dummyPipelineConfig}); err != nil {
			return fmt.Errorf("failed to update pipelines: %w", err)
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
