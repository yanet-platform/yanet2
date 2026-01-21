package utils

import (
	"fmt"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/yanet-platform/yanet2/common/go/logging"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	mock "github.com/yanet-platform/yanet2/mock/go"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	balancer "github.com/yanet-platform/yanet2/modules/balancer/agent/go"
	"go.uber.org/zap/zapcore"
)

var DeviceName string = "01:00.0"
var PipelineName string = "pipeline0"
var FunctionName string = "function0"
var ChainName string = "chain0"
var BalancerName string = "balancer0"

////////////////////////////////////////////////////////////////////////////////

type TestConfig struct {
	Mock        *mock.YanetMockConfig
	Balancer    *balancerpb.BalancerConfig
	AgentMemory *datasize.ByteSize
}

func SingleWorkerMockConfig(
	cpMemory datasize.ByteSize,
	dpMemory datasize.ByteSize,
) *mock.YanetMockConfig {
	return &mock.YanetMockConfig{
		AgentsMemory: cpMemory,
		DpMemory:     dpMemory,
		Workers:      1,
		Devices: []mock.YanetMockDeviceConfig{
			{
				Id:   0,
				Name: DeviceName,
			},
		},
	}
}

type TestSetup struct {
	Mock     *mock.YanetMock
	Agent    *balancer.BalancerAgent
	Balancer *balancer.BalancerManager
}

func Make(config *TestConfig) (*TestSetup, error) {
	if config.Mock.AgentsMemory < 8*datasize.MB {
		return nil, fmt.Errorf("CP memory must be at least 8MB")
	}
	mock, err := mock.NewYanetMock(config.Mock)
	if err != nil {
		return nil, fmt.Errorf("failed to create new mock: %v", err)
	}
	logLevel := zapcore.InfoLevel
	sugaredLogger, _, _ := logging.Init(&logging.Config{
		Level: logLevel,
	})
	agentMemory := 4 * datasize.MB
	if config.AgentMemory != nil {
		agentMemory = *config.AgentMemory
	}
	agent, err := balancer.NewBalancerAgent(
		mock.SharedMemory(),
		agentMemory,
		sugaredLogger,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create new balancer agent: %v", err)
	}
	if err := agent.NewBalancerManager(BalancerName, config.Balancer); err != nil {
		return nil, fmt.Errorf("failed to create new balancer manager: %v", err)
	}
	balancer, err := agent.BalancerManager(BalancerName)
	if err != nil {
		panic("failed to get balancer after successful creation")
	}

	bootstrap, err := mock.SharedMemory().AgentAttach("bootstrap", 0, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("failed to attach to bootstrap agent: %v", err)
	}

	if err := setupCp(bootstrap); err != nil {
		return nil, fmt.Errorf("failed to setup controlplane: %v", err)
	}

	return &TestSetup{
		Mock:     mock,
		Agent:    agent,
		Balancer: balancer,
	}, nil
}

func setupCp(agent *ffi.Agent) error {
	{
		functionConfig := ffi.FunctionConfig{
			Name: FunctionName,
			Chains: []ffi.FunctionChainConfig{
				{
					Weight: 1,
					Chain: ffi.ChainConfig{
						Name: ChainName,
						Modules: []ffi.ChainModuleConfig{
							{
								Type: "balancer",
								Name: BalancerName,
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
			Name:      PipelineName,
			Functions: []string{FunctionName},
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
			Name: DeviceName,
			Input: []ffi.DevicePipelineConfig{
				{
					Name:   PipelineName,
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

func (ts *TestSetup) Free() {
	ts.Balancer.Free()
	ts.Mock.Free()
}

// EnableAllReals enables all real servers in the balancer configuration
func EnableAllReals(t *testing.T, ts *TestSetup) {
	t.Helper()

	config := ts.Balancer.Config()
	var updates []*balancerpb.RealUpdate
	enableTrue := true

	for _, vs := range config.PacketHandler.Vs {
		for _, real := range vs.Reals {
			updates = append(updates, &balancerpb.RealUpdate{
				RealId: &balancerpb.RealIdentifier{
					Vs:   vs.Id,
					Real: real.Id,
				},
				Enable: &enableTrue,
			})
		}
	}

	_, err := ts.Balancer.UpdateReals(updates, false)
	if err != nil {
		t.Fatalf("failed to enable reals: %v", err)
	}
}
