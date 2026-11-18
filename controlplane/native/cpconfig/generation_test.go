package cpconfig_test

import (
	"os"
	"testing"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/coracle"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	cplain "github.com/yanet-platform/yanet2/devices/plain/bindings/go/cplain"
	"github.com/yanet-platform/yanet2/modules/blackhole/bindings/go/cblackhole"
)

// publishedFixture is a dataplane whose generation the cgo control plane
// filled: three blackhole configurations, a function, two pipelines and a
// device, owned by the agent "owner".
type publishedFixture struct {
	*nativetest.Fixture
	cAgent *ffi.Agent
	owner  *agent.Agent
}

func newPublishedFixture(t *testing.T) *publishedFixture {
	t.Helper()
	memory := nativetest.NewFixture(t, dataplaneut.Config{
		WorkerCount:   2,
		Devices:       []string{"port0"},
		Modules:       []string{"blackhole"},
		DevicesToLoad: []string{"plain"},
	})
	cAgent, err := memory.Harness.SharedMemory().AgentAttach("owner", 0, 16*datasize.MB)
	require.NoError(t, err)
	var modules []ffi.ModuleConfig
	for _, name := range []string{"a", "b", "c"} {
		config, err := cblackhole.NewModuleConfig(cAgent, name)
		require.NoError(t, err)
		modules = append(modules, config.AsFFIModule())
	}
	require.NoError(t, cAgent.UpdateModules(modules))
	require.NoError(t, cAgent.UpdateFunction(ffi.FunctionConfig{
		Name: "drop",
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name:    "drop_chain",
				Modules: []ffi.ChainModuleConfig{{Type: "blackhole", Name: "a"}},
			},
		}},
	}))
	require.NoError(t, cAgent.UpdatePipeline(ffi.PipelineConfig{Name: "drop", Functions: []string{"drop"}}))
	require.NoError(t, cAgent.UpdatePipeline(ffi.PipelineConfig{Name: "dummy"}))
	_, err = cplain.UpdateDevices(cAgent, []ffi.DeviceConfig{{
		Name:   "port0",
		Input:  []ffi.DevicePipelineConfig{{Name: "drop", Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}})
	require.NoError(t, err)
	owner, ok := agent.Find(memory.Instance(), "owner")
	require.True(t, ok)
	return &publishedFixture{Fixture: memory, cAgent: cAgent, owner: owner}
}

// cNewGeneration copies the published generation with the C registry
// copies, in the order cp_config_gen_new_from copies them.
func cNewGeneration(t *testing.T, memory *publishedFixture) *layout.CpConfigGen {
	t.Helper()
	cp := memory.Instance().Cp
	old := cp.CpConfigGen.Get()
	context := unsafe.Pointer(&cp.MemoryContext)
	block := coracle.ContextAlloc(context, unsafe.Sizeof(layout.CpConfigGen{}))
	require.NotNil(t, block)
	clear(unsafe.Slice((*byte)(block), unsafe.Sizeof(layout.CpConfigGen{})))
	gen := (*layout.CpConfigGen)(block)
	gen.Gen = old.Gen + 1
	gen.Refcnt = 1
	gen.DpConfig.Set(old.DpConfig.Get())
	gen.CpConfig.Set(old.CpConfig.Get())
	for _, registry := range []struct {
		kind       coracle.RegistryKind
		fresh, old unsafe.Pointer
	}{
		{coracle.ModuleRegistry, unsafe.Pointer(&gen.ModuleRegistry), unsafe.Pointer(&old.ModuleRegistry)},
		{coracle.FunctionRegistry, unsafe.Pointer(&gen.FunctionRegistry), unsafe.Pointer(&old.FunctionRegistry)},
		{coracle.PipelineRegistry, unsafe.Pointer(&gen.PipelineRegistry), unsafe.Pointer(&old.PipelineRegistry)},
		{coracle.DeviceRegistry, unsafe.Pointer(&gen.DeviceRegistry), unsafe.Pointer(&old.DeviceRegistry)},
		{coracle.ObjectRegistry, unsafe.Pointer(&gen.ObjectRegistry), unsafe.Pointer(&old.ObjectRegistry)},
	} {
		require.NoError(t, coracle.RegistryCopy(registry.kind, context, registry.fresh, registry.old))
	}
	return gen
}

// Test_NewGeneration_MatchesC verifies that copying a published
// generation from Go, every registry with its items' references, agents'
// loaded counts and hash index, leaves the storage as the C copies do.
func Test_NewGeneration_MatchesC(t *testing.T) {
	memory := newPublishedFixture(t)
	cp := memory.Instance().Cp
	memory.CompareWithC(t,
		func() {
			coracle.WithConfigLock(unsafe.Pointer(cp), func() { cNewGeneration(t, memory) })
		},
		func() {
			lock := shm.LockPid(&cp.ConfigLock, int32(os.Getpid()))
			defer lock.Unlock()
			_, err := cpconfig.NewGeneration(cp, cp.CpConfigGen.Get())
			require.NoError(t, err)
		},
	)
}

// Test_UpsertModule_MatchesC verifies that upserting a module
// configuration into a copied generation from Go, inserting a new name or
// replacing a published one whose counters it continues, leaves the
// storage as the C upsert does.
func Test_UpsertModule_MatchesC(t *testing.T) {
	for _, name := range []string{"fresh", "b"} {
		t.Run("module "+name, func(t *testing.T) {
			memory := newPublishedFixture(t)
			cp := memory.Instance().Cp
			memory.CompareWithC(t,
				func() {
					config, err := cblackhole.NewModuleConfig(memory.cAgent, name)
					require.NoError(t, err)
					coracle.WithConfigLock(unsafe.Pointer(cp), func() {
						gen := cNewGeneration(t, memory)
						require.NoError(t, coracle.ModuleUpsert(unsafe.Pointer(&gen.ModuleRegistry), config.AsFFIModule().AsRawPtr()))
					})
				},
				func() {
					module, err := cpconfig.NewBlackhole(memory.owner, name)
					require.NoError(t, err)
					lock := shm.LockPid(&cp.ConfigLock, int32(os.Getpid()))
					defer lock.Unlock()
					gen, err := cpconfig.NewGeneration(cp, cp.CpConfigGen.Get())
					require.NoError(t, err)
					require.NoError(t, cpconfig.UpsertModule(&gen.ModuleRegistry, module))
				},
			)
		})
	}
}

// Test_BuildWorkerContexts_MatchesC verifies that building every worker's
// execution contexts for a generation with an upserted module from Go,
// counter storages continued from the published generation included,
// leaves the storage as config_gen_ectxs_create does.
func Test_BuildWorkerContexts_MatchesC(t *testing.T) {
	for _, name := range []string{"fresh", "a"} {
		t.Run("module "+name, func(t *testing.T) {
			memory := newPublishedFixture(t)
			cp := memory.Instance().Cp
			workers := memory.Instance().Dp.WorkerCount
			memory.CompareWithC(t,
				func() {
					config, err := cblackhole.NewModuleConfig(memory.cAgent, name)
					require.NoError(t, err)
					coracle.WithConfigLock(unsafe.Pointer(cp), func() {
						old := cp.CpConfigGen.Get()
						gen := cNewGeneration(t, memory)
						require.NoError(t, coracle.ModuleUpsert(unsafe.Pointer(&gen.ModuleRegistry), config.AsFFIModule().AsRawPtr()))
						require.NotNil(t, coracle.BuildWorkerContexts(unsafe.Pointer(gen), unsafe.Pointer(old), workers))
					})
				},
				func() {
					module, err := cpconfig.NewBlackhole(memory.owner, name)
					require.NoError(t, err)
					lock := shm.LockPid(&cp.ConfigLock, int32(os.Getpid()))
					defer lock.Unlock()
					old := cp.CpConfigGen.Get()
					gen, err := cpconfig.NewGeneration(cp, old)
					require.NoError(t, err)
					require.NoError(t, cpconfig.UpsertModule(&gen.ModuleRegistry, module))
					_, err = cpconfig.BuildWorkerContexts(gen, old, workers)
					require.NoError(t, err)
				},
			)
		})
	}
}
