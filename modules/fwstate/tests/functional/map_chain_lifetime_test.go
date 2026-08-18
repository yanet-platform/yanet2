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
// name with sync enabled and 1024-entry maps.
//
// Unlike the package's shared setup helper, it returns the handle instead
// of freeing it at test end, because this test frees each generation
// itself, in the same order the production update path does.
func newMapChainConfig(t *testing.T, agent *ffi.Agent, name string) *cfwstate.ModuleConfig {
	t.Helper()

	syncConfig := cfwstate.SyncConfig{
		DstAddrMulticast: [16]byte{
			0xff, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01,
		},
		PortMulticast: syncPort,
		TcpSynAck:     uint64(120e9),
		TcpSyn:        uint64(120e9),
		TcpFin:        uint64(120e9),
		Tcp:           uint64(120e9),
		Udp:           uint64(30e9),
		Default:       uint64(16e9),
	}

	modCfg, err := cfwstate.NewModuleConfig(
		agent,
		name,
		nil,
		&syncConfig,
		cfwstate.MapConfig{
			IndexSize:        1024,
			ExtraBucketCount: 64,
		},
		1,
	)
	require.NoError(t, err)

	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{modCfg.AsFFIModule()}))

	return modCfg
}

// TestFWStateUpdate_SecondUpdateKeepsLiveMaps pins the map-chain ownership
// contract: a state entry survives a republish under the same name.
//
// Republishing propagates the live map chain into the new config and detaches
// it from the old one before releasing it. Between the publish and that
// detach-then-free, it constructs a module of this type on an unrelated name,
// modeling a concurrent update racing this one. The first generation still
// holds its own creator reference at that point, so the unrelated construction
// drains nothing of it. A release never drains by itself; only a further
// construction after the detach-then-free drains the park list.
func TestFWStateUpdate_SecondUpdateKeepsLiveMaps(t *testing.T) {
	h, agent := setupFWStateHarness(t)
	shm := h.SharedMemory()
	const agentName = "fwstate-test"

	v1 := newMapChainConfig(t, agent, "fw0")

	// Insert directly into the first generation's live map rather than driving a
	// real sync packet.
	//
	// This package never runs a worker round, and a second publish after one has
	// run would otherwise block forever waiting for a round nothing here drives.
	srcAddr := [16]byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01}
	dstAddr := [16]byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x02}
	require.NoError(t, insertFWStateIPv6Entry(
		v1.AsFFIModule().AsRawPtr(), 1, srcAddr, dstAddr, 1234, 80,
	))

	statsBefore := v1.GetMapsStats()
	require.Equal(
		t, uint64(1), statsBefore.IPv6.TotalElements,
		"one state entry must be live before the second update",
	)

	entriesBefore, _, _, err := v1.ReadForward(true, 0, 0, true, 1, 10)
	require.NoError(t, err)
	require.Len(t, entriesBefore, 1)
	keyBefore := entriesBefore[0].Key

	v2, err := cfwstate.NewModuleConfig(
		agent,
		"fw0",
		v1,
		nil,
		cfwstate.MapConfig{
			IndexSize:        1024,
			ExtraBucketCount: 64,
		},
		1,
	)
	require.NoError(t, err)
	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{v2.AsFFIModule()}))

	afterPublish := mapChainAgentRootMemoryNode(t, shm, agentName)

	// Construct a module of this type on an unrelated name, the only call that
	// drains the park list.
	//
	// The construction is straddled by a checkpoint, so a premature drain of the
	// first generation there cannot be masked by that config's own later release
	// landing on the same count.
	// Zero worker count: an unmapped config, the same footprint the
	// pre-merge construction used to leave.
	other, err := cfwstate.NewModuleConfig(agent, "fw1", nil, nil, cfwstate.MapConfig{}, 0)
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

	v1.DetachMaps()
	v1.Free()
	t.Cleanup(v2.Free)

	afterV1Free := mapChainAgentRootMemoryNode(t, shm, agentName)
	require.Equalf(
		t, afterUnrelatedFree.BFreeCount, afterV1Free.BFreeCount,
		"releasing v1 must park it, not destroy it immediately",
	)

	// Construct a module of this type once more, draining both the unrelated
	// config and the first generation.
	//
	// This is the exact call that would have destroyed its map chain out from
	// under the new generation, had the earlier detach not already run.
	other2, err := cfwstate.NewModuleConfig(agent, "fw2", nil, nil, cfwstate.MapConfig{}, 0)
	require.NoError(t, err)
	t.Cleanup(other2.Free)

	afterDrain := mapChainAgentRootMemoryNode(t, shm, agentName)
	require.Equalf(
		t, afterV1Free.BFreeCount+2, afterDrain.BFreeCount,
		"fwstate's next construction must drain both the unrelated config and v1",
	)

	statsAfter := v2.GetMapsStats()
	require.Equalf(
		t, statsBefore.IPv6.TotalElements, statsAfter.IPv6.TotalElements,
		"the entry inserted before the second update must still be live "+
			"in the maps the new config took over, not a fresh empty map",
	)

	entriesAfter, _, _, err := v2.ReadForward(true, 0, 0, true, 1, 10)
	require.NoError(t, err)
	require.Len(t, entriesAfter, 1)
	require.Equalf(
		t, keyBefore, entriesAfter[0].Key,
		"the surviving entry must be the exact one inserted before the "+
			"update, not a coincidentally-sized fresh map",
	)
}
