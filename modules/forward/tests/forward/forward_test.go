package test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
)

func TestForwardConfigMemoryLeak(t *testing.T) {
	// Here is the MAGIC:
	//
	// We use 8 rules so that the targets array size difference between
	// sizeof(forward_target) * 8 = 192 and sizeof(forward_target*) * 8 = 64
	// lands in different block allocator's pools even with ASAN red zones,
	// which are +128 bytes.
	//
	// With fewer rules both sizes round up to the same power-of-2 pool and
	// the leak is invisible to "block_allocator_free_size".
	rules := make([]cforward.ForwardRule, 8)
	for idx := range rules {
		rules[idx] = cforward.ForwardRule{
			Target:  defaultDeviceName,
			Mode:    cforward.ModeIn,
			Counter: fmt.Sprintf("rule%d", idx),
		}
	}

	setup, err := SetupTest(&TestConfig{
		rules: rules,
	})
	require.NoError(t, err)
	defer setup.Free()

	freeSizeB := setup.agent.BlockAllocatorFreeSize()

	// Create a second config generation to replace the first.
	moduleB, err := cforward.NewModuleConfig(setup.agent, defaultConfigName)
	require.NoError(t, err)

	err = moduleB.Update(rules)
	require.NoError(t, err)

	err = setup.agent.UpdateModules([]ffi.ModuleConfig{moduleB.AsFFIModule()})
	require.NoError(t, err)

	// Free the old config.
	//
	// The memory allocated by the module must be returned to the block
	// allocator.
	setup.module.Free()

	freeSizeA := setup.agent.BlockAllocatorFreeSize()
	require.Equal(t, freeSizeB, freeSizeA)
}
