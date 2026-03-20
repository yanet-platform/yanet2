package controlplane_test

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	mock "github.com/yanet-platform/yanet2/mock/go"
	balancer "github.com/yanet-platform/yanet2/modules/balancer/agent/go/ffi"
)

func TestControlplaneUpdates(t *testing.T) {
	config := mock.YanetMockConfig{
		AgentsMemory: 128 * datasize.MB,
		DpMemory:     1 * datasize.MB,
		Workers:      1,
		Devices: []mock.YanetMockDeviceConfig{
			{
				ID:   0,
				Name: "device",
			},
		},
	}

	yanet, err := mock.NewYanetMock(&config)
	require.NoError(t, err, "failed to create yanet mock")
	defer yanet.Free()

	shm := yanet.SharedMemory()
	bootstrap, err := shm.AgentAttach("bootstrap", 0, 64*datasize.MB)
	require.NoError(t, err, "failed to attach bootstrap agent")

	// It is allowed to reference not-existent pipelines, function, chains and module configs
	// before installing them in the dataplane.
	//
	// Installation happens on linking device with pipeline.
	// In this case, if some pipeline, function, chain or module config is undefined,
	// one receives error.

	// Functions may be stored even if some referenced module configs are not registered yet.
	// This subtest verifies that unresolved balancer0/balancer1 references do not fail early.
	t.Run("FunctionWithUndefinedModule", func(t *testing.T) {
		config := ffi.FunctionConfig{
			Name: "function0",
			Chains: []ffi.FunctionChainConfig{
				{
					Weight: 1,
					Chain: ffi.ChainConfig{
						Name: "chain0",
						Modules: []ffi.ChainModuleConfig{
							{
								Type: "balancer",
								Name: "balancer0",
							},
						},
					},
				},
				{
					Weight: 2,
					Chain: ffi.ChainConfig{
						Name: "chain1",
						Modules: []ffi.ChainModuleConfig{
							{
								Type: "balancer",
								Name: "balancer1",
							},
						},
					},
				},
			},
		}
		err := bootstrap.UpdateFunction(config)
		require.NoError(t, err, "failed to update function")

		// check function appeared
		functions := bootstrap.DPConfig().Functions()
		require.Equal(t, 1, len(functions))

		function := &functions[0]

		require.Equal(t, "function0", function.Name)
		require.Equal(t, 2, len(function.Chains))

		chain0 := &function.Chains[0]
		require.Equal(t, "chain0", chain0.Name)
		require.Equal(t, uint64(1), chain0.Weight)
		require.Equal(t, 1, len(chain0.Modules))
		require.Equal(t, "balancer", chain0.Modules[0].Type)
		require.Equal(t, "balancer0", chain0.Modules[0].Name)

		chain1 := &function.Chains[1]
		require.Equal(t, "chain1", chain1.Name)
		require.Equal(t, uint64(2), chain1.Weight)
		require.Equal(t, 1, len(chain1.Modules))
		require.Equal(t, "balancer", chain1.Modules[0].Type)
		require.Equal(t, "balancer1", chain1.Modules[0].Name)
	})

	// Pipelines may reference functions that are not defined yet.
	// The reference becomes validated only when a device is linked to the pipeline.
	t.Run("PipelineWithUndefinedFunction", func(t *testing.T) {
		config := ffi.PipelineConfig{
			Name:      "pipeline0",
			Functions: []string{"function0", "function1"},
		}
		err := bootstrap.UpdatePipeline(config)
		require.NoError(t, err, "failed to update pipeline")

		pipelines := bootstrap.DPConfig().Pipelines()
		require.Equal(t, 1, len(pipelines))
		require.Equal(t, "pipeline0", pipelines[0].Name)
		require.Equal(t, []string{"function0", "function1"}, pipelines[0].Functions)
	})

	balancerAgent, err := balancer.NewBalancerAgent(yanet.SharedMemory(), uint(64*datasize.MB))
	require.NoError(t, err, "failed to create balancer agent")

	// Register only balancer0 first, leaving balancer1 unresolved in function0.
	t.Run("RegisterBalancer0", func(t *testing.T) {
		config := balancer.BalancerManagerConfig{
			Balancer: balancer.BalancerConfig{
				Handler: balancer.PacketHandlerConfig{
					SourceV4: netip.MustParseAddr("1.1.1.1"),
					SourceV6: netip.MustParseAddr("::1"),
				},
				State: balancer.StateConfig{
					TableCapacity: 1,
				},
			},
		}
		_, err := balancerAgent.NewManager("balancer0", &config)
		require.NoError(t, err, "failed to create balancer manager")
	})

	// Add function1 with a fully defined module config so pipeline0 can reference it.
	t.Run("FunctionWithDefinedModule", func(t *testing.T) {
		config := ffi.FunctionConfig{
			Name: "function1",
			Chains: []ffi.FunctionChainConfig{
				{
					Weight: 1,
					Chain: ffi.ChainConfig{
						Name: "chain2",
						Modules: []ffi.ChainModuleConfig{
							{
								Type: "balancer",
								Name: "balancer0",
							},
						},
					},
				},
			},
		}
		err := bootstrap.UpdateFunction(config)
		require.NoError(t, err, "failed to update function")

		functions := bootstrap.DPConfig().Functions()
		require.Equal(t, 2, len(functions))
		require.Equal(t, "function1", functions[1].Name)
		require.Equal(t, 1, len(functions[1].Chains))
		require.Equal(t, "chain2", functions[1].Chains[0].Name)
		require.Equal(t, uint64(1), functions[1].Chains[0].Weight)
		require.Equal(t, 1, len(functions[1].Chains[0].Modules))
		require.Equal(t, "balancer", functions[1].Chains[0].Modules[0].Type)
		require.Equal(t, "balancer0", functions[1].Chains[0].Modules[0].Name)
	})

	// Create an auxiliary pipeline with no functions to use as a valid output pipeline.
	t.Run("DummyPipeline", func(t *testing.T) {
		config := ffi.PipelineConfig{
			Name:      "dummy",
			Functions: nil,
		}
		err := bootstrap.UpdatePipeline(config)
		require.NoError(t, err, "failed to update dummy pipeline")

		pipelines := bootstrap.DPConfig().Pipelines()
		require.Equal(t, 2, len(pipelines))
		require.Equal(t, "pipeline0", pipelines[0].Name)
		require.Equal(t, []string{"function0", "function1"}, pipelines[0].Functions)
		require.Equal(t, "dummy", pipelines[1].Name)
		require.Empty(t, pipelines[1].Functions)
	})

	// Linking the device should fail because pipeline0 still reaches function0,
	// whose second chain references unresolved balancer1.
	t.Run("UpdateDeviceBalancer1Undefined", func(t *testing.T) {
		config := []ffi.DeviceConfig{
			{
				Name: "device",
				Input: []ffi.DevicePipelineConfig{
					{
						Name:   "pipeline0",
						Weight: 1,
					},
				},
				Output: []ffi.DevicePipelineConfig{
					{
						Name:   "dummy",
						Weight: 1,
					},
				},
			},
		}
		err := bootstrap.UpdatePlainDevices(config)
		require.Error(t, err, "device update should fail when balancer1 is undefined")
	})

	// Register balancer1 so all module references used by pipeline0 become valid.
	t.Run("RegisterBalancer1", func(t *testing.T) {
		config := balancer.BalancerManagerConfig{
			Balancer: balancer.BalancerConfig{
				Handler: balancer.PacketHandlerConfig{
					SourceV4: netip.MustParseAddr("1.1.1.1"),
					SourceV6: netip.MustParseAddr("::1"),
				},
				State: balancer.StateConfig{
					TableCapacity: 1,
				},
			},
		}
		_, err := balancerAgent.NewManager("balancer1", &config)
		require.NoError(t, err, "failed to create balancer manager")
	})

	// Device linking should also fail when the referenced input pipeline does not exist.
	t.Run("UpdateDeviceInputPipelineUndefined", func(t *testing.T) {
		config := []ffi.DeviceConfig{
			{
				Name: "device",
				Input: []ffi.DevicePipelineConfig{
					{
						Name:   "dummy1",
						Weight: 1,
					},
				},
				Output: []ffi.DevicePipelineConfig{
					{
						Name:   "dummy",
						Weight: 1,
					},
				},
			},
		}
		err := bootstrap.UpdatePlainDevices(config)
		require.Error(t, err, "device update should fail when input pipeline is undefined")
	})

	// Once all referenced modules and pipelines exist, device linking should succeed.
	t.Run("UpdateDeviceWithValidPipelines", func(t *testing.T) {
		config := []ffi.DeviceConfig{
			{
				Name: "device",
				Input: []ffi.DevicePipelineConfig{
					{
						Name:   "pipeline0",
						Weight: 1,
					},
				},
				Output: []ffi.DevicePipelineConfig{
					{
						Name:   "dummy",
						Weight: 1,
					},
				},
			},
		}
		err := bootstrap.UpdatePlainDevices(config)
		require.NoError(t, err, "failed to update device with valid pipelines")

		devices := bootstrap.DPConfig().Devices()
		require.Equal(t, 1, len(devices))
		require.Equal(t, "device", devices[0].Name)
		require.Equal(t, 1, len(devices[0].InputPipelines))
		require.Equal(t, "pipeline0", devices[0].InputPipelines[0].Name)
		require.Equal(t, uint64(1), devices[0].InputPipelines[0].Weight)
		require.Equal(t, 1, len(devices[0].OutputPipelines))
		require.Equal(t, "dummy", devices[0].OutputPipelines[0].Name)
		require.Equal(t, uint64(1), devices[0].OutputPipelines[0].Weight)
	})

	// Updating a function that is already used by an installed device must validate the new module config.
	// Replacing function0 with a chain that references missing balancer2 should therefore fail.
	t.Run("UpdateFunctionWithInvalidModule", func(t *testing.T) {
		config := ffi.FunctionConfig{
			Name: "function0",
			Chains: []ffi.FunctionChainConfig{
				{
					Weight: 1,
					Chain: ffi.ChainConfig{
						Name: "chain2",
						Modules: []ffi.ChainModuleConfig{
							{
								Type: "balancer",
								Name: "balancer2",
							},
						},
					},
				},
			},
		}
		err := bootstrap.UpdateFunction(config)
		require.Error(
			t,
			err,
			"function update should fail when module is invalid and function is referenced by dataplane processing",
		)
	})

	// Validation must also reject a referenced function update when the module type/name pair is unknown.
	// This covers invalid module identity, not just a missing instance of a known module type.
	t.Run("UpdateFunctionWithInvalidModule2", func(t *testing.T) {
		config := ffi.FunctionConfig{
			Name: "function0",
			Chains: []ffi.FunctionChainConfig{
				{
					Weight: 1,
					Chain: ffi.ChainConfig{
						Name: "chain2",
						Modules: []ffi.ChainModuleConfig{
							{
								Type: "some_type",
								Name: "some_name",
							},
						},
					},
				},
			},
		}
		err := bootstrap.UpdateFunction(config)
		require.Error(
			t,
			err,
			"function update should fail when module is invalid and function is referenced by dataplane processing",
		)
	})

	// Unreferenced functions are still allowed to carry unresolved module configs.
	// Because function2 is not attached to any installed pipeline/device path, the update should succeed.
	t.Run("UpdateNotReferencedFunctionWithInvalidModule", func(t *testing.T) {
		config := ffi.FunctionConfig{
			Name: "function2",
			Chains: []ffi.FunctionChainConfig{
				{
					Weight: 1,
					Chain: ffi.ChainConfig{
						Name: "chain0",
						Modules: []ffi.ChainModuleConfig{
							{
								Type: "balancer",
								Name: "balancer3",
							},
						},
					},
				},
			},
		}
		err := bootstrap.UpdateFunction(config)
		require.NoError(t, err, "failed to update function")
	})

	// Device with input pipeline equals output pipeline emits error
	t.Run("UpdateDeviceWithInputPipelineEqualsOutputPipeline", func(t *testing.T) {
		config := []ffi.DeviceConfig{
			{
				Name: "device",
				Input: []ffi.DevicePipelineConfig{
					{
						Name:   "pipeline0",
						Weight: 1,
					},
				},
				Output: []ffi.DevicePipelineConfig{
					{
						Name:   "pipeline0",
						Weight: 1,
					},
				},
			},
		}
		err := bootstrap.UpdatePlainDevices(config)
		require.Error(t, err, "failed to update device with input pipeline equals output pipeline")
	})

	// No segfault on trying to create a heavy pipeline
	t.Run("HeavyPipeline", func(t *testing.T) {
		config := []ffi.DeviceConfig{
			{
				Name: "device",
				Input: []ffi.DevicePipelineConfig{
					{
						Name:   "pipeline0",
						Weight: 1000000000,
					},
				},
				Output: []ffi.DevicePipelineConfig{
					{
						Name:   "dummy",
						Weight: 1,
					},
				},
			},
		}
		if err = bootstrap.UpdatePlainDevices(config); err != nil {
			t.Logf("Got expected error: %v", err)
		}
	})

	// It must be allowed to remove function from pipeline0
	t.Run("RemoveFunctionFromPipeline0", func(t *testing.T) {
		err := bootstrap.UpdatePipeline(ffi.PipelineConfig{
			Name: "pipeline0",
		})
		require.NoError(t, err)
	})

	// No segfault on creating a lot of pipelines
	t.Run("ManyPipelines", func(t *testing.T) {
		// Create and register pipelines
		pipelineCount := 500
		pipelines := make([]ffi.DevicePipelineConfig, pipelineCount)
		for i := range pipelineCount {
			pipelines[i] = ffi.DevicePipelineConfig{
				Name:   fmt.Sprintf("pipeline%d", i),
				Weight: 100,
			}

			err := bootstrap.UpdatePipeline(ffi.PipelineConfig{
				Name: pipelines[i].Name,
			})
			require.NoErrorf(t, err, "failed to update pipeline %s", pipelines[i].Name)
		}

		config := []ffi.DeviceConfig{
			{
				Name:  "device",
				Input: pipelines,
				Output: []ffi.DevicePipelineConfig{
					{
						Name:   "dummy",
						Weight: 1,
					},
				},
			},
		}
		if err = bootstrap.UpdatePlainDevices(config); err != nil {
			t.Logf("Got expected error: %v", err)
		}

		bootstrap.UpdatePlainDevices([]ffi.DeviceConfig{
			{
				Name: "device",
			},
		})

		// Delete all pipelines (they not referenced)
		for i := range pipelines {
			err := bootstrap.DeletePipeline(pipelines[i].Name)
			require.NoErrorf(t, err, "failed to delete pipeline %s", pipelines[i].Name)
		}
	})

	// TODO: Test empty pipeline is allowed

	// TODO: Test empty function is allowed

	// TODO: Test empty chain is allowed
}
