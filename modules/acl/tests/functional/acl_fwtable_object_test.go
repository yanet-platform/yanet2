package acl_test

import (
	"net"
	"testing"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
	"github.com/yanet-platform/yanet2/modules/acl/tests/functional/fwtable"
	objfwstate "github.com/yanet-platform/yanet2/objects/fwstate/bindings/go/cfwstate"
)

// setupACLFWTableHarness builds a harness with the fwstate-map object
// types registered so acl configs can link them by name.
func setupACLFWTableHarness(tb testing.TB) (*dataplaneut.Harness, *ffi.Agent, acl.Backend) {
	return setupACLFWTableHarnessSized(tb, aclMemSize)
}

// setupACLFWTableHarnessSized is the fwtable harness with an explicit
// agent memory budget, for scenarios holding several ACL module configs
// alive at once. The control-plane zone grows to match, because the
// agent arena is carved out of it.
func setupACLFWTableHarnessSized(
	tb testing.TB, agentMemory datasize.ByteSize,
) (*dataplaneut.Harness, *ffi.Agent, acl.Backend) {
	tb.Helper()

	cpMemory := aclCPSize
	if agentMemory > aclMemSize {
		cpMemory = agentMemory * 4
	}

	cfg := dataplaneut.Config{
		CPMemory:      uint64(cpMemory),
		DPMemory:      uint64(aclDPSize),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		Modules:       []string{"acl", "forward"},
		DevicesToLoad: []string{"plain"},
		ObjectsToLoad: []string{"fwstate_map_v4", "fwstate_map_v6"},
	}
	h, err := dataplaneut.NewHarness(cfg)
	require.NoError(tb, err)
	tb.Cleanup(h.Free)

	shm := h.SharedMemory()
	agent, err := shm.AgentAttach("acl-fwtable-test", 0, agentMemory)
	require.NoError(tb, err)
	tb.Cleanup(func() { _ = agent.CleanUp() })

	backend := acl.NewBackend(agent)
	return h, agent, backend
}

// checkStateDeny4Rule builds a rule that allows a packet only when its
// reverse direction has state in the linked fwtable.
func checkStateDeny4Rule() cacl.AclRule {
	return cacl.AclRule{
		Actions: []cacl.AclAction{
			{Kind: cacl.ActionCheckState},
			{Kind: cacl.ActionDeny},
		},
		Devices:       filter.Devices{{Name: "port0"}},
		Src4s:         filter.IPNets{filter.UnspecifiedIPv4},
		Dst4s:         filter.IPNets{filter.UnspecifiedIPv4},
		Src6s:         filter.IPNets{},
		Dst6s:         filter.IPNets{},
		SrcPortRanges: allPorts,
		DstPortRanges: allPorts,
		ProtoRanges:   udpProto,
		Fragment:      filter.FragmentAny,
	}
}

// TestACL_FWTableObjectStateLookup verifies that an ACL config linking
// fwstate-map objects resolves their fwtables and answers CHECK_STATE
// from them: a packet whose reverse direction has state passes, an
// unrelated packet is denied.
func TestACL_FWTableObjectStateLookup(t *testing.T) {
	h, agent, backend := setupACLFWTableHarness(t)

	map4, err := objfwstate.NewMapObjectConfig(agent, "obj4", objfwstate.KindV4)
	require.NoError(t, err)
	require.NoError(t, map4.CreateMap(1024, 0, 1))
	require.NoError(t, map4.Publish(agent))
	t.Cleanup(func() { _ = map4.Free() })

	map6, err := objfwstate.NewMapObjectConfig(agent, "obj6", objfwstate.KindV6)
	require.NoError(t, err)
	require.NoError(t, map6.CreateMap(1024, 0, 1))
	require.NoError(t, map6.Publish(agent))
	t.Cleanup(func() { _ = map6.Free() })

	handle, err := backend.NewModule(
		"acl0", []cacl.AclRule{checkStateDeny4Rule()}, "obj4", "obj6", nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = handle.Free() })
	require.NoError(t, backend.UpdateModule(handle))

	wireACLPipeline(t, agent, "port0", "acl0")

	now := time.Now()
	h.SetCurrentTime(now)
	const ttl = 60 * uint64(time.Second)

	srcIP := net.ParseIP("192.0.2.1")
	dstIP := net.ParseIP("10.0.0.1")
	// CHECK_STATE looks up the reverse of the packet's tuple, so the
	// state entry is seeded for the opposite direction.
	require.True(t, fwtable.InsertV4State(
		map4, 0, uint64(now.UnixNano()), ttl,
		uint16(layers.IPProtocolUDP), dstIP, 80, srcIP, 12345,
	), "failed to insert state into v4 fwtable")

	eth := layers.Ethernet{
		EthernetType: layers.EthernetTypeIPv4,
		SrcMAC:       net.HardwareAddr{0x02, 0, 0, 0, 0, 1},
		DstMAC:       net.HardwareAddr{0x02, 0, 0, 0, 0, 2},
	}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    srcIP,
		DstIP:    dstIP,
	}
	udp := layers.UDP{SrcPort: 12345, DstPort: 80}
	udp.SetNetworkLayerForChecksum(&ip4)
	stateful := xpacket.LayersToPacket(t, &eth, &ip4, &udp)

	result, err := h.HandlePackets(stateful)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "packet with matching state must be allowed")
	require.Empty(t, result.Drop)
	requireModuleCounterPackets(t, h, aclCounterPath("port0", "acl0"), "acl_action_check_pass", 1)

	udpMiss := layers.UDP{SrcPort: 12345, DstPort: 81}
	udpMiss.SetNetworkLayerForChecksum(&ip4)
	stateless := xpacket.LayersToPacket(t, &eth, &ip4, &udpMiss)

	result, err = h.HandlePackets(stateless)
	require.NoError(t, err)
	require.Len(t, result.Drop, 1, "packet without matching state must be denied")
	require.Empty(t, result.Output)
	requireModuleCounterPackets(t, h, aclCounterPath("port0", "acl0"), "acl_action_check_miss", 1)
}

// TestACL_FWTableObjectFallbackWithoutNames verifies that a config with no
// map-object names keeps the pre-object behavior: CHECK_STATE finds no
// state anywhere and the packet is denied.
func TestACL_FWTableObjectFallbackWithoutNames(t *testing.T) {
	h, agent, backend := setupACLFWTableHarness(t)

	handle, err := backend.NewModule(
		"acl0", []cacl.AclRule{checkStateDeny4Rule()}, "", "", nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = handle.Free() })
	require.NoError(t, backend.UpdateModule(handle))

	wireACLPipeline(t, agent, "port0", "acl0")

	eth := layers.Ethernet{
		EthernetType: layers.EthernetTypeIPv4,
		SrcMAC:       net.HardwareAddr{0x02, 0, 0, 0, 0, 1},
		DstMAC:       net.HardwareAddr{0x02, 0, 0, 0, 0, 2},
	}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    net.ParseIP("192.0.2.1"),
		DstIP:    net.ParseIP("10.0.0.1"),
	}
	udp := layers.UDP{SrcPort: 12345, DstPort: 80}
	udp.SetNetworkLayerForChecksum(&ip4)
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &udp)

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, result.Drop, 1, "state check without any state source must miss and deny")
	require.Empty(t, result.Output)
	requireModuleCounterPackets(t, h, aclCounterPath("port0", "acl0"), "acl_action_check_miss", 1)
}

// TestACL_FWTableObjectUnknownNameRejected verifies that publishing a
// config whose map-object link names no published object fails the
// publish itself with the linked-object error naming the missing object,
// instead of silently resolving a NULL table.
func TestACL_FWTableObjectUnknownNameRejected(t *testing.T) {
	_, _, backend := setupACLFWTableHarness(t)

	handle, err := backend.NewModule(
		"acl0", []cacl.AclRule{checkStateDeny4Rule()}, "no-such-map", "", nil,
	)
	require.NoError(t, err, "construction only declares the link and must succeed")
	t.Cleanup(func() { _ = handle.Free() })

	err = backend.UpdateModule(handle)
	require.Error(t, err, "publishing a config that links an unknown object must fail")
	require.Contains(t, err.Error(), "linked object")
	require.Contains(t, err.Error(), "no-such-map")
}

// TestACL_FWTableObjectDeleteRefusedWhileLinked verifies that deleting a
// published map object is refused while a published module config links
// it, and that republishing the module without the link unpins the
// object so the deletion then succeeds.
func TestACL_FWTableObjectDeleteRefusedWhileLinked(t *testing.T) {
	// The scenario holds two ACL configs alive at once (the linked one
	// stays referenced by its published generation), so the agent needs
	// a doubled memory budget.
	_, agent, backend := setupACLFWTableHarnessSized(t, aclMemSize*2)

	map4, err := objfwstate.NewMapObjectConfig(agent, "obj4", objfwstate.KindV4)
	require.NoError(t, err)
	require.NoError(t, map4.CreateMap(1024, 0, 1))
	require.NoError(t, map4.Publish(agent))
	t.Cleanup(func() { _ = map4.Free() })

	map6, err := objfwstate.NewMapObjectConfig(agent, "obj6", objfwstate.KindV6)
	require.NoError(t, err)
	require.NoError(t, map6.CreateMap(1024, 0, 1))
	require.NoError(t, map6.Publish(agent))
	t.Cleanup(func() { _ = map6.Free() })

	handle, err := backend.NewModule(
		"acl0", []cacl.AclRule{checkStateDeny4Rule()}, "obj4", "obj6", nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = handle.Free() })
	require.NoError(t, backend.UpdateModule(handle))

	wireACLPipeline(t, agent, "port0", "acl0")

	err = objfwstate.DeleteMapObject(agent, objfwstate.KindV4.ObjectType(), "obj4")
	require.Error(t, err, "deleting a map a published module links must be refused")
	require.Contains(t, err.Error(), "is linked by module")

	unlinked, err := backend.NewModule(
		"acl0", []cacl.AclRule{checkStateDeny4Rule()}, "", "", nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = unlinked.Free() })
	require.NoError(t, backend.UpdateModule(unlinked))
	handle.Free()

	require.NoError(t, objfwstate.DeleteMapObject(agent, objfwstate.KindV4.ObjectType(), "obj4"))
}
