package nat64s_test

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/nat64s/bindings/go/cnat64s"
	"github.com/yanet-platform/yanet2/modules/nat64s/controlplane"
)

// newTestRegistry wires a map registry to an agent on a dataplane that has
// the session-map object type loaded. The agent's zone holds the
// default-sized tables of one map.
func newTestRegistry(t *testing.T) (*ffi.Agent, *nat64s.MapRegistry) {
	t.Helper()

	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(640 * datasize.MB),
		DPMemory:      uint64(4 * datasize.MB),
		WorkerCount:   1,
		ObjectsToLoad: []string{cnat64s.MapObjectType},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	agent, err := harness.SharedMemory().AgentAttach(
		"nat64s-registry-test",
		0,
		512*datasize.MB,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	return agent, nat64s.NewMapRegistry(agent)
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
