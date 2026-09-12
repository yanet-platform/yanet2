package l3b_test

import (
	"bytes"
	"fmt"
	"net"
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
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
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	forward "github.com/yanet-platform/yanet2/modules/forward/controlplane"
	"github.com/yanet-platform/yanet2/modules/l3b/bindings/go/cl3b"
	cl3bobject "github.com/yanet-platform/yanet2/objects/l3b/bindings/go/cl3bobject"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

const (
	l3bCPSize  = 64 * datasize.MB
	l3bDPSize  = 4 * datasize.MB
	l3bMemSize = 16 * datasize.MB
)

// setupL3bHarness builds the harness with the l3b and forward modules loaded,
// attaches a control-plane agent, and publishes an empty l3b module config.
//
// The forward module is loaded so a catch-all sink can route pass-through
// packets to egress. Returns the harness and the agent. Cleanup is wired via
// t.Cleanup in LIFO order. Pipeline wiring must follow after calling this
// function.
func setupL3bHarness(
	t *testing.T,
	deviceName string,
	configName string,
) (*dataplaneut.Harness, *ffi.Agent) {
	t.Helper()

	cfg := dataplaneut.Config{
		CPMemory:      uint64(l3bCPSize),
		DPMemory:      uint64(l3bDPSize),
		WorkerCount:   1,
		Devices:       []string{deviceName},
		Modules:       []string{"l3b", "forward"},
		DevicesToLoad: []string{"plain"},
		ObjectsToLoad: []string{"l3b_virtual_service", "l3b_session_table"},
	}
	h, err := dataplaneut.NewHarness(cfg)
	require.NoError(t, err)
	t.Cleanup(h.Free)

	shm := h.SharedMemory()
	agent, err := shm.AgentAttach("l3b-test", 0, l3bMemSize)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	mod, err := cl3b.NewModuleConfig(agent, configName)
	require.NoError(t, err)
	t.Cleanup(func() { _ = mod.Free() })

	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{mod.AsFFIModule()}))

	return h, agent
}

// catchAllForwardRules returns forward rules with ModeOut that route every
// packet to the given device's output stage.
func catchAllForwardRules(device string) []cforward.ForwardRule {
	return []cforward.ForwardRule{
		{
			Target:  device,
			Mode:    cforward.ModeOut,
			Counter: "sink4",
			Src4s:   []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			Dst4s:   []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		},
		{
			Target:  device,
			Mode:    cforward.ModeOut,
			Counter: "sink6",
			Src6s:   []xnetip.BiContiguous{filter.UnspecifiedIPv6},
			Dst6s:   []xnetip.BiContiguous{filter.UnspecifiedIPv6},
		},
	}
}

// wirePipeline wires a chain[l3b:configName -> forward:sink] -> pipeline ->
// plain device topology. The forward sink routes pass-through packets to the
// device output; packets dropped by l3b never reach it.
func wirePipeline(
	t *testing.T,
	agent *ffi.Agent,
	deviceName, configName string,
) {
	t.Helper()

	sinkName := configName + "-sink"
	sinkBackend := forward.NewBackend(agent)
	sinkHandle, err := sinkBackend.UpdateModule(sinkName, catchAllForwardRules(deviceName))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sinkHandle.Free() })

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: configName,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: configName + "_chain",
				Modules: []ffi.ChainModuleConfig{
					{Type: "l3b", Name: configName},
					{Type: "forward", Name: sinkName},
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
	_, err = plain.UpdateDevices(agent, []ffi.DeviceConfig{{
		Name:   deviceName,
		Input:  []ffi.DevicePipelineConfig{{Name: configName, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}})
	require.NoError(t, err)
}

// TestL3b_ForwardsNonTcpUdpPackets verifies that packets which are neither
// TCP nor UDP (here ICMPv4) pass through the module unchanged: they are not
// eligible for load balancing and reach the output via the sink.
func TestL3b_ForwardsNonTcpUdpPackets(t *testing.T) {
	h, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")

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
	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
	}

	packetCount := 3
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
	packets := make([]gopacket.Packet, 0, packetCount)
	for range packetCount {
		packets = append(packets, pkt)
	}

	result, err := h.HandlePackets(packets...)
	require.NoError(t, err)
	require.Empty(t, result.Drop, "non-TCP/UDP packets must not be dropped")
	require.Len(t, result.Output, packetCount, "non-TCP/UDP packets must be forwarded")
}

// TestL3b_DropsTcpWithoutVirtualService verifies that TCP packets are dropped
// when no virtual service is configured: with no matching virtual service the
// scheduler has nowhere to send the packet.
func TestL3b_DropsTcpWithoutVirtualService(t *testing.T) {
	h, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    net.ParseIP("10.0.0.1"),
		DstIP:    net.ParseIP("192.168.1.1"),
	}
	tcp := layers.TCP{
		SrcPort: 12345,
		DstPort: 80,
		Seq:     1,
		Window:  1024,
	}
	tcp.SetNetworkLayerForChecksum(&ip4)

	packetCount := 3
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &tcp)
	packets := make([]gopacket.Packet, 0, packetCount)
	for range packetCount {
		packets = append(packets, pkt)
	}

	result, err := h.HandlePackets(packets...)
	require.NoError(t, err)
	require.Empty(t, result.Output, "TCP packets must not be forwarded without a virtual service")
	require.Len(t, result.Drop, packetCount, "TCP packets must be dropped without a virtual service")
}

// ethHeaderLen is the Ethernet header length stripped when comparing the
// encapsulated inner packet with the original frame.
const ethHeaderLen = 14

// publishVirtualService creates a named virtual service object with a single
// IPv4 real server, installs a one-slot scheduler ring and publishes it into
// the dataplane. The real server tunnels towards realDst deriving the outer
// source from sourceNet.
func publishVirtualService(
	t *testing.T,
	agent *ffi.Agent,
	name string,
	sourceNet string,
	realDst string,
) *cl3bobject.VirtualServiceObject {
	t.Helper()

	serviceConfig := cl3bobject.VirtualServiceConfig{
		SourceFilterRules: []cl3bobject.SourceFilterRule{{
			Net4s:      []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			PortRanges: filter.PortRanges{{From: 1, To: 65535}},
		}},
		RealServers: []cl3bobject.RealServer{{
			Type:               cl3bobject.IPv4,
			DestinationAddress: xerror.Unwrap(netip.ParseAddr(realDst)),
			SourceNet:          xnetip.MustParseNetwork(sourceNet),
		}},
		HashMask:         0,
		IndexMask:        0,
		RingCapacity:     1 * 1000,
		SessionIndexSize: 4096,
	}

	object, err := cl3bobject.CreateVirtualService(agent, name, 1, serviceConfig, nil)
	require.NoError(t, err)

	require.NoError(t, object.UpdateRing([]uint32{0}))
	require.NoError(t, object.Publish(agent))
	return object
}

// publishModuleConfig installs a module configuration whose destination filter
// routes TCP traffic to 192.168.1.0/24 to the named virtual service object.
func publishModuleConfig(
	t *testing.T,
	agent *ffi.Agent,
	configName string,
	serviceName string,
) *cl3b.ModuleConfig {
	t.Helper()

	module, err := cl3b.NewModuleConfig(agent, configName)
	require.NoError(t, err)

	rules := []cl3b.DestinationFilterRule{{
		Net4s:          []xnetip.Contiguous[xnetip.Network4]{xnetip.MustParseContiguous4("192.168.1.0/24")},
		ProtoRanges:    filter.ProtoRanges{filter.NewProtoRange(6, filter.AnySubtype())},
		VirtualService: serviceName,
	}}
	require.NoError(t, module.Update(rules))

	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}))
	return module
}

// TestL3b_EncapsulatesTcpIntoIpip verifies the full object-linked path: a TCP
// packet matched by the module destination filter reaches the linked virtual
// service object and is encapsulated towards the real server with the derived
// outer source address.
func TestL3b_EncapsulatesTcpIntoIpip(t *testing.T) {
	h, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")

	service := publishVirtualService(t, agent, "svc", "192.0.2.0/24", "172.16.0.10")
	t.Cleanup(func() { _ = service.Free() })
	module := publishModuleConfig(t, agent, "test", "svc")
	t.Cleanup(func() { _ = module.Free() })

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	innerIP4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    net.ParseIP("10.0.0.1"),
		DstIP:    net.ParseIP("192.168.1.1"),
	}
	tcp := layers.TCP{
		SrcPort: 12345,
		DstPort: 80,
		Seq:     1,
		Window:  1024,
	}
	tcp.SetNetworkLayerForChecksum(&innerIP4)

	pkt := xpacket.LayersToPacket(t, &eth, &innerIP4, &tcp)
	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Empty(t, result.Drop, "matched TCP packets must not be dropped")
	require.Len(t, result.Output, 1, "matched TCP packets must be forwarded")

	info, err := framework.NewPacketParser().ParsePacket(result.Output[0].RawData)
	require.NoError(t, err)

	require.True(t, info.IsTunneled, "the output must be an IP-in-IP encapsulation")
	require.Equal(t, "ip4in4", info.TunnelType)
	require.Equal(t, layers.IPProtocolIPv4, info.Protocol,
		"the outer protocol must be IPPROTO_IPIP")
	require.Equal(t, "172.16.0.10", info.DstIP.String(),
		"the outer destination is the real server address")
	require.Equal(t, "192.0.2.1", info.SrcIP.String(),
		"the outer source must be source_net XOR (inner source AND ~mask)")

	require.NotNil(t, info.InnerPacket)
	require.Equal(t, layers.IPProtocolTCP, info.InnerPacket.Protocol)
	require.Equal(t, "10.0.0.1", info.InnerPacket.SrcIP.String())
	require.Equal(t, "192.168.1.1", info.InnerPacket.DstIP.String())
	require.True(t, bytes.HasSuffix(result.Output[0].RawData, pkt.Data()[ethHeaderLen:]),
		"the inner packet must be carried byte-identical")
}

// TestL3b_ForwardsNonInitialFragments verifies that a non-initial TCP
// fragment — which carries the flow's protocol number but no transport
// header — passes through the module untouched instead of being classified
// from payload bytes.
func TestL3b_ForwardsNonInitialFragments(t *testing.T) {
	h, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")

	service := publishVirtualService(t, agent, "svc", "192.0.2.0/24", "172.16.0.10")
	t.Cleanup(func() { _ = service.Free() })
	module := publishModuleConfig(t, agent, "test", "svc")
	t.Cleanup(func() { _ = module.Free() })

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:    4,
		TTL:        64,
		Protocol:   layers.IPProtocolTCP,
		SrcIP:      net.ParseIP("10.0.0.1"),
		DstIP:      net.ParseIP("192.168.1.1"),
		FragOffset: 1,
		Flags:      layers.IPv4MoreFragments,
	}

	pkt := xpacket.LayersToPacket(t, &eth, &ip4)
	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Empty(t, result.Drop, "non-initial fragments must not be dropped")
	require.Len(t, result.Output, 1, "non-initial fragments must pass through")

	info, err := framework.NewPacketParser().ParsePacket(result.Output[0].RawData)
	require.NoError(t, err)
	require.False(t, info.IsTunneled, "the fragment must not be encapsulated")
	require.Equal(t, "10.0.0.1", info.SrcIP.String())
	require.Equal(t, "192.168.1.1", info.DstIP.String())
}

// TestL3b_SessionSticksFlowToReal verifies the session table semantics: a
// flow keeps the real server it was pinned to even after the scheduler ring
// changes underneath, while a new flow follows the updated ring.
func TestL3b_SessionSticksFlowToReal(t *testing.T) {
	h, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")

	serviceConfig := cl3bobject.VirtualServiceConfig{
		SourceFilterRules: []cl3bobject.SourceFilterRule{{
			Net4s:      []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			PortRanges: filter.PortRanges{{From: 1, To: 65535}},
		}},
		RealServers: []cl3bobject.RealServer{
			{
				Type:               cl3bobject.IPv4,
				DestinationAddress: xerror.Unwrap(netip.ParseAddr("172.16.0.10")),
				SourceNet:          xnetip.MustParseNetwork("192.0.2.0/24"),
			},
			{
				Type:               cl3bobject.IPv4,
				DestinationAddress: xerror.Unwrap(netip.ParseAddr("172.16.0.11")),
				SourceNet:          xnetip.MustParseNetwork("192.0.2.0/24"),
			},
		},
		HashMask:         0,
		IndexMask:        0,
		RingCapacity:     2 * 1000,
		SessionIndexSize: 4096,
	}

	service, err := cl3bobject.CreateVirtualService(agent, "svc", 1, serviceConfig, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = service.Free() })
	require.NoError(t, service.UpdateRing([]uint32{0}))
	require.NoError(t, service.Publish(agent))

	module := publishModuleConfig(t, agent, "test", "svc")
	t.Cleanup(func() { _ = module.Free() })

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	packetOf := func(srcIP string, srcPort uint16) gopacket.Packet {
		ip4 := layers.IPv4{
			Version:  4,
			TTL:      64,
			Protocol: layers.IPProtocolTCP,
			SrcIP:    net.ParseIP(srcIP),
			DstIP:    net.ParseIP("192.168.1.1"),
		}
		tcp := layers.TCP{
			SrcPort: layers.TCPPort(srcPort),
			DstPort: 80,
			Seq:     1,
			Window:  1024,
		}
		tcp.SetNetworkLayerForChecksum(&ip4)
		return xpacket.LayersToPacket(t, &eth, &ip4, &tcp)
	}

	outerDstOf := func(packet gopacket.Packet) string {
		result, err := h.HandlePackets(packet)
		require.NoError(t, err)
		require.Empty(t, result.Drop)
		require.Len(t, result.Output, 1)
		info, err := framework.NewPacketParser().ParsePacket(result.Output[0].RawData)
		require.NoError(t, err)
		require.True(t, info.IsTunneled)
		return info.DstIP.String()
	}

	// The first flow is pinned to real 0 by the initial ring.
	flowA := packetOf("10.0.0.1", 12345)
	require.Equal(t, "172.16.0.10", outerDstOf(flowA))

	// Retarget the ring at real 1 only; the pinned flow must stay on
	// real 0 while a new flow lands on real 1.
	require.NoError(t, service.UpdateRing([]uint32{1}))

	require.Equal(t, "172.16.0.10", outerDstOf(flowA),
		"an existing session must keep its real across a ring update")
	require.Equal(t, "172.16.0.11", outerDstOf(packetOf("10.0.0.2", 54321)),
		"a new session must follow the updated ring")
}

// TestL3b_SessionSurvivesServiceUpdate verifies that replacing a virtual
// service object keeps its session table: flows pinned before the update keep
// their real servers while new flows follow the replacement's ring.
func TestL3b_SessionSurvivesServiceUpdate(t *testing.T) {
	h, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")

	serviceConfig := cl3bobject.VirtualServiceConfig{
		SourceFilterRules: []cl3bobject.SourceFilterRule{{
			Net4s:      []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			PortRanges: filter.PortRanges{{From: 1, To: 65535}},
		}},
		RealServers: []cl3bobject.RealServer{
			{
				Type:               cl3bobject.IPv4,
				DestinationAddress: xerror.Unwrap(netip.ParseAddr("172.16.0.10")),
				SourceNet:          xnetip.MustParseNetwork("192.0.2.0/24"),
			},
			{
				Type:               cl3bobject.IPv4,
				DestinationAddress: xerror.Unwrap(netip.ParseAddr("172.16.0.11")),
				SourceNet:          xnetip.MustParseNetwork("192.0.2.0/24"),
			},
		},
		HashMask:         0,
		IndexMask:        0,
		RingCapacity:     2 * 1000,
		SessionIndexSize: 4096,
	}

	service, err := cl3bobject.CreateVirtualService(agent, "svc", 1, serviceConfig, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = service.Free() })
	require.NoError(t, service.UpdateRing([]uint32{0}))
	require.NoError(t, service.Publish(agent))

	module := publishModuleConfig(t, agent, "test", "svc")
	t.Cleanup(func() { _ = module.Free() })

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	packetOf := func(srcIP string, srcPort uint16) gopacket.Packet {
		ip4 := layers.IPv4{
			Version:  4,
			TTL:      64,
			Protocol: layers.IPProtocolTCP,
			SrcIP:    net.ParseIP(srcIP),
			DstIP:    net.ParseIP("192.168.1.1"),
		}
		tcp := layers.TCP{
			SrcPort: layers.TCPPort(srcPort),
			DstPort: 80,
			Seq:     1,
			Window:  1024,
		}
		tcp.SetNetworkLayerForChecksum(&ip4)
		return xpacket.LayersToPacket(t, &eth, &ip4, &tcp)
	}
	outerDstOf := func(packet gopacket.Packet) string {
		result, err := h.HandlePackets(packet)
		require.NoError(t, err)
		require.Empty(t, result.Drop)
		require.Len(t, result.Output, 1)
		info, err := framework.NewPacketParser().ParsePacket(result.Output[0].RawData)
		require.NoError(t, err)
		require.True(t, info.IsTunneled)
		return info.DstIP.String()
	}

	// Pin the first flow to real 0 by the initial ring.
	flowA := packetOf("10.0.0.1", 12345)
	require.Equal(t, "172.16.0.10", outerDstOf(flowA))

	// Replace the service under the same name, adopting the session table
	// and retargeting the ring at real 1.
	replacement, err := cl3bobject.CreateVirtualService(agent, "svc", 1, serviceConfig, service)
	require.NoError(t, err)
	t.Cleanup(func() { _ = replacement.Free() })
	require.NoError(t, replacement.UpdateRing([]uint32{1}))
	require.NoError(t, replacement.Publish(agent))
	require.NoError(t, service.RetireService())

	require.Equal(t, "172.16.0.10", outerDstOf(flowA),
		"a session pinned before the update must keep its real")
	require.Equal(t, "172.16.0.11", outerDstOf(packetOf("10.0.0.2", 54321)),
		"a new session must follow the replacement's ring")

	// The inspection must report both real servers, live.
	info, err := replacement.Inspect()
	require.NoError(t, err)
	require.EqualValues(t, 0, info.HashMask)
	require.Len(t, info.RealServers, 2)
	require.Equal(t, "172.16.0.10", info.RealServers[0].DestinationAddress.String())
	require.Equal(t, "172.16.0.11", info.RealServers[1].DestinationAddress.String())
	require.True(t, info.RealServers[0].Enabled)
	require.True(t, info.RealServers[1].Enabled)

	// Both pinned flows must be visible through the session listing.
	sessions, next, _, err := replacement.ReadSessions(agent, 0, 100)
	require.NoError(t, err)
	require.Zero(t, next, "the listing must be complete")
	require.Len(t, sessions, 2)

	bySource := map[string]string{}
	for _, session := range sessions {
		bySource[fmt.Sprintf("%s:%d", session.SourceAddress, session.SourcePort)] =
			session.RealAddress.String()
	}
	require.Equal(t, map[string]string{
		"10.0.0.1:12345": "172.16.0.10",
		"10.0.0.2:54321": "172.16.0.11",
	}, bySource)

	// Replace the service again with a different real server list: the
	// pinned backend is gone, so the flow must re-pin onto the new ring
	// instead of landing on whatever now sits at its old position.
	replacementConfig := serviceConfig
	replacementConfig.RealServers = []cl3bobject.RealServer{
		{
			Type:               cl3bobject.IPv4,
			DestinationAddress: xerror.Unwrap(netip.ParseAddr("172.16.0.20")),
			SourceNet:          xnetip.MustParseNetwork("192.0.2.0/24"),
		},
		{
			Type:               cl3bobject.IPv4,
			DestinationAddress: xerror.Unwrap(netip.ParseAddr("172.16.0.21")),
			SourceNet:          xnetip.MustParseNetwork("192.0.2.0/24"),
		},
	}

	relisted, err := cl3bobject.CreateVirtualService(agent, "svc", 1, replacementConfig, replacement)
	require.NoError(t, err)
	t.Cleanup(func() { _ = relisted.Free() })
	require.NoError(t, relisted.UpdateRing([]uint32{0}))
	require.NoError(t, relisted.Publish(agent))
	require.NoError(t, replacement.RetireService())

	require.Equal(t, "172.16.0.20", outerDstOf(flowA),
		"a session whose backend left the list must re-pin onto the new ring")
}

// TestL3b_OnePacketSchedulingFromLinkCounter verifies one-packet scheduling:
// with the counter mode set, consecutive new flows rotate across the ring in
// arrival order regardless of their packet hashes, driven by the per-worker
// link packets counter.
func TestL3b_OnePacketSchedulingFromLinkCounter(t *testing.T) {
	h, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")

	serviceConfig := cl3bobject.VirtualServiceConfig{
		SourceFilterRules: []cl3bobject.SourceFilterRule{{
			Net4s:      []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			PortRanges: filter.PortRanges{{From: 1, To: 65535}},
		}},
		RealServers: []cl3bobject.RealServer{
			{
				Type:               cl3bobject.IPv4,
				DestinationAddress: xerror.Unwrap(netip.ParseAddr("172.16.0.10")),
				SourceNet:          xnetip.MustParseNetwork("192.0.2.0/24"),
			},
			{
				Type:               cl3bobject.IPv4,
				DestinationAddress: xerror.Unwrap(netip.ParseAddr("172.16.0.11")),
				SourceNet:          xnetip.MustParseNetwork("192.0.2.0/24"),
			},
		},
		HashMask:         0,
		IndexMask:        1,
		RingCapacity:     2 * 1000,
		SessionIndexSize: 4096,
		SchedulerFlags:   cl3bobject.SchedulerCounter,
	}

	service, err := cl3bobject.CreateVirtualService(agent, "svc", 1, serviceConfig, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = service.Free() })
	require.NoError(t, service.UpdateRing([]uint32{0, 1}))
	require.NoError(t, service.Publish(agent))

	module := publishModuleConfig(t, agent, "test", "svc")
	t.Cleanup(func() { _ = module.Free() })

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	packetOf := func(srcIP string, srcPort uint16) gopacket.Packet {
		ip4 := layers.IPv4{
			Version:  4,
			TTL:      64,
			Protocol: layers.IPProtocolTCP,
			SrcIP:    net.ParseIP(srcIP),
			DstIP:    net.ParseIP("192.168.1.1"),
		}
		tcp := layers.TCP{
			SrcPort: layers.TCPPort(srcPort),
			DstPort: 80,
			Seq:     1,
			Window:  1024,
		}
		tcp.SetNetworkLayerForChecksum(&ip4)
		return xpacket.LayersToPacket(t, &eth, &ip4, &tcp)
	}
	outerDstOf := func(packet gopacket.Packet) string {
		result, err := h.HandlePackets(packet)
		require.NoError(t, err)
		require.Empty(t, result.Drop)
		require.Len(t, result.Output, 1)
		info, err := framework.NewPacketParser().ParsePacket(result.Output[0].RawData)
		require.NoError(t, err)
		require.True(t, info.IsTunneled)
		return info.DstIP.String()
	}

	// Distinct flows (fresh sessions) land on the reals in strict ring
	// order, independent of each packet's hash: the per-worker link
	// counter drives the slot, so the first packet of the worker takes
	// slot 1 % 2 = 1 and the rotation continues from there.
	require.Equal(t, "172.16.0.11", outerDstOf(packetOf("10.0.0.1", 1001)))
	require.Equal(t, "172.16.0.10", outerDstOf(packetOf("10.0.0.2", 1002)))
	require.Equal(t, "172.16.0.11", outerDstOf(packetOf("10.0.0.3", 1003)))
	require.Equal(t, "172.16.0.10", outerDstOf(packetOf("10.0.0.4", 1004)))
}

// TestL3b_ServiceAndRealCounters verifies the service object's counters:
// incoming, filter rejections, ring-empty and disabled-real drops, plus the
// per-real throughput pairs.
func TestL3b_ServiceAndRealCounters(t *testing.T) {
	h, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")

	// The source filter accepts only port 1000; ring [0, 1] over two
	// reals; hash masks zero so the scheduler always picks slot 0.
	serviceConfig := cl3bobject.VirtualServiceConfig{
		SourceFilterRules: []cl3bobject.SourceFilterRule{{
			Net4s:      []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			PortRanges: filter.PortRanges{{From: 1000, To: 1000}},
		}},
		RealServers: []cl3bobject.RealServer{
			{
				Type:               cl3bobject.IPv4,
				DestinationAddress: xerror.Unwrap(netip.ParseAddr("172.16.0.10")),
				SourceNet:          xnetip.MustParseNetwork("192.0.2.0/24"),
			},
			{
				Type:               cl3bobject.IPv4,
				DestinationAddress: xerror.Unwrap(netip.ParseAddr("172.16.0.11")),
				SourceNet:          xnetip.MustParseNetwork("192.0.2.0/24"),
			},
		},
		HashMask:         0,
		IndexMask:        0,
		RingCapacity:     2 * 1000,
		SessionIndexSize: 4096,
	}

	service, err := cl3bobject.CreateVirtualService(agent, "svc", 1, serviceConfig, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = service.Free() })
	require.NoError(t, service.UpdateRing([]uint32{0, 1}))
	require.NoError(t, service.Publish(agent))

	module := publishModuleConfig(t, agent, "test", "svc")
	t.Cleanup(func() { _ = module.Free() })

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	packetOf := func(srcIP string, srcPort, dstPort uint16, syn ...bool) gopacket.Packet {
		ip4 := layers.IPv4{
			Version:  4,
			TTL:      64,
			Protocol: layers.IPProtocolTCP,
			SrcIP:    net.ParseIP(srcIP),
			DstIP:    net.ParseIP("192.168.1.1"),
		}
		tcp := layers.TCP{
			SrcPort: layers.TCPPort(srcPort),
			DstPort: layers.TCPPort(dstPort),
			Seq:     1,
			Window:  1024,
		}
		if len(syn) > 0 && syn[0] {
			tcp.SYN = true
		}
		tcp.SetNetworkLayerForChecksum(&ip4)
		return xpacket.LayersToPacket(t, &eth, &ip4, &tcp)
	}

	send := func(packet gopacket.Packet) {
		result, err := h.HandlePackets(packet)
		require.NoError(t, err)
		_ = result
	}
	counter := func(name string) (uint64, uint64) {
		ectx, err := h.PublishedExecutionContext(0)
		require.NoError(t, err)
		packets, bytes, err := cl3bobject.ReadServiceCounter(ectx, "svc", name)
		require.NoError(t, err)
		return packets, bytes
	}
	frameLen := func(packet gopacket.Packet) uint64 {
		return uint64(len(packet.Data()))
	}

	// Two accepted flows (destination port on the filter) land on real 0.
	accepted1 := packetOf("10.0.0.1", 1111, 1000)
	accepted2 := packetOf("10.0.0.2", 2222, 1000)
	send(accepted1)
	send(accepted2)

	// A flow outside the source filter's port range is rejected by it.
	filtered := packetOf("10.0.0.3", 3333, 80)
	send(filtered)

	inPackets, inBytes := counter("incoming")
	require.EqualValues(t, 3, inPackets)
	require.Equal(t, frameLen(accepted1)*2+frameLen(filtered), inBytes)

	filterPackets, filterBytes := counter("filter_rejected")
	require.EqualValues(t, 1, filterPackets)
	require.Equal(t, frameLen(filtered), filterBytes)

	realPackets, realBytes := counter("real/0")
	require.EqualValues(t, 2, realPackets)
	require.Equal(t, frameLen(accepted1)*2, realBytes)

	otherPackets, _ := counter("real/1")
	require.EqualValues(t, 0, otherPackets)

	// Empty the ring: the next fresh flow is rejected by the empty ring.
	require.NoError(t, service.UpdateRing(nil))
	ringEmptyFlow := packetOf("10.0.0.4", 4444, 1000)
	send(ringEmptyFlow)
	emptyPackets, emptyBytes := counter("ring_empty")
	require.EqualValues(t, 1, emptyPackets)
	require.Equal(t, frameLen(ringEmptyFlow), emptyBytes)

	// Disable real 0, pin a session to it beforehand... the earlier flows
	// already pinned sessions to real 0. Restore the ring and send one of
	// the pinned flows: the session's real is disabled, which counts as a
	// state rejection and re-pins onto the remaining real.
	require.NoError(t, service.SetRealServerState(0, false))
	require.NoError(t, service.UpdateRing([]uint32{1}))
	// A SYN may reschedule onto the remaining real; a flagless TCP flow
	// would be dropped under the session policy.
	pinnedFlow := packetOf("10.0.0.1", 1111, 1000, true)
	send(pinnedFlow)
	disabledPackets, disabledBytes := counter("real_disabled")
	require.EqualValues(t, 1, disabledPackets)
	require.Equal(t, frameLen(pinnedFlow), disabledBytes)

	// The re-pinned flow now counts on real 1.
	real1Packets, real1Bytes := counter("real/1")
	require.EqualValues(t, 1, real1Packets)
	require.Equal(t, frameLen(pinnedFlow), real1Bytes)
}

// TestL3b_AnswersIcmpEchoForServiceAddress verifies that ICMP echo requests
// matched by a destination rule are answered by the balancer in place —
// reply type, swapped addresses and refreshed TTL — instead of being
// dispatched to a real server, mirroring the first-generation balancer.
func TestL3b_AnswersIcmpEchoForServiceAddress(t *testing.T) {
	h, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")

	service, err := cl3bobject.CreateVirtualService(
		agent,
		"svc",
		1,
		cl3bobject.VirtualServiceConfig{
			SourceFilterRules: []cl3bobject.SourceFilterRule{{
				Net4s:      []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
				PortRanges: filter.PortRanges{{From: 1, To: 65535}},
			}},
			RealServers: []cl3bobject.RealServer{{
				Type:               cl3bobject.IPv4,
				DestinationAddress: xerror.Unwrap(netip.ParseAddr("172.16.0.10")),
				SourceNet:          xnetip.MustParseNetwork("192.0.2.0/24"),
			}},
			HashMask:         0,
			IndexMask:        0,
			RingCapacity:     1000,
			SessionIndexSize: 4096,
		},
		nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = service.Free() })
	require.NoError(t, service.UpdateRing([]uint32{0}))
	require.NoError(t, service.Publish(agent))

	// The destination rule routes ICMP echo requests (proto 1, subtype 8)
	// to the service.
	module, err := cl3b.NewModuleConfig(agent, "test")
	require.NoError(t, err)
	t.Cleanup(func() { _ = module.Free() })
	rules := []cl3b.DestinationFilterRule{{
		Net4s: []xnetip.Contiguous[xnetip.Network4]{
			xnetip.MustParseContiguous4("192.168.1.0/24"),
		},
		ProtoRanges: filter.ProtoRanges{
			filter.NewProtoRange(1, filter.ExactSubtype(8)),
		},
		VirtualService: "svc",
	}}
	require.NoError(t, module.Update(rules))
	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}))

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      1,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    net.ParseIP("10.0.0.1"),
		DstIP:    net.ParseIP("192.168.1.1"),
	}
	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
		Id:       0x1234,
		Seq:      7,
	}
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Empty(t, result.Drop, "echo requests must be answered, not dropped")
	require.Len(t, result.Output, 1, "the reply must be forwarded")

	info, err := framework.NewPacketParser().ParsePacket(result.Output[0].RawData)
	require.NoError(t, err)
	require.False(t, info.IsTunneled, "the reply comes from the balancer itself")
	require.Equal(t, "192.168.1.1", info.SrcIP.String(),
		"the reply's source is the service address")
	require.Equal(t, "10.0.0.1", info.DstIP.String(),
		"the reply returns to the requester")

	// The generated counter pair must observe the reply.
	ectx, err := h.PublishedExecutionContext(0)
	require.NoError(t, err)
	replied, _, err := cl3bobject.ReadServiceCounter(ectx, "svc", "icmp_replied")
	require.NoError(t, err)
	require.EqualValues(t, 1, replied)
	incoming, _, err := cl3bobject.ReadServiceCounter(ectx, "svc", "incoming")
	require.NoError(t, err)
	require.EqualValues(t, 1, incoming)
}

// TestL3b_FixesMssAndAppliesSessionPolicy verifies the service flags and the
// session lifetime policy: a SYN's MSS option is clamped to 1220 with the
// checksums kept valid, and an established TCP flow whose real went disabled
// is dropped instead of being rescheduled mid-connection.
func TestL3b_FixesMssAndAppliesSessionPolicy(t *testing.T) {
	h, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")

	service, err := cl3bobject.CreateVirtualService(
		agent,
		"svc",
		1,
		cl3bobject.VirtualServiceConfig{
			SourceFilterRules: []cl3bobject.SourceFilterRule{{
				Net4s:      []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
				PortRanges: filter.PortRanges{{From: 1, To: 65535}},
			}},
			RealServers: []cl3bobject.RealServer{{
				Type:               cl3bobject.IPv4,
				DestinationAddress: xerror.Unwrap(netip.ParseAddr("172.16.0.10")),
				SourceNet:          xnetip.MustParseNetwork("192.0.2.0/24"),
			}},
			HashMask:         0,
			IndexMask:        0,
			RingCapacity:     1000,
			SessionIndexSize: 4096,
			Flags:            cl3bobject.FixMSS,
		},
		nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = service.Free() })
	require.NoError(t, service.UpdateRing([]uint32{0}))
	require.NoError(t, service.Publish(agent))

	module := publishModuleConfig(t, agent, "test", "svc")
	t.Cleanup(func() { _ = module.Free() })

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	packetOf := func(syn, ack bool, mss uint16) gopacket.Packet {
		ip4 := layers.IPv4{
			Version:  4,
			TTL:      64,
			Protocol: layers.IPProtocolTCP,
			SrcIP:    net.ParseIP("10.0.0.1"),
			DstIP:    net.ParseIP("192.168.1.1"),
		}
		tcp := layers.TCP{
			SrcPort: 1000,
			DstPort: 80,
			Seq:     1,
			Window:  1024,
			SYN:     syn,
			ACK:     ack,
			Options: []layers.TCPOption{
				{OptionType: layers.TCPOptionKindMSS, OptionLength: 4, OptionData: []byte{byte(mss >> 8), byte(mss)}},
			},
		}
		tcp.SetNetworkLayerForChecksum(&ip4)
		return xpacket.LayersToPacket(t, &eth, &ip4, &tcp)
	}
	// The SYN carries MSS 9000; the output must carry it clamped to 1220.
	syn := packetOf(true, false, 9000)
	result, err := h.HandlePackets(syn)
	require.NoError(t, err)
	require.Len(t, result.Output, 1)
	outputDecoded := gopacket.NewPacket(
		result.Output[0].RawData, layers.LayerTypeEthernet, gopacket.Default,
	)
	// The output decodes as eth > outer ip > inner ip > tcp; gopacket
	// decapsulates IPIP automatically.
	tcpLayer := outputDecoded.Layer(layers.LayerTypeTCP)
	require.NotNil(t, tcpLayer, "the encapsulated TCP header must be decodable")
	tcpOut := tcpLayer.(*layers.TCP)
	var outMss uint16
	found := false
	for _, option := range tcpOut.Options {
		if option.OptionType == layers.TCPOptionKindMSS && len(option.OptionData) >= 2 {
			outMss = uint16(option.OptionData[0])<<8 | uint16(option.OptionData[1])
			found = true
		}
	}
	require.True(t, found, "the SYN must still carry an MSS option")
	require.EqualValues(t, 1220, outMss, "the MSS must be clamped to 1220")

	// An established flow (ACK) on the now-disabled real must be dropped
	// rather than rescheduled.
	require.NoError(t, service.SetRealServerState(0, false))
	require.NoError(t, service.UpdateRing(nil))
	ack := packetOf(false, true, 1400)
	result, err = h.HandlePackets(ack)
	require.NoError(t, err)
	require.NotEmpty(t, result.Drop, "an established flow must not move between reals")
	require.Empty(t, result.Output)
}

// TestL3b_PureL3SharesOnePinPerSource verifies pure L3 balancing: with the
// flag set, flows from one source address with different ports share a
// single session pin, exactly like the first-generation l3_balancing key.
func TestL3b_PureL3SharesOnePinPerSource(t *testing.T) {
	h, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")

	service, err := cl3bobject.CreateVirtualService(
		agent,
		"svc",
		1,
		cl3bobject.VirtualServiceConfig{
			SourceFilterRules: []cl3bobject.SourceFilterRule{{
				// Pure L3 classification: the full port range.
				Net4s:      []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
				PortRanges: filter.PortRanges{{From: 0, To: 65535}},
			}},
			RealServers: []cl3bobject.RealServer{
				{
					Type:               cl3bobject.IPv4,
					DestinationAddress: xerror.Unwrap(netip.ParseAddr("172.16.0.10")),
					SourceNet:          xnetip.MustParseNetwork("192.0.2.0/24"),
				},
				{
					Type:               cl3bobject.IPv4,
					DestinationAddress: xerror.Unwrap(netip.ParseAddr("172.16.0.11")),
					SourceNet:          xnetip.MustParseNetwork("192.0.2.0/24"),
				},
			},
			HashMask:         0,
			IndexMask:        1,
			RingCapacity:     2 * 1000,
			SessionIndexSize: 4096,
			Flags:            cl3bobject.PureL3,
			// One-packet scheduling rotates per packet, so only the
			// shared session keeps both flows on one real.
			SchedulerFlags: cl3bobject.SchedulerCounter,
		},
		nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = service.Free() })
	require.NoError(t, service.UpdateRing([]uint32{0, 1}))
	require.NoError(t, service.Publish(agent))

	module := publishModuleConfig(t, agent, "test", "svc")
	t.Cleanup(func() { _ = module.Free() })

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	packetOf := func(srcIP string, srcPort uint16) gopacket.Packet {
		ip4 := layers.IPv4{
			Version:  4,
			TTL:      64,
			Protocol: layers.IPProtocolTCP,
			SrcIP:    net.ParseIP(srcIP),
			DstIP:    net.ParseIP("192.168.1.1"),
		}
		tcp := layers.TCP{
			SrcPort: layers.TCPPort(srcPort),
			DstPort: 80,
			Seq:     1,
			Window:  1024,
		}
		tcp.SetNetworkLayerForChecksum(&ip4)
		return xpacket.LayersToPacket(t, &eth, &ip4, &tcp)
	}
	outerDstOf := func(packet gopacket.Packet) string {
		result, err := h.HandlePackets(packet)
		require.NoError(t, err)
		require.Len(t, result.Output, 1)
		info, err := framework.NewPacketParser().ParsePacket(result.Output[0].RawData)
		require.NoError(t, err)
		return info.DstIP.String()
	}

	first := outerDstOf(packetOf("10.0.0.1", 1111))
	// A different port of the same source reuses the pin even though the
	// counter scheduler would rotate to the other real.
	require.Equal(t, first, outerDstOf(packetOf("10.0.0.1", 2222)),
		"a pure L3 service must share one pin across the source's ports")
	require.Equal(t, first, outerDstOf(packetOf("10.0.0.1", 3333)))

	// A different source address still gets its own pin.
	other := outerDstOf(packetOf("10.0.0.2", 1111))
	require.NotEqual(t, first, other,
		"a different source address must get its own pin")
}

// TestL3b_MarksOuterDscp verifies the outer-header DSCP marking modes: always
// rewrites the outer DSCP, onlyDefault marks only a zero DSCP and the
// default inherits the inner value.
func TestL3b_MarksOuterDscp(t *testing.T) {
	h, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")

	realServer := cl3bobject.RealServer{
		Type:               cl3bobject.IPv4,
		DestinationAddress: xerror.Unwrap(netip.ParseAddr("172.16.0.10")),
		SourceNet:          xnetip.MustParseNetwork("192.0.2.0/24"),
	}
	sourceRule := cl3bobject.SourceFilterRule{
		Net4s:      []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		PortRanges: filter.PortRanges{{From: 1, To: 65535}},
	}

	publish := func(name string, dscpFlags uint32) {
		service, err := cl3bobject.CreateVirtualService(
			agent,
			name,
			1,
			cl3bobject.VirtualServiceConfig{
				SourceFilterRules: []cl3bobject.SourceFilterRule{sourceRule},
				RealServers:       []cl3bobject.RealServer{realServer},
				HashMask:          0,
				IndexMask:         0,
				RingCapacity:      1000,
				SessionIndexSize:  4096,
				DSCPFlags:         dscpFlags,
			},
			nil,
		)
		require.NoError(t, err)
		t.Cleanup(func() { _ = service.Free() })
		require.NoError(t, service.UpdateRing([]uint32{0}))
		require.NoError(t, service.Publish(agent))

		// The chained module config is always "test"; the service it
		// routes to is swapped by the re-publish.
		module, err := cl3b.NewModuleConfig(agent, "test")
		require.NoError(t, err)
		t.Cleanup(func() { _ = module.Free() })
		rules := []cl3b.DestinationFilterRule{{
			Net4s:          []xnetip.Contiguous[xnetip.Network4]{xnetip.MustParseContiguous4("192.168.1.0/24")},
			ProtoRanges:    filter.ProtoRanges{filter.NewProtoRange(6, filter.AnySubtype())},
			VirtualService: name,
		}}
		require.NoError(t, module.Update(rules))
		require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}))
	}
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	packetWithTOS := func(tos byte) gopacket.Packet {
		ip4 := layers.IPv4{
			Version:  4,
			TTL:      64,
			TOS:      tos,
			Protocol: layers.IPProtocolTCP,
			SrcIP:    net.ParseIP("10.0.0.1"),
			DstIP:    net.ParseIP("192.168.1.1"),
		}
		tcp := layers.TCP{SrcPort: 1000, DstPort: 80, Seq: 1, Window: 1024}
		tcp.SetNetworkLayerForChecksum(&ip4)
		return xpacket.LayersToPacket(t, &eth, &ip4, &tcp)
	}
	outerTOS := func(packet gopacket.Packet, srcPort uint16) byte {
		result, err := h.HandlePackets(packet)
		require.NoError(t, err)
		require.Len(t, result.Output, 1)
		decoded := gopacket.NewPacket(
			result.Output[0].RawData, layers.LayerTypeEthernet, gopacket.Lazy,
		)
		outer := decoded.Layer(layers.LayerTypeIPv4).(*layers.IPv4)
		return outer.TOS
	}

	// Both services route through the chained "test" module config, one
	// at a time: re-publishing the module swaps the active service.
	// always: the outer DSCP is rewritten to 40 (0x28 << 2 = 0xA0)
	// regardless of the inner value.
	publish("always", cl3bobject.DSCPFlagsOf(cl3bobject.DSCPMarkAlways, 40))
	require.EqualValues(t, 0xA0, outerTOS(packetWithTOS(0x04), 1000))
	require.EqualValues(t, 0xA0, outerTOS(packetWithTOS(0x88), 1001))

	// onlyDefault: a zero inner DSCP is marked, a set one is inherited.
	publish("default", cl3bobject.DSCPFlagsOf(cl3bobject.DSCPMark, 40))
	require.EqualValues(t, 0xA0, outerTOS(packetWithTOS(0x00), 2000))
	require.EqualValues(t, 0x88, outerTOS(packetWithTOS(0x88), 2001))
}
