//go:build linux && amd64

package cpconfig_test

import (
	"strconv"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/nativetest"
	"github.com/yanet-platform/yanet2/modules/blackhole/bindings/go/cblackhole"
)

// Test_NewBlackhole_MatchesC verifies that building blackhole module
// configurations from Go, header, counter registry and its index, memory
// context and device link included, leaves the storage byte for byte as
// building them from C does.
func Test_NewBlackhole_MatchesC(t *testing.T) {
	for _, count := range []int{1, 20} {
		t.Run(strconv.Itoa(count)+" configurations", func(t *testing.T) {
			memory := nativetest.NewFixture(t, dataplaneut.Config{WorkerCount: 1, Modules: []string{"blackhole"}})
			cAgent, err := memory.Harness.SharedMemory().AgentAttach("owner", 0, 16*datasize.MB)
			require.NoError(t, err)
			owner, ok := agent.Find(memory.Instance(), "owner")
			require.True(t, ok)

			memory.CompareWithC(t,
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
