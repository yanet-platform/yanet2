package route_test

import (
	"net"
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	route "github.com/yanet-platform/yanet2/modules/route/controlplane"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb"
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
	t *testing.T,
	deviceName string,
) (*dataplaneut.Harness, *ffi.Agent, route.Backend) {
	t.Helper()

	cfg := dataplaneut.Config{
		CPMemory:      uint64(routeCPSize),
		DPMemory:      uint64(routeDPSize),
		WorkerCount:   1,
		Devices:       []string{deviceName},
		Modules:       []string{"route"},
		DevicesToLoad: []string{"plain"},
	}
	h, err := dataplaneut.NewHarness(cfg)
	require.NoError(t, err)
	t.Cleanup(h.Free)

	shm := h.SharedMemory()
	agent, err := shm.AgentAttach("r-test", 0, routeMemSize)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

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
	t *testing.T,
	agent *ffi.Agent,
	deviceName, configName string,
) {
	t.Helper()

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
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
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      configName,
		Functions: []string{configName},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name: "dummy",
	}))
	require.NoError(t, agent.UpdatePlainDevices([]ffi.DeviceConfig{{
		Name:   deviceName,
		Input:  []ffi.DevicePipelineConfig{{Name: configName, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}}))
}

// applyFIB pushes entries via backend.UpdateModule and registers cleanup.
//
// Returns the route.ModuleHandle; the caller may inspect it. The handle is
// freed via t.Cleanup.
func applyFIB(
	t *testing.T,
	backend route.Backend,
	name string,
	entries []FIBEntry,
) route.ModuleHandle {
	t.Helper()

	pbEntries := make([]*routepb.FIBEntry, 0, len(entries))
	for _, e := range entries {
		nexthops := make([]*routepb.FIBNexthop, 0, len(e.Nexthops))
		for _, nh := range e.Nexthops {
			nexthops = append(nexthops, &routepb.FIBNexthop{
				DstMac: commonpb.NewMACAddressEUI48([6]byte(nh.DstMAC)),
				SrcMac: commonpb.NewMACAddressEUI48([6]byte(nh.SrcMAC)),
				Device: nh.Device,
			})
		}
		pbEntries = append(pbEntries, &routepb.FIBEntry{
			Prefix:   e.Prefix.String(),
			Nexthops: nexthops,
		})
	}

	handle, err := backend.UpdateModule(name, pbEntries)
	require.NoError(t, err)
	t.Cleanup(handle.Free)
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

		byName := dataplaneut.SingleValueCounters(h.SharedMemory().DPConfig(0).PipelineCounters("port0", "test"))

		// The route module dispatches forwarded packets to the device output
		// queue inside the pipeline, so packet_front->output is empty at the
		// end of pipeline_ectx_process.
		//
		// The pipeline "output" counter therefore reflects packets that
		// remained in the output list — zero for a routed packet.
		//
		// The distinguishing property of a forwarded packet is that
		// drop == 0 while input == 1.
		require.Equal(t, uint64(1), byName["input"], "input counter must equal 1")
		require.Equal(t, uint64(0), byName["output"], "routed packet leaves via device queue, not pipeline output list")
		require.Equal(t, uint64(0), byName["drop"], "drop counter must equal 0")
		require.Equal(t, pktSize, byName["input_bytes"], "input_bytes must equal packet size")
		require.Equal(t, uint64(0), byName["output_bytes"], "output_bytes must equal 0 for device-dispatched packet")
		require.Equal(t, uint64(0), byName["drop_bytes"], "drop_bytes must equal 0")

		// Device tx counter is the real pass signal: device_ectx_process_output
		// increments tx when the packet is handed off to the NIC queue.
		byDevName := dataplaneut.SingleValueCounters(h.SharedMemory().DPConfig(0).DeviceCounters("port0"))
		require.Equal(t, uint64(1), byDevName["rx"], "device rx must equal 1")
		require.Equal(t, uint64(1), byDevName["tx"], "device tx (pass) must equal 1")
		require.Equal(t, pktSize, byDevName["rx_bytes"], "device rx_bytes must equal packet size")
		require.Equal(t, pktSize, byDevName["tx_bytes"], "device tx_bytes (pass) must equal packet size")
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

		byName := dataplaneut.SingleValueCounters(h.SharedMemory().DPConfig(0).PipelineCounters("port0", "test"))
		require.Equal(t, uint64(1), byName["input"], "input counter must equal 1")
		require.Equal(t, uint64(0), byName["output"], "output counter must equal 0")
		require.Equal(t, uint64(1), byName["drop"], "drop counter must equal 1")
		require.Equal(t, pktSize, byName["input_bytes"], "input_bytes must equal packet size")
		require.Equal(t, uint64(0), byName["output_bytes"], "output_bytes must equal 0")
		require.Equal(t, pktSize, byName["drop_bytes"], "drop_bytes must equal packet size")

		// Device tx stays zero for a dropped packet: device_ectx_process_output
		// is never reached, so no tx increment occurs.
		byDevName := dataplaneut.SingleValueCounters(h.SharedMemory().DPConfig(0).DeviceCounters("port0"))
		require.Equal(t, uint64(1), byDevName["rx"], "device rx must equal 1")
		require.Equal(t, uint64(0), byDevName["tx"], "device tx must equal 0 — packet dropped")
		require.Equal(t, pktSize, byDevName["rx_bytes"], "device rx_bytes must equal packet size")
		require.Equal(t, uint64(0), byDevName["tx_bytes"], "device tx_bytes must equal 0")
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
