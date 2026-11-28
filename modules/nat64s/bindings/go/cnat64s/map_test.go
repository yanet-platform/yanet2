package cnat64s_test

import (
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/nat64s/bindings/go/cnat64s"
	nat64spb "github.com/yanet-platform/yanet2/modules/nat64s/controlplane/nat64spb/v1"
)

// finalizerSentinel is large enough that the allocator never shares its
// block with another object, which would leave its finalizer unrun.
type finalizerSentinel struct{ padding [64]byte }

// collectFinalizers runs collections until the finalizer queued behind
// them all has run, so every finalizer of an unreachable object has run
// too, several times over.
func collectFinalizers(t *testing.T) {
	t.Helper()

	for range 3 {
		done := make(chan struct{})
		sentinel := new(finalizerSentinel)
		runtime.SetFinalizer(sentinel, func(*finalizerSentinel) { close(done) })
		sentinel = nil
		runtime.GC()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("the finalizers did not run")
		}
	}
}

// Test_TimeoutMax_MatchesTheRequestBound verifies that the request
// boundary refuses exactly the timeouts the session state cannot hold.
func Test_TimeoutMax_MatchesTheRequestBound(t *testing.T) {
	require.Equal(t, uint32(nat64spb.MaxTimeout), uint32(cnat64s.TimeoutMax))
}

// Test_PortHiMax_MatchesTheRequestBound verifies that the request boundary
// refuses exactly the port windows the dataplane cannot hold.
func Test_PortHiMax_MatchesTheRequestBound(t *testing.T) {
	require.Equal(t, uint32(nat64spb.PortRangeHiMax), uint32(cnat64s.PortHiMax))
}

// Test_MTUv6Max_MatchesTheRequestBound verifies that the request boundary
// refuses exactly the IPv6 MTU limits the translation cannot carry.
func Test_MTUv6Max_MatchesTheRequestBound(t *testing.T) {
	require.Equal(t, uint32(nat64spb.MaxIPv6MTU), uint32(cnat64s.MTUv6Max))
}

// Test_MapObjectConfig_CreateMap_ReportsTheCauseOfARefusal verifies that
// a refused creation says why.
//
// A second creation finds the tables already there, and a worker count
// of zero is an argument the tables refuse.
func Test_MapObjectConfig_CreateMap_ReportsTheCauseOfARefusal(t *testing.T) {
	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(64 * datasize.MB),
		DPMemory:      uint64(4 * datasize.MB),
		WorkerCount:   1,
		ObjectsToLoad: []string{cnat64s.MapObjectType},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	agent, err := harness.SharedMemory().AgentAttach(
		"nat64s-binding-cause-test",
		0,
		16*datasize.MB,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	sizing := cnat64s.MapConfig{IndexSize: 1024, ExtraBucketCount: 64, WorkerCount: 1}
	created, err := cnat64s.NewMapObjectConfig(agent, "created")
	require.NoError(t, err)
	t.Cleanup(func() { _ = created.Free() })
	require.NoError(t, created.CreateMap(sizing))
	require.ErrorIs(t, created.CreateMap(sizing), syscall.EEXIST)

	zeroWorkers := cnat64s.MapConfig{IndexSize: 1024, ExtraBucketCount: 64}
	unworked, err := cnat64s.NewMapObjectConfig(agent, "unworked")
	require.NoError(t, err)
	t.Cleanup(func() { _ = unworked.Free() })
	require.ErrorIs(t, unworked.CreateMap(zeroWorkers), syscall.EINVAL)
}

// Test_MapObjectConfig_CreateMap_ReportsExhaustionByKind verifies that
// tables the agent's zone cannot hold are reported as the resource running
// out, the kind a publish maps to its status, with the allocator's own
// cause in the chain.
func Test_MapObjectConfig_CreateMap_ReportsExhaustionByKind(t *testing.T) {
	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(64 * datasize.MB),
		DPMemory:      uint64(4 * datasize.MB),
		WorkerCount:   1,
		ObjectsToLoad: []string{cnat64s.MapObjectType},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	agent, err := harness.SharedMemory().AgentAttach(
		"nat64s-binding-exhaustion-test",
		0,
		datasize.MB,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	created, err := cnat64s.NewMapObjectConfig(agent, "too-large")
	require.NoError(t, err)
	t.Cleanup(func() { _ = created.Free() })

	// More than the zone holds, and within every limit: about 4.5 MiB of
	// tables.
	tooLarge := cnat64s.MapConfig{
		IndexSize:        16384,
		ExtraBucketCount: 1024,
		WorkerCount:      1,
	}
	err = created.CreateMap(tooLarge)
	require.ErrorIs(t, err, ffi.ErrResourceExhausted)
	require.ErrorIs(t, err, syscall.ENOMEM)
}

// Test_MapObjectConfig_DroppedUnreleasedCollectsQuietly verifies that a
// map object handle nobody releases is merely leaked: once its agent is
// gone, nothing touches shared memory on the handle's behalf.
func Test_MapObjectConfig_DroppedUnreleasedCollectsQuietly(t *testing.T) {
	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(64 * datasize.MB),
		DPMemory:      uint64(4 * datasize.MB),
		WorkerCount:   1,
		ObjectsToLoad: []string{cnat64s.MapObjectType},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	agent, err := harness.SharedMemory().AgentAttach(
		"nat64s-binding-test",
		0,
		16*datasize.MB,
	)
	require.NoError(t, err)

	// Allocated and then dropped without being released.
	func() {
		object, err := cnat64s.NewMapObjectConfig(agent, "unreleased")
		require.NoError(t, err)
		_ = object
	}()

	// The agent goes away first; the handle is collected afterwards.
	require.NoError(t, agent.CleanUp())
	collectFinalizers(t)
}
