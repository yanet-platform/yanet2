package agent_test

import (
	"bytes"
	"testing"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// rawSharedMemory mirrors the C handle struct yanet_shm.
type rawSharedMemory struct {
	base unsafe.Pointer
	size uintptr
}

// fixture is an in-process dataplane with its storage seen both through
// the cgo control plane and the native one.
type fixture struct {
	harness *dataplaneut.Harness
	image   []byte
	storage *zone.Storage
}

// newFixture returns a one-worker dataplane harness and its storage.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:    uint64(64 * datasize.MB),
		DPMemory:    uint64(4 * datasize.MB),
		WorkerCount: 1,
		Modules:     []string{"blackhole"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)
	raw := (*rawSharedMemory)(harness.SharedMemory().AsRawPtr())
	image := unsafe.Slice((*byte)(raw.base), raw.size)
	storage, err := zone.FromMemory(image)
	require.NoError(t, err)
	return &fixture{harness: harness, image: image, storage: storage}
}

// compareWithC runs an operation through C, keeps the image it leaves,
// restores the image from before it and runs the operation through Go:
// both start from the same bytes at the same addresses, so they must end
// at the same bytes.
func compareWithC(t *testing.T, memory *fixture, viaC, viaGo func()) {
	t.Helper()
	before := bytes.Clone(memory.image)
	viaC()
	want := bytes.Clone(memory.image)
	copy(memory.image, before)
	viaGo()
	if bytes.Equal(want, memory.image) {
		return
	}
	for idx := range want {
		if want[idx] != memory.image[idx] {
			t.Fatalf("storage differs first at offset %#x: %#x, want %#x", idx, memory.image[idx], want[idx])
		}
	}
}

// Test_Attach_MatchesC verifies that attaching an agent from Go leaves
// the storage byte for byte as attaching it from C does, for a new agent
// and for one superseding an agent of the same name.
func Test_Attach_MatchesC(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, memory *fixture)
	}{
		{"new agent", func(*testing.T, *fixture) {}},
		{"superseding agent", func(t *testing.T, memory *fixture) {
			_, err := memory.harness.SharedMemory().AgentAttach("probe", 0, 2*datasize.MB)
			require.NoError(t, err)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := newFixture(t)
			tc.setup(t, memory)
			compareWithC(t, memory,
				func() {
					_, err := memory.harness.SharedMemory().AgentAttach("probe", 0, 8*datasize.MB)
					require.NoError(t, err)
				},
				func() {
					_, err := agent.Attach(memory.storage.Instances[0], "probe", uint64(8*datasize.MB))
					require.NoError(t, err)
				},
			)
		})
	}
}

// Test_Attach_SharesRegistryWithC verifies that an agent Go attached is
// listed by the cgo control plane with its name, process and memory.
func Test_Attach_SharesRegistryWithC(t *testing.T) {
	memory := newFixture(t)
	attached, err := agent.Attach(memory.storage.Instances[0], "native", uint64(8*datasize.MB))
	require.NoError(t, err)
	var found bool
	for _, info := range memory.harness.SharedMemory().DPConfig(0).Agents() {
		if info.Name != "native" {
			continue
		}
		found = true
		require.Equal(t, uint32(attached.Agent.Pid), info.Instances[0].PID)
		require.Equal(t, uint64(8*datasize.MB), info.Instances[0].MemoryLimit)
	}
	require.True(t, found, "the cgo control plane does not list the native agent")
}
