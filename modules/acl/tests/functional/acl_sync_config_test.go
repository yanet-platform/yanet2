package acl_test

import (
	"bytes"
	"encoding/binary"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
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

// publishSyncMaps publishes the state tables and default-capacity stashes
// required for local sync creation.
func publishSyncMaps(
	t *testing.T, agent *ffi.Agent,
) (*objfwstate.MapObjectConfig, *objfwstate.MapObjectConfig) {
	t.Helper()
	return publishSyncMapsWithStashSize(t, agent, 0)
}

// publishSyncMapsWithStashSize publishes one v4 and one v6 map whose
// per-worker stash buffer is stashSize bytes, zero selecting the default.
func publishSyncMapsWithStashSize(
	t *testing.T, agent *ffi.Agent, stashSize uint64,
) (*objfwstate.MapObjectConfig, *objfwstate.MapObjectConfig) {
	t.Helper()

	map4, err := objfwstate.NewMapObjectConfig(agent, "sync-map4", objfwstate.KindV4)
	require.NoError(t, err)
	require.NoError(t, map4.CreateMap(objfwstate.MapConfig{IndexSize: 1024, ExtraBucketCount: 64, WorkerCount: 1, StashSize: stashSize}))
	require.NoError(t, map4.Publish(agent))
	t.Cleanup(func() { _ = map4.Free() })

	map6, err := objfwstate.NewMapObjectConfig(agent, "sync-map6", objfwstate.KindV6)
	require.NoError(t, err)
	require.NoError(t, map6.CreateMap(objfwstate.MapConfig{IndexSize: 1024, ExtraBucketCount: 64, WorkerCount: 1, StashSize: stashSize}))
	require.NoError(t, map6.Publish(agent))
	t.Cleanup(func() { _ = map6.Free() })

	return map4, map6
}

// syncStatePacket builds the TCP packet matched by the state-creation rule.
func syncStatePacket(t *testing.T) gopacket.Packet {
	t.Helper()
	return syncStatePacketWithPort(t, 12345)
}

// syncStatePacketWithPort builds the TCP packet matched by the
// state-creation rule with the given source port.
func syncStatePacketWithPort(t *testing.T, srcPort layers.TCPPort) gopacket.Packet {
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
		SrcPort: srcPort,
		DstPort: 80,
		SYN:     true,
	}
	require.NoError(t, tcp.SetNetworkLayerForChecksum(&ip4))
	return xpacket.LayersToPacket(t, &eth, &ip4, &tcp)
}

// syncStatePacket6 builds the IPv6 TCP packet matched by the IPv6
// state-creation rule.
func syncStatePacket6(t *testing.T) gopacket.Packet {
	t.Helper()

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv6,
	}
	ip6 := layers.IPv6{
		Version:    6,
		HopLimit:   64,
		NextHeader: layers.IPProtocolTCP,
		SrcIP:      net.ParseIP("2001:db8::10"),
		DstIP:      net.ParseIP("2001:db8::20"),
	}
	tcp := layers.TCP{
		SrcPort: 23456,
		DstPort: 443,
		SYN:     true,
	}
	require.NoError(t, tcp.SetNetworkLayerForChecksum(&ip6))
	return xpacket.LayersToPacket(t, &eth, &ip6, &tcp)
}

// createStateRules returns the IPv4 and IPv6 rules that create state for
// and allow every TCP packet.
func createStateRules() []cacl.ACLRule {
	rule4 := allow4Rule(
		[]xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		[]xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		tcpProto,
	)
	rule4.Actions = []cacl.ACLAction{{Kind: cacl.ActionCreateState}, {Kind: cacl.ActionAllow}}
	rule6 := allow6Rule(
		[]xnetip.BiContiguous{filter.UnspecifiedIPv6},
		[]xnetip.BiContiguous{filter.UnspecifiedIPv6},
		tcpProto,
	)
	rule6.Actions = []cacl.ACLAction{{Kind: cacl.ActionCreateState}, {Kind: cacl.ActionAllow}}
	return []cacl.ACLRule{rule4, rule6}
}

// Test_ACL_UpdateRules_StashesSyncRecord verifies that state creation
// leaves only the original packets in ACL output and appends one pending
// record per packet to the worker's stash slot of its family map.
func Test_ACL_UpdateRules_StashesSyncRecord(t *testing.T) {
	h, agent, backend := setupACLFWStateSyncHarness(t)
	map4, map6 := publishSyncMaps(t, agent)

	handle, err := backend.NewModule(
		"sync-emit", createStateRules(), map4.Name(), map6.Name(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = handle.Free() })

	require.NoError(t, backend.UpdateModule(handle))
	wireACLPipeline(t, agent, "port0", "sync-emit")

	packet4 := syncStatePacket(t)
	packet6 := syncStatePacket6(t)
	result, err := h.HandlePackets(packet4, packet6)
	require.NoError(t, err)
	require.Len(t, result.Output, 2, "ACL output carries only the original packets")
	require.Equal(t, packet4.Data(), result.Output[0].RawData)
	require.Equal(t, packet6.Data(), result.Output[1].RawData)

	for _, stash := range []*objfwstate.MapObjectConfig{map4, map6} {
		records, err := stash.StashRecords(0)
		require.NoError(t, err)
		require.Len(t, records, 1, "map %s", stash.Name())
		require.Equal(t, objfwstate.StashRecordPending, records[0].Status)
		require.Len(t, records[0].Frame, objfwstate.SyncFrameSize)
	}
	requireModuleCounterPackets(t, h, aclCounterPath("port0", "sync-emit"), "acl_sync_sent", 2)
	requireModuleCounterBytes(t, h, aclCounterPath("port0", "sync-emit"), "acl_sync_sent", 2*uint64(objfwstate.SyncFrameSize))
	requireModuleCounterPackets(t, h, aclCounterPath("port0", "sync-emit"), "acl_sync_overflow", 0)
}

// Test_ACL_UpdateRules_WithoutStateMapWritesNothing verifies that
// CREATE_STATE without a linked map still allows the packet but records
// and counts no sync event.
func Test_ACL_UpdateRules_WithoutStateMapWritesNothing(t *testing.T) {
	h, agent, backend := setupACLHarness(t, []string{"port0"})
	handle, err := backend.NewModule(
		"sync-without-state", createStateRules(), "", "",
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = handle.Free() })
	require.NoError(t, backend.UpdateModule(handle))
	wireACLPipeline(t, agent, "port0", "sync-without-state")

	result, err := h.HandlePackets(syncStatePacket(t), syncStatePacket6(t))
	require.NoError(t, err)
	require.Len(t, result.Output, 2)
	path := aclCounterPath("port0", "sync-without-state")
	requireModuleCounterPackets(t, h, path, "acl_action_create_state", 2)
	requireModuleCounterPackets(t, h, path, "acl_sync_sent", 0)
	requireModuleCounterPackets(t, h, path, "acl_sync_overflow", 0)
}

// Test_ACL_UpdateRules_FullStashCountsOverflow verifies that, for either
// family, records past the stash capacity are discarded and counted while
// every packet keeps its allow verdict, and that a new round starts from an
// empty slot.
func Test_ACL_UpdateRules_FullStashCountsOverflow(t *testing.T) {
	cases := []struct {
		name   string
		packet func(t *testing.T) gopacket.Packet
		ipv6   bool
	}{
		{name: "ipv4", packet: syncStatePacket},
		{name: "ipv6", packet: syncStatePacket6, ipv6: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, agent, backend := setupACLFWStateSyncHarness(t)
			// A buffer that holds exactly two records.
			map4, map6 := publishSyncMapsWithStashSize(t, agent, 2*uint64(objfwstate.StashRecordSize))
			stash, other := map4, map6
			if tc.ipv6 {
				stash, other = map6, map4
			}

			handle, err := backend.NewModule(
				"sync-overflow", createStateRules(), map4.Name(), map6.Name(),
			)
			require.NoError(t, err)
			t.Cleanup(func() { _ = handle.Free() })
			require.NoError(t, backend.UpdateModule(handle))
			wireACLPipeline(t, agent, "port0", "sync-overflow")

			result, err := h.HandlePackets(tc.packet(t), tc.packet(t), tc.packet(t))
			require.NoError(t, err)
			require.Len(t, result.Output, 3, "overflow must not change the verdict")
			records, err := stash.StashRecords(0)
			require.NoError(t, err)
			require.Len(t, records, 2)
			records, err = other.StashRecords(0)
			require.NoError(t, err)
			require.Empty(t, records, "the other family's slot stays untouched")
			path := aclCounterPath("port0", "sync-overflow")
			requireModuleCounterPackets(t, h, path, "acl_sync_sent", 2)
			requireModuleCounterPackets(t, h, path, "acl_sync_overflow", 1)

			// A new round starts from an empty slot.
			result, err = h.HandlePackets(tc.packet(t))
			require.NoError(t, err)
			require.Len(t, result.Output, 1)
			records, err = stash.StashRecords(0)
			require.NoError(t, err)
			require.Len(t, records, 1)
			requireModuleCounterPackets(t, h, path, "acl_sync_overflow", 1)
		})
	}
}

// Test_ACL_Fragment_IPv6NonInitialSkipsStateCreation verifies that a
// non-initial IPv6 TCP fragment matched by a state-creation rule is allowed
// without stashing a sync record, since it carries no transport header.
func Test_ACL_Fragment_IPv6NonInitialSkipsStateCreation(t *testing.T) {
	h, agent, backend := setupACLFWStateSyncHarness(t)
	map4, map6 := publishSyncMaps(t, agent)

	handle, err := backend.NewModule(
		"fragment-state6", createStateRules(), map4.Name(), map6.Name(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = handle.Free() })
	require.NoError(t, backend.UpdateModule(handle))
	wireACLPipeline(t, agent, "port0", "fragment-state6")

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv6,
	}
	ip6 := layers.IPv6{
		Version:    6,
		HopLimit:   64,
		NextHeader: layers.IPProtocolIPv6Fragment,
		SrcIP:      net.ParseIP("2001:db8::10"),
		DstIP:      net.ParseIP("2001:db8::20"),
	}
	frag := layers.IPv6Fragment{
		NextHeader:     layers.IPProtocolTCP,
		FragmentOffset: 8,
		Identification: 0x12345678,
	}
	// Flow payload only: deliberately shorter than any TCP header.
	payload := gopacket.Payload([]byte{0x5b, 0xa0, 0x01, 0xbb, 0x00, 0x00, 0x00, 0x50})
	fragment := serializeFragPacket(t, &eth, &ip6, &frag, payload)

	result, err := h.HandlePackets(fragment)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "a non-initial fragment must be allowed")
	records, err := map6.StashRecords(0)
	require.NoError(t, err)
	require.Empty(t, records, "no sync record may be captured from fragment payload")
	requireModuleCounterPackets(t, h, aclCounterPath("port0", "fragment-state6"), "acl_sync_sent", 0)
}

// Test_ACL_Fragment_ShortNonInitialSkipsStateCreation verifies that a short
// non-initial TCP fragment matched by a state-creation rule is allowed
// without creating state or stashing a sync record: there is no transport
// header to derive the state from, while an unfragmented packet under the same
// rule stashes one.
func Test_ACL_Fragment_ShortNonInitialSkipsStateCreation(t *testing.T) {
	rule := allow4Rule(
		[]xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		[]xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		tcpProto,
	)
	rule.Actions = []cacl.ACLAction{{Kind: cacl.ActionCreateState}, {Kind: cacl.ActionAllow}}

	h, agent, backend := setupACLFWStateSyncHarness(t)
	map4, map6 := publishSyncMaps(t, agent)

	handle, err := backend.NewModule(
		"fragment-state", []cacl.ACLRule{rule}, map4.Name(), map6.Name(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = handle.Free() })

	require.NoError(t, backend.UpdateModule(handle))
	wireACLPipeline(t, agent, "port0", "fragment-state")

	fragment := rawIPv4Frame(false, true, 1, layers.IPProtocolTCP, make([]byte, 8))
	result, err := h.HandleSegmentedPackets([][]byte{fragment})
	require.NoError(t, err)
	require.Len(t, result.Output, 1,
		"a non-initial fragment must be allowed")
	assert.True(t, bytes.Equal(result.Output[0], fragment),
		"an allowed fragment must pass through byte-identical")
	requireModuleCounterPackets(t, h, aclCounterPath("port0", "fragment-state"), "acl_sync_sent", 0)

	entries, _, _, err := map4.ReadForward(0, 0, true, 0, 10)
	require.NoError(t, err)
	require.Empty(t, entries, "no state may be created from fragment payload")

	records, err := map4.StashRecords(0)
	require.NoError(t, err)
	require.Empty(t, records, "no sync record may be captured from fragment payload")

	dfOnly := rawIPv4Frame(true, false, 0, layers.IPProtocolTCP, bytes.Repeat([]byte{0x51}, 20))
	result, err = h.HandleSegmentedPackets([][]byte{dfOnly})
	require.NoError(t, err)
	require.Len(t, result.Output, 1)
	records, err = map4.StashRecords(0)
	require.NoError(t, err)
	require.Len(t, records, 1,
		"an unfragmented packet under the same rule stashes the sync event")

	requireModuleCounterPackets(t, h, aclCounterPath("port0", "fragment-state"), "acl_sync_sent", 1)
}

// Test_ACL_FWState_StashedEventEmitsConfiguredDestinations verifies that one
// local insertion emits checksum-valid endpoint copies and suppresses repeats.
func Test_ACL_FWState_StashedEventEmitsConfiguredDestinations(t *testing.T) {
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
			rule.Actions = []cacl.ACLAction{{Kind: cacl.ActionCreateState}, {Kind: cacl.ActionAllow}}
			aclConfig, err := backend.NewModule(
				"sync-acl", []cacl.ACLRule{rule}, map4.Name(), map6.Name(),
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
			require.Len(t, second.Output, 1, "a suppressed refresh is not emitted")
			require.Empty(t, second.Drop)
		})
	}
}

// Test_ACL_FWStateChain_DecidesOnceAndEmitsPerConfig verifies that with
// ACL, a forwarding module that diverts the originals out of the chain and
// two fwstate configurations on the same maps, the forwarding module sees
// only the original packets, every frame is inserted once by the first
// fwstate, and each fwstate emits the batched frames to its own endpoints
// while the second passes the first one's emission through.
func Test_ACL_FWStateChain_DecidesOnceAndEmitsPerConfig(t *testing.T) {
	h, agent, backend := setupACLFWStateSyncHarness(t)
	map4, map6 := publishSyncMaps(t, agent)

	syncSource := netip.MustParseAddr("2001:db8::1").As16()
	syncMulticast := netip.MustParseAddr("ff02::1").As16()
	syncUnicast := netip.MustParseAddr("2001:db8::2").As16()
	upstreamConfig := cfwstate.DefaultSyncConfig()
	copy(upstreamConfig.SrcAddr[:], syncSource[:])
	upstreamConfig.DstEther = [6]byte{0x33, 0x33, 0x00, 0x00, 0x00, 0x01}
	copy(upstreamConfig.DstAddrMulticast[:], syncMulticast[:])
	upstreamConfig.PortMulticast = 9999
	// The downstream config receives on the same multicast endpoint, so
	// only the local-emission flag keeps it from consuming the upstream
	// packets as external sync.
	downstreamConfig := upstreamConfig
	copy(downstreamConfig.DstAddrUnicast[:], syncUnicast[:])
	downstreamConfig.PortUnicast = 10000

	for _, cfg := range []struct {
		name   string
		config cfwstate.SyncConfig
	}{
		{name: "fwstate-up", config: upstreamConfig},
		{name: "fwstate-down", config: downstreamConfig},
	} {
		fwConfig, err := cfwstate.NewModuleConfig(
			agent, cfg.name, &cfg.config, map4.Name(), map6.Name(),
		)
		require.NoError(t, err)
		t.Cleanup(func() { _ = fwConfig.Free() })
		require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{fwConfig.AsFFIModule()}))
	}

	aclConfig, err := backend.NewModule(
		"chain-acl", createStateRules(), map4.Name(), map6.Name(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = aclConfig.Free() })
	require.NoError(t, backend.UpdateModule(aclConfig))
	wireACLForwardFWStatePipeline(t, agent, "chain-acl", "fwstate-up", "fwstate-down")
	h.SetCurrentTime(time.Unix(1, 0))

	packet4 := syncStatePacket(t)
	packet6 := syncStatePacket6(t)
	result, err := h.HandlePackets(packet4, packet6)
	require.NoError(t, err)
	require.Empty(t, result.Drop)

	forwardPath := aclCounterPath("port0", "chain-acl")
	forwardPath.ModuleType = "forward"
	forwardPath.ModuleName = "chain-acl-middle"
	requireModuleCounterPackets(t, h, forwardPath, "rx", 2)

	require.Len(t, result.Output, 5)
	require.Equal(t, packet4.Data(), result.Output[0].RawData)
	require.Equal(t, packet6.Data(), result.Output[1].RawData)
	type emitted struct {
		destination string
		port        uint16
		frames      int
	}
	var got []emitted
	var payloads [][]byte
	for _, output := range result.Output[2:] {
		_, ip6, udp, payload := parseSyncPacket(t, output.RawData)
		got = append(got, emitted{ip6.DstIP.String(), uint16(udp.DstPort), len(payload) / objfwstate.SyncFrameSize})
		payloads = append(payloads, payload)
	}
	require.Equal(t, []emitted{
		{"ff02::1", 9999, 2},
		{"ff02::1", 9999, 2},
		{"2001:db8::2", 10000, 2},
	}, got)
	require.Equal(t, payloads[0], payloads[1])
	require.Equal(t, payloads[0], payloads[2])

	upstreamPath := aclCounterPath("port0", "chain-acl")
	upstreamPath.ModuleType = "fwstate"
	upstreamPath.ModuleName = "fwstate-up"
	downstreamPath := upstreamPath
	downstreamPath.ModuleName = "fwstate-down"
	requireModuleCounterPackets(t, h, upstreamPath, "fwstate_sync_v4_inserted", 1)
	requireModuleCounterPackets(t, h, upstreamPath, "fwstate_sync_v6_inserted", 1)
	requireModuleCounterPackets(t, h, downstreamPath, "fwstate_sync_v4_inserted", 0)
	requireModuleCounterPackets(t, h, downstreamPath, "fwstate_sync_v6_inserted", 0)
	requireModuleCounterPackets(t, h, downstreamPath, "fwstate_sync", 0)
	// The middle module sends the originals out of the chain, so the
	// upstream emission is all the downstream config receives.
	requireModuleCounterPackets(t, h, upstreamPath, "rx", 0)
	requireModuleCounterPackets(t, h, downstreamPath, "fwstate_passthrough", 1)

	for _, stash := range []*objfwstate.MapObjectConfig{map4, map6} {
		records, err := stash.StashRecords(0)
		require.NoError(t, err)
		require.Len(t, records, 1)
		require.Equal(t, objfwstate.StashRecordApplied, records[0].Status)
	}
}

// Test_ACL_FWState_SecondWorkerUsesItsOwnSlotAndCursor verifies that the
// execution contexts each worker's ACL and fwstate commit link to that
// worker's own stash slot: a stateful packet handled by worker 1 is
// stashed in worker 1's slot, applied and emitted there, leaving worker
// 0's slot empty, and a later packet on worker 0 fills only worker 0's
// slot.
func Test_ACL_FWState_SecondWorkerUsesItsOwnSlotAndCursor(t *testing.T) {
	const workerCount = 2
	h, agent, backend := setupACLFWStateSyncHarnessWorkers(t, uint64(workerCount))

	map4, err := objfwstate.NewMapObjectConfig(agent, "sync-map4", objfwstate.KindV4)
	require.NoError(t, err)
	require.NoError(t, map4.CreateMap(objfwstate.MapConfig{IndexSize: 1024, ExtraBucketCount: 64, WorkerCount: workerCount}))
	require.NoError(t, map4.Publish(agent))
	t.Cleanup(func() { _ = map4.Free() })

	syncConfig := cfwstate.DefaultSyncConfig()
	syncSource := netip.MustParseAddr("2001:db8::1").As16()
	syncMulticast := netip.MustParseAddr("ff02::1").As16()
	copy(syncConfig.SrcAddr[:], syncSource[:])
	syncConfig.DstEther = [6]byte{0x33, 0x33, 0x00, 0x00, 0x00, 0x01}
	copy(syncConfig.DstAddrMulticast[:], syncMulticast[:])
	syncConfig.PortMulticast = 9999
	fwConfig, err := cfwstate.NewModuleConfig(agent, "sync-fwstate", &syncConfig, map4.Name(), "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = fwConfig.Free() })
	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{fwConfig.AsFFIModule()}))

	aclConfig, err := backend.NewModule("sync-acl", createStateRules(), map4.Name(), "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = aclConfig.Free() })
	require.NoError(t, backend.UpdateModule(aclConfig))
	wireACLFWStateSyncPipeline(t, agent, "sync-acl", "sync-fwstate")
	h.SetCurrentTime(time.Unix(1, 0))

	result, err := h.HandlePacketsOnWorker(1, syncStatePacket(t))
	require.NoError(t, err)
	require.Len(t, result.Output, 2, "the original and one emitted sync packet")
	_, ip6, _, payload := parseSyncPacket(t, result.Output[1].RawData)
	require.True(t, ip6.DstIP.Equal(net.ParseIP("ff02::1")))

	records, err := map4.StashRecords(1)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, objfwstate.StashRecordApplied, records[0].Status)
	require.Equal(t, records[0].Frame, payload)
	records, err = map4.StashRecords(0)
	require.NoError(t, err)
	require.Empty(t, records, "worker 0's slot stays untouched")

	// Worker 0 then uses its own slot, and worker 1's record stays put.
	result, err = h.HandlePacketsOnWorker(0, syncStatePacketWithPort(t, 2222))
	require.NoError(t, err)
	require.Len(t, result.Output, 2)
	_, _, _, payload0 := parseSyncPacket(t, result.Output[1].RawData)
	records, err = map4.StashRecords(0)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, records[0].Frame, payload0)
	require.NotEqual(t, payload, payload0)
	records, err = map4.StashRecords(1)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, payload, records[0].Frame)
}

// Test_ACL_FWState_ConfigOnTwoDevicesEmitsOnce verifies that one fwstate
// config whose function is attached to two devices, and so runs in two
// execution contexts on the same worker every round, emits a stashed
// record once rather than once per context.
func Test_ACL_FWState_ConfigOnTwoDevicesEmitsOnce(t *testing.T) {
	h, agent, backend := setupACLFWStateSyncHarnessTopology(t, 1, []string{"port0", "port1"})
	map4, map6 := publishSyncMaps(t, agent)

	syncConfig := cfwstate.DefaultSyncConfig()
	syncSource := netip.MustParseAddr("2001:db8::1").As16()
	syncMulticast := netip.MustParseAddr("ff02::1").As16()
	copy(syncConfig.SrcAddr[:], syncSource[:])
	syncConfig.DstEther = [6]byte{0x33, 0x33, 0x00, 0x00, 0x00, 0x01}
	copy(syncConfig.DstAddrMulticast[:], syncMulticast[:])
	syncConfig.PortMulticast = 9999
	fwConfig, err := cfwstate.NewModuleConfig(agent, "shared-fwstate", &syncConfig, map4.Name(), map6.Name())
	require.NoError(t, err)
	t.Cleanup(func() { _ = fwConfig.Free() })
	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{fwConfig.AsFFIModule()}))

	aclConfig, err := backend.NewModule("shared-acl", createStateRules(), map4.Name(), map6.Name())
	require.NoError(t, err)
	t.Cleanup(func() { _ = aclConfig.Free() })
	require.NoError(t, backend.UpdateModule(aclConfig))

	sinkRules := []cforward.ForwardRule{
		{Target: "port0", Mode: cforward.ModeOut, Counter: "sink4"},
		{Target: "port0", Mode: cforward.ModeOut, Counter: "sink6"},
	}
	sinkHandle, err := forward.NewBackend(agent).UpdateModule("shared-sink", sinkRules)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sinkHandle.Free() })
	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: "shared",
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: "shared_chain",
				Modules: []ffi.ChainModuleConfig{
					{Type: "acl", Name: "shared-acl"},
					{Type: "fwstate", Name: "shared-fwstate"},
					{Type: "forward", Name: "shared-sink"},
				},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "shared", Functions: []string{"shared"}}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "dummy"}))
	var devices []ffi.DeviceConfig
	for _, name := range []string{"port0", "port1"} {
		devices = append(devices, ffi.DeviceConfig{
			Name:   name,
			Input:  []ffi.DevicePipelineConfig{{Name: "shared", Weight: 1}},
			Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
		})
	}
	_, err = plain.UpdateDevices(agent, devices)
	require.NoError(t, err)
	h.SetCurrentTime(time.Unix(1, 0))

	packet := syncStatePacket(t)
	result, err := h.HandlePackets(packet)
	require.NoError(t, err)
	require.Len(t, result.Output, 2, "the original and one emitted sync packet")
	require.Equal(t, packet.Data(), result.Output[0].RawData)
	_, _, _, payload := parseSyncPacket(t, result.Output[1].RawData)
	require.Len(t, payload, objfwstate.SyncFrameSize)

	var forwarded uint64
	for _, device := range []string{"port0", "port1"} {
		counters := h.SharedMemory().DPConfig(0).ModuleCounters(
			device, "shared", "shared", "shared_chain",
			"fwstate", "shared-fwstate", []string{"fwstate_internal_forwarded"},
		)
		for _, counter := range counters {
			for _, values := range counter.Values {
				forwarded += values[0]
			}
		}
	}
	require.Equal(t, uint64(1), forwarded, "one emission across both device contexts")
}

// setupACLFWStateSyncHarness returns an object-backed harness with both state
// modules loaded for one worker.
func setupACLFWStateSyncHarness(t *testing.T) (*dataplaneut.Harness, *ffi.Agent, acl.Backend) {
	t.Helper()
	return setupACLFWStateSyncHarnessWorkers(t, 1)
}

// setupACLFWStateSyncHarnessWorkers is setupACLFWStateSyncHarness with the
// given number of dataplane workers.
func setupACLFWStateSyncHarnessWorkers(t *testing.T, workerCount uint64) (*dataplaneut.Harness, *ffi.Agent, acl.Backend) {
	t.Helper()
	return setupACLFWStateSyncHarnessTopology(t, workerCount, []string{"port0"})
}

// setupACLFWStateSyncHarnessTopology is setupACLFWStateSyncHarness with the
// given number of dataplane workers and devices.
func setupACLFWStateSyncHarnessTopology(
	t *testing.T, workerCount uint64, devices []string,
) (*dataplaneut.Harness, *ffi.Agent, acl.Backend) {
	t.Helper()

	h, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(aclCPSize),
		DPMemory:      uint64(aclDPSize),
		WorkerCount:   workerCount,
		Devices:       devices,
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

// wireACLForwardFWStatePipeline chains ACL, a forwarding module standing in
// for any module between the state producer and its consumers, the named
// fwstate configurations in order, and a final forwarding module that sends
// what the fwstate configurations emit to the test device.
func wireACLForwardFWStatePipeline(t *testing.T, agent *ffi.Agent, aclName string, fwstateNames ...string) {
	t.Helper()

	sinkRules := []cforward.ForwardRule{
		{Target: "port0", Mode: cforward.ModeOut, Counter: "sink4"},
		{Target: "port0", Mode: cforward.ModeOut, Counter: "sink6"},
	}
	middleName := aclName + "-middle"
	sinkName := aclName + "-sink"
	for _, name := range []string{middleName, sinkName} {
		handle, err := forward.NewBackend(agent).UpdateModule(name, sinkRules)
		require.NoError(t, err)
		t.Cleanup(func() { _ = handle.Free() })
	}

	modules := []ffi.ChainModuleConfig{
		{Type: "acl", Name: aclName},
		{Type: "forward", Name: middleName},
	}
	for _, name := range fwstateNames {
		modules = append(modules, ffi.ChainModuleConfig{Type: "fwstate", Name: name})
	}
	modules = append(modules, ffi.ChainModuleConfig{Type: "forward", Name: sinkName})
	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: aclName,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain:  ffi.ChainConfig{Name: aclName + "_chain", Modules: modules},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: aclName, Functions: []string{aclName}}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "dummy"}))
	_, err := plain.UpdateDevices(agent, []ffi.DeviceConfig{{
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
