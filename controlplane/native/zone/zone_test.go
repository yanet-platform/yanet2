//go:build linux && (amd64 || arm64)

package zone_test

import (
	"os"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// agentLimit is the memory limit of the agent that publishes the
// topology.
const agentLimit = 4 * datasize.MB

// published is a dataplane of two workers to which an agent published two
// blackhole configurations, a function whose chain runs the first one, a
// pipeline of that function, an empty pipeline and a plain device whose
// input and output run them.
type published struct {
	Dataplane *nativetest.Dataplane
	Instance  *zone.Instance
	Owner     *agent.Agent
	// AttachedAt is the generation published when the agent attached.
	AttachedAt uint64
}

// newPublished returns a dataplane that loads the blackhole module, the
// plain device and the ring object, with the published topology.
func newPublished(t *testing.T) *published {
	t.Helper()
	dataplane := nativetest.NewDataplane(t, nativetest.Config{
		CPMemory:    uint64(32 * datasize.MB),
		Workers:     2,
		Devices:     []string{"port0"},
		Modules:     []string{"blackhole"},
		DeviceTypes: []string{"plain"},
		ObjectTypes: []string{"ring"},
	})
	instance := dataplane.Instance()
	attachedAt := instance.Cp.CpConfigGen.Get().Gen
	owner, err := agent.Attach(instance, "native-demo", uint64(agentLimit))
	require.NoError(t, err)
	m := &published{Dataplane: dataplane, Instance: instance, Owner: owner, AttachedAt: attachedAt}

	var modules []*layout.CpModule
	for _, name := range []string{"first", "second"} {
		module, err := cpconfig.NewBlackhole(owner, name)
		require.NoError(t, err)
		modules = append(modules, module)
	}
	require.NoError(t, cpconfig.UpdateModules(instance, modules, m.Deliver()))
	require.NoError(t, cpconfig.UpdateFunctions(instance, []cpconfig.FunctionConfig{{
		Name: "drop",
		Chains: []cpconfig.FunctionChainConfig{{
			Weight: 1,
			Chain: cpconfig.ChainConfig{
				Name:    "drop_chain",
				Modules: []cpconfig.ChainModuleConfig{{Type: "blackhole", Name: "first"}},
			},
		}},
	}}, m.Deliver()))
	require.NoError(t, cpconfig.UpdatePipelines(instance, []cpconfig.PipelineConfig{
		{Name: "drop", Functions: []string{"drop"}},
		{Name: "dummy"},
	}, m.Deliver()))
	device, err := cpconfig.NewPlainDevice(owner, cpconfig.DeviceConfig{
		Name:   "port0",
		Input:  []cpconfig.DevicePipelineConfig{{Name: "drop", Weight: 1}},
		Output: []cpconfig.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	})
	require.NoError(t, err)
	require.NoError(t, cpconfig.UpdateDevices(instance, []*layout.CpDevice{device}, m.Deliver()))
	return m
}

// Deliver returns the option that hands a publication to the dataplane's
// workers.
func (m *published) Deliver() cpconfig.UpdateOption {
	return cpconfig.WithDelivery(m.Dataplane.Deliver)
}

// Generation returns the number of the published generation.
func (m *published) Generation() uint64 {
	return m.Instance.Cp.CpConfigGen.Get().Gen
}

// publishedModules returns the module configurations the pinned
// generation publishes.
func publishedModules(t *testing.T, instance *zone.Instance) []zone.Module {
	t.Helper()
	generation, err := instance.Pin()
	require.NoError(t, err)
	defer generation.Release()
	return generation.Modules()
}

// Test_Storage_ReadsPublishedTopology verifies that the native readers
// report the workers, module types, published configurations and agents
// of the topology an agent published.
func Test_Storage_ReadsPublishedTopology(t *testing.T) {
	memory := newPublished(t)
	for worker := range 2 {
		_, err := memory.Dataplane.Round(worker, 0)
		require.NoError(t, err)
	}
	native := memory.Instance

	cases := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"numa node", func(t *testing.T) {
			// The harness runs its one instance on the first node.
			require.Zero(t, native.Dp.NumaIdx)
		}},
		{"workers", func(t *testing.T) {
			got, err := native.Workers()
			require.NoError(t, err)
			require.Len(t, got, 2)
			for idx, worker := range got {
				// The harness pins each worker to the core of its
				// index with the dataplane burst size, and each
				// worker polls its own queue of the one device.
				require.Equal(t, zone.Worker{
					Index:       uint64(idx),
					CoreID:      uint32(idx),
					RxBurstSize: 32,
					Generation:  memory.Generation(),
					Assignments: []zone.Assignment{{DeviceID: 0, QueueID: uint32(idx), Weight: 1}},
				}, worker)
			}
		}},
		{"dataplane module types", func(t *testing.T) {
			var got []string
			for _, module := range native.DataplaneModules() {
				got = append(got, module.Name)
			}
			require.Equal(t, []string{"blackhole"}, got)
		}},
		{"published module configurations", func(t *testing.T) {
			require.ElementsMatch(t, []zone.Module{
				{Type: "blackhole", Name: "first", Agent: "native-demo"},
				{Type: "blackhole", Name: "second", Agent: "native-demo"},
			}, publishedModules(t, native))
		}},
		{"agents", func(t *testing.T) {
			agents := native.Agents()
			require.Len(t, agents, 1)
			agents[0].LoadedEntityCount = 0
			require.Equal(t, zone.Agent{
				Name:        "native-demo",
				Pid:         int32(os.Getpid()),
				MemoryLimit: uint64(agentLimit),
				Generation:  memory.AttachedAt,
			}, agents[0])
		}},
		{"loaded entity count", func(t *testing.T) {
			agents := native.Agents()
			require.Len(t, agents, 1)
			// The two module configurations and the device.
			require.Equal(t, uint64(3), agents[0].LoadedEntityCount)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.check)
	}
}

// Test_Instance_AgentsCountsSuperseded verifies that an agent attached
// again under its name reports the earlier one, which still holds
// configurations, as superseded.
func Test_Instance_AgentsCountsSuperseded(t *testing.T) {
	memory := newPublished(t)
	_, err := agent.Attach(memory.Instance, "native-demo", uint64(agentLimit))
	require.NoError(t, err)

	agents := memory.Instance.Agents()
	require.Len(t, agents, 1)
	require.Equal(t, "native-demo", agents[0].Name)
	require.Equal(t, 1, agents[0].Superseded)
}

// Test_Generation_ModulesSkipsDeleted verifies that a module configuration
// deleted after its publication is not reported by a later generation.
func Test_Generation_ModulesSkipsDeleted(t *testing.T) {
	memory := newPublished(t)
	require.NoError(t, cpconfig.DeleteModule(memory.Instance, "blackhole", "second", memory.Deliver()))

	modules := publishedModules(t, memory.Instance)
	require.Len(t, modules, 1)
	require.Equal(t, "first", modules[0].Name)
}
