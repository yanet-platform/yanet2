package pdump_test

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	testshm "github.com/yanet-platform/yanet2/common/go/testutils/shm"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	pdump "github.com/yanet-platform/yanet2/modules/pdump/controlplane"
	"github.com/yanet-platform/yanet2/modules/pdump/internal/testutils"
	ring "github.com/yanet-platform/yanet2/objects/ring/controlplane"
	ringpb "github.com/yanet-platform/yanet2/objects/ring/controlplane/ringpb/v1"
)

// newTestBackend returns a real one-worker backend with a published capture ring.
func newTestBackend(t *testing.T) (pdump.Backend, *ffi.Agent) {
	t.Helper()
	storage := testshm.NewStorage(t, testshm.StorageTypes{
		Modules: []string{"pdump"},
		Objects: []string{"ring"},
	})
	memory, err := ffi.AttachSharedMemory(storage)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, memory.Detach()) })
	require.NoError(t, testutils.PrepareWorker(memory))
	t.Cleanup(func() { testutils.FreeWorker(memory) })
	agent, err := memory.AgentAttach("pdump-backend-test", 0, 16*datasize.MB)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, agent.CleanUp()) })
	ringService := ring.NewRingService(agent)
	_, err = ringService.CreateRing(t.Context(), &ringpb.CreateRingRequest{
		Name:     "capture-ring",
		Capacity: uint64(pdump.MinRingCapacity),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := ringService.DeleteRing(t.Context(), &ringpb.DeleteRingRequest{Name: "capture-ring"})
		require.NoError(t, err)
	})
	return pdump.NewBackend(agent), agent
}

// Test_Backend_UpdateModule_PublishesCaptureRate verifies that production
// publication initializes the C bucket and replacement with zero is unlimited.
func Test_Backend_UpdateModule_PublishesCaptureRate(t *testing.T) {
	backend, agent := newTestBackend(t)
	modules := []pdump.Module{}
	t.Cleanup(func() {
		if len(modules) != 0 {
			assert.NoError(t, backend.DeleteModule("capture"))
		}
		for _, module := range modules {
			assert.NoError(t, module.Free())
		}
	})
	for _, rate := range []uint64{32001, 0} {
		module, err := backend.UpdateModule("capture", pdump.Settings{
			Filter:   "ip",
			Mode:     1,
			Snaplen:  128,
			RingName: "capture-ring",
			RatePPS:  rate,
		})
		require.NoError(t, err)
		modules = append(modules, module)
		snapshot, err := testutils.ReadRate(agent, "capture")
		require.NoError(t, err)
		want := testutils.RateSnapshot{}
		if rate != 0 {
			want = testutils.RateSnapshot{RatePPS: 32001, Cost: 31250, Cap: 1_031_250, HasBucket: true}
		}
		require.Equal(t, want, snapshot)
	}
}
