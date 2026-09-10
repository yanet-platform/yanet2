package acl_test

import (
	"encoding/binary"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/xnetip"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	plain "github.com/yanet-platform/yanet2/devices/plain/controlplane"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	forward "github.com/yanet-platform/yanet2/modules/forward/controlplane"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
	objfwstate "github.com/yanet-platform/yanet2/objects/fwstate/bindings/go/cfwstate"
)

// publishSyncMaps publishes the state tables required for local sync
// creation.
func publishSyncMaps(
	t *testing.T, agent *ffi.Agent,
) (*objfwstate.MapObjectConfig, *objfwstate.MapObjectConfig) {
	t.Helper()

	map4, err := objfwstate.NewMapObjectConfig(agent, "sync-map4", objfwstate.KindV4)
	require.NoError(t, err)
	require.NoError(t, map4.CreateMap(1024, 64, 1))
	require.NoError(t, map4.Publish(agent))
	t.Cleanup(func() { _ = map4.Free() })

	map6, err := objfwstate.NewMapObjectConfig(agent, "sync-map6", objfwstate.KindV6)
	require.NoError(t, err)
	require.NoError(t, map6.CreateMap(1024, 64, 1))
	require.NoError(t, map6.Publish(agent))
	t.Cleanup(func() { _ = map6.Free() })

	return map4, map6
}

// syncStatePacket builds the TCP packet matched by the state-creation rule.
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

// Test_ACL_UpdateRules_CreatesNeutralSyncEvent verifies that state creation
// adds one destination-neutral event for fwstate to consume.
func Test_ACL_UpdateRules_CreatesNeutralSyncEvent(t *testing.T) {
	rule := allow4Rule(
		[]xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		[]xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		tcpProto,
	)
	rule.Actions = []cacl.AclAction{{Kind: cacl.ActionCreateState}, {Kind: cacl.ActionAllow}}

	h, agent, backend := setupACLFWStateSyncHarness(t)
	map4, map6 := publishSyncMaps(t, agent)

	handle, err := backend.NewModule(
		"sync-emit", []cacl.AclRule{rule}, map4.Name(), map6.Name(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = handle.Free() })

	require.NoError(t, backend.UpdateModule(handle))
	wireACLPipeline(t, agent, "port0", "sync-emit")

	result, err := h.HandlePackets(syncStatePacket(t))
	require.NoError(t, err)
	require.Len(t, result.Output, 2)
	ether, ip6, udp, _ := parseSyncPacket(t, result.Output[1].RawData)
	require.Equal(t, net.HardwareAddr{0, 0, 0, 0, 0, 0}, ether.DstMAC)
	require.True(t, ip6.DstIP.Equal(net.IPv6zero))
	require.Zero(t, udp.DstPort)
}

// Test_ACL_UpdateRules_WithoutStateMapCreatesNeutralSyncEvent verifies that
// CREATE_STATE does not depend on ACL's optional lookup maps.
func Test_ACL_UpdateRules_WithoutStateMapCreatesNeutralSyncEvent(t *testing.T) {
	rule := allow4Rule(
		[]xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		[]xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		tcpProto,
	)
	rule.Actions = []cacl.AclAction{{Kind: cacl.ActionCreateState}, {Kind: cacl.ActionAllow}}

	h, agent, backend := setupACLHarness(t, []string{"port0"})
	handle, err := backend.NewModule(
		"sync-without-state", []cacl.AclRule{rule}, "", "",
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = handle.Free() })
	require.NoError(t, backend.UpdateModule(handle))
	wireACLPipeline(t, agent, "port0", "sync-without-state")

	result, err := h.HandlePackets(syncStatePacket(t))
	require.NoError(t, err)
	require.Len(t, result.Output, 2)
	ether, ip6, udp, _ := parseSyncPacket(t, result.Output[1].RawData)
	require.Equal(t, net.HardwareAddr{0, 0, 0, 0, 0, 0}, ether.DstMAC)
	require.True(t, ip6.DstIP.Equal(net.IPv6zero))
	require.Zero(t, udp.DstPort)
}

// Test_ACL_FWState_InternalEventEmitsConfiguredDestinations verifies that one
// local insertion emits checksum-valid endpoint copies and suppresses repeats.
func Test_ACL_FWState_InternalEventEmitsConfiguredDestinations(t *testing.T) {
	type endpoint struct {
		address string
		port    uint16
	}
	tests := []struct {
		name      string
		multicast bool
		unicast   bool
		want      []endpoint
	}{
		{
			name:      "multicast only",
			multicast: true,
			want:      []endpoint{{address: "ff02::1", port: 9999}},
		},
		{
			name:    "unicast only",
			unicast: true,
			want:    []endpoint{{address: "2001:db8::2", port: 10000}},
		},
		{
			name:      "multicast and unicast",
			multicast: true,
			unicast:   true,
			want: []endpoint{
				{address: "ff02::1", port: 9999},
				{address: "2001:db8::2", port: 10000},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, agent, backend := setupACLFWStateSyncHarness(t)
			map4, map6 := publishSyncMaps(t, agent)

			syncConfig := cfwstate.DefaultSyncConfig()
			syncSource := netip.MustParseAddr("2001:db8::1").As16()
			syncMulticast := netip.MustParseAddr("ff02::1").As16()
			syncUnicast := netip.MustParseAddr("2001:db8::2").As16()
			copy(syncConfig.SrcAddr[:], syncSource[:])
			syncConfig.DstEther = [6]byte{0x33, 0x33, 0x00, 0x00, 0x00, 0x01}
			if tc.multicast {
				copy(syncConfig.DstAddrMulticast[:], syncMulticast[:])
				syncConfig.PortMulticast = 9999
			}
			if tc.unicast {
				copy(syncConfig.DstAddrUnicast[:], syncUnicast[:])
				syncConfig.PortUnicast = 10000
			}
			syncConfig.SyncSuppressTimeout = uint64(time.Minute)

			fwConfig, err := cfwstate.NewModuleConfig(
				agent, "sync-fwstate", &syncConfig, map4.Name(), map6.Name(),
			)
			require.NoError(t, err)
			t.Cleanup(func() { _ = fwConfig.Free() })
			require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{fwConfig.AsFFIModule()}))

			rule := allow4Rule(
				[]xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
				[]xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
				tcpProto,
			)
			rule.Actions = []cacl.AclAction{{Kind: cacl.ActionCreateState}, {Kind: cacl.ActionAllow}}
			aclConfig, err := backend.NewModule(
				"sync-acl", []cacl.AclRule{rule}, map4.Name(), map6.Name(),
			)
			require.NoError(t, err)
			t.Cleanup(func() { _ = aclConfig.Free() })
			require.NoError(t, backend.UpdateModule(aclConfig))
			wireACLFWStateSyncPipeline(t, agent, "sync-acl", "sync-fwstate")
			h.SetCurrentTime(time.Unix(1, 0))

			first, err := h.HandlePackets(syncStatePacket(t))
			require.NoError(t, err)
			require.Empty(t, first.Drop)
			require.Len(t, first.Output, 1+len(tc.want))

			var payload []byte
			for idx, want := range tc.want {
				ether, ip6, udp, gotPayload := parseSyncPacket(t, first.Output[idx+1].RawData)
				require.Equal(t, net.HardwareAddr{0x33, 0x33, 0, 0, 0, 1}, ether.DstMAC)
				require.True(t, ip6.SrcIP.Equal(net.ParseIP("2001:db8::1")))
				require.True(t, ip6.DstIP.Equal(net.ParseIP(want.address)))
				require.Equal(t, layers.UDPPort(want.port), udp.SrcPort)
				require.Equal(t, layers.UDPPort(want.port), udp.DstPort)
				require.True(t, validIPv6UDPChecksum(ip6, udp))
				if idx == 0 {
					payload = gotPayload
				} else {
					require.Equal(t, payload, gotPayload)
				}
			}

			entries, _, _, err := map4.ReadForward(0, 0, true, 0, 10)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			require.Equal(t, uint64(1), entries[0].Value.PacketsForward)

			second, err := h.HandlePackets(syncStatePacket(t))
			require.NoError(t, err)
			require.Len(t, second.Output, 1)
			require.Len(t, second.Drop, 1)
		})
	}
}

// setupACLFWStateSyncHarness returns an object-backed harness with both state
// modules loaded for one worker.
func setupACLFWStateSyncHarness(t *testing.T) (*dataplaneut.Harness, *ffi.Agent, acl.Backend) {
	t.Helper()

	h, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(aclCPSize),
		DPMemory:      uint64(aclDPSize),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		Modules:       []string{"acl", "fwstate", "forward"},
		DevicesToLoad: []string{"plain"},
		ObjectsToLoad: []string{"fwstate_map_v4", "fwstate_map_v6"},
	})
	require.NoError(t, err)
	t.Cleanup(h.Free)

	agent, err := h.SharedMemory().AgentAttach("acl-fwstate-sync", 0, aclMemSize)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })
	return h, agent, acl.NewBackend(agent)
}

// wireACLFWStateSyncPipeline connects the state producer directly to the state
// consumer before forwarding packets to the test device.
func wireACLFWStateSyncPipeline(t *testing.T, agent *ffi.Agent, aclName, fwstateName string) {
	t.Helper()

	sinkName := aclName + "-sink"
	sinkRules := []cforward.ForwardRule{
		{Target: "port0", Mode: cforward.ModeOut, Counter: "sink4"},
		{Target: "port0", Mode: cforward.ModeOut, Counter: "sink6"},
	}
	sinkHandle, err := forward.NewBackend(agent).UpdateModule(sinkName, sinkRules)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sinkHandle.Free() })

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: aclName,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: aclName + "_chain",
				Modules: []ffi.ChainModuleConfig{
					{Type: "acl", Name: aclName},
					{Type: "fwstate", Name: fwstateName},
					{Type: "forward", Name: sinkName},
				},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: aclName, Functions: []string{aclName}}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "dummy"}))
	_, err = plain.UpdateDevices(agent, []ffi.DeviceConfig{{
		Name:   "port0",
		Input:  []ffi.DevicePipelineConfig{{Name: aclName, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}})
	require.NoError(t, err)
}

// parseSyncPacket decodes the wire headers and copies the synchronization
// frame payload for comparisons between emitted destinations.
func parseSyncPacket(t *testing.T, raw []byte) (*layers.Ethernet, *layers.IPv6, *layers.UDP, []byte) {
	t.Helper()

	packet := gopacket.NewPacket(raw, layers.LayerTypeEthernet, gopacket.Default)
	require.Nil(t, packet.ErrorLayer())
	etherLayer := packet.Layer(layers.LayerTypeEthernet)
	require.NotNil(t, etherLayer)
	ipLayer := packet.Layer(layers.LayerTypeIPv6)
	require.NotNil(t, ipLayer)
	udpLayer := packet.Layer(layers.LayerTypeUDP)
	require.NotNil(t, udpLayer)
	ether := etherLayer.(*layers.Ethernet)
	ip6 := ipLayer.(*layers.IPv6)
	udp := udpLayer.(*layers.UDP)
	require.Equal(t, uint16(len(udp.Contents)+len(udp.Payload)), udp.Length)
	require.Equal(t, uint16(len(udp.Contents)+len(udp.Payload)), ip6.Length)
	return ether, ip6, udp, append([]byte(nil), udp.Payload...)
}

// validIPv6UDPChecksum reports whether the decoded datagram satisfies the
// IPv6 pseudo-header checksum.
func validIPv6UDPChecksum(ip6 *layers.IPv6, udp *layers.UDP) bool {
	udpCopy := *udp
	udpCopy.Checksum = 0
	if err := udpCopy.SetNetworkLayerForChecksum(ip6); err != nil {
		return false
	}
	buffer := gopacket.NewSerializeBuffer()
	if err := gopacket.SerializeLayers(
		buffer,
		gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true},
		&udpCopy,
		gopacket.Payload(udp.Payload),
	); err != nil {
		return false
	}
	return udp.Checksum == binary.BigEndian.Uint16(buffer.Bytes()[6:8])
}
