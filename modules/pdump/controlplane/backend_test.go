package pdump_test

import (
	"syscall"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/ffi/ffitest"
	cpdump "github.com/yanet-platform/yanet2/modules/pdump/bindings/go/cpdump"
	pdump "github.com/yanet-platform/yanet2/modules/pdump/controlplane"
	"github.com/yanet-platform/yanet2/modules/pdump/internal/testutils"
	"github.com/yanet-platform/yanet2/objects/ring/bindings/go/cring"
	ring "github.com/yanet-platform/yanet2/objects/ring/controlplane"
	ringpb "github.com/yanet-platform/yanet2/objects/ring/controlplane/ringpb/v1"
)

// adapts the real ring service's concrete lease to the capture contract
type realRingOwner struct {
	*ring.RingService
}

func (m *realRingOwner) Acquire(name string, handle pdump.RingHandle) (pdump.RingLease, error) {
	lease, err := m.RingService.Acquire(name, handle)
	if err != nil {
		return nil, err
	}
	return &realRingLease{Lease: lease}, nil
}

// exposes the leased ring's capacity and sources without changing ownership
type realRingLease struct {
	*ring.Lease
}

func (m *realRingLease) Capacity() uint32 { return m.Object().Capacity() }

func (m *realRingLease) Sources() ([]cring.RecordSource, error) {
	return m.Object().Sources()
}

// newTestBackend returns a real one-worker backend with a published capture ring.
func newTestBackend(t *testing.T) (pdump.Backend, *ffi.Agent, pdump.RingOwner) {
	t.Helper()
	storage := ffitest.NewStorage(t, ffitest.StorageTypes{
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
		Capacity: uint64(cpdump.MinRingCapacity),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := ringService.DeleteRing(t.Context(), &ringpb.DeleteRingRequest{Name: "capture-ring"})
		require.NoError(t, err)
	})
	return pdump.NewBackend(agent), agent, &realRingOwner{RingService: ringService}
}

// Test_Backend_UpdateModule_PublishesCaptureRate verifies that production
// publication anchors credit to worker time, rejects missing time without
// replacement, and permits an unlimited replacement before clock readiness.
func Test_Backend_UpdateModule_PublishesCaptureRate(t *testing.T) {
	backend, agent, _ := newTestBackend(t)
	modules := []pdump.Module{}
	t.Cleanup(func() {
		if len(modules) != 0 {
			assert.NoError(t, backend.DeleteModule("capture"))
		}
		for _, module := range modules {
			assert.NoError(t, module.Free())
		}
	})
	const timestamp = uint64(123_456_789)
	settings := pdump.Settings{
		Filter:   "ip",
		Mode:     1,
		Snaplen:  128,
		RingName: "capture-ring",
		RatePPS:  32001,
	}
	testutils.SetWorkerTime(agent, timestamp)
	module, err := backend.UpdateModule("capture", settings)
	require.NoError(t, err, "positive rate publishes with worker time")
	modules = append(modules, module)
	published, err := testutils.ReadRate(agent, "capture")
	require.NoError(t, err, "positive rate has a published snapshot")
	require.Equal(t, testutils.RateSnapshot{
		Cost: 31250, Cap: 1_031_250, EDT: timestamp, HasBucket: true,
	}, published,
		"fresh credit is anchored exactly to published worker time",
	)

	testutils.SetWorkerTime(agent, 0)
	module, err = backend.UpdateModule("capture", settings)
	require.ErrorIs(t, err, syscall.EAGAIN, "missing worker time rejects a positive replacement")
	require.Nil(t, module, "failed replacement returns no module handle")
	snapshot, err := testutils.ReadRate(agent, "capture")
	require.NoError(t, err, "failed replacement leaves the prior config published")
	require.Equal(t, published, snapshot, "failed replacement preserves the published snapshot")

	settings.RatePPS = 0
	module, err = backend.UpdateModule("capture", settings)
	require.NoError(t, err, "zero rate succeeds without worker time")
	modules = append(modules, module)
	snapshot, err = testutils.ReadRate(agent, "capture")
	require.NoError(t, err, "zero rate replacement is published")
	require.Equal(t, testutils.RateSnapshot{}, snapshot, "zero rate is unlimited without a bucket")
}
