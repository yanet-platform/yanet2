package balancer

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	mock "github.com/yanet-platform/yanet2/mock/go"
	balancer "github.com/yanet-platform/yanet2/modules/balancer/controlplane"
)

////////////////////////////////////////////////////////////////////////////////

var BalancerConfigName string = "balancer0"

////////////////////////////////////////////////////////////////////////////////

type TestContextConfig struct {
	mock             *mock.YanetMockConfig
	balancer         *balancer.ModuleInstanceConfig
	timeouts         *balancer.SessionsTimeouts
	sessionTableSize int
}

type TestContext struct {
	mock     *mock.YanetMock
	agent    *ffi.Agent
	balancer *balancer.ModuleInstance
}

func CreateTestContext(config *TestContextConfig) (*TestContext, error) {
	if config.mock == nil {
		config.mock = &mock.YanetMockConfig{
			CpMemory: 1 << 28,
			DpMemory: 1 << 26,
			Workers:  1,
			Devices: []mock.YanetMockDeviceConfig{
				{
					Id:   0,
					Name: "01:00.0",
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

	agent, err := mock.SharedMemory().AgentAttach("balancer", 0, uint(config.mock.CpMemory)-(1<<26))
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent: %w", err)
	}

	balancer, err := balancer.NewModuleInstance(agent, BalancerConfigName, config.balancer, uint64(sessionTableSize), config.timeouts)
	if err != nil {
		return nil, fmt.Errorf("failed to create new balancer module instance: %w", err)
	}

	if err := setupCp(agent); err != nil {
		return nil, fmt.Errorf("failed to setup yanet mock: %w", err)
	}

	return &TestContext{
		mock:     mock,
		agent:    agent,
		balancer: balancer,
	}, nil
}

func setupCp(agent *ffi.Agent) error {
	{
		functionConfig := ffi.FunctionConfig{
			Name: "test",
			Chains: []ffi.FunctionChainConfig{
				{
					Weight: 1,
					Chain: ffi.ChainConfig{
						Name: "ch0",
						Modules: []ffi.ChainModuleConfig{
							{
								Type: "balancer",
								Name: BalancerConfigName,
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
			Name:      "test",
			Functions: []string{"test"},
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
			Name: "01:00.0",
			Input: []ffi.DevicePipelineConfig{
				{
					Name:   "test",
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

func (ctx *TestContext) Free() {
	ctx.balancer.Free()
	ctx.agent.Close()
	ctx.mock.Free()
}
