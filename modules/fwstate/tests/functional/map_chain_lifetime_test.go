package fwstate_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
)

// mapChainAgentRootMemoryNode returns the named agent's own root
// memory-context node.
//
// A config's outer structure is allocated and freed directly against this
// root context, so its free count advances once and only once per config
// actually torn down.
func mapChainAgentRootMemoryNode(t *testing.T, shm *ffi.SharedMemory, name string) ffi.AgentMemoryNode {
	t.Helper()

	for _, agentInfo := range shm.DPConfig(0).Agents() {
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

// newMapChainConfig builds and publishes an fwstate config under the given
// name, linked to the given published map objects.
//
// Unlike the package's shared setup helper, it returns the handle instead
// of freeing it at test end, because this test frees each generation
// itself, in the same order the production update path does.
func newMapChainConfig(
	t *testing.T,
	agent *ffi.Agent,
	name, fw4MapName, fw6MapName string,
) *cfwstate.ModuleConfig {
	t.Helper()

	syncConfig := fwstateTestSyncConfig()
	modCfg, err := cfwstate.NewModuleConfig(
		agent, name, &syncConfig, fw4MapName, fw6MapName,
	)
	require.NoError(t, err)

	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{modCfg.AsFFIModule()}))

	return modCfg
}

// TestFWStateUpdate_SecondUpdateKeepsLiveMaps pins the map-chain ownership
// contract: a state entry survives a republish under the same name.
//
// The map objects own the tables, so republishing the module config keeps
// the linked objects' chains and the entry in them. Between the publish
// and the old generation's free, it constructs a module of this type on
// an unrelated name, modeling a concurrent update racing this one. The
// first generation still holds its own creator reference at that point,
// so the unrelated construction drains nothing of it. A release never
// drains by itself; only a further construction after the free drains
// the park list.
func TestFWStateUpdate_SecondUpdateKeepsLiveMaps(t *testing.T) {
	h, agent := setupFWStateHarness(t)
	shm := h.SharedMemory()
	const agentName = "fwstate-test"

	mapV4, mapV6 := newPublishedFWStateMaps(t, agent, "maps", 1024)

	v1 := newMapChainConfig(t, agent, "fw0", mapV4.Name(), mapV6.Name())

	// Insert directly into the map object's live layer rather than driving a
	// real sync packet.
	//
	// This package never runs a worker round, and a second publish after one
	// has run would otherwise block forever waiting for a round nothing here
	// drives.
	srcAddr := [16]byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01}
	dstAddr := [16]byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x02}
	require.NoError(t, insertFWStateIPv6Entry(
		mapV6.ResolveMap(0), 1, srcAddr, dstAddr, 1234, 80,
	))

	statsBefore := mapV6.GetStats()
	require.Equal(
		t, uint64(1), statsBefore.TotalElements,
		"one state entry must be live before the second update",
	)

	entriesBefore, _, _, err := mapV6.ReadForward(0, 0, true, 1, 10)
	require.NoError(t, err)
	require.Len(t, entriesBefore, 1)
	keyBefore := entriesBefore[0].Key

	syncConfig := fwstateTestSyncConfig()
	v2, err := cfwstate.NewModuleConfig(
		agent, "fw0", &syncConfig, mapV4.Name(), mapV6.Name(),
	)
	require.NoError(t, err)
	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{v2.AsFFIModule()}))

	afterPublish := mapChainAgentRootMemoryNode(t, shm, agentName)

	// Construct a module of this type on an unrelated name, the only call that
	// drains the park list.
	//
	// The construction is straddled by a checkpoint, so a premature drain of
	// the first generation there cannot be masked by that config's own later
	// release landing on the same count.
	other, err := cfwstate.NewModuleConfig(agent, "fw1", nil, "", "")
	require.NoError(t, err)

	afterUnrelatedCreate := mapChainAgentRootMemoryNode(t, shm, agentName)
	require.Equalf(
		t, afterPublish.BFreeCount, afterUnrelatedCreate.BFreeCount,
		"creating the unrelated config must not destroy anything by itself",
	)

	other.Free()

	afterUnrelatedFree := mapChainAgentRootMemoryNode(t, shm, agentName)
	require.Equalf(
		t, afterUnrelatedCreate.BFreeCount, afterUnrelatedFree.BFreeCount,
		"releasing the unrelated config must park it, not destroy it immediately",
	)

	v1.Free()
	t.Cleanup(v2.Free)

	afterV1Free := mapChainAgentRootMemoryNode(t, shm, agentName)
	require.Equalf(
		t, afterUnrelatedFree.BFreeCount, afterV1Free.BFreeCount,
		"releasing v1 must park it, not destroy it immediately",
	)

	// Construct a module of this type once more, draining both the unrelated
	// config and the first generation.
	other2, err := cfwstate.NewModuleConfig(agent, "fw2", nil, "", "")
	require.NoError(t, err)
	t.Cleanup(other2.Free)

	afterDrain := mapChainAgentRootMemoryNode(t, shm, agentName)
	require.Equalf(
		t, afterV1Free.BFreeCount+2, afterDrain.BFreeCount,
		"fwstate's next construction must drain both the unrelated config and v1",
	)

	statsAfter := mapV6.GetStats()
	require.Equalf(
		t, statsBefore.TotalElements, statsAfter.TotalElements,
		"the entry inserted before the second update must still be live "+
			"in the map objects the new config relinked to, not a fresh empty map",
	)

	entriesAfter, _, _, err := mapV6.ReadForward(0, 0, true, 1, 10)
	require.NoError(t, err)
	require.Len(t, entriesAfter, 1)
	require.Equalf(
		t, keyBefore, entriesAfter[0].Key,
		"the surviving entry must be the exact one inserted before the "+
			"update, not a coincidentally-sized fresh map",
	)
}
