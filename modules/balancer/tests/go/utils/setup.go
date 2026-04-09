package utils

import (
	"fmt"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	mock "github.com/yanet-platform/yanet2/mock/go"
	balancer "github.com/yanet-platform/yanet2/modules/balancer/controlplane"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

var (
	DeviceName   = "01:00.0"
	PipelineName = "pipeline0"
	FunctionName = "function0"
	ChainName    = "chain0"
	BalancerName = "balancer0"
)

type TestConfig struct {
	Mock        *mock.YanetMockConfig
	Balancer    *balancerpb.BalancerConfig
	AgentMemory datasize.ByteSize // 0 means default (4 MB)
}

type TestSetup struct {
	Mock     *mock.YanetMock
	Agent    *balancer.BalancerAgent
	Balancer *balancer.Balancer
	Config   *balancerpb.BalancerConfig
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
				ID:   0,
				Name: DeviceName,
			},
		},
	}
}

func Make(config *TestConfig) (*TestSetup, error) {
	if config.Mock.AgentsMemory < 8*datasize.MB {
		return nil, fmt.Errorf("CP memory must be at least 8MB")
	}

	m, err := mock.NewYanetMock(config.Mock)
	if err != nil {
		return nil, fmt.Errorf("create mock: %w", err)
	}

	agentMemory := 4 * datasize.MB
	if config.AgentMemory != 0 {
		agentMemory = config.AgentMemory
	}

	agent, err := balancer.ReattachBalancerAgent(
		m.SharedMemory(),
		0,
		agentMemory,
	)
	if err != nil {
		m.Free()
		return nil, fmt.Errorf("attach balancer agent: %w", err)
	}

	b, err := balancer.NewBalancer(agent, BalancerName, config.Balancer)
	if err != nil {
		m.Free()
		return nil, fmt.Errorf("create balancer: %w", err)
	}

	bootstrap, err := m.SharedMemory().AgentReattach("bootstrap", 0, 1<<20)
	if err != nil {
		b.Destroy()
		m.Free()
		return nil, fmt.Errorf("attach bootstrap agent: %w", err)
	}

	if err := setupCp(bootstrap); err != nil {
		b.Destroy()
		m.Free()
		return nil, fmt.Errorf("setup controlplane: %w", err)
	}

	return &TestSetup{
		Mock:     m,
		Agent:    agent,
		Balancer: b,
		Config:   config.Balancer,
	}, nil
}

func setupCp(agent *ffi.Agent) error {
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
		return fmt.Errorf("update function: %w", err)
	}

	inputPipeline := ffi.PipelineConfig{
		Name:      PipelineName,
		Functions: []string{FunctionName},
	}
	dummyPipeline := ffi.PipelineConfig{
		Name:      "dummy",
		Functions: []string{},
	}
	if err := agent.UpdatePipeline(inputPipeline); err != nil {
		return fmt.Errorf("update input pipeline: %w", err)
	}
	if err := agent.UpdatePipeline(dummyPipeline); err != nil {
		return fmt.Errorf("update dummy pipeline: %w", err)
	}

	deviceConfig := ffi.DeviceConfig{
		Name: DeviceName,
		Input: []ffi.DevicePipelineConfig{
			{Name: PipelineName, Weight: 1},
		},
		Output: []ffi.DevicePipelineConfig{
			{Name: "dummy", Weight: 1},
		},
	}
	if err := agent.UpdatePlainDevices([]ffi.DeviceConfig{deviceConfig}); err != nil {
		return fmt.Errorf("update devices: %w", err)
	}

	return nil
}

func (ts *TestSetup) Free() {
	ts.Balancer.Destroy()
	ts.Mock.Free()
}

func EnableAllReals(t *testing.T, ts *TestSetup) {
	t.Helper()

	config := ts.Config
	if config.PacketHandler == nil {
		return
	}

	enableTrue := true
	var updates []*balancerpb.RealUpdate
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

func StateRef() *balancerpb.PacketHandlerRef {
	return &balancerpb.PacketHandlerRef{
		Device:   &DeviceName,
		Pipeline: &PipelineName,
		Function: &FunctionName,
		Chain:    &ChainName,
	}
}
