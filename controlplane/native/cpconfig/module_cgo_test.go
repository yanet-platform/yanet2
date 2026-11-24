//go:build linux && (amd64 || arm64) && cgo

package cpconfig_test

import (
	"errors"
	"strconv"
	"testing"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
	"github.com/yanet-platform/yanet2/modules/blackhole/bindings/go/cblackhole"
)

// attachOwner attaches an agent of that name through the cgo control
// plane and returns its cgo handle and its native view.
func attachOwner(t testing.TB, memory *nativetest.Fixture, name string) (*ffi.Agent, *agent.Agent) {
	t.Helper()
	attached, err := memory.Harness.SharedMemory().AgentAttach(name, 0, 16*datasize.MB)
	require.NoError(t, err)
	return attached, &agent.Agent{Instance: memory.Instance(), Agent: (*layout.Agent)(attached.AsRawPtr())}
}

// Test_NewBlackhole_MatchesC verifies that blackhole configurations built
// from Go leave the storage as building them from C does.
//
// The build covers the header, the counter registry and its index, the
// memory context and the device link.
func Test_NewBlackhole_MatchesC(t *testing.T) {
	for _, count := range []int{1, 20} {
		t.Run(strconv.Itoa(count)+" configurations", func(t *testing.T) {
			memory := nativetest.NewFixture(t, dataplaneut.Config{WorkerCount: 1, Modules: []string{"blackhole"}})
			cAgent, owner := attachOwner(t, memory, "owner")

			memory.CompareWithC(
				t,
				func() {
					for idx := range count {
						_, err := cblackhole.NewModuleConfig(cAgent, "blackhole"+strconv.Itoa(idx))
						require.NoError(t, err)
					}
				},
				func() {
					for idx := range count {
						_, err := cpconfig.NewBlackhole(owner, "blackhole"+strconv.Itoa(idx))
						require.NoError(t, err)
					}
				},
			)
		})
	}
}

// Test_NewBlackhole_FailureFreesEverything verifies that a blackhole
// configuration that cannot be built gives its agent back every byte.
//
// The header and whatever it took are returned and its memory context is
// unlinked from the agent's.
func Test_NewBlackhole_FailureFreesEverything(t *testing.T) {
	cases := []struct {
		name    string
		modules []string
		// setup leaves the agent's allocator as the case needs it.
		setup func(t *testing.T, owner *agent.Agent)
		// exhausted marks a build the agent's memory ran out for.
		exhausted bool
	}{
		{
			name:    "dataplane without the blackhole module",
			modules: []string{"forward"},
			setup:   func(*testing.T, *agent.Agent) {},
		},
		{
			name:    "agent memory exhausted after the header",
			modules: []string{"blackhole"},
			setup: func(t *testing.T, owner *agent.Agent) {
				nativetest.LeaveOnly(t, &owner.Agent.BlockAllocator, unsafe.Sizeof(layout.CpModule{}))
			},
			exhausted: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := nativetest.NewFixture(t, dataplaneut.Config{WorkerCount: 1, Modules: tc.modules})
			_, owner := attachOwner(t, memory, "owner")
			tc.setup(t, owner)
			free := shmalloc.FreeSize(&owner.Agent.BlockAllocator)

			_, err := cpconfig.NewBlackhole(owner, "probe")

			require.Error(t, err)
			require.Equal(t, tc.exhausted, errors.Is(err, shmalloc.ErrNoMemory))
			require.Equal(t, free, shmalloc.FreeSize(&owner.Agent.BlockAllocator), "the agent's free bytes")
			require.True(t, owner.Agent.MemoryContext.FirstChild.IsNil(), "a failed configuration leaves no memory context")
		})
	}
}
