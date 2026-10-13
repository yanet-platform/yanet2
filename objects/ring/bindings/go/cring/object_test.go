package cring_test

import (
	"strings"
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

// Test_Object_NewObject_ValidatesName verifies that a name C would cut is an
// invalid argument and that the longest name that fits is accepted.
func Test_Object_NewObject_ValidatesName(t *testing.T) {
	agent := newTestAgent(t, 1)

	cases := []struct {
		name    string
		ring    string
		wantErr bool
	}{
		{name: "empty", ring: "", wantErr: true},
		{name: "embedded NUL", ring: "ring\x00tail", wantErr: true},
		{name: "overlong", ring: strings.Repeat("r", cring.MaxNameLen), wantErr: true},
		{name: "longest", ring: strings.Repeat("r", cring.MaxNameLen-1)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			object, err := cring.NewObject(agent, tc.ring, 64, cring.DefaultPublishBatch)
			if tc.wantErr {
				require.ErrorIs(t, err, cerrors.InvalidArgument)
				return
			}
			require.NoError(t, err)
			require.NoError(t, object.Free())
		})
	}
}

// Test_Object_MalformedNameNeverReachesAnotherRing verifies that a name C
// would cut neither finds nor deletes the ring named by its prefix.
func Test_Object_MalformedNameNeverReachesAnotherRing(t *testing.T) {
	agent := newTestAgent(t, 1)
	prefix := strings.Repeat("r", cring.MaxNameLen-1)
	for _, name := range []string{"capture", prefix} {
		newRingObject(t, agent, name, 64)
	}

	for _, name := range []string{"capture\x00other", prefix + "x"} {
		require.False(t, cring.Exists(agent, name), "%q", name)
		require.ErrorIs(t, cring.DeleteObject(agent, name), cerrors.InvalidArgument, "%q", name)
	}
	require.True(t, cring.Exists(agent, "capture"))
	require.True(t, cring.Exists(agent, prefix))
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

// Test_Parity_Constants verifies that the C name limit, record frame size and
// publish batch bounds equal the ones the proto package checks on its own.
func Test_Parity_Constants(t *testing.T) {
	require.Equal(t, ringpb.MaxRingNameLen, cring.MaxNameLen)
	require.Equal(t, uint32(ringpb.MinRingCapacity), cring.RecordFrameSize)
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
