//go:build linux && (amd64 || arm64) && cgo

package zone_test

import (
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/coracle"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/modules/blackhole/bindings/go/cblackhole"
)

// Test_Generation_Release_FreesGenerationRetiredWhilePinned verifies that
// releasing the last pin of a retired generation frees it as C does.
//
// The release also reclaims the superseded agent only that generation
// held, leaving the storage as cp_config_gen_release does.
func Test_Generation_Release_FreesGenerationRetiredWhilePinned(t *testing.T) {
	memory := nativetest.NewFixture(t, dataplaneut.Config{WorkerCount: 2, Modules: []string{"blackhole"}})
	instance := memory.Instance()
	publish := func(name string) {
		attached, err := memory.Harness.SharedMemory().AgentAttach("restarting", 0, 2*datasize.MB)
		require.NoError(t, err)
		config, err := cblackhole.NewModuleConfig(attached, name)
		require.NoError(t, err)
		require.NoError(t, attached.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}))
	}
	superseded := func() int {
		for _, entry := range instance.Agents() {
			if entry.Name == "restarting" {
				return entry.Superseded
			}
		}
		t.Fatal("no agent named restarting")
		return 0
	}
	publish("held")
	pinned, err := instance.Pin()
	require.NoError(t, err)
	// A restarted agent replaces the configuration; the pin keeps the
	// generation that held the first agent's.
	publish("held")
	retired := pinned.Gen
	require.NotSame(t, retired, instance.Cp.CpConfigGen.Get())
	require.Equal(t, uint64(1), atomic.LoadUint64(&retired.Refcnt))
	require.Equal(t, 1, superseded())

	memory.CompareWithC(
		t,
		func() { coracle.GenRelease(unsafe.Pointer(instance.Cp), unsafe.Pointer(retired)) },
		pinned.Release,
	)
	require.Nil(t, pinned.Gen)
	require.Zero(t, superseded(), "the release reclaims the agent only the retired generation held")
}

// Test_Generation_Release_FreesFunctionsAndPipelinesRetiredWhilePinned
// verifies that a last release frees the function and pipeline it held.
//
// C updates replace the function and the pipeline while a pin holds the
// generation that published them, so the pin keeps their last reference,
// and the storage must end as cp_config_gen_release leaves it.
func Test_Generation_Release_FreesFunctionsAndPipelinesRetiredWhilePinned(t *testing.T) {
	memory := nativetest.NewFixture(t, dataplaneut.Config{WorkerCount: 2, Modules: []string{"blackhole"}})
	instance := memory.Instance()
	attached, err := memory.Harness.SharedMemory().AgentAttach("owner", 0, 2*datasize.MB)
	require.NoError(t, err)
	config, err := cblackhole.NewModuleConfig(attached, "held")
	require.NoError(t, err)
	require.NoError(t, attached.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}))
	publish := func() {
		require.NoError(t, attached.UpdateFunction(ffi.FunctionConfig{
			Name: "drop",
			Chains: []ffi.FunctionChainConfig{{
				Weight: 1,
				Chain: ffi.ChainConfig{
					Name:    "drop_chain",
					Modules: []ffi.ChainModuleConfig{{Type: "blackhole", Name: "held"}},
				},
			}},
		}))
		require.NoError(t, attached.UpdatePipeline(ffi.PipelineConfig{Name: "drop", Functions: []string{"drop"}}))
	}
	publish()
	pinned, err := instance.Pin()
	require.NoError(t, err)
	publish()
	retired := pinned.Gen
	require.Equal(t, uint64(1), atomic.LoadUint64(&retired.Refcnt))

	memory.CompareWithC(
		t,
		func() { coracle.GenRelease(unsafe.Pointer(instance.Cp), unsafe.Pointer(retired)) },
		pinned.Release,
	)
}

// Test_Generation_Release_DropsThePin verifies that a pin holds one more
// reference on the published generation, that Release drops it, and that
// a second Release drops nothing more.
func Test_Generation_Release_DropsThePin(t *testing.T) {
	harness, _ := newPublishedHarness(t)
	native := nativeStorage(t, harness).Instances[0]
	published := native.Cp.CpConfigGen.Load()
	require.NotNil(t, published)
	before := atomic.LoadUint64(&published.Refcnt)

	pinned, err := native.Pin()
	require.NoError(t, err)
	require.Same(t, published, pinned.Gen)
	require.Equal(t, before+1, atomic.LoadUint64(&published.Refcnt))

	pinned.Release()
	require.Nil(t, pinned.Gen)
	require.Equal(t, before, atomic.LoadUint64(&published.Refcnt))

	pinned.Release()
	require.Equal(t, before, atomic.LoadUint64(&published.Refcnt))
}
