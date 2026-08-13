package acl_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
)

// setupACLFWStateHarness builds a harness with acl, forward, and fwstate
// loaded and attaches one agent to all three, mirroring how the acl module
// wires its own agent to the fwstate service in production.
func setupACLFWStateHarness(tb testing.TB) (*ffi.Agent, acl.Backend) {
	tb.Helper()

	cfg := dataplaneut.Config{
		CPMemory:      uint64(aclCPSize),
		DPMemory:      uint64(aclDPSize),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		Modules:       []string{"acl", "forward", "fwstate"},
		DevicesToLoad: []string{"plain"},
	}
	h, err := dataplaneut.NewHarness(cfg)
	require.NoError(tb, err)
	tb.Cleanup(h.Free)

	shm := h.SharedMemory()
	agent, err := shm.AgentAttach("acl", 0, aclMemSize)
	require.NoError(tb, err)
	tb.Cleanup(func() { _ = agent.CleanUp() })

	backend := acl.NewBackend(agent, uint64(aclMemSize))
	return agent, backend
}

// aclSharedAgentRootMemoryNode returns the shared "acl" agent's own root
// memory-context node.
//
// A parked entry's own stored teardown frees its outer struct directly
// against this root context, so BFreeCount/BFreeSize advance once and only
// once per real teardown, unlike the live-module count, which never
// observes a park.
func aclSharedAgentRootMemoryNode(t *testing.T, agent *ffi.Agent) ffi.AgentMemoryNode {
	t.Helper()

	const agentName = "acl"
	for _, agentInfo := range agent.DPConfig().Agents() {
		if agentInfo.Name != agentName {
			continue
		}
		require.Lenf(t, agentInfo.Instances, 1, "agent %q: expected exactly one live instance", agentName)

		for _, node := range agentInfo.Instances[0].MemoryTree {
			if node.ParentIdx == math.MaxUint32 {
				return node
			}
		}
		t.Fatalf("agent %q: no root memory-context node in its snapshot", agentName)
	}

	t.Fatalf("agent %q not found in dataplane config", agentName)
	return ffi.AgentMemoryNode{}
}

// TestACL_FWStateAgentSharing_UnrelatedUpdateReclaimsParkedModule pins the
// type-agnostic drain contract: constructing an access-control module on
// the shared agent reclaims a parked firewall-state entry through that
// entry's own stored teardown, not just entries of the constructing type.
//
// The acl module attaches one agent and hands it to both the ACL and
// fwstate services, so this is the only place in the tree where a drain
// actually crosses module types. fw0 is built with real maps, then
// detached before release the way the fwstate service detaches a
// superseded config's maps: leaving them attached instead would trip the
// separate, already-filed map-ownership gap (#2003), which this test is
// not about. A release alone must not run fw0's teardown; only the later
// ACL construction, sharing the agent but not fw0's type, does.
func TestACL_FWStateAgentSharing_UnrelatedUpdateReclaimsParkedModule(t *testing.T) {
	agent, backend := setupACLFWStateHarness(t)

	fwCfg, err := cfwstate.NewModuleConfig(agent, "fw0")
	require.NoError(t, err)
	require.NoError(t, fwCfg.CreateMaps(cfwstate.MapConfig{
		IndexSize:        1024,
		ExtraBucketCount: 64,
	}, 1))
	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{fwCfg.AsFFIModule()}))

	// Detach fw0's maps before releasing it, mirroring the fwstate
	// service's own supersede path, then release the creator's reference
	// while the published generation still holds fw0: it must not park
	// yet.
	fwCfg.DetachMaps()
	fwCfg.Free()

	// Retiring the generation that still references fw0 is what actually
	// parks it.
	require.NoError(t, agent.DeleteModuleConfig("fwstate", "fw0"))

	afterPark := aclSharedAgentRootMemoryNode(t, agent)
	require.Equalf(
		t, uint64(0), afterPark.BFreeCount,
		"parking fw0 must not itself run its teardown",
	)

	// An ACL construction on the shared agent is the only call able to
	// reach fw0 next: it shares the agent but not fw0's module type.
	rules := []cacl.AclRule{
		allow4Rule(
			filter.IPNets{filter.UnspecifiedIPv4},
			filter.IPNets{filter.UnspecifiedIPv4},
			udpProto,
		),
	}
	handle := applyACLRules(t, backend, "acl0", rules)
	require.NotNil(t, handle)

	afterACLUpdate := aclSharedAgentRootMemoryNode(t, agent)
	require.Equalf(
		t, afterPark.BFreeCount+1, afterACLUpdate.BFreeCount,
		"the ACL construction must drain fw0 through its own stored teardown",
	)
	require.Greaterf(
		t, afterACLUpdate.BFreeSize, afterPark.BFreeSize,
		"fw0's teardown must have reclaimed its own bytes, not merely "+
			"emptied the parked list",
	)
}
