package route_test

import (
	"encoding/hex"
	"net"
	"net/netip"
	"strings"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xnetip"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	route "github.com/yanet-platform/yanet2/modules/route/controlplane"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
)

const (
	routeCPSize  = 16 * datasize.MB
	routeDPSize  = 4 * datasize.MB
	routeMemSize = 2 * datasize.MB
)

// FIBNexthop is a test-domain nexthop descriptor.
type FIBNexthop struct {
	DstMAC net.HardwareAddr
	SrcMAC net.HardwareAddr
	Device string
	// Counter is the per-nexthop dataplane counter name.
	//
	// applyFIB passes this straight to backend.UpdateModule, bypassing
	// RouteService.UpdateFIB, so nothing on this path auto-fills an empty
	// value.
	Counter string
}

// FIBEntry is a test-domain FIB prefix with associated nexthops.
type FIBEntry struct {
	Prefix   netip.Prefix
	Nexthops []FIBNexthop
}

// routeNextHop is the shared nexthop used across single-hop tests.
//
// Packets forwarded via this hop will have their Ethernet header rewritten
// with these MACs and egress via "port0".
var routeNextHop = FIBNexthop{
	DstMAC: xerror.Unwrap(net.ParseMAC("de:ad:be:ef:00:01")),
	SrcMAC: xerror.Unwrap(net.ParseMAC("ca:fe:ba:be:00:01")),
	Device: "port0",
}

// setupRouteHarness builds the harness, attaches a control-plane agent,
// and constructs the production route.Backend over the attached agent.
//
// The harness, agent, and backend are returned. Cleanup is wired via
// t.Cleanup in LIFO order. Pipeline wiring must follow after calling
// applyFIB, because the route module config must exist in shared memory
// before UpdatePlainDevices resolves chain module references.
func setupRouteHarness(
	tb testing.TB,
	deviceName string,
) (*dataplaneut.Harness, *ffi.Agent, route.Backend) {
	tb.Helper()

	cfg := dataplaneut.Config{
		CPMemory:      uint64(routeCPSize),
		DPMemory:      uint64(routeDPSize),
		WorkerCount:   1,
		Devices:       []string{deviceName},
		Modules:       []string{"route"},
		DevicesToLoad: []string{"plain"},
	}
	h, err := dataplaneut.NewHarness(cfg)
	require.NoError(tb, err)
	tb.Cleanup(h.Free)

	shm := h.SharedMemory()
	agent, err := shm.AgentAttach("r-test", 0, routeMemSize)
	require.NoError(tb, err)
	tb.Cleanup(func() { _ = agent.CleanUp() })

	backend := route.NewBackend(agent)
	return h, agent, backend
}

// wirePipeline wires a chain[route:configName] -> function -> pipeline -> plain
// device topology.
//
// Must be called after applyFIB so that the route module config named
// configName is already present in shared memory when the pipeline resolves
// its chain module references.
func wirePipeline(
	tb testing.TB,
	agent *ffi.Agent,
	deviceName, configName string,
) {
	tb.Helper()

	require.NoError(tb, agent.UpdateFunction(ffi.FunctionConfig{
		Name: configName,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: configName + "_chain",
				Modules: []ffi.ChainModuleConfig{
					{Type: "route", Name: configName},
				},
			},
		}},
	}))
	require.NoError(tb, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      configName,
		Functions: []string{configName},
	}))
	require.NoError(tb, agent.UpdatePipeline(ffi.PipelineConfig{
		Name: "dummy",
	}))
	require.NoError(tb, agent.UpdatePlainDevices([]ffi.DeviceConfig{{
		Name:   deviceName,
		Input:  []ffi.DevicePipelineConfig{{Name: configName, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}}))
}

// toFIBEntries converts test-domain FIB entries to their wire form.
func toFIBEntries(tb testing.TB, entries []FIBEntry) []*routepb.FIBEntry {
	tb.Helper()

	pbEntries := make([]*routepb.FIBEntry, 0, len(entries))
	for _, e := range entries {
		nexthops := make([]*routepb.FIBNexthop, 0, len(e.Nexthops))
		for _, nh := range e.Nexthops {
			nexthops = append(nexthops, &routepb.FIBNexthop{
				DstMac:  commonpb.NewMACAddressEUI48([6]byte(nh.DstMAC)),
				SrcMac:  commonpb.NewMACAddressEUI48([6]byte(nh.SrcMAC)),
				Device:  nh.Device,
				Counter: nh.Counter,
			})
		}
		ipRange, err := commonpb.NewIPRange(e.Prefix.Addr(), xnetip.LastAddr(e.Prefix))
		require.NoError(tb, err)
		pbEntries = append(pbEntries, &routepb.FIBEntry{
			Range:    ipRange,
			Nexthops: nexthops,
		})
	}
	return pbEntries
}

// applyFIB pushes entries via backend.UpdateModule and registers cleanup.
//
// Returns the route.ModuleHandle. The caller may inspect it. The handle is
// freed via tb.Cleanup.
func applyFIB(
	tb testing.TB,
	backend route.Backend,
	name string,
	entries []FIBEntry,
) route.ModuleHandle {
	tb.Helper()

	handle, err := backend.UpdateModule(name, toFIBEntries(tb, entries))
	require.NoError(tb, err)
	tb.Cleanup(handle.Free)
	return handle
}

// testingEtherLayers returns a reusable set of Ethernet and IP layers for
// building route test packets.
func testingEtherLayers() (layers.Ethernet, layers.IPv4, layers.IPv6, layers.ICMPv4) {
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    net.ParseIP("10.0.0.1"),
		DstIP:    net.ParseIP("192.168.1.1"),
	}
	ip6 := layers.IPv6{
		Version:    6,
		HopLimit:   64,
		NextHeader: layers.IPProtocolICMPv6,
		SrcIP:      net.ParseIP("::1"),
		DstIP:      net.ParseIP("2001:db8::1"),
	}
	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
	}
	return eth, ip4, ip6, icmp
}

// TestRoute_IPv4_Forward verifies that an IPv4 packet destined for a known
// prefix is forwarded with Ethernet header rewritten and TTL decremented by
// one.
func TestRoute_IPv4_Forward(t *testing.T) {
	eth, ip4, _, icmp := testingEtherLayers()

	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
	t.Log("Origin packet", pkt)

	prefix := netip.MustParsePrefix("192.168.1.0/24")

	h, agent, backend := setupRouteHarness(t, "port0")
	applyFIB(t, backend, "test", []FIBEntry{
		{Prefix: prefix, Nexthops: []FIBNexthop{routeNextHop}},
	})
	wirePipeline(t, agent, "port0", "test")

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "expected one forwarded packet")
	require.Empty(t, result.Drop, "expected no dropped packets")

	resultPkt := xpacket.ParseEtherPacket(result.Output[0].RawData)
	t.Log("Result packet", resultPkt)

	// Ethernet header must be rewritten with the nexthop MACs.
	ethOut := layers.Ethernet{
		SrcMAC:       routeNextHop.SrcMAC,
		DstMAC:       routeNextHop.DstMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	// TTL must be decremented by one.
	ip4Out := ip4
	ip4Out.TTL = 63
	expectedPkt := xpacket.LayersToPacket(t, &ethOut, &ip4Out, &icmp)
	t.Log("Expected packet", expectedPkt)

	diff := cmp.Diff(
		expectedPkt.Layers(),
		resultPkt.Layers(),
		cmpopts.IgnoreUnexported(layers.ICMPv4{}),
	)
	require.Empty(t, diff)

	// Verify that the IPv4 layer parsed cleanly.
	ip4Layer := resultPkt.Layer(layers.LayerTypeIPv4)
	require.NotNil(t, ip4Layer, "IPv4 layer must be present in result")
}

// TestRoute_IPv6_Forward verifies that an IPv6 packet destined for a known
// prefix is forwarded with Ethernet header rewritten and HopLimit decremented
// by one.
func TestRoute_IPv6_Forward(t *testing.T) {
	_, _, ip6, _ := testingEtherLayers()

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv6,
	}
	icmp6 := layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
	}
	icmp6.SetNetworkLayerForChecksum(&ip6)

	pkt := xpacket.LayersToPacket(t, &eth, &ip6, &icmp6)
	t.Log("Origin packet", pkt)

	prefix := netip.MustParsePrefix("2001:db8::/32")

	h, agent, backend := setupRouteHarness(t, "port0")
	applyFIB(t, backend, "test", []FIBEntry{
		{Prefix: prefix, Nexthops: []FIBNexthop{routeNextHop}},
	})
	wirePipeline(t, agent, "port0", "test")

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "expected one forwarded packet")
	require.Empty(t, result.Drop, "expected no dropped packets")

	resultPkt := xpacket.ParseEtherPacket(result.Output[0].RawData)
	t.Log("Result packet", resultPkt)

	ethOut := layers.Ethernet{
		SrcMAC:       routeNextHop.SrcMAC,
		DstMAC:       routeNextHop.DstMAC,
		EthernetType: layers.EthernetTypeIPv6,
	}
	// HopLimit must be decremented by one.
	ip6Out := ip6
	ip6Out.HopLimit = 63
	expectedPkt := xpacket.LayersToPacket(t, &ethOut, &ip6Out, &icmp6)
	t.Log("Expected packet", expectedPkt)

	diff := cmp.Diff(
		expectedPkt.Layers(),
		resultPkt.Layers(),
		cmpopts.IgnoreUnexported(layers.IPv6{}, layers.ICMPv6{}),
	)
	require.Empty(t, diff)
}

// TestRoute_TTL_Drop verifies that IPv4 packets with TTL ≤ 1 are dropped and
// packets with TTL ≥ 2 are forwarded with the TTL decremented.
func TestRoute_TTL_Drop(t *testing.T) {
	prefix := netip.MustParsePrefix("192.168.1.0/24")

	cases := []struct {
		name            string
		ttl             uint8
		expectForwarded bool
	}{
		{name: "ttl_zero_drop", ttl: 0, expectForwarded: false},
		{name: "ttl_one_drop", ttl: 1, expectForwarded: false},
		{name: "ttl_two_forward", ttl: 2, expectForwarded: true},
		{name: "ttl_64_forward", ttl: 64, expectForwarded: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eth, ip4, _, icmp := testingEtherLayers()
			ip4.TTL = tc.ttl

			pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)

			h, agent, backend := setupRouteHarness(t, "port0")
			applyFIB(t, backend, "test", []FIBEntry{
				{Prefix: prefix, Nexthops: []FIBNexthop{routeNextHop}},
			})
			wirePipeline(t, agent, "port0", "test")

			result, err := h.HandlePackets(pkt)
			require.NoError(t, err)

			if tc.expectForwarded {
				require.Len(t, result.Output, 1, "expected forwarded packet")
				require.Empty(t, result.Drop, "expected no dropped packets")
				resultPkt := xpacket.ParseEtherPacket(result.Output[0].RawData)
				ip4Layer := resultPkt.Layer(layers.LayerTypeIPv4).(*layers.IPv4)
				require.Equal(t, uint8(tc.ttl-1), ip4Layer.TTL)
			} else {
				require.Empty(t, result.Output, "expected no forwarded packets")
				require.Len(t, result.Drop, 1, "expected dropped packet")
			}
		})
	}
}

// TestRoute_HopLimit_Drop verifies that IPv6 packets with HopLimit ≤ 1 are
// dropped and packets with HopLimit ≥ 2 are forwarded with HopLimit
// decremented.
func TestRoute_HopLimit_Drop(t *testing.T) {
	prefix := netip.MustParsePrefix("2001:db8::/32")

	cases := []struct {
		name            string
		hopLimit        uint8
		expectForwarded bool
	}{
		{name: "hop_limit_zero_drop", hopLimit: 0, expectForwarded: false},
		{name: "hop_limit_one_drop", hopLimit: 1, expectForwarded: false},
		{name: "hop_limit_two_forward", hopLimit: 2, expectForwarded: true},
		{name: "hop_limit_64_forward", hopLimit: 64, expectForwarded: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eth := layers.Ethernet{
				SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
				DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
				EthernetType: layers.EthernetTypeIPv6,
			}
			_, _, ip6, _ := testingEtherLayers()
			ip6.HopLimit = tc.hopLimit
			icmp6 := layers.ICMPv6{
				TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
			}
			icmp6.SetNetworkLayerForChecksum(&ip6)

			pkt := xpacket.LayersToPacket(t, &eth, &ip6, &icmp6)

			h, agent, backend := setupRouteHarness(t, "port0")
			applyFIB(t, backend, "test", []FIBEntry{
				{Prefix: prefix, Nexthops: []FIBNexthop{routeNextHop}},
			})
			wirePipeline(t, agent, "port0", "test")

			result, err := h.HandlePackets(pkt)
			require.NoError(t, err)

			if tc.expectForwarded {
				require.Len(t, result.Output, 1, "expected forwarded packet")
				require.Empty(t, result.Drop, "expected no dropped packets")
				resultPkt := xpacket.ParseEtherPacket(result.Output[0].RawData)
				ip6Layer := resultPkt.Layer(layers.LayerTypeIPv6).(*layers.IPv6)
				require.Equal(t, uint8(tc.hopLimit-1), ip6Layer.HopLimit)
			} else {
				require.Empty(t, result.Output, "expected no forwarded packets")
				require.Len(t, result.Drop, 1, "expected dropped packet")
			}
		})
	}
}

// TestRoute_NoMatch_Drop verifies that a packet whose destination address is
// not covered by any installed prefix is dropped.
func TestRoute_NoMatch_Drop(t *testing.T) {
	eth, ip4, _, icmp := testingEtherLayers()
	// Destination is not covered by any prefix in the LPM.
	ip4.DstIP = net.ParseIP("10.99.99.99")

	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
	t.Log("Origin packet", pkt)

	prefix := netip.MustParsePrefix("192.168.1.0/24")

	h, agent, backend := setupRouteHarness(t, "port0")
	applyFIB(t, backend, "test", []FIBEntry{
		{Prefix: prefix, Nexthops: []FIBNexthop{routeNextHop}},
	})
	wirePipeline(t, agent, "port0", "test")

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Empty(t, result.Output, "unrouted packet must be dropped")
	require.Len(t, result.Drop, 1, "expected exactly one dropped packet")
}

// TestRoute_NonIP_Drop verifies that non-IP Ethernet frames (e.g. ARP) are
// dropped by the route module.
func TestRoute_NonIP_Drop(t *testing.T) {
	prefix := netip.MustParsePrefix("192.168.1.0/24")

	h, agent, backend := setupRouteHarness(t, "port0")
	applyFIB(t, backend, "test", []FIBEntry{
		{Prefix: prefix, Nexthops: []FIBNexthop{routeNextHop}},
	})
	wirePipeline(t, agent, "port0", "test")

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("ff:ff:ff:ff:ff:ff")),
		EthernetType: layers.EthernetTypeARP,
	}
	arp := layers.ARP{
		AddrType:          layers.LinkTypeEthernet,
		Protocol:          layers.EthernetTypeIPv4,
		HwAddressSize:     6,
		ProtAddressSize:   4,
		Operation:         layers.ARPRequest,
		SourceHwAddress:   eth.SrcMAC,
		SourceProtAddress: net.ParseIP("10.0.0.1").To4(),
		DstHwAddress:      net.HardwareAddr{0, 0, 0, 0, 0, 0},
		DstProtAddress:    net.ParseIP("10.0.0.2").To4(),
	}

	pkt := xpacket.LayersToPacket(t, &eth, &arp)
	t.Log("Origin packet", pkt)

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Empty(t, result.Output, "non-IP packet must be dropped")
	require.Len(t, result.Drop, 1, "expected exactly one dropped packet")
}

// TestRoute_ECMP_HashSelection verifies ECMP nexthop selection based on
// per-packet hash values.
//
// Two nexthops share one prefix. Packets with hash=0 select slot 0 (hop0)
// and packets with hash=1 select slot 1 (hop1), because the route module
// picks route_list[packet->hash % count].
func TestRoute_ECMP_HashSelection(t *testing.T) {
	const (
		// hashForFirstHop selects route_list[0 % 2] = hop0.
		hashForFirstHop uint32 = 0
		// hashForSecondHop selects route_list[1 % 2] = hop1.
		hashForSecondHop uint32 = 1
	)

	hop0 := FIBNexthop{
		DstMAC: xerror.Unwrap(net.ParseMAC("de:ad:00:00:00:01")),
		SrcMAC: xerror.Unwrap(net.ParseMAC("ca:fe:00:00:00:01")),
		Device: "port0",
	}
	hop1 := FIBNexthop{
		DstMAC: xerror.Unwrap(net.ParseMAC("de:ad:00:00:00:02")),
		SrcMAC: xerror.Unwrap(net.ParseMAC("ca:fe:00:00:00:02")),
		Device: "port1",
	}

	prefix := netip.MustParsePrefix("192.168.1.0/24")

	// Build a two-device harness so both nexthop egress ports are registered.
	cfg := dataplaneut.Config{
		CPMemory:      uint64(routeCPSize),
		DPMemory:      uint64(routeDPSize),
		WorkerCount:   1,
		Devices:       []string{"port0", "port1"},
		Modules:       []string{"route"},
		DevicesToLoad: []string{"plain"},
	}
	h, err := dataplaneut.NewHarness(cfg)
	require.NoError(t, err)
	t.Cleanup(h.Free)

	shm := h.SharedMemory()
	agent, err := shm.AgentAttach("r-ecmp", 0, routeMemSize)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	backend := route.NewBackend(agent)

	applyFIB(t, backend, "test", []FIBEntry{
		{Prefix: prefix, Nexthops: []FIBNexthop{hop0, hop1}},
	})

	// Wire both devices through the pipeline. Each device gets its own
	// function and chain so the module config reference ("test") resolves.
	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: "test",
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: "test_chain",
				Modules: []ffi.ChainModuleConfig{
					{Type: "route", Name: "test"},
				},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      "test",
		Functions: []string{"test"},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name: "dummy0",
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name: "dummy1",
	}))
	require.NoError(t, agent.UpdatePlainDevices([]ffi.DeviceConfig{
		{
			Name:   "port0",
			Input:  []ffi.DevicePipelineConfig{{Name: "test", Weight: 1}},
			Output: []ffi.DevicePipelineConfig{{Name: "dummy0", Weight: 1}},
		},
		{
			Name:   "port1",
			Input:  []ffi.DevicePipelineConfig{{Name: "test", Weight: 1}},
			Output: []ffi.DevicePipelineConfig{{Name: "dummy1", Weight: 1}},
		},
	}))

	// Build four identical packets and inject explicit hashes: two packets
	// with hash=0 must hit hop0 and two with hash=1 must hit hop1.
	eth, ip4, _, icmp := testingEtherLayers()
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
	hashes := []uint32{hashForFirstHop, hashForSecondHop, hashForFirstHop, hashForSecondHop}

	result, err := h.HandlePacketsWithHashes(hashes, pkt, pkt, pkt, pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 4, "all packets must be forwarded")
	require.Empty(t, result.Drop, "no packets should be dropped")

	// Identify which nexthop each packet used by inspecting its destination MAC.
	hop0Count, hop1Count := 0, 0
	for _, info := range result.Output {
		resultPkt := xpacket.ParseEtherPacket(info.RawData)
		ethLayer := resultPkt.Layer(layers.LayerTypeEthernet).(*layers.Ethernet)
		switch ethLayer.DstMAC.String() {
		case hop0.DstMAC.String():
			hop0Count++
		case hop1.DstMAC.String():
			hop1Count++
		}
	}
	t.Logf("ECMP distribution: hop0=%d hop1=%d", hop0Count, hop1Count)
	require.Equal(t, 2, hop0Count, "hop0 must be selected for hash=0 packets")
	require.Equal(t, 2, hop1Count, "hop1 must be selected for hash=1 packets")
}

// TestRoute_Counters verifies that the pipeline increments per-direction packet
// and byte counters correctly for both forwarded and dropped packets.
func TestRoute_Counters(t *testing.T) {
	prefix := netip.MustParsePrefix("10.0.0.0/24")

	t.Run("pass", func(t *testing.T) {
		eth, ip4, _, icmp := testingEtherLayers()
		ip4.DstIP = net.ParseIP("10.0.0.5")

		pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
		pktSize := uint64(len(pkt.Data()))

		h, agent, backend := setupRouteHarness(t, "port0")
		applyFIB(t, backend, "test", []FIBEntry{
			{Prefix: prefix, Nexthops: []FIBNexthop{routeNextHop}},
		})
		wirePipeline(t, agent, "port0", "test")

		result, err := h.HandlePackets(pkt)
		require.NoError(t, err)
		require.Len(t, result.Output, 1, "expected one forwarded packet")
		require.Empty(t, result.Drop, "expected no dropped packets")

		byName := dataplaneut.ValueCounters(h.SharedMemory().DPConfig(0).PipelineCounters("port0", "test"))

		// The route module dispatches forwarded packets to the device output
		// queue inside the pipeline, so packet_front->output is empty at the
		// end of pipeline_ectx_process.
		//
		// The pipeline "output" counter therefore reflects packets that
		// remained in the output list — zero for a routed packet.
		//
		// The distinguishing property of a forwarded packet is that
		// drop == 0 while input == 1.
		require.Equal(t, uint64(1), byName["input"][0], "input counter must equal 1")
		require.Equal(t, uint64(0), byName["output"][0], "routed packet leaves via device queue, not pipeline output list")
		require.Equal(t, uint64(0), byName["drop"][0], "drop counter must equal 0")
		require.Equal(t, pktSize, byName["input"][1], "input_bytes must equal packet size")
		require.Equal(t, uint64(0), byName["output"][1], "output_bytes must equal 0 for device-dispatched packet")
		require.Equal(t, uint64(0), byName["drop"][1], "drop_bytes must equal 0")
		require.Equal(t, uint64(0), byName["pending_input"][0], "pipeline pending_input must equal 0")
		require.Equal(t, uint64(1), byName["pending_output"][0], "pipeline pending_output must equal 1 — route defers packet to output path")
		require.Equal(t, uint64(0), byName["pending_input"][1], "pipeline pending_input bytes must equal 0")
		require.Equal(t, pktSize, byName["pending_output"][1], "pipeline pending_output bytes must equal packet size")

		// size-2 vector: [0] = packets, [1] = bytes.
		byDevName := dataplaneut.ValueCounters(h.SharedMemory().DPConfig(0).DeviceCounters("port0"))
		require.Equal(t, uint64(1), byDevName["input_rx"][0], "device input_rx must equal 1")
		require.Equal(t, uint64(1), byDevName["input_entry"][0], "device input_entry (handler output) must equal 1")
		require.Equal(t, uint64(0), byDevName["input_tx"][0], "device input_tx (survived input handler) must equal 0")
		require.Equal(t, uint64(0), byDevName["input_drop"][0], "device input_drop must equal 0")
		require.Equal(t, uint64(1), byDevName["output_rx"][0], "device output_rx must equal 1")
		require.Equal(t, uint64(1), byDevName["output_entry"][0], "device output_entry (handler output) must equal 1")
		require.Equal(t, uint64(1), byDevName["output_tx"][0], "device output_tx (pass) must equal 1")
		require.Equal(t, uint64(0), byDevName["output_drop"][0], "device output_drop must equal 0")
		require.Equal(t, pktSize, byDevName["input_rx"][1], "device input_rx bytes must equal packet size")
		require.Equal(t, pktSize, byDevName["input_entry"][1], "device input_entry bytes must equal packet size")
		require.Equal(t, uint64(0), byDevName["input_tx"][1], "device input_tx bytes must equal 0")
		require.Equal(t, uint64(0), byDevName["input_drop"][1], "device input_drop bytes must equal 0")
		require.Equal(t, pktSize, byDevName["output_rx"][1], "device output_rx bytes must equal packet size")
		require.Equal(t, pktSize, byDevName["output_entry"][1], "device output_entry bytes must equal packet size")
		require.Equal(t, pktSize, byDevName["output_tx"][1], "device output_tx bytes (pass) must equal packet size")
		require.Equal(t, uint64(0), byDevName["output_drop"][1], "device output_drop bytes must equal 0")
		require.Equal(t, uint64(0), byDevName["input_pending_input"][0], "device input_pending_input must equal 0")
		require.Equal(t, uint64(1), byDevName["input_pending_output"][0], "device input_pending_output must equal 1 — route defers packet to output path")
		require.Equal(t, uint64(0), byDevName["output_pending_input"][0], "device output_pending_input must equal 0")
		require.Equal(t, uint64(0), byDevName["output_pending_output"][0], "device output_pending_output must equal 0")

		// The route module forwards a matched packet, so its own drop
		// counter stays zero (the module-level drop uses a per-module
		// delta, so it reflects only what this module dropped).
		routeModulePath := dataplaneut.CounterPath{
			Device: "port0", Pipeline: "test", Function: "test",
			Chain: "test_chain", ModuleType: "route", ModuleName: "test",
		}
		dataplaneut.RequireModuleCounter(t, h, routeModulePath, "rx", 1, pktSize)
		dataplaneut.RequireModuleCounter(t, h, routeModulePath, "tx", 0, 0)
		dataplaneut.RequireModuleCounter(t, h, routeModulePath, "drop", 0, 0)
	})

	t.Run("drop", func(t *testing.T) {
		eth, ip4, _, icmp := testingEtherLayers()
		ip4.DstIP = net.ParseIP("192.168.99.99")

		pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
		pktSize := uint64(len(pkt.Data()))

		h, agent, backend := setupRouteHarness(t, "port0")
		applyFIB(t, backend, "test", []FIBEntry{
			{Prefix: prefix, Nexthops: []FIBNexthop{routeNextHop}},
		})
		wirePipeline(t, agent, "port0", "test")

		result, err := h.HandlePackets(pkt)
		require.NoError(t, err)
		require.Empty(t, result.Output, "expected no forwarded packets")
		require.Len(t, result.Drop, 1, "expected one dropped packet")

		byName := dataplaneut.ValueCounters(h.SharedMemory().DPConfig(0).PipelineCounters("port0", "test"))
		require.Equal(t, uint64(1), byName["input"][0], "input counter must equal 1")
		require.Equal(t, uint64(0), byName["output"][0], "output counter must equal 0")
		require.Equal(t, uint64(1), byName["drop"][0], "drop counter must equal 1")
		require.Equal(t, pktSize, byName["input"][1], "input_bytes must equal packet size")
		require.Equal(t, uint64(0), byName["output"][1], "output_bytes must equal 0")
		require.Equal(t, pktSize, byName["drop"][1], "drop_bytes must equal packet size")
		require.Equal(t, uint64(0), byName["pending_input"][0], "pipeline pending_input must equal 0")
		require.Equal(t, uint64(0), byName["pending_output"][0], "pipeline pending_output must equal 0")

		// size-2 vector: [0] = packets, [1] = bytes.
		byDevName := dataplaneut.ValueCounters(h.SharedMemory().DPConfig(0).DeviceCounters("port0"))
		require.Equal(t, uint64(1), byDevName["input_rx"][0], "device input_rx must equal 1")
		require.Equal(t, uint64(1), byDevName["input_entry"][0], "device input_entry (handler output) must equal 1")
		require.Equal(t, uint64(0), byDevName["input_tx"][0], "device input_tx must equal 0")
		require.Equal(t, uint64(1), byDevName["input_drop"][0], "device input_drop must equal 1")
		require.Equal(t, uint64(0), byDevName["output_rx"][0], "device output_rx must equal 0 — packet never reached output handler")
		require.Equal(t, uint64(0), byDevName["output_entry"][0], "device output_entry must equal 0 — packet never reached output handler")
		require.Equal(t, uint64(0), byDevName["output_tx"][0], "device output_tx must equal 0 — packet dropped")
		require.Equal(t, uint64(0), byDevName["output_drop"][0], "device output_drop must equal 0")
		require.Equal(t, pktSize, byDevName["input_rx"][1], "device input_rx bytes must equal packet size")
		require.Equal(t, pktSize, byDevName["input_entry"][1], "device input_entry bytes must equal packet size")
		require.Equal(t, uint64(0), byDevName["input_tx"][1], "device input_tx bytes must equal 0")
		require.Equal(t, pktSize, byDevName["input_drop"][1], "device input_drop bytes must equal packet size")
		require.Equal(t, uint64(0), byDevName["output_rx"][1], "device output_rx bytes must equal 0")
		require.Equal(t, uint64(0), byDevName["output_entry"][1], "device output_entry bytes must equal 0")
		require.Equal(t, uint64(0), byDevName["output_tx"][1], "device output_tx bytes must equal 0")
		require.Equal(t, uint64(0), byDevName["output_drop"][1], "device output_drop bytes must equal 0")
		require.Equal(t, uint64(0), byDevName["input_pending_input"][0], "device input_pending_input must equal 0")
		require.Equal(t, uint64(0), byDevName["input_pending_output"][0], "device input_pending_output must equal 0")
		require.Equal(t, uint64(0), byDevName["output_pending_input"][0], "device output_pending_input must equal 0")
		require.Equal(t, uint64(0), byDevName["output_pending_output"][0], "device output_pending_output must equal 0")

		// The route module drops an unmatched packet itself, so its own
		// drop counter records it (rx in, drop out, tx stays zero).
		routeModulePath := dataplaneut.CounterPath{
			Device: "port0", Pipeline: "test", Function: "test",
			Chain: "test_chain", ModuleType: "route", ModuleName: "test",
		}
		dataplaneut.RequireModuleCounter(t, h, routeModulePath, "rx", 1, pktSize)
		dataplaneut.RequireModuleCounter(t, h, routeModulePath, "tx", 0, 0)
		dataplaneut.RequireModuleCounter(t, h, routeModulePath, "drop", 1, pktSize)
	})
}

// TestRoute_DeviceTranslation_Drop verifies that a route referencing an
// unregistered output device causes the packet to be dropped.
//
// When a nexthop names a device ("phantom") that is not registered as a
// cp_device, the mc_index slot for that module-device stays at the sentinel
// value (-1).
//
// The route handler calls module_ectx_encode_device which returns the
// sentinel, then drops the packet.
func TestRoute_DeviceTranslation_Drop(t *testing.T) {
	eth, ip4, _, icmp := testingEtherLayers()
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
	t.Log("Origin packet", pkt)

	prefix := netip.MustParsePrefix("192.168.1.0/24")

	// The nexthop references "phantom" — a device name that will never
	// appear in UpdatePlainDevices, leaving mc_index at the sentinel.
	phantomHop := FIBNexthop{
		DstMAC: xerror.Unwrap(net.ParseMAC("de:ad:00:00:00:ff")),
		SrcMAC: xerror.Unwrap(net.ParseMAC("ca:fe:00:00:00:ff")),
		Device: "phantom",
	}

	h, agent, backend := setupRouteHarness(t, "port0")
	applyFIB(t, backend, "test", []FIBEntry{
		{Prefix: prefix, Nexthops: []FIBNexthop{phantomHop}},
	})

	// Wire only "port0" through UpdatePlainDevices.
	//
	// The "phantom" device referenced by the nexthop has no matching
	// cp_device, so its mc_index slot remains at the sentinel (-1) after
	// the link step.
	wirePipeline(t, agent, "port0", "test")

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Empty(t, result.Output, "packet with unregistered device must be dropped")
	require.Len(t, result.Drop, 1, "expected exactly one dropped packet")
}

// routeCounterNames lists every per-outcome counter registered by the route
// module in modules/route/api/controlplane.c.
//
// TestRoute_PerOutcomeCounters asserts the target counter for each outcome
// moved by exactly one packet while every sibling stayed at zero.
var routeCounterNames = []string{
	"route_forwarded_v4",
	"route_forwarded_v6",
	"route_drop_no_route_v4",
	"route_drop_no_route_v6",
	"route_drop_ttl_expired_v4",
	"route_drop_ttl_expired_v6",
	"route_drop_non_ip",
	"route_drop_empty_route_list_v4",
	"route_drop_empty_route_list_v6",
	"route_drop_device_unresolved_v4",
	"route_drop_device_unresolved_v6",
}

// buildRouteIPv4Packet builds an ICMPv4 echo-request Ethernet frame with the
// given destination address and TTL.
func buildRouteIPv4Packet(t *testing.T, dstIP string, ttl uint8) gopacket.Packet {
	t.Helper()

	eth, ip4, _, icmp := testingEtherLayers()
	ip4.DstIP = net.ParseIP(dstIP)
	ip4.TTL = ttl
	return xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
}

// buildRouteIPv6Packet builds an ICMPv6 echo-request Ethernet frame with the
// given destination address and hop limit.
func buildRouteIPv6Packet(t *testing.T, dstIP string, hopLimit uint8) gopacket.Packet {
	t.Helper()

	_, _, ip6, _ := testingEtherLayers()
	ip6.DstIP = net.ParseIP(dstIP)
	ip6.HopLimit = hopLimit
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv6,
	}
	icmp6 := layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
	}
	icmp6.SetNetworkLayerForChecksum(&ip6)
	return xpacket.LayersToPacket(t, &eth, &ip6, &icmp6)
}

// buildRouteARPPacket builds a non-IP (ARP) Ethernet frame.
func buildRouteARPPacket(t *testing.T) gopacket.Packet {
	t.Helper()

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("ff:ff:ff:ff:ff:ff")),
		EthernetType: layers.EthernetTypeARP,
	}
	arp := layers.ARP{
		AddrType:          layers.LinkTypeEthernet,
		Protocol:          layers.EthernetTypeIPv4,
		HwAddressSize:     6,
		ProtAddressSize:   4,
		Operation:         layers.ARPRequest,
		SourceHwAddress:   eth.SrcMAC,
		SourceProtAddress: net.ParseIP("10.0.0.1").To4(),
		DstHwAddress:      net.HardwareAddr{0, 0, 0, 0, 0, 0},
		DstProtAddress:    net.ParseIP("10.0.0.2").To4(),
	}
	return xpacket.LayersToPacket(t, &eth, &arp)
}

// TestRoute_PerOutcomeCounters verifies the full dataplane counter chain for
// each reachable route outcome.
//
// A matching packet flows through route_handle_packets, increments the real
// per-outcome counter_storage slot, and is read back under the exact registered
// name via DPConfig.ModuleCounters. For every case the target counter must move
// by exactly one packet and the injected byte length, while every sibling
// per-outcome counter stays at zero — proving both non-vacuity (the asserted
// byte count is the real frame length, not merely > 0) and cross-counter
// isolation.
//
// Two of the eleven counters are intentionally uncovered:
// route_drop_empty_route_list_{v4,v6} is unreachable because the backend skips
// prefixes with no nexthops, so no packet can ever reach that outcome.
func TestRoute_PerOutcomeCounters(t *testing.T) {
	// The phantomNextHop name refers to a device that is never wired via
	// UpdatePlainDevices, leaving mc_index at the sentinel (-1) so
	// module_ectx_encode_device fails and the packet is dropped — the same
	// construction as TestRoute_DeviceTranslation_Drop.
	phantomNextHop := FIBNexthop{
		DstMAC: xerror.Unwrap(net.ParseMAC("de:ad:00:00:00:ff")),
		SrcMAC: xerror.Unwrap(net.ParseMAC("ca:fe:00:00:00:ff")),
		Device: "phantom",
	}

	// FIB installed so matching packets reach a nexthop. A TTL check fires
	// before the lookup, so ttl-expired cases still target a real prefix.
	fib := []FIBEntry{
		{Prefix: netip.MustParsePrefix("10.0.0.0/24"), Nexthops: []FIBNexthop{routeNextHop}},
		{Prefix: netip.MustParsePrefix("2001:db8::/32"), Nexthops: []FIBNexthop{routeNextHop}},
		{Prefix: netip.MustParsePrefix("172.16.0.0/24"), Nexthops: []FIBNexthop{phantomNextHop}},
		{Prefix: netip.MustParsePrefix("2001:db8:beef::/32"), Nexthops: []FIBNexthop{phantomNextHop}},
	}

	cases := []struct {
		name          string
		packet        func(t *testing.T) gopacket.Packet
		wantCounter   string
		wantForwarded bool
	}{
		{
			name:          "forwarded_v4",
			packet:        func(t *testing.T) gopacket.Packet { return buildRouteIPv4Packet(t, "10.0.0.5", 64) },
			wantCounter:   "route_forwarded_v4",
			wantForwarded: true,
		},
		{
			name:          "forwarded_v6",
			packet:        func(t *testing.T) gopacket.Packet { return buildRouteIPv6Packet(t, "2001:db8::1", 64) },
			wantCounter:   "route_forwarded_v6",
			wantForwarded: true,
		},
		{
			name:          "ttl_expired_v4",
			packet:        func(t *testing.T) gopacket.Packet { return buildRouteIPv4Packet(t, "10.0.0.5", 1) },
			wantCounter:   "route_drop_ttl_expired_v4",
			wantForwarded: false,
		},
		{
			name:          "ttl_expired_v6",
			packet:        func(t *testing.T) gopacket.Packet { return buildRouteIPv6Packet(t, "2001:db8::1", 1) },
			wantCounter:   "route_drop_ttl_expired_v6",
			wantForwarded: false,
		},
		{
			name:          "no_route_v4",
			packet:        func(t *testing.T) gopacket.Packet { return buildRouteIPv4Packet(t, "10.99.99.99", 64) },
			wantCounter:   "route_drop_no_route_v4",
			wantForwarded: false,
		},
		{
			name:          "no_route_v6",
			packet:        func(t *testing.T) gopacket.Packet { return buildRouteIPv6Packet(t, "2002::1", 64) },
			wantCounter:   "route_drop_no_route_v6",
			wantForwarded: false,
		},
		{
			name:          "non_ip",
			packet:        func(t *testing.T) gopacket.Packet { return buildRouteARPPacket(t) },
			wantCounter:   "route_drop_non_ip",
			wantForwarded: false,
		},
		{
			name:          "device_unresolved_v4",
			packet:        func(t *testing.T) gopacket.Packet { return buildRouteIPv4Packet(t, "172.16.0.5", 64) },
			wantCounter:   "route_drop_device_unresolved_v4",
			wantForwarded: false,
		},
		{
			name:          "device_unresolved_v6",
			packet:        func(t *testing.T) gopacket.Packet { return buildRouteIPv6Packet(t, "2001:db8:beef::1", 64) },
			wantCounter:   "route_drop_device_unresolved_v6",
			wantForwarded: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pkt := tc.packet(t)
			pktSize := uint64(len(pkt.Data()))

			h, agent, backend := setupRouteHarness(t, "port0")
			applyFIB(t, backend, "test", fib)
			wirePipeline(t, agent, "port0", "test")

			result, err := h.HandlePackets(pkt)
			require.NoError(t, err)
			if tc.wantForwarded {
				require.Len(t, result.Output, 1, "expected one forwarded packet")
				require.Empty(t, result.Drop, "expected no dropped packets")
			} else {
				require.Empty(t, result.Output, "expected no forwarded packets")
				require.Len(t, result.Drop, 1, "expected one dropped packet")
			}

			path := dataplaneut.CounterPath{
				Device: "port0", Pipeline: "test", Function: "test",
				Chain: "test_chain", ModuleType: "route", ModuleName: "test",
			}

			for _, name := range routeCounterNames {
				if name == tc.wantCounter {
					dataplaneut.RequireModuleCounter(t, h, path, name, 1, pktSize)
				} else {
					dataplaneut.RequireModuleCounter(t, h, path, name, 0, 0)
				}
			}
		})
	}
}

// TestRoute_NexthopCounter verifies that a nexthop carrying an explicit
// counter name reaches the real C counter registry: a matching packet
// increments the named counter by exactly one packet and its own byte
// length.
func TestRoute_NexthopCounter(t *testing.T) {
	const counterName = "nexthop_port0_deadbeef0001"

	countedHop := FIBNexthop{
		DstMAC:  xerror.Unwrap(net.ParseMAC("de:ad:be:ef:00:01")),
		SrcMAC:  xerror.Unwrap(net.ParseMAC("ca:fe:ba:be:00:01")),
		Device:  "port0",
		Counter: counterName,
	}

	h, agent, backend := setupRouteHarness(t, "port0")
	handle := applyFIB(t, backend, "test", []FIBEntry{
		{Prefix: netip.MustParsePrefix("10.0.0.0/24"), Nexthops: []FIBNexthop{countedHop}},
	})
	wirePipeline(t, agent, "port0", "test")

	pkt := buildRouteIPv4Packet(t, "10.0.0.5", 64)
	pktSize := uint64(len(pkt.Data()))

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "expected one forwarded packet")
	require.Empty(t, result.Drop, "expected no dropped packets")

	path := dataplaneut.CounterPath{
		Device: "port0", Pipeline: "test", Function: "test",
		Chain: "test_chain", ModuleType: "route", ModuleName: "test",
	}
	dataplaneut.RequireRuleCounter(t, h, path, counterName, 1, pktSize)

	// Close the round trip: ShowFIB's DumpFIB path must resolve the same
	// name back out of the real C counter registry, not just accept it on
	// write.
	fib, err := handle.DumpFIB()
	require.NoError(t, err)
	require.Len(t, fib, 1)
	require.Len(t, fib[0].Nexthops, 1)
	require.Equal(t, counterName, fib[0].Nexthops[0].Counter)
}

// TestRoute_ECMPSameIdentitySameCounterDedupesToOneMember verifies that
// listing the same (src_mac, dst_mac, device) nexthop twice under the same
// counter name resolves to a single route-list member, not two.
//
// A duplicated member takes 2/N of the ECMP hash space instead of 1/N: this
// exercises the real backend.UpdateModule dedup against shared memory and
// inspects the resulting nexthop set via DumpFIB, since a fake backend
// cannot observe route-list membership at all.
func TestRoute_ECMPSameIdentitySameCounterDedupesToOneMember(t *testing.T) {
	prefix := netip.MustParsePrefix("192.168.4.0/24")
	hop := FIBNexthop{
		DstMAC:  xerror.Unwrap(net.ParseMAC("de:ad:be:ef:00:04")),
		SrcMAC:  xerror.Unwrap(net.ParseMAC("ca:fe:ba:be:00:04")),
		Device:  "port0",
		Counter: "nexthop_dup",
	}

	_, _, backend := setupRouteHarness(t, "port0")
	handle := applyFIB(t, backend, "test", []FIBEntry{
		{Prefix: prefix, Nexthops: []FIBNexthop{hop, hop}},
	})

	fib, err := handle.DumpFIB()
	require.NoError(t, err)
	require.Len(t, fib, 1)
	require.Len(t, fib[0].Nexthops, 1, "a duplicated hardware route must collapse to one route-list member")
}

// TestRoute_ECMPDistinctSourceMACRemainsSeparate verifies that two nexthops
// sharing a destination MAC and device but differing in source MAC remain
// distinct route-list members, since source MAC is part of the forwarding
// identity.
func TestRoute_ECMPDistinctSourceMACRemainsSeparate(t *testing.T) {
	prefix := netip.MustParsePrefix("192.168.5.0/24")
	dstMAC := xerror.Unwrap(net.ParseMAC("de:ad:be:ef:00:05"))
	hopA := FIBNexthop{
		DstMAC:  dstMAC,
		SrcMAC:  xerror.Unwrap(net.ParseMAC("ca:fe:ba:be:00:05")),
		Device:  "port0",
		Counter: "nexthop_a",
	}
	hopB := FIBNexthop{
		DstMAC:  dstMAC,
		SrcMAC:  xerror.Unwrap(net.ParseMAC("ca:fe:ba:be:00:06")),
		Device:  "port0",
		Counter: "nexthop_b",
	}

	_, _, backend := setupRouteHarness(t, "port0")
	handle := applyFIB(t, backend, "test", []FIBEntry{
		{Prefix: prefix, Nexthops: []FIBNexthop{hopA, hopB}},
	})

	fib, err := handle.DumpFIB()
	require.NoError(t, err)
	require.Len(t, fib, 1)
	require.Len(t, fib[0].Nexthops, 2, "nexthops differing only in src_mac must remain distinct routes")
}

// TestUpdateFIB_EmptyNexthopEntryDoesNotDisplaceEarlierEntry verifies the
// UpdateFIBRequest doc comment's claim that an entry with no nexthops is
// skipped rather than overwriting an earlier overlapping entry.
//
// It exercises the real backend.UpdateModule against shared memory, since
// the fake backend used by the unit tests in modules/route/controlplane
// only records the entries handed to it and cannot observe the
// dataplane-visible skip.
func TestUpdateFIB_EmptyNexthopEntryDoesNotDisplaceEarlierEntry(t *testing.T) {
	prefix := netip.MustParsePrefix("10.0.0.0/24")

	_, _, backend := setupRouteHarness(t, "port0")

	handle := applyFIB(t, backend, "cfg", []FIBEntry{
		{Prefix: prefix, Nexthops: []FIBNexthop{routeNextHop}},
		// Later entry covers the same range but carries no nexthops, so
		// it must be skipped rather than blackholing or removing the
		// earlier entry's route.
		{Prefix: prefix},
	})

	fib, err := handle.DumpFIB()
	require.NoError(t, err)

	require.Len(t, fib, 1, "expected the earlier nexthop-bearing entry to survive alone")
	require.Len(t, fib[0].Nexthops, 1)
	require.Equal(t, "port0", fib[0].Nexthops[0].Device)
	require.Empty(t, fib[0].Nexthops[0].Counter, "uncounted nexthop must round-trip as empty, not a stale or garbage name")
}

// nexthopCounterLabels returns the "counter" label value of every
// route_nexthop_packets series.
func nexthopCounterLabels(all []*commonpb.Metric) []string {
	var names []string
	for _, metric := range all {
		if metric.GetName() != "route_nexthop_packets" {
			continue
		}
		for _, label := range metric.GetLabels() {
			if label.GetName() == "counter" {
				names = append(names, label.GetValue())
			}
		}
	}
	return names
}

// TestUpdateFIB_ShadowedNexthopCounterExcludedFromMetrics verifies that a
// nexthop entirely shadowed by a later, overlapping entry never surfaces a
// route_nexthop_* series, while the surviving nexthop's series does.
//
// It goes through RouteService.UpdateFIB and Metrics with the real backend:
// only the real LPM resolves the overlap, and a fake ModuleCounters can't
// reveal which names the metrics path actually asked the dataplane for.
func TestUpdateFIB_ShadowedNexthopCounterExcludedFromMetrics(t *testing.T) {
	shadowedHop := FIBNexthop{
		DstMAC:  xerror.Unwrap(net.ParseMAC("de:ad:be:ef:00:07")),
		SrcMAC:  xerror.Unwrap(net.ParseMAC("ca:fe:ba:be:00:07")),
		Device:  "port0",
		Counter: "nexthop_shadowed",
	}
	survivingHop := FIBNexthop{
		DstMAC:  xerror.Unwrap(net.ParseMAC("de:ad:be:ef:00:08")),
		SrcMAC:  xerror.Unwrap(net.ParseMAC("ca:fe:ba:be:00:08")),
		Device:  "port0",
		Counter: "nexthop_surviving",
	}

	_, agent, backend := setupRouteHarness(t, "port0")
	service := route.NewRouteService(backend)

	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "test",
		Entries: toFIBEntries(t, []FIBEntry{
			{Prefix: netip.MustParsePrefix("10.0.0.0/24"), Nexthops: []FIBNexthop{shadowedHop}},
			// Applied later, so it wins the whole range above plus more.
			{Prefix: netip.MustParsePrefix("10.0.0.0/23"), Nexthops: []FIBNexthop{survivingHop}},
		}),
	})
	require.NoError(t, err)

	wirePipeline(t, agent, "port0", "test")

	all, err := service.Metrics()
	require.NoError(t, err)

	names := nexthopCounterLabels(all)
	require.NotContains(t, names, "nexthop_shadowed", "a fully shadowed nexthop must not be scraped")
	require.Contains(t, names, "nexthop_surviving")
}

// nexthopCounterPrefix mirrors the unexported prefix constant in service.go
// without depending on it, so the generated-name check and the disabled
// arm's "no per-nexthop counter registered" check cannot drift apart.
const nexthopCounterPrefix = "nexthop_"

// nexthopCounterNames computes the per-nexthop counter names a counted arm
// must materialize for fib, deduplicated since several nexthops can share
// a (device, DstMAC) pair.
//
// A renamed materializer in service.go still gets caught by a name mismatch.
func nexthopCounterNames(fib []FIBEntry) []string {
	seen := map[string]bool{}
	names := make([]string, 0)
	for _, entry := range fib {
		for _, nexthop := range entry.Nexthops {
			name := nexthopCounterPrefix + nexthop.Device + "_" + hex.EncodeToString(nexthop.DstMAC)
			if seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

// applyRouteForwardArm pushes fib through service.UpdateFIB under module
// name "arms" and wires the pipeline topology on top of it.
//
// Shared by both counter arms so their only difference is the service's
// nexthop-counter option.
func applyRouteForwardArm(
	t *testing.T,
	service *route.RouteService,
	agent *ffi.Agent,
	fib []FIBEntry,
) {
	t.Helper()

	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "arms",
		Entries:    toFIBEntries(t, fib),
	})
	require.NoError(t, err)

	wirePipeline(t, agent, "port0", "arms")
}

// requireCounterModesDistinct guards the table this test derives its own
// expectations from: it fails if modes collapses to one kind instead of
// covering both.
//
// The test's assertions branch on the same mode.disabled field the service
// wiring branches on, so a table with only counted rows or only disabled
// rows would pass every sub-test while asserting nothing about the arm the
// row name claims to be.
func requireCounterModesDistinct(t *testing.T, modes []routeForwardCounterMode) {
	t.Helper()

	var haveCounted, haveDisabled bool
	for _, mode := range modes {
		if mode.disabled {
			haveDisabled = true
		} else {
			haveCounted = true
		}
	}
	require.True(t, haveCounted, "the counter axis must keep a counted mode")
	require.True(t, haveDisabled, "the counter axis must keep a disabled mode")
}

// TestRoute_NexthopCounterArms verifies that BenchmarkRouteForward's two
// counter modes exercise genuinely distinct dataplane paths.
//
// It ranges over routeForwardCounterModes, the same table the benchmark
// builds its arms from: the counted mode must materialize and increment a
// per-nexthop counter for every packet forwarded, and the disabled mode
// must never register one.
func TestRoute_NexthopCounterArms(t *testing.T) {
	requireCounterModesDistinct(t, routeForwardCounterModes)
	require.NotEmpty(t, routeForwardShapes, "the shape table must keep at least one shape")

	path := dataplaneut.CounterPath{
		Device: "port0", Pipeline: "arms", Function: "arms",
		Chain: "arms_chain", ModuleType: "route", ModuleName: "arms",
	}

	for _, shape := range routeForwardShapes {
		t.Run(shape.name, func(t *testing.T) {
			fib := buildRouteForwardFIB(shape)
			names := nexthopCounterNames(fib)
			require.NotEmpty(t, names, "shape must materialize at least one nexthop counter name")

			for _, mode := range routeForwardCounterModes {
				t.Run(mode.name, func(t *testing.T) {
					h, agent, backend := setupRouteHarness(t, "port0")
					service := route.NewRouteService(backend, mode.serviceOptions()...)
					applyRouteForwardArm(t, service, agent, fib)

					packets := buildRouteForwardPackets(t)
					result, err := h.HandlePackets(packets...)
					require.NoError(t, err)
					require.Len(t, result.Output, len(packets), "all packets must be forwarded")
					require.Empty(t, result.Drop)

					if mode.disabled {
						// Unfiltered on purpose: a filtered query would only
						// prove the *expected* names are absent, not that no
						// per-nexthop counter exists under any name.
						for _, counter := range dataplaneut.RuleCounters(t, h, path, nil) {
							require.False(t, strings.HasPrefix(counter.Name, nexthopCounterPrefix),
								"disabled arm must register no per-nexthop counter, found %q", counter.Name)
						}
						return
					}

					// Positive control for the disabled arm's unfiltered
					// query above: asserts the unfiltered result is a
					// superset of names, so a nil sentinel that degraded to a
					// filter excluding per-nexthop counters fails here.
					unfiltered := dataplaneut.RuleCounters(t, h, path, nil)
					unfilteredNames := make(map[string]bool, len(unfiltered))
					for _, counter := range unfiltered {
						unfilteredNames[counter.Name] = true
					}
					for _, name := range names {
						require.True(t, unfilteredNames[name],
							"unfiltered query must include per-nexthop counter %q", name)
					}

					counters := dataplaneut.RuleCounters(t, h, path, names)

					var wantBytes uint64
					for _, packet := range packets {
						wantBytes += uint64(len(packet.Data()))
					}

					byName := map[string][]uint64{}
					for _, counter := range counters {
						require.NotEmpty(t, counter.Values)
						_, dup := byName[counter.Name]
						require.False(t, dup, "counter name %q must not repeat in the query result", counter.Name)
						byName[counter.Name] = counter.Values[0]
					}

					var gotPackets, gotBytes uint64
					for _, name := range names {
						values, ok := byName[name]
						require.True(t, ok, "materialized counter %q must be registered", name)
						require.GreaterOrEqual(t, len(values), 2, "counter %q must have at least two values (packets, bytes)", name)
						gotPackets += values[0]
						gotBytes += values[1]
					}
					require.Equal(t, uint64(len(packets)), gotPackets, "per-nexthop counters must sum to every forwarded packet")
					require.Equal(t, wantBytes, gotBytes, "per-nexthop counters must sum to every forwarded byte")
				})
			}
		})
	}
}
