package balancer

import (
	"fmt"
	"time"

	"github.com/c2h5oh/datasize"
	"go.uber.org/zap/zapcore"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/yanet-platform/yanet2/common/go/logging"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	mock "github.com/yanet-platform/yanet2/mock/go"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/module"
)

////////////////////////////////////////////////////////////////////////////////

var defaultDeviceName string = "01:00.0"
var defaultPipelineName string = "pipeline0"
var defaultFunctionName string = "function0"
var defaultChainName string = "chain0"
var defaultConfigName string = "balancer0"

////////////////////////////////////////////////////////////////////////////////

type TestConfig struct {
	mock         *mock.YanetMockConfig
	moduleConfig *balancerpb.ModuleConfig
	stateConfig  *balancerpb.ModuleStateConfig
	debug        bool
}

type SetupStats struct {
	balancerCreationTime time.Duration
}

type TestSetup struct {
	mock     *mock.YanetMock
	agent    *ffi.Agent
	balancer *module.Balancer
	stats    SetupStats
}

func SetupTest(config *TestConfig) (*TestSetup, error) {
	if config.mock == nil {
		config.mock = &mock.YanetMockConfig{
			CpMemory: datasize.MB * 512,
			DpMemory: datasize.MB * 128,
			Workers:  1,
			Devices: []mock.YanetMockDeviceConfig{
				{
					Id:   0,
					Name: defaultDeviceName,
				},
			},
		}
	}
	if config.mock.CpMemory < datasize.MB*128 {
		return nil, fmt.Errorf("need at least 128MB for the controlplane")
	}

	if config.moduleConfig == nil {
		config.moduleConfig = &balancerpb.ModuleConfig{}
	}

	if config.stateConfig == nil {
		config.stateConfig = &balancerpb.ModuleStateConfig{
			SessionTableCapacity:      128,
			SessionTableMaxLoadFactor: 0.75,
			SessionTableScanPeriod:    durationpb.New(0),
		}
	}

	// create mock

	mockInstance, err := mock.NewYanetMock(config.mock)
	if err != nil {
		return nil, fmt.Errorf("failed to create new yanet mock: %w", err)
	}

	agent, err := mockInstance.SharedMemory().
		AgentAttach("balancer", 0, uint(config.mock.CpMemory-datasize.MB*128))
	if err != nil {
		return nil, err
	}

	// Create logger for balancer with colorful output
	logLevel := zapcore.InfoLevel
	if config.debug {
		logLevel = zapcore.DebugLevel
	}
	sugaredLogger, _, _ := logging.Init(&logging.Config{
		Level: logLevel,
	})

	balancerCreateStart := time.Now()
	balancerInstance, err := module.NewBalancerFromProto(
		*agent,
		defaultConfigName,
		config.moduleConfig,
		config.stateConfig,
		sugaredLogger,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to create new balancer module instance: %w",
			err,
		)
	}

	stats := SetupStats{
		balancerCreationTime: time.Since(balancerCreateStart),
	}

	if err := setupCp(agent); err != nil {
		return nil, fmt.Errorf("failed to setup yanet mock: %w", err)
	}

	return &TestSetup{
		mock:     mockInstance,
		agent:    agent,
		balancer: balancerInstance,
		stats:    stats,
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
