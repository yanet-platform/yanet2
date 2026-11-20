//go:build linux && (amd64 || arm64)

package cpconfig_test

import (
	"testing"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
)

// Test_ReleaseGeneration_FreesAGenerationRetiredWhilePinned verifies that
// releasing the pin of a generation a publication retired meanwhile
// frees the generation, and with it its hold on the module configuration
// it published.
func Test_ReleaseGeneration_FreesAGenerationRetiredWhilePinned(t *testing.T) {
	memory := newPublishedFixture(t)
	instance := memory.Instance()
	deliver := cpconfig.WithDelivery(nativetest.Deliver)
	module, err := cpconfig.NewBlackhole(memory.owner, "d")
	require.NoError(t, err)
	require.NoError(t, cpconfig.UpdateModules(instance, []*layout.CpModule{module}, deliver))

	pinned := instance.Pin()
	replacement, err := cpconfig.NewBlackhole(memory.owner, "d")
	require.NoError(t, err)
	require.NoError(t, cpconfig.UpdateModules(instance, []*layout.CpModule{replacement}, deliver))
	size := unsafe.Sizeof(layout.CpModule{})
	require.ErrorIs(t, cpconfig.DestroyModule(module, size, nil), cpconfig.ErrStillReferenced,
		"the pinned generation holds the replaced module",
	)

	cpconfig.ReleaseGeneration(instance.Cp, pinned)
	require.Nil(t, pinned.Gen)
	require.NoError(t, cpconfig.DestroyModule(module, size, nil))
}

// Test_ReleaseGeneration_KeepsThePublishedGeneration verifies that
// releasing the pin of the generation still published drops only the
// pin: the publication keeps its reference.
func Test_ReleaseGeneration_KeepsThePublishedGeneration(t *testing.T) {
	memory := newPublishedFixture(t)
	instance := memory.Instance()
	published := instance.Cp.CpConfigGen.Get()
	references := published.Refcnt

	cpconfig.ReleaseGeneration(instance.Cp, instance.Pin())
	require.Same(t, published, instance.Cp.CpConfigGen.Get())
	require.Equal(t, references, published.Refcnt)
}

// Test_ReleaseGeneration_ReclaimsAnAgentSupersededWhilePinned verifies
// that the release freeing the last generation an agent superseded
// meanwhile published in reclaims the agent, as cp_config_gen_release
// does.
func Test_ReleaseGeneration_ReclaimsAnAgentSupersededWhilePinned(t *testing.T) {
	memory := newPublishedFixture(t)
	instance := memory.Instance()
	deliver := cpconfig.WithDelivery(nativetest.Deliver)
	superseded := func() int {
		for _, entry := range instance.Agents() {
			if entry.Name == "restarting" {
				return entry.Superseded
			}
		}
		return 0
	}
	owner, err := agent.Attach(instance, "restarting", uint64(4*datasize.MB))
	require.NoError(t, err)
	module, err := cpconfig.NewBlackhole(owner, "r")
	require.NoError(t, err)
	require.NoError(t, cpconfig.UpdateModules(instance, []*layout.CpModule{module}, deliver))

	pinned := instance.Pin()
	successor, err := agent.Attach(instance, "restarting", uint64(4*datasize.MB))
	require.NoError(t, err)
	replacement, err := cpconfig.NewBlackhole(successor, "r")
	require.NoError(t, err)
	require.NoError(t, cpconfig.UpdateModules(instance, []*layout.CpModule{replacement}, deliver))
	require.Equal(t, 1, superseded(), "the pinned generation holds the superseded agent's module")

	cpconfig.ReleaseGeneration(instance.Cp, pinned)
	require.Equal(t, 0, superseded())
}
