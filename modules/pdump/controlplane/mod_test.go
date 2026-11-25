package pdump_test

import (
	"cmp"
	"slices"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/ffi/ffitest"
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	cpdump "github.com/yanet-platform/yanet2/modules/pdump/bindings/go/cpdump"
	pdump "github.com/yanet-platform/yanet2/modules/pdump/controlplane"
	"github.com/yanet-platform/yanet2/modules/pdump/internal/testutils"
	"github.com/yanet-platform/yanet2/objects/ring/bindings/go/cring"
	"github.com/yanet-platform/yanet2/objects/ring/tests/ringtest"
)

// cleanupStorage provides native publications with an idle worker, not packet I/O.
func cleanupStorage(t *testing.T, extraModules ...string) (string, *ffi.SharedMemory, *ffi.Agent) {
	t.Helper()
	path := ffitest.NewStorage(t, ffitest.StorageTypes{
		Modules: append([]string{"pdump"}, extraModules...), Objects: []string{"ring"},
	},
	)
	memory, err := ffi.AttachSharedMemory(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, memory.Detach()) })
	require.NoError(t, testutils.PrepareWorker(memory))
	t.Cleanup(func() { testutils.FreeWorker(memory) })
	agent, err := memory.AgentAttach("observer", 0, 4*datasize.MB)
	require.NoError(t, err)
	testutils.SetWorkerTime(agent, 1_000_000)
	return path, memory, agent
}

// cleanupConfig selects a sufficient fresh startup arena for the file fixture.
func cleanupConfig(path string) *pdump.Config {
	config := pdump.DefaultConfig()
	config.InstanceID = xcfg.NewRequired(uint32(0))
	config.MemoryPath = xcfg.MustNonEmptyString(path)
	config.MemoryRequirements = xcfg.MustNonZero(16 * datasize.MB)
	return config
}

// publishCleanupRing leaves a ring owned only through native publication.
func publishCleanupRing(t *testing.T, agent *ffi.Agent, name string) {
	t.Helper()
	object, err := cring.NewObject(agent, name, cpdump.MinRingCapacity, cring.DefaultPublishBatch)
	require.NoError(t, err)
	require.NoError(t, object.Publish())
}

// publishCleanupCapture creates a published capture linked to an existing ring.
func publishCleanupCapture(t *testing.T, agent *ffi.Agent, name, ringName string) {
	t.Helper()
	config, err := cpdump.NewModuleConfig(agent, name)
	require.NoError(t, err)
	require.NoError(t, config.LinkRing(ringName))
	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}))
}

// pdumpReservations reports actual native arena owners, including predecessors.
func pdumpReservations(agent *ffi.Agent) []uint64 {
	for _, info := range agent.DPConfig().Agents() {
		if info.Name == "pdump" {
			reservations := make([]uint64, 0, len(info.Instances))
			for _, instance := range info.Instances {
				reservations = append(reservations, instance.MemoryLimit)
			}
			return reservations
		}
	}
	return nil
}

// Test_PdumpModule_StartupClearsPredecessors verifies that manual and replayed
// captures and unused rings disappear, returning every old arena reservation.
func Test_PdumpModule_StartupClearsPredecessors(t *testing.T) {
	path, memory, observerAgent := cleanupStorage(t)
	for _, name := range []string{"manual", "operator"} {
		old, err := memory.AgentAttach("pdump", 0, 16*datasize.MB)
		require.NoError(t, err)
		publishCleanupRing(t, old, name)
		publishCleanupCapture(t, old, name, name)
		publishCleanupRing(t, old, name+"-unused")
		require.NoError(t, old.Close())
	}
	require.Len(t, pdumpReservations(observerAgent), 2)
	freeBefore := observerAgent.DPConfig().CPAllocatorStats().FreeBytes

	for range 3 {
		func() {
			module, err := pdump.NewPdumpModule(cleanupConfig(path))
			require.NoError(t, err)
			defer func() { require.NoError(t, module.Close()) }()

			require.Empty(t, module.Agent().DPConfig().CPConfigs())
			for _, name := range []string{"manual", "manual-unused", "operator", "operator-unused"} {
				require.False(t, cring.Exists(module.Agent(), name))
			}
			require.Equal(t, []uint64{16 * uint64(datasize.MB)}, pdumpReservations(module.Agent()))
			require.GreaterOrEqual(t,
				module.Agent().DPConfig().CPAllocatorStats().FreeBytes,
				freeBefore+uint64(15*datasize.MB),
			)
			publishCleanupRing(t, module.Agent(), "live")
			publishCleanupCapture(t, module.Agent(), "live", "live")
		}()
	}
}

// Test_PdumpModule_StartupPreservesFunctionShape verifies that only stale pdump
// positions disappear while chain identity, order, weights and pipelines survive.
func Test_PdumpModule_StartupPreservesFunctionShape(t *testing.T) {
	path, memory, observerAgent := cleanupStorage(t, "forward")
	old, err := memory.AgentAttach("pdump", 0, 16*datasize.MB)
	require.NoError(t, err)
	publishCleanupRing(t, old, "old-ring")
	publishCleanupCapture(t, old, "old-capture", "old-ring")
	publishCleanupRing(t, observerAgent, "foreign-ring")
	publishCleanupCapture(t, observerAgent, "foreign-capture", "foreign-ring")
	keep, err := cforward.NewModuleConfig(observerAgent, "keep")
	require.NoError(t, err)
	require.NoError(t, observerAgent.UpdateModules([]ffi.ModuleConfig{keep.AsFFIModule()}))
	err = observerAgent.UpdateFunction(ffi.FunctionConfig{
		Name: "function",
		Chains: []ffi.FunctionChainConfig{
			{Chain: ffi.ChainConfig{Name: "mixed", Modules: []ffi.ChainModuleConfig{
				{Type: "pdump", Name: "old-capture"}, {Type: "forward", Name: "keep"},
				{Type: "pdump", Name: "foreign-capture"}, {Type: "pdump", Name: "old-capture"},
			}}, Weight: 7},
			{Chain: ffi.ChainConfig{Name: "only", Modules: []ffi.ChainModuleConfig{
				{Type: "pdump", Name: "old-capture"},
			}}, Weight: 3},
			{Chain: ffi.ChainConfig{Name: "empty"}, Weight: 2},
		},
	},
	)
	require.NoError(t, err)
	err = observerAgent.UpdateFunction(ffi.FunctionConfig{
		Name: "unchanged", Chains: []ffi.FunctionChainConfig{
			{Chain: ffi.ChainConfig{Name: "foreign", Modules: []ffi.ChainModuleConfig{
				{Type: "pdump", Name: "foreign-capture"},
			}}, Weight: 9},
		},
	},
	)
	require.NoError(t, err)
	err = observerAgent.UpdatePipeline(ffi.PipelineConfig{
		Name: "pipeline", Functions: []string{"function", "unchanged"},
	},
	)
	require.NoError(t, err)
	pipelines := observerAgent.DPConfig().Pipelines()
	require.NoError(t, old.Close())

	module, err := pdump.NewPdumpModule(cleanupConfig(path))
	require.NoError(t, err)
	defer func() { require.NoError(t, module.Close()) }()
	functions := module.Agent().DPConfig().Functions()
	slices.SortFunc(functions, func(left, right ffi.Function) int {
		return cmp.Compare(left.Name, right.Name)
	},
	)
	require.Equal(t, []ffi.Function{
		{Name: "function", Chains: []ffi.Chain{
			{Name: "mixed", Weight: 7, Modules: []ffi.ChainModule{
				{Type: "forward", Name: "keep"}, {Type: "pdump", Name: "foreign-capture"},
			}},
			{Name: "only", Weight: 3, Modules: []ffi.ChainModule{}},
			{Name: "empty", Weight: 2, Modules: []ffi.ChainModule{}},
		}},
		{Name: "unchanged", Chains: []ffi.Chain{
			{Name: "foreign", Weight: 9, Modules: []ffi.ChainModule{{Type: "pdump", Name: "foreign-capture"}}},
		}},
	}, functions,
	)
	require.Equal(t, pipelines, module.Agent().DPConfig().Pipelines())
}

// Test_PdumpModule_StartupRetainsForeignConsumerRing verifies that a non-pdump
// consumer protects its ring and old arena until a later startup can remove it.
func Test_PdumpModule_StartupRetainsForeignConsumerRing(t *testing.T) {
	path, memory, observerAgent := cleanupStorage(t, "forward")
	old, err := memory.AgentAttach("pdump", 0, 16*datasize.MB)
	require.NoError(t, err)
	publishCleanupRing(t, old, "shared")
	publishCleanupCapture(t, old, "old-capture", "shared")
	foreign, err := cforward.NewModuleConfig(observerAgent, "consumer")
	require.NoError(t, err)
	require.NoError(t, ringtest.LinkRing(foreign.AsFFIModule().AsRawPtr(), "shared"))
	require.NoError(t, observerAgent.UpdateModules([]ffi.ModuleConfig{foreign.AsFFIModule()}))
	require.NoError(t, old.Close())
	logCore, logs := observer.New(zap.WarnLevel)

	for idx := range 2 {
		func() {
			module, err := pdump.NewPdumpModule(cleanupConfig(path), pdump.WithLog(zap.New(logCore)))
			require.NoError(t, err)
			defer func() { require.NoError(t, module.Close()) }()

			require.True(t, cring.Exists(module.Agent(), "shared"))
			require.Len(t, pdumpReservations(module.Agent()), 2)
			if idx == 1 {
				require.NoError(t, module.Agent().DeleteModuleConfig("forward", "consumer"))
			}
		}()
	}
	warnings := logs.FilterLevelExact(zap.WarnLevel).All()
	require.Len(t, warnings, 2)
	for _, warning := range warnings {
		require.Equal(t, "shared", warning.ContextMap()["ring"])
		require.NotEmpty(t, warning.ContextMap()["error"])
	}
	module, err := pdump.NewPdumpModule(cleanupConfig(path))
	require.NoError(t, err)
	defer func() { require.NoError(t, module.Close()) }()
	require.False(t, cring.Exists(module.Agent(), "shared"))
	require.Equal(t, []uint64{16 * uint64(datasize.MB)}, pdumpReservations(module.Agent()))
}
