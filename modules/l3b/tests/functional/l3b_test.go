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
	// and retargeting the ring at real 1. Harness workers only advance
	// their generation inside rounds, so the publish's wait for them is
	// released by restoring the high-water generations.
	h.ResetWorkerGenerations()
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

	h.ResetWorkerGenerations()
	relisted, err := cl3bobject.CreateVirtualService(agent, "svc", 1, replacementConfig, replacement)
	require.NoError(t, err)
	t.Cleanup(func() { _ = relisted.Free() })
	require.NoError(t, relisted.UpdateRing([]uint32{0}))
	require.NoError(t, relisted.Publish(agent))
	require.NoError(t, replacement.RetireService())

	require.Equal(t, "172.16.0.20", outerDstOf(flowA),
		"a session whose backend left the list must re-pin onto the new ring")
}
