package cnat64s_test

import (
	"runtime"
	"testing"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/modules/nat64s/bindings/go/cnat64s"
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
