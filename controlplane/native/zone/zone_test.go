//go:build linux && amd64

package zone_test

import (
	"slices"
	"testing"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
	cplain "github.com/yanet-platform/yanet2/devices/plain/bindings/go/cplain"
	"github.com/yanet-platform/yanet2/modules/blackhole/bindings/go/cblackhole"
)

// rawSharedMemory mirrors the C handle struct yanet_shm: the base and
// size of the storage in this process.
type rawSharedMemory struct {
	base unsafe.Pointer
	size uintptr
}

// newPublishedHarness returns an in-process dataplane of two workers with
// two blackhole configurations, a function, a pipeline and a device
// published through the cgo control plane, and the agent that did it.
func newPublishedHarness(t *testing.T) (*dataplaneut.Harness, *ffi.Agent) {
	t.Helper()
	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(32 * datasize.MB),
		DPMemory:      uint64(4 * datasize.MB),
		WorkerCount:   2,
		Devices:       []string{"port0"},
		Modules:       []string{"blackhole"},
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	agent, err := harness.SharedMemory().AgentAttach("native-demo", 0, 4*datasize.MB)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	var modules []ffi.ModuleConfig
	for _, name := range []string{"first", "second"} {
		config, err := cblackhole.NewModuleConfig(agent, name)
		require.NoError(t, err)
		t.Cleanup(func() { _ = config.Free() })
		modules = append(modules, config.AsFFIModule())
	}
	require.NoError(t, agent.UpdateModules(modules))
	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: "drop",
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name:    "drop_chain",
				Modules: []ffi.ChainModuleConfig{{Type: "blackhole", Name: "first"}},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "drop", Functions: []string{"drop"}}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "dummy"}))
	_, err = cplain.UpdateDevices(agent, []ffi.DeviceConfig{{
		Name:   "port0",
		Input:  []ffi.DevicePipelineConfig{{Name: "drop", Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}})
	require.NoError(t, err)
	return harness, agent
}

// nativeStorage reads the harness's storage through the native package.
func nativeStorage(t *testing.T, harness *dataplaneut.Harness) *zone.Storage {
	t.Helper()
	raw := (*rawSharedMemory)(harness.SharedMemory().AsRawPtr())
	storage, err := zone.FromMemory(unsafe.Slice((*byte)(raw.base), raw.size))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, storage.Close()) })
	return storage
}

// Test_Storage_MatchesCgoReaders verifies that the native readers report
// the same topology, module types, published configurations and agents as
// the cgo control plane reads from one storage.
func Test_Storage_MatchesCgoReaders(t *testing.T) {
	harness, _ := newPublishedHarness(t)
	storage := nativeStorage(t, harness)
	require.Len(t, storage.Instances, 1)
	native := storage.Instances[0]
	cgo := harness.SharedMemory().DPConfig(0)

	cases := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"numa node", func(t *testing.T) {
			require.Equal(t, cgo.NumaIdx(), native.Dp.NumaIdx)
		}},
		{"worker count", func(t *testing.T) {
			workers, err := native.Workers()
			require.NoError(t, err)
			require.Len(t, workers, int(cgo.WorkerCount()))
		}},
		{"dataplane module types", func(t *testing.T) {
			var want []string
			for _, module := range cgo.Modules() {
				want = append(want, module.Name())
			}
			var got []string
			for _, module := range native.DataplaneModules() {
				got = append(got, module.Name)
			}
			require.Equal(t, want, got)
		}},
		{"published module configurations", func(t *testing.T) {
			var want []zone.Module
			for _, config := range cgo.CPConfigs() {
				want = append(want, zone.Module{Type: config.Type, Name: config.Name, Generation: config.Gen})
			}
			generation := native.Pin()
			defer generation.Release()
			var got []zone.Module
			for _, module := range generation.Modules() {
				require.Equal(t, "native-demo", module.Agent)
				module.Agent = ""
				got = append(got, module)
			}
			require.ElementsMatch(t, want, got)
			require.Len(t, got, 2)
		}},
		{"agents", func(t *testing.T) {
			type agent struct {
				name        string
				pid         uint32
				memoryLimit uint64
				generation  uint64
				superseded  int
			}
			var want []agent
			for _, info := range cgo.Agents() {
				current := info.Instances[0]
				want = append(want, agent{
					info.Name, current.PID, current.MemoryLimit, current.Gen, len(info.Instances) - 1,
				})
			}
			var got []agent
			for _, info := range native.Agents() {
				got = append(got, agent{
					info.Name, uint32(info.Pid), info.MemoryLimit, info.Generation, info.Superseded,
				})
			}
			require.ElementsMatch(t, want, got)
			require.True(t, slices.ContainsFunc(got, func(entry agent) bool {
				return entry.name == "native-demo"
			}))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.check)
	}
}
