package test

import (
	"fmt"
	"testing"

	"github.com/c2h5oh/datasize"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	mock "github.com/yanet-platform/yanet2/mock/go"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
	"github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb"
	"go.uber.org/zap"
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
	rules []cacl.AclRule
}

type TestSetup struct {
	mock    *mock.YanetMock
	agent   *ffi.Agent
	service *acl.ACLService
}

func SetupTest(t *testing.T, config *TestConfig) (*TestSetup, error) {
	t.Helper()
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
		AgentAttach("acl", 0, config.mock.GetAgentsMemory())
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent: %w", err)
	}

	service := acl.NewACLService(acl.NewBackend(agent, uint64(config.mock.GetAgentsMemory().Bytes())), zap.NewNop())

	_, err = service.UpdateConfig(t.Context(), &aclpb.UpdateConfigRequest{
		Name:  defaultConfigName,
		Rules: aclpb.FromRules(config.rules),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update acl module: %w", err)
	}

	if err := setupCp(agent); err != nil {
		return nil, fmt.Errorf("failed to setup yanet mock: %w", err)
	}

	return &TestSetup{
		mock:    mock,
		agent:   agent,
		service: service,
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
	ctx.agent.Close()
	ctx.mock.Free()
}
