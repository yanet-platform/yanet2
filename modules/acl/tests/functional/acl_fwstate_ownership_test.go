package acl_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
)

// agentRootMemoryNode returns the named agent's own root memory-context
// node from its live dataplane snapshot.
//
// A cp_module's outer struct and everything it owns directly, of any
// module type sharing this agent, is freed against this root context, so
// its free count advances once and only once per config actually torn
// down, regardless of which module type triggered the teardown.
func agentRootMemoryNode(t *testing.T, agent *ffi.Agent, name string) ffi.AgentMemoryNode {
	t.Helper()

	for _, agentInfo := range agent.DPConfig().Agents() {
		if agentInfo.Name != name {
			continue
		}
		require.Lenf(t, agentInfo.Instances, 1, "agent %q: expected exactly one live instance", name)

		for _, node := range agentInfo.Instances[0].MemoryTree {
			if node.ParentIdx == math.MaxUint32 {
				return node
			}
		}
		t.Fatalf("agent %q: no root memory-context node in its snapshot", name)
	}

	t.Fatalf("agent %q not found in dataplane config", name)
	return ffi.AgentMemoryNode{}
}

// TestACL_FWStateOwnership_DeleteFWStateDoesNotFreeLinkedMaps is a
// regression test for issue #2003.
//
// Linking an ACL config to a fwstate config copies offsets into maps the
// fwstate config owns, plus a hold on that fwstate config. Only the last
// linked ACL config's own teardown may release the hold and free the maps.
//
// Each measurement is driven through a construction of the drained type, the
// only call that destroys a parked entry: a release alone only parks, so
// measuring right after one would show the delay, not the ownership.
func TestACL_FWStateOwnership_DeleteFWStateDoesNotFreeLinkedMaps(t *testing.T) {
	agent, backend := setupACLFWStateHarness(t)

	fwCfg, err := cfwstate.NewModuleConfig(agent, "fw0")
	require.NoError(t, err)
	require.NoError(t, fwCfg.CreateMaps(cfwstate.MapConfig{
		IndexSize:        1024,
		ExtraBucketCount: 64,
	}, 1))
	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{fwCfg.AsFFIModule()}))

	handle, err := backend.NewModule("acl0")
	require.NoError(t, err)
	rules := []cacl.AclRule{
		allow4Rule(
			filter.IPNets{filter.UnspecifiedIPv4},
			filter.IPNets{filter.UnspecifiedIPv4},
			udpProto,
		),
	}
	require.NoError(t, handle.UpdateRules(rules))
	handle.SetFwStateConfig(fwCfg.AsFFIModule())
	require.NoError(t, backend.UpdateModule(handle))

	beforeFWStateDelete := agentRootMemoryNode(t, agent, "acl")

	// Release fw0's own creator and publish references. ACL's own hold,
	// taken by SetFwStateConfig, is the only thing left standing between
	// fw0 and its teardown.
	fwCfg.Free()
	require.NoError(t, agent.DeleteModuleConfig("fwstate", "fw0"))

	// Construct another fwstate config: the only call that would drain
	// and destroy a parked fwstate entry. If ACL's hold were missing,
	// fw0 would already be parked by now and would be destroyed right
	// here, out from under the still-linked acl0.
	other, err := cfwstate.NewModuleConfig(agent, "fw1")
	require.NoError(t, err)
	t.Cleanup(other.Free)

	afterFWStateDelete := agentRootMemoryNode(t, agent, "acl")
	require.Equalf(
		t, beforeFWStateDelete.BFreeCount, afterFWStateDelete.BFreeCount,
		"deleting the linked fwstate config, plus a fwstate construction "+
			"able to drain it, must not free its maps while the linked "+
			"ACL config is still alive",
	)

	// Tear down the linked ACL config itself, then construct another ACL
	// config to drain and destroy it, which runs acl0's own destructor
	// and drops its hold on fw0.
	handle.Free()
	require.NoError(t, backend.DeleteModule("acl0"))

	other2, err := backend.NewModule("acl1")
	require.NoError(t, err)
	t.Cleanup(other2.Free)

	// Snapshot here, not before: acl0's own config block has already been
	// freed against the root context by the drain above, which would mask
	// the assertion below regardless of fw0's own fate. fw0 itself is
	// still only parked. A successful fwstate construction only allocates
	// against the root context, and its device link reallocates against
	// its own child context, so any further rise from here is
	// attributable solely to fw0 actually being reclaimed.
	beforeFWStateDrain := agentRootMemoryNode(t, agent, "acl")

	// Construct one more fwstate config to drain and destroy fw0, now
	// that nothing holds a reference on it.
	other3, err := cfwstate.NewModuleConfig(agent, "fw2")
	require.NoError(t, err)
	t.Cleanup(other3.Free)

	afterACLTeardown := agentRootMemoryNode(t, agent, "acl")
	require.Greaterf(
		t, afterACLTeardown.BFreeCount, beforeFWStateDrain.BFreeCount,
		"tearing down the last linked ACL config must let fw0's maps be "+
			"reclaimed",
	)
}
