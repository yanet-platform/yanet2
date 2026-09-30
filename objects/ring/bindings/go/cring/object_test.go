package cring_test

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/objects/ring/bindings/go/cring"
	ringpb "github.com/yanet-platform/yanet2/objects/ring/controlplane/ringpb/v1"
)

// newTestAgent creates a test dataplane with the ring object loaded and
// attaches one agent to it.
//
// Both are released when the test ends.
func newTestAgent(t testing.TB, workerCount uint64) *ffi.Agent {
	t.Helper()

	h, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(64 * datasize.MB),
		DPMemory:      uint64(4 * datasize.MB),
		WorkerCount:   workerCount,
		ObjectsToLoad: []string{"ring"},
	})
	require.NoError(t, err)
	t.Cleanup(h.Free)

	agent, err := h.SharedMemory().AgentAttach("ring-test", 0, 16*datasize.MB)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	return agent
}

// Test_Object_NewObject_RejectsBadParameters verifies that a bad or too
// large capacity, or a publish batch out of range, is an invalid argument.
//
// A service on top of this binding turns that error kind into a gRPC
// status.
func Test_Object_NewObject_RejectsBadParameters(t *testing.T) {
	agent := newTestAgent(t, 1)

	cases := []struct {
		name         string
		capacity     uint32
		publishBatch uint32
	}{
		{name: "not a power of two", capacity: 24, publishBatch: cring.DefaultPublishBatch},
		{name: "above allocator maximum", capacity: 1 << 27, publishBatch: cring.DefaultPublishBatch},
		{name: "zero publish batch", capacity: 64, publishBatch: 0},
		{name: "publish batch above maximum", capacity: 64, publishBatch: cring.MaxPublishBatch + 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cring.NewObject(agent, "bad-"+tc.name, tc.capacity, tc.publishBatch)
			require.Error(t, err)
			require.ErrorIs(t, err, cerrors.InvalidArgument)
		})
	}
}

// Test_Object_NewObject_KeepsPublishBatch verifies that the object reports
// the publish batch it was created with.
func Test_Object_NewObject_KeepsPublishBatch(t *testing.T) {
	agent := newTestAgent(t, 1)

	object, err := cring.NewObject(agent, "batch", 64, cring.MaxPublishBatch)
	require.NoError(t, err)
	t.Cleanup(func() { _ = object.Free() })

	require.Equal(t, cring.MaxPublishBatch, object.PublishBatch())
}

// Test_Object_Free_RefusedWhileReferenced verifies that a published object
// cannot be freed while its generation is live.
//
// The error says the object is still referenced.
func Test_Object_Free_RefusedWhileReferenced(t *testing.T) {
	agent := newTestAgent(t, 1)

	object, err := cring.NewObject(agent, "referenced", 64, cring.DefaultPublishBatch)
	require.NoError(t, err)
	require.NoError(t, object.Publish())

	require.ErrorIs(t, object.Free(), ffi.ErrStillReferenced)
}

// Test_Object_Exists_TracksPublishAndDelete verifies that Exists is true
// only between publishing and deleting the ring.
func Test_Object_Exists_TracksPublishAndDelete(t *testing.T) {
	agent := newTestAgent(t, 1)

	require.False(t, cring.Exists(agent, "maybe"))

	object, err := cring.NewObject(agent, "maybe", 64, cring.DefaultPublishBatch)
	require.NoError(t, err)
	require.False(t, cring.Exists(agent, "maybe"))

	require.NoError(t, object.Publish())
	require.True(t, cring.Exists(agent, "maybe"))

	require.NoError(t, cring.DeleteObject(agent, "maybe"))
	require.False(t, cring.Exists(agent, "maybe"))
}

// Test_Parity_MaxNameLen verifies that the C object-name limit equals the
// name limit that the proto package checks on its own.
func Test_Parity_MaxNameLen(t *testing.T) {
	require.Equal(t, ringpb.MaxRingNameLen, cring.MaxNameLen)
}

// Test_Parity_PublishBatch verifies that the C default and maximum publish
// batch equal the ones that the proto package checks on its own.
func Test_Parity_PublishBatch(t *testing.T) {
	require.Equal(t, uint32(ringpb.DefaultPublishBatch), cring.DefaultPublishBatch)
	require.Equal(t, uint32(ringpb.MaxPublishBatch), cring.MaxPublishBatch)
}

// Test_Object_Free_LeavesHandleInert verifies that every accessor of a freed
// handle returns zero or an error and does not touch the freed memory.
func Test_Object_Free_LeavesHandleInert(t *testing.T) {
	agent := newTestAgent(t, 1)

	object, err := cring.NewObject(agent, "freed", 64, cring.DefaultPublishBatch)
	require.NoError(t, err)
	require.NoError(t, object.Free())
	require.NoError(t, object.Free(), "a second Free must be a no-op")

	require.Zero(t, object.Capacity())
	require.Zero(t, object.PublishBatch())
	_, err = object.Sources()
	require.Error(t, err)
	_, err = object.OpenReaders()
	require.Error(t, err)
	require.Error(t, object.Publish())
}
