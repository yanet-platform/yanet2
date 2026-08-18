package acl_test

import (
	"net"
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
)

// syncEmitConfig builds an emission config that passes the usable check and
// addresses the crafted sync frames to a link-local multicast destination.
func syncEmitConfig() *cfwstate.SyncEmitConfig {
	multicast := netip.MustParseAddr("ff02::1").As16()
	cfg := &cfwstate.SyncEmitConfig{
		DstEther:      [6]byte{0x33, 0x33, 0x00, 0x00, 0x00, 0x01},
		PortMulticast: 9999,
	}
	copy(cfg.DstAddrMulticast[:], multicast[:])
	return cfg
}

// syncStatePacket builds the TCP packet the CREATE_STATE rule matches.
func syncStatePacket(t *testing.T) gopacket.Packet {
	t.Helper()

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    net.ParseIP("192.0.2.1"),
		DstIP:    net.ParseIP("10.0.0.1"),
	}
	tcp := layers.TCP{
		SrcPort: 12345,
		DstPort: 80,
		SYN:     true,
	}
	require.NoError(t, tcp.SetNetworkLayerForChecksum(&ip4))
	return xpacket.LayersToPacket(t, &eth, &ip4, &tcp)
}

// TestACL_UpdateRules_EmitConfigDrivesSyncFrames verifies the positive
// emission path: a ruleset that creates state and carries a usable emit
// config synthesizes one state-sync frame per matched packet.
func TestACL_UpdateRules_EmitConfigDrivesSyncFrames(t *testing.T) {
	// CREATE_STATE is non-terminal: the packet passes and the module
	// synthesizes a state-sync frame when the emit config is usable.
	rule := allow4Rule(
		filter.IPNets{filter.UnspecifiedIPv4},
		filter.IPNets{filter.UnspecifiedIPv4},
		tcpProto,
	)
	rule.Actions = []cacl.AclAction{{Kind: cacl.ActionCreateState}, {Kind: cacl.ActionAllow}}

	h, agent, backend := setupACLHarness(t, []string{"port0"})

	handle, err := backend.NewModule("sync-emit", []cacl.AclRule{rule}, syncEmitConfig())
	require.NoError(t, err)
	t.Cleanup(handle.Free)

	require.NoError(t, backend.UpdateModule(handle))
	wireACLPipeline(t, agent, "port0", "sync-emit")

	result, err := h.HandlePackets(syncStatePacket(t))
	require.NoError(t, err)
	require.Len(t, result.Output, 2, "a usable emit config must add a state-sync frame to the output")
}

// TestACL_UpdateRules_NilEmitConfigClearsPreviousSyncConfig verifies the
// nil contract of the exported binding: a replacement config constructed
// with a nil emit config emits no state-sync frames, instead of crafting
// them to the destination the previous config installed.
func TestACL_UpdateRules_NilEmitConfigClearsPreviousSyncConfig(t *testing.T) {
	rule := allow4Rule(
		filter.IPNets{filter.UnspecifiedIPv4},
		filter.IPNets{filter.UnspecifiedIPv4},
		tcpProto,
	)
	rule.Actions = []cacl.AclAction{{Kind: cacl.ActionCreateState}, {Kind: cacl.ActionAllow}}

	// The replacement constructs a second config in the same module
	// arena next to the first, so the harness carries a larger agent
	// arena (the sizes the net6-share tests proved under ASan).
	h, agent, backend := setupACLHarnessSized(t, []string{"port0"}, 192*datasize.MB, 48*datasize.MB)

	emitHandle, err := backend.NewModule("sync-emit", []cacl.AclRule{rule}, syncEmitConfig())
	require.NoError(t, err)
	t.Cleanup(emitHandle.Free)
	require.NoError(t, backend.UpdateModule(emitHandle))
	wireACLPipeline(t, agent, "port0", "sync-emit")

	result, err := h.HandlePackets(syncStatePacket(t))
	require.NoError(t, err)
	require.Len(t, result.Output, 2, "the emit config must drive a state-sync frame first")

	handle, err := backend.NewModule("sync-emit", []cacl.AclRule{rule}, nil)
	require.NoError(t, err)
	t.Cleanup(handle.Free)

	// A module publish waits for a worker round to acknowledge the new
	// generation, and only HandlePackets drives rounds in this harness.
	// Drive bare rounds until the replacement publish completes.
	publishDone := make(chan error, 1)
	go func() {
		publishDone <- backend.UpdateModule(handle)
	}()
	publishing := true
	for publishing {
		select {
		case err := <-publishDone:
			require.NoError(t, err)
			publishing = false
		default:
			_, err := h.HandlePackets()
			require.NoError(t, err)
		}
	}

	result, err = h.HandlePackets(syncStatePacket(t))
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "a nil emit config must clear the previously installed one")
}
