package nat64s_test

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"testing"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/nat64s/bindings/go/cnat64s"
	"github.com/yanet-platform/yanet2/modules/nat64s/controlplane"
)

// newTestRegistry wires a map registry to an agent on a dataplane that has
// the session-map object type loaded.
//
// The maps are created small: the default sizing needs a block as large
// as the allocator's largest, which a sanitizer build cannot provide.
func newTestRegistry(t *testing.T) (*ffi.Agent, *nat64s.MapRegistry) {
	t.Helper()

	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(64 * datasize.MB),
		DPMemory:      uint64(4 * datasize.MB),
		WorkerCount:   1,
		ObjectsToLoad: []string{cnat64s.MapObjectType},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	agent, err := harness.SharedMemory().AgentAttach(
		"nat64s-registry-test",
		0,
		16*datasize.MB,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	return agent, nat64s.NewMapRegistry(agent, nat64s.WithRegistryMapSizing(
		cnat64s.MapConfig{IndexSize: 1024, ExtraBucketCount: 64},
	))
}

// Test_MapRegistry_DefaultSizing_CreatesTheProductionMap verifies that a
// registry left unsized creates the map production runs with, and that
// this map fits the zone the module's defaults give their agent.
//
// Sanitizer builds skip it: the default sizing needs a block as large as
// the allocator's largest, which a sanitizer build cannot provide.
func Test_MapRegistry_DefaultSizing_CreatesTheProductionMap(t *testing.T) {
	if asanBuild {
		t.Skip("the default tables need a block a sanitizer build cannot provide")
	}

	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(640 * datasize.MB),
		DPMemory:      uint64(4 * datasize.MB),
		WorkerCount:   1,
		ObjectsToLoad: []string{cnat64s.MapObjectType},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	agent, err := harness.SharedMemory().AgentAttach(
		"nat64s-default-registry-test",
		0,
		512*datasize.MB,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	registry := nat64s.NewMapRegistry(agent)
	before := agent.BlockAllocatorFreeSize()
	require.NoError(t, registry.Acquire("t0", "sessions"))
	used := before - agent.BlockAllocatorFreeSize()
	t.Logf("a default-sized map takes %s of its agent's zone", datasize.ByteSize(used))

	require.Less(t,
		used,
		uint64(nat64s.DefaultConfig().MemoryRequirements.Unwrap()),
		"the default sizing must fit the zone the module gives its agent",
	)

	releaseMap(t, registry, "t0", "sessions")
}

// Test_MapRegistry_Acquire_ReclaimsUnreferencedMapsBeforeAllocating
// verifies that a map a config asks for takes the room of the maps no
// config references anymore, whatever their names, instead of failing for
// want of room until the next sweep.
//
// The agent's zone is filled with maps, each held by its own config, until
// an allocation fails for want of memory: that is how many maps the zone
// holds, whatever the build makes of a map. Once every config lets go, the
// zone admits as many maps again under new names, and no more.
func Test_MapRegistry_Acquire_ReclaimsUnreferencedMapsBeforeAllocating(t *testing.T) {
	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(64 * datasize.MB),
		DPMemory:      uint64(4 * datasize.MB),
		WorkerCount:   1,
		ObjectsToLoad: []string{cnat64s.MapObjectType},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	agent, err := harness.SharedMemory().AgentAttach(
		"nat64s-reclaim-test",
		0,
		4*datasize.MB,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	registry := nat64s.NewMapRegistry(agent, nat64s.WithRegistryMapSizing(
		cnat64s.MapConfig{IndexSize: 1024, ExtraBucketCount: 64},
	))

	// Held maps, each by its own config, until the zone is full.
	var held []string
	for number := 0; ; number++ {
		name := fmt.Sprintf("held%d", number)
		err := registry.Acquire(fmt.Sprintf("t%d", number), name)
		if errors.Is(err, syscall.ENOMEM) {
			break
		}
		require.NoError(t, err)
		held = append(held, name)
	}
	require.GreaterOrEqual(t,
		len(held),
		2,
		"the zone must hold more than one map for the round to mean anything",
	)

	// Every config lets go: the zone admits as many maps again, since each
	// allocation takes the room of the maps no config references anymore.
	for number := range held {
		registry.Release(fmt.Sprintf("t%d", number), held[number])
	}
	for number := range held {
		require.NoError(t,
			registry.Acquire(
				fmt.Sprintf("fresh%d", number),
				fmt.Sprintf("fresh%d", number),
			),
			"the maps no config references must give the new ones their room",
		)
	}
	require.ErrorIs(t,
		registry.Acquire("one-too-many", "one-too-many"),
		syscall.ENOMEM,
		"the zone must have no room beyond the maps it holds",
	)

	for number := range held {
		registry.Release(
			fmt.Sprintf("fresh%d", number),
			fmt.Sprintf("fresh%d", number),
		)
	}
	registry.ReclaimUnreferenced()
}

// Test_MapRegistry_Acquire_ReplacesUnreferencedMap verifies that a map no
// config references anymore is never handed to the next one, while a
// config referencing the map it already holds keeps it.
//
// The tables of the released map hold the sessions of the config that
// used it, so the next config must get a map of its own.
func Test_MapRegistry_Acquire_ReplacesUnreferencedMap(t *testing.T) {
	_, registry := newTestRegistry(t)
	require.NoError(t, registry.Acquire("t0", "sessions"))
	used, err := registry.Ensure("sessions", 0)
	require.NoError(t, err)

	// The holder referencing the map again keeps the very same object.
	require.NoError(t, registry.Acquire("t0", "sessions"))
	kept, err := registry.Ensure("sessions", 0)
	require.NoError(t, err)
	require.Same(t, used, kept)

	registry.Release("t0", "sessions")

	require.NoError(t, registry.Acquire("t1", "sessions"))
	fresh, err := registry.Ensure("sessions", 0)
	require.NoError(t, err)
	require.NotSame(
		t,
		used,
		fresh,
		"a map another config used must be replaced",
	)

	releaseMap(t, registry, "t1", "sessions")
}

// Test_MapRegistry_RunSweeper_LeavesReplacedMapsAlone verifies that maps
// replaced while the sweeper runs are never touched by it.
//
// The sweeper and a replacing reference reach one map from two goroutines;
// without the registry's lock between them the race detector reports it.
func Test_MapRegistry_RunSweeper_LeavesReplacedMapsAlone(t *testing.T) {
	_, registry := newTestRegistry(t)

	ctx, cancel := context.WithCancel(t.Context())
	var sweeper errgroup.Group
	sweeper.Go(func() error {
		registry.RunSweeper(ctx, time.Millisecond)
		return nil
	})
	stopped := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = sweeper.Wait()
		close(stopped)
	}()
	join := func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			t.Fatal("the sweeper did not join before the shared memory goes away")
		}
		require.NoError(t, waitErr)
	}
	// Registered after the harness's own cleanups, so it runs ahead of
	// them: the sweeper is joined before the shared memory goes away,
	// on a failed assertion too.
	t.Cleanup(join)

	for range 300 {
		require.NoError(t, registry.Acquire("t0", "sessions"))
		registry.Release("t0", "sessions")
	}
	join()

	require.Empty(t, registry.MapOf("t0"))
	registry.ReclaimUnreferenced()
}

// releaseMap lets the config give its map back and frees the object at
// once, so that no map object outlives the agent.
func releaseMap(t *testing.T, registry *nat64s.MapRegistry, config, name string) {
	t.Helper()

	registry.Release(config, name)
	registry.ReclaimUnreferenced()
}

// Test_MapRegistry_Acquire_BindsConfigToItsMap verifies that referencing a
// map for a config creates the map and binds the config to it, which is
// what keeps a reclaim round from deleting an object a config is about to
// link.
func Test_MapRegistry_Acquire_BindsConfigToItsMap(t *testing.T) {
	_, registry := newTestRegistry(t)

	require.NoError(t, registry.Acquire("t0", "sessions"))
	require.Equal(t, "sessions", registry.MapOf("t0"))

	releaseMap(t, registry, "t0", "sessions")
	require.Empty(t, registry.MapOf("t0"))
}

// Test_MapRegistry_Acquire_RefusesSecondConfig verifies that a map bound
// to one config is refused for another: its tables hold the sessions of
// exactly one instance.
func Test_MapRegistry_Acquire_RefusesSecondConfig(t *testing.T) {
	_, registry := newTestRegistry(t)
	require.NoError(t, registry.Acquire("t0", "sessions"))

	err := registry.Acquire("t1", "sessions")
	require.ErrorIs(t, err, nat64s.ErrMapReserved)
	require.ErrorIs(t, err, ffi.ErrFailedPrecondition)

	// The refusal leaves the holder bound, and the map is free again
	// once the holder lets go.
	require.Equal(t, "sessions", registry.MapOf("t0"))
	require.Empty(t, registry.MapOf("t1"))
	releaseMap(t, registry, "t0", "sessions")

	require.NoError(t, registry.Acquire("t1", "sessions"))
	require.Equal(t, "sessions", registry.MapOf("t1"))

	releaseMap(t, registry, "t1", "sessions")
}

// Test_MapRegistry_ReclaimUnreferenced_WaitsForItsConfig verifies that a
// map outlives every reclaim round while its config holds it, and is
// deleted from the dataplane once the config lets go.
func Test_MapRegistry_ReclaimUnreferenced_WaitsForItsConfig(t *testing.T) {
	agent, registry := newTestRegistry(t)
	require.NoError(t, registry.Acquire("t0", "sessions"))
	created, err := registry.Ensure("sessions", 0)
	require.NoError(t, err)

	registry.ReclaimUnreferenced()
	kept, err := registry.Ensure("sessions", 0)
	require.NoError(t, err)
	require.Same(t, created, kept)

	registry.Release("t0", "sessions")
	registry.ReclaimUnreferenced()
	err = agent.DeleteObject(cnat64s.MapObjectType, "sessions")
	require.ErrorIs(t, err, ffi.ErrNotFound)
}
