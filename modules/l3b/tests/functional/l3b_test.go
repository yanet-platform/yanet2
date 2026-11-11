package l3b_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/xnetip"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	cplain "github.com/yanet-platform/yanet2/devices/plain/bindings/go/cplain"
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	forward "github.com/yanet-platform/yanet2/modules/forward/controlplane"
	"github.com/yanet-platform/yanet2/modules/l3b/bindings/go/cl3b"
	l3b "github.com/yanet-platform/yanet2/modules/l3b/controlplane"
	"github.com/yanet-platform/yanet2/modules/l3b/tests/internal/probe"
	"github.com/yanet-platform/yanet2/objects/l3b/bindings/go/cl3bobject"
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
	sinkHandle, err := sinkBackend.Update(sinkName, forward.RuleSet{Compiled: catchAllForwardRules(deviceName)})
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
	_, err = cplain.UpdateDevices(agent, []ffi.DeviceConfig{{
		Name:   deviceName,
		Input:  []ffi.DevicePipelineConfig{{Name: configName, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}})
	require.NoError(t, err)
}

// TestL3b_ForwardsShortIcmpPackets verifies that an ICMP packet whose
// transport region holds no bytes at all passes through the module
// unchanged: the type byte cannot be read, so the packet is not eligible
// for load balancing, and nothing reads beyond the frame.
func TestL3b_ForwardsShortIcmpPackets(t *testing.T) {
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

	packetCount := 3
	pkt := xpacket.LayersToPacket(t, &eth, &ip4)
	packets := make([]gopacket.Packet, 0, packetCount)
	for range packetCount {
		packets = append(packets, pkt)
	}

	result, err := h.HandlePackets(packets...)
	require.NoError(t, err)
	require.Len(t, result.Output, packetCount,
		"a headerless ICMP packet is not eligible and must pass through")
	require.Empty(t, result.Drop)
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

// newVirtualService creates a session table and a virtual service with every
// configured real server enabled.
//
// The caller installs the scheduler ring and publishes the service. Teardown
// attempts to free the service before the session table it borrows.
func newVirtualService(
	t *testing.T,
	agent *ffi.Agent,
	name string,
	config cl3bobject.VirtualServiceConfig,
) (*cl3bobject.VirtualServiceObject, *cl3bobject.SessionTableObject) {
	t.Helper()

	table, err := cl3bobject.CreateSessionTable(agent, name, 1, 4096)
	require.NoError(t, err)
	t.Cleanup(func() { _ = table.Free() })

	service, err := cl3bobject.CreateVirtualService(agent, name, config, table)
	require.NoError(t, err)
	t.Cleanup(func() { _ = service.Free() })
	for idx := range config.RealServers {
		require.NoError(t, service.SetRealServerState(uint32(idx), true))
	}

	return service, table
}

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
		HashMask:     0,
		IndexMask:    0,
		RingCapacity: 1 * 1000,
	}

	object, _ := newVirtualService(t, agent, name, serviceConfig)
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

// l3bTestCounterPath is the module-counter location of the "test" l3b
// configuration wired by wirePipeline.
var l3bTestCounterPath = dataplaneut.CounterPath{
	Device:     "port0",
	Pipeline:   "test",
	Function:   "test",
	Chain:      "test_chain",
	ModuleType: "l3b",
	ModuleName: "test",
}

// ipv4TestFrame builds an unpadded Ethernet frame carrying an IPv4 TCP header
// set with the given DF and MF flags and the given 8-byte-unit fragment
// offset. The payload follows the IP header verbatim, so the frame length
// never hides a short fragment behind serializer padding.
func ipv4TestFrame(dontFragment, moreFragments bool, offsetUnits uint16, payload []byte) []byte {
	frame := make([]byte, ethHeaderLen+20+len(payload))
	copy(frame[0:6], []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66})
	copy(frame[6:12], []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff})
	binary.BigEndian.PutUint16(frame[12:14], 0x0800)
	frame[14] = 0x45
	binary.BigEndian.PutUint16(frame[16:18], uint16(20+len(payload)))
	fragmentField := offsetUnits
	if moreFragments {
		fragmentField |= 1 << 13
	}
	if dontFragment {
		fragmentField |= 1 << 14
	}
	binary.BigEndian.PutUint16(frame[20:22], fragmentField)
	frame[22] = 64
	frame[23] = 6
	copy(frame[26:30], net.ParseIP("10.0.0.1").To4())
	copy(frame[30:34], net.ParseIP("192.168.1.1").To4())
	copy(frame[34:], payload)
	return frame
}

// ipv6TestFrame builds an unpadded Ethernet frame carrying an IPv6 header, a
// Fragment extension header with the given next header, More Fragments bit
// and 8-byte-unit offset, and the payload verbatim.
func ipv6TestFrame(
	nextHeader byte,
	moreFragments bool,
	offsetUnits uint16,
	payload []byte,
) []byte {
	src6 := net.ParseIP("2001:db8::1").To16()
	dst6 := net.ParseIP("2001:db8::2").To16()

	frame := make([]byte, ethHeaderLen+40+8+len(payload))
	copy(frame[0:6], []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66})
	copy(frame[6:12], []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff})
	binary.BigEndian.PutUint16(frame[12:14], 0x86dd)
	frame[14] = 0x60
	binary.BigEndian.PutUint16(frame[18:20], uint16(8+len(payload)))
	frame[20] = 44
	frame[21] = 64
	copy(frame[22:38], src6)
	copy(frame[38:54], dst6)
	frame[54] = nextHeader
	offsetFlag := offsetUnits << 3
	if moreFragments {
		offsetFlag |= 1
	}
	binary.BigEndian.PutUint16(frame[56:58], offsetFlag)
	copy(frame[62:], payload)
	return frame
}

// TestL3b_DropsRealFragments verifies that every real fragment — any packet
// with the More Fragments bit set or a nonzero offset, in both families — is
// counted and dropped before virtual-service lookup, with the frame bytes
// preserved in the drop list.
func TestL3b_DropsRealFragments(t *testing.T) {
	h, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")

	service := publishVirtualService(t, agent, "svc", "192.0.2.0/24", "172.16.0.10")
	t.Cleanup(func() { _ = service.Free() })
	module := publishModuleConfig(t, agent, "test", "svc")
	t.Cleanup(func() { _ = module.Free() })

	tcpPayload := bytes.Repeat([]byte{0xAB}, 20)
	cases := []struct {
		name  string
		frame []byte
	}{
		{"ipv4 initial fragment", ipv4TestFrame(false, true, 0, tcpPayload)},
		{"ipv4 non-initial fragment", ipv4TestFrame(false, true, 1, make([]byte, 8))},
		{"ipv4 last fragment", ipv4TestFrame(false, false, 1, make([]byte, 8))},
		{"ipv4 DF+MF fragment", ipv4TestFrame(true, true, 0, tcpPayload)},
		{"ipv4 DF+offset fragment", ipv4TestFrame(true, false, 1, make([]byte, 8))},
		{"ipv6 initial fragment", ipv6TestFrame(6, true, 0, tcpPayload)},
		{"ipv6 non-initial fragment", ipv6TestFrame(6, true, 1, make([]byte, 8))},
		{"ipv6 last fragment", ipv6TestFrame(6, false, 1, make([]byte, 8))},
	}

	var wantPackets, wantBytes uint64
	for _, tc := range cases {
		result, err := h.HandleSegmentedPackets([][]byte{tc.frame})
		require.NoError(t, err)
		require.Empty(t, result.Output, "%s must not be forwarded", tc.name)
		require.Len(t, result.Drop, 1, "%s must be dropped", tc.name)
		require.True(t, bytes.Equal(result.Drop[0], tc.frame),
			"%s must be dropped byte-identical", tc.name)
		wantPackets += 1
		wantBytes += uint64(len(tc.frame))
	}

	dataplaneut.RequireModuleCounter(
		t, h, l3bTestCounterPath, "drop", wantPackets, wantBytes,
	)
}

// TestL3b_DropsSegmentedFragment verifies that a non-initial fragment whose
// transport-header region spans an mbuf segment boundary is counted and
// dropped. The drop byte count reports head-segment bytes only, matching the
// packet_front data_len convention; tail-segment bytes are not counted.
func TestL3b_DropsSegmentedFragment(t *testing.T) {
	h, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")

	service := publishVirtualService(t, agent, "svc", "192.0.2.0/24", "172.16.0.10")
	t.Cleanup(func() { _ = service.Free() })
	module := publishModuleConfig(t, agent, "test", "svc")
	t.Cleanup(func() { _ = module.Free() })

	frame := ipv6TestFrame(6, false, 1, make([]byte, 8))
	require.Equal(t, 70, len(frame), "fixture frame length")

	result, err := h.HandleSegmentedPackets(
		[][]byte{frame[:64], frame[64:]},
	)
	require.NoError(t, err)
	require.Empty(t, result.Output, "the segmented fragment must not be forwarded")
	require.Len(t, result.Drop, 1, "the segmented fragment must be dropped")

	dataplaneut.RequireModuleCounter(t, h, l3bTestCounterPath, "drop", 1, 64)
}

// TestL3b_ForwardsUnfragmentedControls verifies that unfragmented traffic
// keeps ordinary processing: a DF-only TCP packet is encapsulated towards the
// virtual service real, an unmatched TCP destination is dropped as before,
// and an atomic IPv6 echo request is forwarded untouched, with no fragment
// policy drop counted.
func TestL3b_ForwardsUnfragmentedControls(t *testing.T) {
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
	dfOnly := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    net.ParseIP("10.0.0.1"),
		DstIP:    net.ParseIP("192.168.1.1"),
		Flags:    layers.IPv4DontFragment,
	}
	tcp := layers.TCP{
		SrcPort: 12345,
		DstPort: 80,
		Seq:     1,
		Window:  1024,
	}
	tcp.SetNetworkLayerForChecksum(&dfOnly)

	pkt := xpacket.LayersToPacket(t, &eth, &dfOnly, &tcp)
	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Empty(t, result.Drop, "a DF-only TCP packet must not be dropped")
	require.Len(t, result.Output, 1, "a DF-only TCP packet must reach the virtual service")

	info, err := framework.NewPacketParser().ParsePacket(result.Output[0].RawData)
	require.NoError(t, err)
	require.True(t, info.IsTunneled, "the DF-only TCP packet must be encapsulated")
	require.Equal(t, "172.16.0.10", info.DstIP.String())

	unmatched := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    net.ParseIP("10.0.0.1"),
		DstIP:    net.ParseIP("10.9.9.9"),
	}
	tcpUnmatched := layers.TCP{
		SrcPort: 12345,
		DstPort: 80,
		Seq:     1,
		Window:  1024,
	}
	tcpUnmatched.SetNetworkLayerForChecksum(&unmatched)

	pkt = xpacket.LayersToPacket(t, &eth, &unmatched, &tcpUnmatched)
	result, err = h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Empty(t, result.Output, "an unmatched TCP destination must not be forwarded")
	require.Len(t, result.Drop, 1, "an unmatched TCP destination must be dropped as before")

	atomicEcho := ipv6TestFrame(58, false, 0, []byte{0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	result2, err := h.HandleSegmentedPackets([][]byte{atomicEcho})
	require.NoError(t, err)
	require.Empty(t, result2.Drop, "an atomic IPv6 echo request must not be dropped")
	require.Len(t, result2.Output, 1, "an atomic IPv6 echo request must be forwarded")
	require.True(t, bytes.Equal(result2.Output[0], atomicEcho),
		"an atomic IPv6 echo request must be forwarded byte-identical")

	dataplaneut.RequireModuleCounter(t, h, l3bTestCounterPath, "drop", 1, uint64(len(result.Drop[0].RawData)))
}

// TestL3b_DropsFragmentInMixedBatch verifies that a fragment is counted and
// dropped inside a mixed batch while unfragmented packets of the same batch
// keep their ordinary dispositions.
func TestL3b_DropsFragmentInMixedBatch(t *testing.T) {
	h, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")

	service := publishVirtualService(t, agent, "svc", "192.0.2.0/24", "172.16.0.10")
	t.Cleanup(func() { _ = service.Free() })
	module := publishModuleConfig(t, agent, "test", "svc")
	t.Cleanup(func() { _ = module.Free() })

	fragment := ipv4TestFrame(false, true, 1, make([]byte, 8))
	dfTcp := ipv4TestFrame(true, false, 0, bytes.Repeat([]byte{0x51}, 20))
	atomicEcho := ipv6TestFrame(58, false, 0, []byte{0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})

	result, err := h.HandleSegmentedPackets(
		[][]byte{fragment}, [][]byte{dfTcp}, [][]byte{atomicEcho},
	)
	require.NoError(t, err)
	require.Len(t, result.Drop, 1, "only the fragment must be dropped")
	require.True(t, bytes.Equal(result.Drop[0], fragment),
		"the dropped packet must be the fragment, byte-identical")
	require.Len(t, result.Output, 2, "unfragmented packets must keep their dispositions")

	dataplaneut.RequireModuleCounter(t, h, l3bTestCounterPath, "drop", 1, uint64(len(fragment)))
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
		HashMask:     0,
		IndexMask:    0,
		RingCapacity: 2 * 1000,
	}

	service, _ := newVirtualService(t, agent, "svc", serviceConfig)
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
// service object keeps the table it pins into: flows pinned before the update keep
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
		HashMask:     0,
		IndexMask:    0,
		RingCapacity: 2 * 1000,
	}

	service, table := newVirtualService(t, agent, "svc", serviceConfig)
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

	// Replace the service under the same name over the live session table,
	// retargeting the ring at real 1.
	replacement, err := cl3bobject.CreateVirtualService(agent, "svc", serviceConfig, table)
	require.NoError(t, err)
	t.Cleanup(func() { _ = replacement.Free() })
	for idx := range serviceConfig.RealServers {
		require.NoError(t, replacement.SetRealServerState(uint32(idx), true))
	}
	require.NoError(t, replacement.UpdateRing([]uint32{1}))
	require.NoError(t, replacement.Publish(agent))
	require.NoError(t, service.Free())

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

	relisted, err := cl3bobject.CreateVirtualService(agent, "svc", replacementConfig, table)
	require.NoError(t, err)
	t.Cleanup(func() { _ = relisted.Free() })
	for idx := range replacementConfig.RealServers {
		require.NoError(t, relisted.SetRealServerState(uint32(idx), true))
	}
	require.NoError(t, relisted.UpdateRing([]uint32{0}))
	require.NoError(t, relisted.Publish(agent))
	require.NoError(t, replacement.Free())

	require.Equal(t, "172.16.0.20", outerDstOf(flowA),
		"a session whose backend left the list must re-pin onto the new ring")
}

// TestL3b_FailedReplacementKeepsSessionTable verifies the session table's
// independence from any single generation of a service: a replacement
// candidate discarded before it is published leaves the table of the live
// service intact, and the retry over that same table keeps every pinned flow.
func TestL3b_FailedReplacementKeepsSessionTable(t *testing.T) {
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
		HashMask:     0,
		IndexMask:    0,
		RingCapacity: 2 * 1000,
	}

	service, table := newVirtualService(t, agent, "svc", serviceConfig)
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

	// An update that fails before its candidate reaches the dataplane:
	// the candidate borrows the live table and is then discarded exactly
	// the way the control plane discards one.
	candidate, err := cl3bobject.CreateVirtualService(agent, "svc", serviceConfig, table)
	require.NoError(t, err)
	require.NoError(t, candidate.UpdateRing([]uint32{1}))
	require.NoError(t, candidate.Free(),
		"a candidate no generation ever referenced must be destroyable at once")

	require.ErrorIs(t, table.Free(), ffi.ErrStillReferenced,
		"the live generation must still hold the session table")

	require.Equal(t, "172.16.0.10", outerDstOf(flowA),
		"a discarded candidate must leave the serving flow untouched")

	sessions, next, _, err := service.ReadSessions(agent, 0, 100)
	require.NoError(t, err)
	require.Zero(t, next, "the listing must be complete")
	require.Len(t, sessions, 1)
	require.Equal(t, "10.0.0.1", sessions[0].SourceAddress.String())
	require.EqualValues(t, 12345, sessions[0].SourcePort)
	require.Equal(t, "172.16.0.10", sessions[0].RealAddress.String())

	// The retry over the surviving table, retargeting the ring at real 1.
	replacement, err := cl3bobject.CreateVirtualService(agent, "svc", serviceConfig, table)
	require.NoError(t, err)
	t.Cleanup(func() { _ = replacement.Free() })
	for idx := range serviceConfig.RealServers {
		require.NoError(t, replacement.SetRealServerState(uint32(idx), true))
	}
	require.NoError(t, replacement.UpdateRing([]uint32{1}))
	require.NoError(t, replacement.Publish(agent))
	require.NoError(t, service.Free())

	require.Equal(t, "172.16.0.10", outerDstOf(flowA),
		"a session pinned before the failure must survive it and the retry")
	require.Equal(t, "172.16.0.11", outerDstOf(packetOf("10.0.0.2", 54321)),
		"a new session must follow the retried ring")

	sessions, next, _, err = replacement.ReadSessions(agent, 0, 100)
	require.NoError(t, err)
	require.Zero(t, next, "the listing must be complete")
	bySource := map[string]string{}
	for _, session := range sessions {
		bySource[fmt.Sprintf("%s:%d", session.SourceAddress, session.SourcePort)] =
			session.RealAddress.String()
	}
	require.Equal(t, map[string]string{
		"10.0.0.1:12345": "172.16.0.10",
		"10.0.0.2:54321": "172.16.0.11",
	}, bySource, "one table must carry the pins across the failure and the retry")
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
		HashMask:       0,
		IndexMask:      1,
		RingCapacity:   2 * 1000,
		SchedulerFlags: cl3bobject.SchedulerCounter,
	}

	service, _ := newVirtualService(t, agent, "svc", serviceConfig)
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
		HashMask:     0,
		IndexMask:    0,
		RingCapacity: 2 * 1000,
	}

	service, _ := newVirtualService(t, agent, "svc", serviceConfig)
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

	// A repeat of the first flow rides the session pin straight to its
	// real: the pinned path counts on real 0 without consulting the ring.
	send(accepted1)

	// A flow outside the source filter's port range is rejected by it.
	filtered := packetOf("10.0.0.3", 3333, 80)
	send(filtered)

	inPackets, inBytes := counter("incoming")
	require.EqualValues(t, 4, inPackets)
	require.Equal(t, frameLen(accepted1)*3+frameLen(filtered), inBytes)

	filterPackets, filterBytes := counter("filter_rejected")
	require.EqualValues(t, 1, filterPackets)
	require.Equal(t, frameLen(filtered), filterBytes)

	realPackets, realBytes := counter("real/0")
	require.EqualValues(t, 3, realPackets)
	require.Equal(t, frameLen(accepted1)*3, realBytes)

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

type echoPCAPPair struct {
	name    string
	request gopacket.Packet
	reply   gopacket.Packet
}

// loadEchoPCAPPairs adapts pinned fixtures to the local Echo handoff contract.
//
// VLAN tags are removed; reply MACs follow the request in the local harness.
// Reply TTL/Hop Limit becomes 65, with the IPv4 header checksum corrected.
// The captures, not the upstream generator, define identifiers and payloads.
func loadEchoPCAPPairs(t *testing.T, batch string) []echoPCAPPair {
	t.Helper()
	fixture, ok := map[string]struct {
		lengths []int
		hashes  [2]string
	}{
		"001": {[]int{46, 82}, [2]string{
			"ba89034ae9c285a27e34c188f7dc0286f86b207ffe0fa109afb215ac0e02d1ce",
			"a271a431ec9f26099f18086de34c82c80b3009ddf83d1b86db3bb6f6170d9b3c",
		}},
		"002": {[]int{66, 102}, [2]string{
			"b216bdc0b291864cd4e714a7ed74b2b0fd004671e8fdf904d99b9fdce0516de3",
			"ba3dac9c4d206895e38411b113a11fef99011942b867cc379a9398f63efc611c",
		}},
		"003": {[]int{82}, [2]string{
			"d9e5883cbbfc5f147923a5e641e2e1fdf2a515e55a2ecfcbf25d4bee8b725bad",
			"a2be6d70d08e21c642403582bff4fb793eacb7bb1a618b00b06b4e293362ddaf",
		}},
	}[batch]
	require.True(t, ok, "unknown Echo fixture batch %q", batch)
	pairs := make([]echoPCAPPair, len(fixture.lengths))
	for direction, suffix := range []string{"send", "expect"} {
		data, err := os.ReadFile("testdata/056_balancer_vs_ping_reply/" + batch + "-" + suffix + ".pcap")
		require.NoError(t, err)
		require.Equal(t, fixture.hashes[direction], fmt.Sprintf("%x", sha256.Sum256(data)))
		reader, err := pcapgo.NewReader(bytes.NewReader(data))
		require.NoError(t, err)
		require.Equal(t, layers.LinkTypeEthernet, reader.LinkType())
		for idx, length := range fixture.lengths {
			frame, capture, err := reader.ReadPacketData()
			require.NoError(t, err)
			require.Len(t, frame, length)
			require.Equal(t, length, capture.CaptureLength)
			require.Equal(t, length, capture.Length)
			require.EqualValues(t, 0x8100, binary.BigEndian.Uint16(frame[12:14]))
			require.EqualValues(t, 100*(direction+1), binary.BigEndian.Uint16(frame[14:16]))
			adapted := append(bytes.Clone(frame[:12]), frame[16:]...)
			require.Len(t, adapted, length-4)
			if direction == 1 {
				copy(adapted[:12], pairs[idx].request.Data()[:12])
			}
			require.Equal(t, frame[18:], adapted[14:])
			if direction == 1 {
				if adapted[14]>>4 == 4 {
					require.EqualValues(t, 64, adapted[22])
					adapted[22] = 65
					adapted[24], adapted[25] = 0, 0
					binary.BigEndian.PutUint16(adapted[24:26], echoChecksum(adapted[14:14+int(adapted[14]&15)*4]))
				} else {
					require.EqualValues(t, 64, adapted[21])
					adapted[21] = 65
				}
			}
			packet := gopacket.NewPacket(adapted, layers.LayerTypeEthernet, gopacket.Default)
			require.Nil(t, packet.ErrorLayer())
			if direction == 0 {
				pairs[idx].name = fmt.Sprintf("legacy_%s_packet_%d", batch, idx+1)
				pairs[idx].request = packet
			} else {
				pairs[idx].reply = packet
			}
		}
		_, _, err = reader.ReadPacketData()
		require.ErrorIs(t, err, io.EOF)
	}
	return pairs
}

// readEchoSessions returns every record and continuation token at a fixed time.
func readEchoSessions(t *testing.T, agent *ffi.Agent, service *cl3bobject.VirtualServiceObject, now time.Time) ([]cl3bobject.Session, []uint64) {
	t.Helper()
	var sessions []cl3bobject.Session
	var cursors []uint64
	seen := map[uint64]bool{}
	var cursor uint64
	for {
		page, next, dataplaneNow, err := service.ReadSessions(agent, cursor, 1)
		require.NoError(t, err)
		require.Equal(t, uint64(now.UnixNano()), dataplaneNow)
		sessions = append(sessions, page...)
		cursors = append(cursors, next)
		if next == 0 {
			require.Empty(t, page, "page size one requires an empty terminal page")
			return sessions, cursors
		}
		require.False(t, seen[next], "repeated nonterminal cursor %d", next)
		seen[next] = true
		cursor = next
	}
}

// Test_L3b_ConfiguredRealsGateEcho verifies that configured reals gate Echo
// replies without changing seeded sessions or scheduling counters.
func Test_L3b_ConfiguredRealsGateEcho(t *testing.T) {
	for _, family := range []string{"IPv4", "IPv6"} {
		t.Run(family, func(t *testing.T) {
			enabledBatch := "001"
			if family == "IPv6" {
				enabledBatch = "002"
			}
			type stateCase struct {
				name                    string
				restrictiveSourceFilter bool
				sessionFill             string
				weights                 []uint32
				legacy                  string
				disabled                bool
				unmatched               bool
				wantDrop                bool
				wantReply               uint64
				wantInput               uint64
			}
			cases := []stateCase{
				{name: "empty configured list drops unchanged", wantDrop: true, wantInput: 1},
				{name: "enabled configured reals reply", weights: []uint32{1}, legacy: enabledBatch, wantReply: 1, wantInput: 1},
				{name: "all configured reals disabled reply", weights: []uint32{1, 1}, disabled: true, wantReply: 1, wantInput: 1},
				{name: "all configured weights zero reply", weights: []uint32{0, 0}, wantReply: 1, wantInput: 1},
				{name: "unmatched destination passes unchanged", weights: []uint32{1, 1}, unmatched: true},
				{name: "restrictive_source_filter_does_not_block_echo", weights: []uint32{1}, restrictiveSourceFilter: true, wantReply: 1, wantInput: 1},
				{name: "empty_session_table_does_not_block_echo", weights: []uint32{1}, sessionFill: "empty", wantReply: 1, wantInput: 1},
				{name: "missing_session_key_does_not_block_echo", weights: []uint32{1}, sessionFill: "unrelated", wantReply: 1, wantInput: 1},
			}
			if family == "IPv4" {
				cases = append(cases, stateCase{
					name: "disabled_ipv6_real_replies_to_legacy_echo", weights: []uint32{1},
					legacy: "003", disabled: true, wantReply: 1, wantInput: 1,
				})
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					harness, agent := setupL3bHarness(t, "port0", "test")
					wirePipeline(t, agent, "port0", "test")
					reals := []cl3bobject.RealServer{
						{Type: cl3bobject.IPv4, DestinationAddress: netip.MustParseAddr("172.16.0.10"), SourceNet: xnetip.MustParseNetwork("192.0.2.0/24")},
						{Type: cl3bobject.IPv4, DestinationAddress: netip.MustParseAddr("172.16.0.11"), SourceNet: xnetip.MustParseNetwork("192.0.2.0/24")},
					}
					if family == "IPv6" {
						reals = []cl3bobject.RealServer{
							{Type: cl3bobject.IPv6, DestinationAddress: netip.MustParseAddr("2001:db8:2::10"), SourceNet: xnetip.MustParseNetwork("2001:db8:3::/64")},
							{Type: cl3bobject.IPv6, DestinationAddress: netip.MustParseAddr("2001:db8:2::11"), SourceNet: xnetip.MustParseNetwork("2001:db8:3::/64")},
						}
					}
					var pairs []echoPCAPPair
					if tc.legacy != "" {
						pairs = loadEchoPCAPPairs(t, tc.legacy)
						switch tc.legacy {
						case "001":
							reals = []cl3bobject.RealServer{{Type: cl3bobject.IPv4, DestinationAddress: netip.MustParseAddr("101.0.0.1"), SourceNet: xnetip.MustParseNetwork("192.0.2.0/24")}}
						case "002":
							reals = []cl3bobject.RealServer{{Type: cl3bobject.IPv6, DestinationAddress: netip.MustParseAddr("2010::2"), SourceNet: xnetip.MustParseNetwork("2001:db8:3::/64")}}
						case "003":
							reals = []cl3bobject.RealServer{{Type: cl3bobject.IPv6, DestinationAddress: netip.MustParseAddr("2010::1"), SourceNet: xnetip.MustParseNetwork("2001:db8:3::/64")}}
						}
					}
					initialTime := time.Unix(1700000000, 0)
					harness.SetCurrentTime(initialTime)
					serviceConfig := cl3bobject.VirtualServiceConfig{
						RealServers: reals, RingCapacity: 1000,
						SourceFilterRules: []cl3bobject.SourceFilterRule{{
							Net4s:      []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
							Net6s:      []xnetip.BiContiguous{filter.UnspecifiedIPv6},
							PortRanges: filter.PortRanges{{From: 80, To: 80}},
						}},
						SessionTimeouts: cl3bobject.SessionTimeouts{UDP: 600, Other: 37},
					}
					seedService, table := newVirtualService(t, agent, "svc", serviceConfig)
					for idx := range reals {
						require.NoError(t, seedService.SetRealServerState(uint32(idx), true))
					}
					require.NoError(t, seedService.UpdateRing([]uint32{0}))
					require.NoError(t, seedService.Publish(agent))
					seedModule, err := cl3b.NewModuleConfig(agent, "test")
					require.NoError(t, err)
					t.Cleanup(func() { _ = seedModule.Free() })
					require.NoError(t, seedModule.Update([]cl3b.DestinationFilterRule{{
						Net4s:          []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
						Net6s:          []xnetip.BiContiguous{filter.UnspecifiedIPv6},
						ProtoRanges:    filter.ProtoRanges{filter.NewProtoRange(17, filter.AnySubtype())},
						VirtualService: "svc",
					}}))
					require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{seedModule.AsFFIModule()}))
					// ICMP type/code aliases this UDP port if Echo reaches session lookup.
					generatedSource := netip.MustParseAddr("10.0.0.1")
					sourcePort := uint16(2048)
					if family == "IPv6" {
						generatedSource = netip.MustParseAddr("2001:db8::1")
						sourcePort = 32768
					}
					var sources []netip.Addr
					seenSources := map[netip.Addr]bool{}
					if tc.legacy != "003" {
						sources = append(sources, generatedSource)
						seenSources[generatedSource] = true
					}
					for _, pair := range pairs {
						source, ok := netip.AddrFromSlice(pair.request.NetworkLayer().NetworkFlow().Src().Raw())
						require.True(t, ok)
						if !seenSources[source] {
							sources = append(sources, source)
							seenSources[source] = true
						}
					}
					switch tc.sessionFill {
					case "", "collision":
					case "empty":
						sources = nil
					case "unrelated":
						sources = []netip.Addr{netip.MustParseAddr("10.0.0.2")}
						if family == "IPv6" {
							sources = []netip.Addr{netip.MustParseAddr("2001:db8::2")}
						}
					default:
						t.Fatalf("unknown session fill mode %q", tc.sessionFill)
					}
					var expectedSessions []cl3bobject.Session
					for _, source := range sources {
						for _, port := range []uint16{sourcePort, sourcePort + 1} {
							seedEthernet := layers.Ethernet{
								SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
								DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
								EthernetType: layers.EthernetTypeIPv4,
							}
							udp := layers.UDP{SrcPort: layers.UDPPort(port), DstPort: 80}
							var seed gopacket.Packet
							if family == "IPv4" {
								network := layers.IPv4{Version: 4, TTL: 64, Protocol: layers.IPProtocolUDP, SrcIP: net.IP(source.AsSlice()), DstIP: net.ParseIP("192.168.1.1")}
								require.NoError(t, udp.SetNetworkLayerForChecksum(&network))
								seed = xpacket.LayersToPacket(t, &seedEthernet, &network, &udp, gopacket.Payload("session seed"))
							} else {
								seedEthernet.EthernetType = layers.EthernetTypeIPv6
								network := layers.IPv6{Version: 6, HopLimit: 64, NextHeader: layers.IPProtocolUDP, SrcIP: net.IP(source.AsSlice()), DstIP: net.ParseIP("2001:db8:1::1")}
								require.NoError(t, udp.SetNetworkLayerForChecksum(&network))
								seed = xpacket.LayersToPacket(t, &seedEthernet, &network, &udp, gopacket.Payload("session seed"))
							}
							result, err := harness.HandlePackets(seed)
							require.NoError(t, err)
							require.Empty(t, result.Drop)
							require.Len(t, result.Output, 1)
							expectedSessions = append(expectedSessions, cl3bobject.Session{
								SourceAddress: source, SourcePort: port, RealAddress: reals[0].DestinationAddress,
								ExpiresAt: uint64(initialTime.Add(600 * time.Second).UnixNano()),
							})
						}
					}
					seedSessions, seedCursors := readEchoSessions(t, agent, seedService, harness.CurrentTime())
					if tc.sessionFill == "empty" {
						require.Len(t, seedSessions, 0)
						require.Equal(t, []uint64{0}, seedCursors)
					} else {
						require.GreaterOrEqual(t, len(seedSessions), 2)
					}
					require.ElementsMatch(t, expectedSessions, seedSessions)
					if tc.sessionFill == "unrelated" {
						for _, session := range seedSessions {
							require.False(t, session.SourceAddress == generatedSource && session.SourcePort == sourcePort, "Echo session key must be absent")
							require.Equal(t, sources[0], session.SourceAddress)
						}
					}
					reals = reals[:len(tc.weights)]
					serviceConfig.RealServers = reals
					if tc.restrictiveSourceFilter {
						serviceConfig.SourceFilterRules[0].Net4s = []xnetip.Contiguous[xnetip.Network4]{xnetip.MustParseContiguous4("192.0.2.0/24")}
						serviceConfig.SourceFilterRules[0].Net6s = []xnetip.BiContiguous{xnetip.MustParseBiContiguous("2001:db8:ffff::/64")}
					} else {
						serviceConfig.SourceFilterRules[0].PortRanges = filter.PortRanges{{From: 0, To: 65535}}
					}
					service, err := cl3bobject.CreateVirtualService(agent, "svc",
						serviceConfig, table,
					)
					require.NoError(t, err)
					t.Cleanup(func() { _ = service.Free() })
					for idx := range reals {
						require.NoError(t, service.SetRealServerState(uint32(idx), !tc.disabled))
					}
					ring := l3b.RingFromWeights(tc.weights)
					if tc.disabled {
						ring = nil
					}
					require.NoError(t, service.UpdateRing(ring))
					require.NoError(t, service.Publish(agent))
					require.NoError(t, seedService.Free())
					replacementSessions, replacementCursors := readEchoSessions(t, agent, service, harness.CurrentTime())
					require.Equal(t, seedSessions, replacementSessions)
					require.Equal(t, seedCursors, replacementCursors)
					info, err := service.Inspect()
					require.NoError(t, err)
					require.Len(t, info.RealServers, len(reals))
					for idx, real := range info.RealServers {
						require.Equal(t, !tc.disabled, real.Enabled)
						require.Equal(t, reals[idx].Type, real.Family)
						require.Equal(t, reals[idx].DestinationAddress, real.DestinationAddress)
						require.Equal(t, reals[idx].SourceNet, real.SourceNet)
					}

					module, err := cl3b.NewModuleConfig(agent, "test")
					require.NoError(t, err)
					t.Cleanup(func() { _ = module.Free() })
					rules := []cl3b.DestinationFilterRule{{
						Net4s:          []xnetip.Contiguous[xnetip.Network4]{xnetip.MustParseContiguous4("192.168.1.0/24")},
						ProtoRanges:    filter.ProtoRanges{filter.NewProtoRange(1, filter.ExactSubtype(8))},
						VirtualService: "svc",
					}, {
						Net6s:          []xnetip.BiContiguous{xnetip.MustParseBiContiguous("2001:db8:1::/64")},
						ProtoRanges:    filter.ProtoRanges{filter.NewProtoRange(58, filter.ExactSubtype(128))},
						VirtualService: "svc",
					}}
					switch tc.legacy {
					case "001", "003":
						vip := "10.0.0.20/32"
						if tc.legacy == "003" {
							vip = "10.0.0.21/32"
						}
						rules = append(rules, cl3b.DestinationFilterRule{
							Net4s:          []xnetip.Contiguous[xnetip.Network4]{xnetip.MustParseContiguous4(vip)},
							ProtoRanges:    filter.ProtoRanges{filter.NewProtoRange(1, filter.ExactSubtype(8))},
							VirtualService: "svc",
						})
					case "002":
						rules = append(rules, cl3b.DestinationFilterRule{
							Net6s:          []xnetip.BiContiguous{xnetip.MustParseBiContiguous("2005:dead:beef::1/128")},
							ProtoRanges:    filter.ProtoRanges{filter.NewProtoRange(58, filter.ExactSubtype(128))},
							VirtualService: "svc",
						})
					}
					require.NoError(t, module.Update(rules))
					require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}))

					if tc.legacy != "003" {
						ethernet := layers.Ethernet{
							SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
							DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
							EthernetType: layers.EthernetTypeIPv4,
						}
						payload := gopacket.Payload("configured-real echo payload")
						var request, reply gopacket.Packet
						if family == "IPv4" {
							destination := net.ParseIP("192.168.1.1")
							if tc.unmatched {
								destination = net.ParseIP("192.168.2.1")
							}
							request = xpacket.LayersToPacket(t, &ethernet,
								&layers.IPv4{Version: 4, TTL: 1, Protocol: layers.IPProtocolICMPv4, SrcIP: net.ParseIP("10.0.0.1"), DstIP: destination},
								&layers.ICMPv4{TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0), Id: 0x1234, Seq: 7}, payload,
							)
							reply = xpacket.LayersToPacket(t, &ethernet,
								&layers.IPv4{Version: 4, TTL: 65, Protocol: layers.IPProtocolICMPv4, SrcIP: destination, DstIP: net.ParseIP("10.0.0.1")},
								&layers.ICMPv4{TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoReply, 0), Id: 0x1234, Seq: 7}, payload,
							)
						} else {
							ethernet.EthernetType = layers.EthernetTypeIPv6
							destination := net.ParseIP("2001:db8:1::1")
							if tc.unmatched {
								destination = net.ParseIP("2001:db8:4::1")
							}
							requestIP := layers.IPv6{Version: 6, HopLimit: 1, NextHeader: layers.IPProtocolICMPv6, SrcIP: net.ParseIP("2001:db8::1"), DstIP: destination}
							requestICMP := layers.ICMPv6{TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0)}
							require.NoError(t, requestICMP.SetNetworkLayerForChecksum(&requestIP))
							request = xpacket.LayersToPacket(t, &ethernet, &requestIP, &requestICMP,
								&layers.ICMPv6Echo{Identifier: 0x1234, SeqNumber: 7}, payload,
							)
							replyIP := layers.IPv6{Version: 6, HopLimit: 65, NextHeader: layers.IPProtocolICMPv6, SrcIP: destination, DstIP: net.ParseIP("2001:db8::1")}
							replyICMP := layers.ICMPv6{TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoReply, 0)}
							require.NoError(t, replyICMP.SetNetworkLayerForChecksum(&replyIP))
							reply = xpacket.LayersToPacket(t, &ethernet, &replyIP, &replyICMP,
								&layers.ICMPv6Echo{Identifier: 0x1234, SeqNumber: 7}, payload,
							)
						}
						pairs = append([]echoPCAPPair{{name: "generated", request: request, reply: reply}}, pairs...)
					}
					for _, pair := range pairs {
						t.Run(pair.name, func(t *testing.T) {
							request, reply := pair.request, pair.reply
							original := bytes.Clone(request.Data())
							executionContext, err := harness.PublishedExecutionContext(0)
							require.NoError(t, err)
							counterNames := []string{"incoming", "icmp_replied", "filter_rejected", "ring_empty", "real_disabled"}
							for idx := range reals {
								counterNames = append(counterNames, fmt.Sprintf("real/%d", idx))
							}
							baseline := map[string][2]uint64{}
							for _, name := range counterNames {
								packets, bytes, err := cl3bobject.ReadServiceCounter(executionContext, "svc", name)
								require.NoError(t, err)
								baseline[name] = [2]uint64{packets, bytes}
							}
							harness.AdvanceTime(time.Second)
							beforeSessions, beforeCursors := readEchoSessions(t, agent, service, harness.CurrentTime())
							require.Equal(t, seedSessions, beforeSessions)
							require.Equal(t, seedCursors, beforeCursors)
							if tc.sessionFill == "empty" {
								require.Len(t, beforeSessions, 0)
								require.Equal(t, []uint64{0}, beforeCursors)
							}
							for _, session := range beforeSessions {
								require.Greater(t, session.ExpiresAt, uint64(harness.CurrentTime().UnixNano()))
							}
							// Raw output preserves short captures without parser-added padding.
							result, err := harness.HandleSegmentedPackets([][]byte{original})
							require.NoError(t, err)
							if tc.wantDrop {
								require.Empty(t, result.Output)
								require.Len(t, result.Drop, 1)
								require.Equal(t, original, result.Drop[0])
							} else {
								require.Empty(t, result.Drop)
								require.Len(t, result.Output, 1)
								expected := reply.Data()
								if tc.unmatched {
									expected = original
								}
								require.Equal(t, expected, result.Output[0])
							}
							afterSessions, afterCursors := readEchoSessions(t, agent, service, harness.CurrentTime())
							require.Equal(t, beforeSessions, afterSessions)
							require.Equal(t, beforeCursors, afterCursors)
							if tc.sessionFill == "empty" {
								require.Len(t, afterSessions, 0)
								require.Equal(t, []uint64{0}, afterCursors)
							}
							incoming, incomingBytes, err := cl3bobject.ReadServiceCounter(executionContext, "svc", "incoming")
							require.NoError(t, err)
							require.Equal(t, tc.wantInput, incoming-baseline["incoming"][0])
							require.Equal(t, tc.wantInput*uint64(len(original)), incomingBytes-baseline["incoming"][1])
							replied, repliedBytes, err := cl3bobject.ReadServiceCounter(executionContext, "svc", "icmp_replied")
							require.NoError(t, err)
							require.Equal(t, tc.wantReply, replied-baseline["icmp_replied"][0])
							require.Equal(t, tc.wantReply*uint64(len(reply.Data())), repliedBytes-baseline["icmp_replied"][1])
							for _, name := range counterNames[2:] {
								packets, bytes, err := cl3bobject.ReadServiceCounter(executionContext, "svc", name)
								require.NoError(t, err)
								require.Equal(t, baseline[name], [2]uint64{packets, bytes}, name)
							}
						})
					}
				})
			}
		})
	}
}

// echoChecksum computes the Internet checksum over complete wire bytes.
func echoChecksum(data []byte) uint16 {
	var sum uint32
	for len(data) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(data))
		data = data[2:]
	}
	if len(data) != 0 {
		sum += uint32(data[0]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}

// Test_L3b_EchoReplyRetainsBytes verifies that admitted Echo replies change only
// contracted fields, without enabling reals, seeding traffic or creating sessions.
func Test_L3b_EchoReplyRetainsBytes(t *testing.T) {
	harness, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")
	harness.SetCurrentTime(time.Unix(1700000000, 0))
	service, _ := newVirtualService(t, agent, "svc", cl3bobject.VirtualServiceConfig{
		RealServers: []cl3bobject.RealServer{{
			Type: cl3bobject.IPv4, DestinationAddress: netip.MustParseAddr("172.16.0.10"),
			SourceNet: xnetip.MustParseNetwork("192.0.2.0/24"),
		}},
		RingCapacity: 1000,
	})
	require.NoError(t, service.SetRealServerState(0, false))
	require.NoError(t, service.UpdateRing(nil))
	require.NoError(t, service.Publish(agent))
	info, err := service.Inspect()
	require.NoError(t, err)
	require.Len(t, info.RealServers, 1)
	require.False(t, info.RealServers[0].Enabled)
	module, err := cl3b.NewModuleConfig(agent, "test")
	require.NoError(t, err)
	t.Cleanup(func() { _ = module.Free() })
	require.NoError(t, module.Update([]cl3b.DestinationFilterRule{{
		Net4s:          []xnetip.Contiguous[xnetip.Network4]{xnetip.MustParseContiguous4("192.168.1.1/32")},
		ProtoRanges:    filter.ProtoRanges{filter.NewProtoRange(1, filter.ExactSubtype(8))},
		VirtualService: "svc",
	}, {
		Net6s:          []xnetip.BiContiguous{xnetip.MustParseBiContiguous("2001:db8:1::1/128")},
		ProtoRanges:    filter.ProtoRanges{filter.NewProtoRange(58, filter.ExactSubtype(128))},
		VirtualService: "svc",
	}}))
	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}))
	executionContext, err := harness.PublishedExecutionContext(0)
	require.NoError(t, err)
	for _, family := range []string{"IPv4", "IPv6"} {
		for _, code := range []byte{0, 1, 255} {
			for _, extended := range []bool{false, true} {
				for _, payload := range [][]byte{nil, {0, 0xff, 0x80, 0, 1, 2, 3}} {
					name := fmt.Sprintf("%s/code_%d/extended_%t/payload_%d", family, code, extended, len(payload))
					t.Run(name, func(t *testing.T) {
						request := classifyFrame("10.0.0.1", "192.168.1.1", 0, 0, 1, 8, payload)
						headerLength := 20
						replyType := byte(0)
						if family == "IPv6" {
							request = classifyFrame("2001:db8::1", "2001:db8:1::1", 0, 0, 58, 128, payload)
							headerLength, replyType = 40, 129
						}
						if extended {
							extensions := []byte{1, 1, 0, 0}
							if family == "IPv6" {
								// Hop-by-Hop, Routing SL0, Destination Options, atomic fragment.
								extensions = []byte{
									43, 0, 1, 4, 0, 0, 0, 0,
									60, 0, 253, 0, 0, 0, 0, 0,
									44, 0, 1, 4, 0, 0, 0, 0,
									58, 0, 0, 0, 0x12, 0x34, 0x56, 0x78,
								}
								request[20] = 0
							}
							request = append(request[:ethHeaderLen+headerLength], append(extensions, request[ethHeaderLen+headerLength:]...)...)
							headerLength += len(extensions)
						}
						network := request[ethHeaderLen:]
						message := network[headerLength:]
						message[1] = code
						binary.BigEndian.PutUint16(message[4:6], 0xa1b2)
						binary.BigEndian.PutUint16(message[6:8], 0xc3d4)
						var pseudoHeader []byte
						if family == "IPv4" {
							network[0], network[1], network[8] = 0x40|byte(headerLength/4), 0xab, 1
							binary.BigEndian.PutUint16(network[2:4], uint16(len(network)))
							binary.BigEndian.PutUint16(network[4:6], 0x9876)
							binary.BigEndian.PutUint16(network[6:8], 0x4000)
							binary.BigEndian.PutUint16(network[10:12], echoChecksum(network[:headerLength]))
						} else {
							binary.BigEndian.PutUint32(network[:4], 0x6ab12345)
							binary.BigEndian.PutUint16(network[4:6], uint16(len(network)-40))
							network[7] = 1
							pseudoHeader = make([]byte, 40)
							copy(pseudoHeader, network[8:40])
							binary.BigEndian.PutUint32(pseudoHeader[32:36], uint32(len(message)))
							pseudoHeader[39] = 58
						}
						binary.BigEndian.PutUint16(message[2:4], echoChecksum(append(bytes.Clone(pseudoHeader), message...)))
						for _, split := range []string{"contiguous", "inside_echo_header"} {
							t.Run(split, func(t *testing.T) {
								segments := [][]byte{request}
								if split != "contiguous" {
									boundary := ethHeaderLen + headerLength + 7
									segments = [][]byte{request[:boundary], request[boundary:]}
								}
								repliedBefore, _, err := cl3bobject.ReadServiceCounter(executionContext, "svc", "icmp_replied")
								require.NoError(t, err)
								result, err := harness.HandleSegmentedPackets(segments)
								require.NoError(t, err)
								repliedAfter, _, err := cl3bobject.ReadServiceCounter(executionContext, "svc", "icmp_replied")
								require.NoError(t, err)
								if split == "inside_echo_header" {
									require.Empty(t, result.Output)
									// Chained drop observations expose only the head segment.
									require.Equal(t, [][]byte{segments[0]}, result.Drop)
									require.Equal(t, repliedBefore, repliedAfter)
								} else {
									require.Empty(t, result.Drop)
									require.Len(t, result.Output, 1)
									require.Equal(t, repliedBefore+1, repliedAfter)
									reply := result.Output[0]
									require.Len(t, reply, len(request))
									expected := bytes.Clone(request)
									replyNetwork := reply[ethHeaderLen:]
									if family == "IPv4" {
										require.Zero(t, echoChecksum(replyNetwork[:headerLength]))
										expected[22] = 65
										copy(expected[26:30], request[30:34])
										copy(expected[30:34], request[26:30])
										copy(expected[24:26], reply[24:26])
									} else {
										expected[21] = 65
										copy(expected[22:38], request[38:54])
										copy(expected[38:54], request[22:38])
										copy(pseudoHeader, replyNetwork[8:40])
									}
									require.Zero(t, echoChecksum(append(bytes.Clone(pseudoHeader), replyNetwork[headerLength:]...)))
									offset := ethHeaderLen + headerLength
									expected[offset], expected[offset+1] = replyType, 0
									copy(expected[offset+2:offset+4], reply[offset+2:offset+4])
									require.Equal(t, expected, reply)
								}
								sessions, cursors := readEchoSessions(t, agent, service, harness.CurrentTime())
								require.Empty(t, sessions)
								require.Equal(t, []uint64{0}, cursors)
								for _, counter := range []string{"real/0", "ring_empty", "real_disabled"} {
									packets, bytes, err := cl3bobject.ReadServiceCounter(executionContext, "svc", counter)
									require.NoError(t, err)
									require.Zero(t, packets, counter)
									require.Zero(t, bytes, counter)
								}
							})
						}
					})
				}
			}
		}
	}
}

// TestL3b_FixesMssAndAppliesSessionPolicy verifies the service flags and the
// session lifetime policy: a SYN's MSS option is clamped to 1220 with the
// checksums kept valid, and an established TCP flow whose real went disabled
// is dropped instead of being rescheduled mid-connection.
func TestL3b_FixesMssAndAppliesSessionPolicy(t *testing.T) {
	h, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")

	service, _ := newVirtualService(t, agent, "svc", cl3bobject.VirtualServiceConfig{
		SourceFilterRules: []cl3bobject.SourceFilterRule{{
			Net4s:      []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			PortRanges: filter.PortRanges{{From: 1, To: 65535}},
		}},
		RealServers: []cl3bobject.RealServer{{
			Type:               cl3bobject.IPv4,
			DestinationAddress: xerror.Unwrap(netip.ParseAddr("172.16.0.10")),
			SourceNet:          xnetip.MustParseNetwork("192.0.2.0/24"),
		}},
		HashMask:     0,
		IndexMask:    0,
		RingCapacity: 1000,
		Flags:        cl3bobject.FixMSS,
	})
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

	service, _ := newVirtualService(t, agent, "svc", cl3bobject.VirtualServiceConfig{
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
		HashMask:     0,
		IndexMask:    1,
		RingCapacity: 2 * 1000,
		Flags:        cl3bobject.PureL3,
		// One-packet scheduling rotates per packet, so only the shared
		// session keeps both flows on one real.
		SchedulerFlags: cl3bobject.SchedulerCounter,
	})
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
		service, _ := newVirtualService(t, agent, name, cl3bobject.VirtualServiceConfig{
			SourceFilterRules: []cl3bobject.SourceFilterRule{sourceRule},
			RealServers:       []cl3bobject.RealServer{realServer},
			HashMask:          0,
			IndexMask:         0,
			RingCapacity:      1000,
			DSCPFlags:         dscpFlags,
		})
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

// newCapacityAgent returns an initially unpublished harness agent with
// the l3b objects loaded for capacity accounting.
//
// Callers may publish services when checking destination links.
func newCapacityAgent(t *testing.T) *ffi.Agent {
	t.Helper()

	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(64 * datasize.MB),
		DPMemory:      uint64(4 * datasize.MB),
		WorkerCount:   1,
		Modules:       []string{"l3b"},
		ObjectsToLoad: []string{"l3b_virtual_service", "l3b_session_table"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	agent, err := harness.SharedMemory().AgentAttach(
		"l3b-capacity-test", 0, 16*datasize.MB,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	return agent
}

// Test_L3bVirtualService_RealServerFamilies verifies that tunnel types and
// address families agree before construction, without normalizing valid inputs.
func Test_L3bVirtualService_RealServerFamilies(t *testing.T) {
	agent := newCapacityAgent(t)
	table, err := cl3bobject.CreateSessionTable(agent, "families", 1, 64)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, table.Free()) })
	source4 := xnetip.MustParseNetwork("192.0.2.0/24")
	source6 := xnetip.MustParseNetwork("2001:db8::/64")
	mappedSource := xnetip.MustParseNetwork("::ffff:192.0.2.0/120")
	destination4 := netip.MustParseAddr("198.51.100.1")
	destination6 := netip.MustParseAddr("2001:db8:1::1")
	mappedDestination := netip.MustParseAddr("::ffff:198.51.100.1")
	for _, tc := range []struct {
		name        string
		family      cl3bobject.IPFamily
		destination netip.Addr
		source      xnetip.Network
		valid       bool
	}{
		{"native IPv4", cl3bobject.IPv4, destination4, source4, true},
		{"native IPv6", cl3bobject.IPv6, destination6, source6, true},
		{"mapped IPv6", cl3bobject.IPv6, mappedDestination, mappedSource, true},
		{"mapped destination native IPv6 source", cl3bobject.IPv6, mappedDestination, source6, true},
		{"native IPv6 destination mapped source", cl3bobject.IPv6, destination6, mappedSource, true},
		{"zero IPv6 network", cl3bobject.IPv6, destination6, xnetip.Network{}, true},
		{"IPv4 zero mask", cl3bobject.IPv4, destination4, xnetip.MustParseNetwork("0.0.0.0/0"), true},
		{"IPv4 arbitrary mask", cl3bobject.IPv4, destination4, xnetip.MustParseNetwork("192.0.2.0/255.0.255.0"), true},
		{"IPv6 arbitrary mask", cl3bobject.IPv6, destination6, xnetip.MustParseNetwork("2001:db8::/ffff:0:ffff::"), true},
		{"IPv4 source IPv6 destination", cl3bobject.IPv6, destination6, source4, false},
		{"IPv6 source IPv4 destination", cl3bobject.IPv4, destination4, source6, false},
		{"IPv4 source mapped destination", cl3bobject.IPv4, mappedDestination, source4, false},
		{"mapped source IPv4 destination", cl3bobject.IPv6, destination4, mappedSource, false},
		{"IPv4 addresses IPv6 type", cl3bobject.IPv6, destination4, source4, false},
		{"IPv6 addresses IPv4 type", cl3bobject.IPv4, destination6, source6, false},
		{"unsupported type", cl3bobject.IPFamily(99), destination4, source4, false},
		{"invalid destination IPv4 source", cl3bobject.IPv4, netip.Addr{}, source4, false},
		{"invalid destination IPv6 source", cl3bobject.IPv6, netip.Addr{}, source6, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := cl3bobject.VirtualServiceConfig{RealServers: []cl3bobject.RealServer{{
				Type: tc.family, DestinationAddress: tc.destination, SourceNet: tc.source,
			}}}
			capacity := agent.BlockAllocatorFreeSize()
			var candidate *cl3bobject.VirtualServiceObject
			var err error
			require.NotPanics(t, func() {
				candidate, err = cl3bobject.CreateVirtualService(agent, "families", config, table)
			})
			if candidate != nil {
				t.Cleanup(func() { require.NoError(t, candidate.Free()) })
			}
			if !tc.valid {
				require.ErrorContains(t, err, "real server 0")
				require.Nil(t, candidate)
				require.Equal(t, capacity, agent.BlockAllocatorFreeSize())
				return
			}
			require.NoError(t, err)
			info, err := candidate.Inspect()
			require.NoError(t, err)
			require.Equal(t, []cl3bobject.RealServerInfo{{
				Family: tc.family, DestinationAddress: tc.destination, SourceNet: tc.source,
			}}, info.RealServers)
		})
	}
	t.Run("empty real collection", func(t *testing.T) {
		candidate, err := cl3bobject.CreateVirtualService(agent, "empty", cl3bobject.VirtualServiceConfig{}, table)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, candidate.Free()) })
		info, err := candidate.Inspect()
		require.NoError(t, err)
		require.Empty(t, info.RealServers)
	})
}

// Test_L3bProbe_ServiceSmoke verifies that included constructors compile both
// address families while leaving their caller-owned table intact on release.
func Test_L3bProbe_ServiceSmoke(t *testing.T) {
	agent := newCapacityAgent(t)
	table, err := probe.NewTable(agent, "probe-table")
	require.NoError(t, err)
	require.True(t, probe.SeedTable(table))
	service, witness, err := probe.NewService(agent, table, "probe-service", probe.Masks{}, false)
	require.NoError(t, err)
	require.Equal(t, probe.Witness{IPv4Decoded: true, IPv6Entered: true}, witness)
	require.NoError(t, probe.FreeService(&service))
	require.True(t, probe.HasSentinel(table))
	require.NoError(t, probe.FreeTable(&table))
}

// Test_L3bProbe_RawMasks verifies that both production entrypoints reject holes
// inside either half while accepting independently contiguous IPv6 halves.
func Test_L3bProbe_RawMasks(t *testing.T) {
	cases := []struct {
		name  string
		masks probe.Masks
		valid bool
	}{
		{name: "IPv4 hole", masks: probe.Masks{IPv4: [4]byte{0xff, 0x7f}}},
		{name: "IPv6 first half hole", masks: probe.Masks{IPv6: [16]byte{0xff, 0x7f}}},
		{name: "IPv6 second half hole", masks: probe.Masks{IPv6: [16]byte{8: 0xff, 9: 0x7f}}},
		{name: "IPv6 cross boundary gap", masks: probe.Masks{IPv6: [16]byte{0xff, 0xff, 8: 0xff, 9: 0xff}}, valid: true},
		{name: "valid first IPv4 network then invalid second", masks: probe.Masks{AdditionalIPv4: [][4]byte{{0xff, 0x7f}}}},
		{name: "valid first IPv6 network then second with first half hole", masks: probe.Masks{AdditionalIPv6: [][16]byte{{0xff, 0x7f}}}},
		{name: "valid first IPv6 network then second with second half hole", masks: probe.Masks{AdditionalIPv6: [][16]byte{{8: 0xff, 9: 0x7f}}}},
		{name: "two valid IPv4 networks", masks: probe.Masks{AdditionalIPv4: [][4]byte{{0xff, 0xff}}}, valid: true},
		{name: "two valid IPv6 networks", masks: probe.Masks{AdditionalIPv6: [][16]byte{{0xff, 0xff}}}, valid: true},
		{name: "valid first IPv6 network then second with cross boundary gap", masks: probe.Masks{AdditionalIPv6: [][16]byte{{0xff, 0xff, 8: 0xff, 9: 0xff}}}, valid: true},
		{name: "second rule IPv4 hole", masks: probe.Masks{InSecondRule: true, IPv4: [4]byte{0xff, 0x7f}}},
		{name: "second rule IPv6 first half hole", masks: probe.Masks{InSecondRule: true, IPv6: [16]byte{0xff, 0x7f}}},
		{name: "second rule IPv6 second half hole", masks: probe.Masks{InSecondRule: true, IPv6: [16]byte{8: 0xff, 9: 0x7f}}},
		{name: "valid second rule IPv4 prefix and IPv6 cross boundary gap", masks: probe.Masks{InSecondRule: true, IPv4: [4]byte{0xff, 0xff}, IPv6: [16]byte{0xff, 0xff, 8: 0xff, 9: 0xff}}, valid: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agent := newCapacityAgent(t)
			table, err := probe.NewTable(agent, "raw-table")
			require.NoError(t, err)
			require.True(t, probe.SeedTable(table))
			published, witness, err := probe.NewService(agent, table, "published-service", probe.Masks{}, false)
			require.NoError(t, err)
			require.Equal(t, probe.Witness{IPv4Decoded: true, IPv6Entered: true}, witness)
			require.NoError(t, agent.UpdateObjects([]ffi.ObjectConfig{published}))
			capacity := agent.BlockAllocatorFreeSize()
			service, witness, err := probe.NewService(agent, table, "raw-service", tc.masks, false)
			if tc.valid {
				require.NoError(t, err)
				require.Equal(t, probe.Witness{IPv4Decoded: true, IPv6Entered: true}, witness)
				require.NoError(t, probe.FreeService(&service))
			} else {
				require.Error(t, err)
				require.Nil(t, service.AsRawPtr())
				require.Equal(t, probe.Witness{}, witness)
			}
			require.Equal(t, capacity, agent.BlockAllocatorFreeSize())
			require.True(t, probe.HasSentinel(table))
			module, err := probe.NewModule(agent, "raw-module")
			require.NoError(t, err)
			witness, err = probe.UpdateModule(module, "published-service", tc.masks, false)
			if tc.valid {
				require.NoError(t, err)
				require.Equal(t, probe.Witness{IPv4Decoded: true, IPv6Entered: true}, witness)
			} else {
				require.Error(t, err)
				require.Equal(t, probe.Witness{}, witness)
			}
			// Failed preflight still owns the earlier service link allocations.
			require.NoError(t, probe.FreeModule(&module))
			require.Equal(t, capacity, agent.BlockAllocatorFreeSize())
			require.True(t, probe.HasSentinel(table))
		})
	}
}

// Test_L3bProbe_SourceFailureRestoresCapacity verifies that a fault after IPv4
// decode releases partial state without destroying a borrowed live table.
func Test_L3bProbe_SourceFailureRestoresCapacity(t *testing.T) {
	agent := newCapacityAgent(t)
	table, err := probe.NewTable(agent, "source-table")
	require.NoError(t, err)
	require.True(t, probe.SeedTable(table))
	service, witness, err := probe.NewService(agent, table, "source-service", probe.Masks{}, false)
	require.NoError(t, err)
	require.Equal(t, probe.Witness{IPv4Decoded: true, IPv6Entered: true}, witness)
	require.NoError(t, probe.FreeService(&service))
	capacity := agent.BlockAllocatorFreeSize()
	for range 3 {
		service, witness, err = probe.NewService(agent, table, "source-service", probe.Masks{}, true)
		require.Error(t, err)
		require.Nil(t, service.AsRawPtr())
		require.Equal(t, probe.Witness{IPv4Decoded: true, IPv6Entered: true, Faults: 1}, witness)
		require.Equal(t, capacity, agent.BlockAllocatorFreeSize())
		require.True(t, probe.HasSentinel(table))
		service, witness, err = probe.NewService(agent, table, "source-service", probe.Masks{}, false)
		require.NoError(t, err)
		require.Equal(t, probe.Witness{IPv4Decoded: true, IPv6Entered: true}, witness)
		require.NoError(t, probe.FreeService(&service))
		require.Equal(t, capacity, agent.BlockAllocatorFreeSize())
		require.True(t, probe.HasSentinel(table))
	}
	require.NoError(t, probe.FreeTable(&table))
}

// Test_L3bProbe_DestinationFailureRestoresCapacity verifies that successful and
// faulted dual-family updates release all candidate-owned classifiers and links.
func Test_L3bProbe_DestinationFailureRestoresCapacity(t *testing.T) {
	agent := newCapacityAgent(t)
	table, err := probe.NewTable(agent, "destination-table")
	require.NoError(t, err)
	require.True(t, probe.SeedTable(table))
	service, witness, err := probe.NewService(agent, table, "destination-service", probe.Masks{}, false)
	require.NoError(t, err)
	require.Equal(t, probe.Witness{IPv4Decoded: true, IPv6Entered: true}, witness)
	require.NoError(t, agent.UpdateObjects([]ffi.ObjectConfig{service}))
	module, err := probe.NewModule(agent, "destination-module")
	require.NoError(t, err)
	witness, err = probe.UpdateModule(module, "destination-service", probe.Masks{}, false)
	require.NoError(t, err)
	require.Equal(t, probe.Witness{IPv4Decoded: true, IPv6Entered: true}, witness)
	require.NoError(t, probe.FreeModule(&module))
	capacity := agent.BlockAllocatorFreeSize()
	for range 3 {
		module, err = probe.NewModule(agent, "destination-module")
		require.NoError(t, err)
		witness, err = probe.UpdateModule(module, "destination-service", probe.Masks{}, true)
		require.Error(t, err)
		require.Equal(t, probe.Witness{IPv4Decoded: true, IPv6Entered: true, Faults: 1}, witness)
		require.NoError(t, probe.FreeModule(&module))
		require.Equal(t, capacity, agent.BlockAllocatorFreeSize())
		require.True(t, probe.HasSentinel(table))
		module, err = probe.NewModule(agent, "destination-module")
		require.NoError(t, err)
		witness, err = probe.UpdateModule(module, "destination-service", probe.Masks{}, false)
		require.NoError(t, err)
		require.Equal(t, probe.Witness{IPv4Decoded: true, IPv6Entered: true}, witness)
		require.NoError(t, probe.FreeModule(&module))
		require.Equal(t, capacity, agent.BlockAllocatorFreeSize())
		require.True(t, probe.HasSentinel(table))
	}
}

// Test_L3bVirtualService_RepeatedCreateReturnsAgentCapacity verifies that
// alternating compiled and rule-less services preserve post-warm-up capacity.
//
// Each round creates and frees a dual-family service, then a rule-less service.
func Test_L3bVirtualService_RepeatedCreateReturnsAgentCapacity(t *testing.T) {
	agent := newCapacityAgent(t)

	dualFamily := cl3bobject.VirtualServiceConfig{
		SourceFilterRules: []cl3bobject.SourceFilterRule{{
			Net4s:      []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			Net6s:      []xnetip.BiContiguous{filter.UnspecifiedIPv6},
			PortRanges: filter.PortRanges{{From: 1, To: 65535}},
		}},
		RingCapacity: 4,
	}

	createAndFree := func(config cl3bobject.VirtualServiceConfig) {
		table, err := cl3bobject.CreateSessionTable(
			agent, "capacity", 1, 64,
		)
		require.NoError(t, err)

		service, err := cl3bobject.CreateVirtualService(
			agent, "capacity", config, table,
		)
		require.NoError(t, err)

		// The service borrows the table, so it must go away first.
		require.NoError(t, service.Free())
		require.NoError(t, table.Free())
	}

	// The capacity reference is measured after one compiled/rule-less warm-up round.
	createAndFree(dualFamily)
	createAndFree(cl3bobject.VirtualServiceConfig{})

	base := agent.BlockAllocatorFreeSize()
	for range 3 {
		createAndFree(dualFamily)
		createAndFree(cl3bobject.VirtualServiceConfig{})
	}
	require.Equal(
		t,
		base,
		agent.BlockAllocatorFreeSize(),
		"repeated create and free cycles lost agent capacity",
	)
}

// classifyFrame builds an unpadded frame with declared transport and IP lengths.
//
// Checksums remain zero because the harness does not validate them.
func classifyFrame(
	source, destination string,
	sourcePort, destinationPort uint16,
	protocol, subtype byte,
	payload []byte,
) []byte {
	transportLength := 8
	if protocol == 6 {
		transportLength = 20
	}
	header := make([]byte, transportLength)
	switch protocol {
	case 6, 17:
		binary.BigEndian.PutUint16(header[0:2], sourcePort)
		binary.BigEndian.PutUint16(header[2:4], destinationPort)
		if protocol == 6 {
			header[12] = 5 << 4
			header[13] = subtype
			binary.BigEndian.PutUint16(header[14:16], 1024)
		} else {
			binary.BigEndian.PutUint16(header[4:6], uint16(transportLength+len(payload)))
		}
	case 1, 58:
		header[0] = subtype
		binary.BigEndian.PutUint16(header[4:6], 0x1234)
		binary.BigEndian.PutUint16(header[6:8], 7)
	}
	sourceAddress := netip.MustParseAddr(source)
	destinationAddress := netip.MustParseAddr(destination)
	networkLength := 20
	if sourceAddress.Is6() {
		networkLength = 40
	}
	frame := make([]byte, ethHeaderLen+networkLength+transportLength+len(payload))
	copy(frame[0:6], []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66})
	copy(frame[6:12], []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff})
	if sourceAddress.Is4() {
		binary.BigEndian.PutUint16(frame[12:14], 0x0800)
		frame[14] = 0x45
		binary.BigEndian.PutUint16(frame[16:18], uint16(networkLength+transportLength+len(payload)))
		frame[22], frame[23] = 64, protocol
		copy(frame[26:30], sourceAddress.AsSlice())
		copy(frame[30:34], destinationAddress.AsSlice())
	} else {
		binary.BigEndian.PutUint16(frame[12:14], 0x86dd)
		frame[14] = 0x60
		binary.BigEndian.PutUint16(frame[18:20], uint16(transportLength+len(payload)))
		frame[20], frame[21] = protocol, 64
		copy(frame[22:38], sourceAddress.AsSlice())
		copy(frame[38:54], destinationAddress.AsSlice())
	}
	copy(frame[ethHeaderLen+networkLength:], header)
	copy(frame[ethHeaderLen+networkLength+transportLength:], payload)
	return frame
}

// publishClassifyService publishes a service with one usable IPv4 real and ring.
func publishClassifyService(
	t *testing.T,
	agent *ffi.Agent,
	name, realIP string,
	rules []cl3bobject.SourceFilterRule,
) *cl3bobject.VirtualServiceObject {
	t.Helper()
	object, _ := newVirtualService(t, agent, name, cl3bobject.VirtualServiceConfig{
		SourceFilterRules: rules,
		RealServers: []cl3bobject.RealServer{{
			Type:               cl3bobject.IPv4,
			DestinationAddress: xerror.Unwrap(netip.ParseAddr(realIP)),
			SourceNet:          xnetip.MustParseNetwork("192.0.2.0/24"),
		}},
		HashMask:     0,
		IndexMask:    0,
		RingCapacity: 1000,
	})
	require.NoError(t, object.UpdateRing([]uint32{0}))
	require.NoError(t, object.Publish(agent))
	return object
}

// publishClassifyModule installs destination rules in the published pipeline.
func publishClassifyModule(
	t *testing.T,
	agent *ffi.Agent,
	configName string,
	rules []cl3b.DestinationFilterRule,
) *cl3b.ModuleConfig {
	t.Helper()
	module, err := cl3b.NewModuleConfig(agent, configName)
	require.NoError(t, err)
	require.NoError(t, module.Update(rules))
	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}))
	t.Cleanup(func() { _ = module.Free() })
	return module
}

// counterOf reads a published service's packet and byte counter pair.
func counterOf(t *testing.T, harness *dataplaneut.Harness, service, name string) (uint64, uint64) {
	t.Helper()
	ctx, err := harness.PublishedExecutionContext(0)
	require.NoError(t, err)
	packets, byteCount, err := cl3bobject.ReadServiceCounter(ctx, service, name)
	require.NoError(t, err)
	return packets, byteCount
}

// Test_L3b_ClassifyOmittedSourcePortRanges verifies that omitted ports admit
// every service port while restrictive source networks still reject outsiders.
func Test_L3b_ClassifyOmittedSourcePortRanges(t *testing.T) {
	harness, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")
	publishClassifyService(t, agent, "svc", "172.16.0.10", []cl3bobject.SourceFilterRule{{
		Net4s: []xnetip.Contiguous[xnetip.Network4]{xnetip.MustParseContiguous4("10.0.0.0/24")},
		Net6s: []xnetip.BiContiguous{xnetip.MustParseBiContiguous("2001:db8:1::/48")},
	}})
	publishClassifyModule(t, agent, "test", []cl3b.DestinationFilterRule{{
		Net4s: []xnetip.Contiguous[xnetip.Network4]{xnetip.MustParseContiguous4("192.168.1.0/24")},
		Net6s: []xnetip.BiContiguous{xnetip.MustParseBiContiguous("2001:db8:2::/48")},
		ProtoRanges: filter.ProtoRanges{
			filter.NewProtoRange(6, filter.AnySubtype()),
			filter.NewProtoRange(17, filter.AnySubtype()),
		},
		VirtualService: "svc",
	}})
	for _, tc := range []struct {
		name, allowed, foreign, destination string
	}{
		{"IPv4", "10.0.0.1", "10.9.9.1", "192.168.1.1"},
		{"IPv6", "2001:db8:1::1", "2001:db8:9::1", "2001:db8:2::1"},
	} {
		for _, protocol := range []byte{6, 17} {
			for _, port := range []uint16{0, 80, 40000, 65535} {
				t.Run(fmt.Sprintf("%s/protocol%d/servicePort%d", tc.name, protocol, port), func(t *testing.T) {
					allowed := classifyFrame(tc.allowed, tc.destination, 12345, port, protocol, 2, nil)
					foreign := classifyFrame(tc.foreign, tc.destination, 12345, port, protocol, 2, nil)
					before, _ := counterOf(t, harness, "svc", "filter_rejected")
					result, err := harness.HandleSegmentedPackets([][]byte{allowed}, [][]byte{foreign})
					require.NoError(t, err)
					require.Len(t, result.Output, 1)
					require.Equal(t, [][]byte{foreign}, result.Drop)
					info, err := framework.NewPacketParser().ParsePacket(result.Output[0])
					require.NoError(t, err)
					require.True(t, info.IsTunneled)
					require.Equal(t, "172.16.0.10", info.DstIP.String())
					after, _ := counterOf(t, harness, "svc", "filter_rejected")
					require.Equal(t, before+1, after)
				})
			}
		}
	}
}

// Test_L3b_ClassifyRestrictedSourcePortRanges verifies that source admission
// restricts the destination service port, not the client's source port.
func Test_L3b_ClassifyRestrictedSourcePortRanges(t *testing.T) {
	for _, tc := range []struct {
		name, source, destination string
	}{
		{"IPv4", "10.0.0.1", "192.168.1.1"},
		{"IPv6", "2001:db8:1::1", "2001:db8:2::1"},
	} {
		for _, protocol := range []byte{6, 17} {
			t.Run(fmt.Sprintf("%s/protocol%d", tc.name, protocol), func(t *testing.T) {
				harness, agent := setupL3bHarness(t, "port0", "test")
				wirePipeline(t, agent, "port0", "test")
				publishClassifyService(t, agent, "svc", "172.16.0.10", []cl3bobject.SourceFilterRule{{
					Net4s:      []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
					Net6s:      []xnetip.BiContiguous{filter.UnspecifiedIPv6},
					PortRanges: filter.PortRanges{{From: 80, To: 80}},
				}})
				publishClassifyModule(t, agent, "test", []cl3b.DestinationFilterRule{{
					Net4s:          []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
					Net6s:          []xnetip.BiContiguous{filter.UnspecifiedIPv6},
					ProtoRanges:    filter.ProtoRanges{filter.NewProtoRange(protocol, filter.AnySubtype())},
					VirtualService: "svc",
				}})
				allowed := classifyFrame(tc.source, tc.destination, 12345, 80, protocol, 2, nil)
				foreign := classifyFrame(tc.source, tc.destination, 80, 12345, protocol, 2, nil)
				result, err := harness.HandleSegmentedPackets([][]byte{allowed}, [][]byte{foreign})
				require.NoError(t, err)
				require.Len(t, result.Output, 1)
				require.Equal(t, [][]byte{foreign}, result.Drop)
				packets, _ := counterOf(t, harness, "svc", "filter_rejected")
				require.EqualValues(t, 1, packets)
			})
		}
	}
}

// Test_L3b_DirectPathPortRanges verifies that reversed ranges cover nothing
// and do not interfere with valid ranges through the direct binding.
func Test_L3b_DirectPathPortRanges(t *testing.T) {
	for _, tc := range []struct {
		name     string
		ranges   filter.PortRanges
		admitted bool
	}{
		{"mixed valid and reversed", filter.PortRanges{{From: 100, To: 200}, {From: 100, To: 50}}, true},
		{"reversed only", filter.PortRanges{{From: 100, To: 50}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			harness, agent := setupL3bHarness(t, "port0", "test")
			wirePipeline(t, agent, "port0", "test")
			publishClassifyService(t, agent, "svc", "172.16.0.10", []cl3bobject.SourceFilterRule{{
				Net4s:      []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
				Net6s:      []xnetip.BiContiguous{filter.UnspecifiedIPv6},
				PortRanges: tc.ranges,
			}})
			publishClassifyModule(t, agent, "test", []cl3b.DestinationFilterRule{{
				Net4s:          []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
				Net6s:          []xnetip.BiContiguous{filter.UnspecifiedIPv6},
				ProtoRanges:    filter.ProtoRanges{filter.NewProtoRange(6, filter.AnySubtype()), filter.NewProtoRange(17, filter.AnySubtype())},
				VirtualService: "svc",
			}})
			for _, family := range []struct{ source, destination string }{
				{"10.0.0.1", "192.168.1.1"}, {"2001:db8:1::1", "2001:db8:2::1"},
			} {
				for _, protocol := range []byte{6, 17} {
					for _, port := range []uint16{0, 50, 60, 99, 100, 150, 200, 201, 65535} {
						frame := classifyFrame(family.source, family.destination, 12345, port, protocol, 2, nil)
						result, err := harness.HandleSegmentedPackets([][]byte{frame})
						require.NoError(t, err)
						if tc.admitted && port >= 100 && port <= 200 {
							require.Len(t, result.Output, 1)
							require.Empty(t, result.Drop)
						} else {
							require.Empty(t, result.Output)
							require.Equal(t, [][]byte{frame}, result.Drop)
						}
					}
				}
			}
		})
	}
}

// Test_L3b_ClassifyEmptyPredicates verifies that missing networks and empty
// destination protocols match no traffic.
//
// Rule-less services reject ordinary transport at source admission.
func Test_L3b_ClassifyEmptyPredicates(t *testing.T) {
	for _, tc := range []struct {
		name             string
		sourceRules      []cl3bobject.SourceFilterRule
		destination      cl3b.DestinationFilterRule
		filterRejections uint64
	}{
		{"rule-less source service", nil, cl3b.DestinationFilterRule{
			Net4s:       []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			Net6s:       []xnetip.BiContiguous{filter.UnspecifiedIPv6},
			ProtoRanges: filter.ProtoRanges{filter.NewProtoRange(6, filter.AnySubtype()), filter.NewProtoRange(17, filter.AnySubtype())},
		}, 4},
		{"empty destination protocols", []cl3bobject.SourceFilterRule{{
			Net4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			Net6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6},
		}}, cl3b.DestinationFilterRule{
			Net4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			Net6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6},
		}, 0},
		{"missing destination families", []cl3bobject.SourceFilterRule{{
			Net4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			Net6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6},
		}}, cl3b.DestinationFilterRule{
			ProtoRanges: filter.ProtoRanges{filter.NewProtoRange(6, filter.AnySubtype()), filter.NewProtoRange(17, filter.AnySubtype())},
		}, 0},
		{"missing source families", []cl3bobject.SourceFilterRule{{PortRanges: filter.PortRanges{{From: 0, To: 65535}}}}, cl3b.DestinationFilterRule{
			Net4s:       []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			Net6s:       []xnetip.BiContiguous{filter.UnspecifiedIPv6},
			ProtoRanges: filter.ProtoRanges{filter.NewProtoRange(6, filter.AnySubtype()), filter.NewProtoRange(17, filter.AnySubtype())},
		}, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			harness, agent := setupL3bHarness(t, "port0", "test")
			wirePipeline(t, agent, "port0", "test")
			publishClassifyService(t, agent, "svc", "172.16.0.10", tc.sourceRules)
			tc.destination.VirtualService = "svc"
			publishClassifyModule(t, agent, "test", []cl3b.DestinationFilterRule{tc.destination})
			for _, family := range []struct{ source, destination string }{
				{"10.0.0.1", "192.168.1.1"}, {"2001:db8:1::1", "2001:db8:2::1"},
			} {
				for _, protocol := range []byte{6, 17} {
					frame := classifyFrame(family.source, family.destination, 12345, 80, protocol, 2, nil)
					result, err := harness.HandleSegmentedPackets([][]byte{frame})
					require.NoError(t, err)
					require.Empty(t, result.Output)
					require.Equal(t, [][]byte{frame}, result.Drop)
				}
			}
			packets, _ := counterOf(t, harness, "svc", "filter_rejected")
			require.Equal(t, tc.filterRejections, packets)
		})
	}
}

// Test_L3b_FirstMatchBatchBoundary verifies that first-match service decoding
// survives the 256-packet family boundary in an interleaved dual-family batch.
func Test_L3b_FirstMatchBatchBoundary(t *testing.T) {
	harness, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")
	for _, tc := range []struct{ name, real string }{{"narrow", "172.16.0.10"}, {"broad", "172.16.0.11"}} {
		publishClassifyService(t, agent, tc.name, tc.real, []cl3bobject.SourceFilterRule{{
			Net4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			Net6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6},
		}})
	}
	publishClassifyModule(t, agent, "test", []cl3b.DestinationFilterRule{
		{
			Net4s:          []xnetip.Contiguous[xnetip.Network4]{xnetip.MustParseContiguous4("192.168.1.1/32")},
			Net6s:          []xnetip.BiContiguous{xnetip.MustParseBiContiguous("2001:db8:2::1/128")},
			ProtoRanges:    filter.ProtoRanges{filter.NewProtoRange(6, filter.AnySubtype())},
			VirtualService: "narrow",
		},
		{
			Net4s:          []xnetip.Contiguous[xnetip.Network4]{xnetip.MustParseContiguous4("192.168.1.0/24")},
			Net6s:          []xnetip.BiContiguous{xnetip.MustParseBiContiguous("2001:db8:2::/48")},
			ProtoRanges:    filter.ProtoRanges{filter.NewProtoRange(6, filter.AnySubtype())},
			VirtualService: "broad",
		},
	})
	packets := make([][][]byte, 0, 514)
	for idx := range 257 {
		destination4, destination6 := "192.168.1.1", "2001:db8:2::1"
		if idx%2 != 0 || idx == 256 {
			destination4, destination6 = "192.168.1.2", "2001:db8:2::2"
		}
		packets = append(packets,
			[][]byte{classifyFrame("10.0.0.1", destination4, uint16(10000+idx), 80, 6, 2, nil)},
			[][]byte{classifyFrame("2001:db8:1::1", destination6, uint16(10000+idx), 80, 6, 2, nil)},
		)
	}
	result, err := harness.HandleSegmentedPackets(packets...)
	require.NoError(t, err)
	require.Empty(t, result.Drop)
	require.Len(t, result.Output, 514)
	for idx, frame := range result.Output {
		info, err := framework.NewPacketParser().ParsePacket(frame)
		require.NoError(t, err)
		require.True(t, info.IsTunneled)
		// Packet order identifies the service expected at each family boundary.
		expected := "172.16.0.10"
		if (idx/2)%2 != 0 || idx/2 == 256 {
			expected = "172.16.0.11"
		}
		require.Equal(t, expected, info.DstIP.String(), "packet %d", idx)
	}
}

// Test_L3b_TCPFlagsSubtypeMatching verifies that readable SYN and ACK flags
// select distinct services while flags zero match neither rule.
func Test_L3b_TCPFlagsSubtypeMatching(t *testing.T) {
	harness, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")
	for _, tc := range []struct{ name, real string }{{"syn", "172.16.0.10"}, {"ack", "172.16.0.11"}} {
		publishClassifyService(t, agent, tc.name, tc.real, []cl3bobject.SourceFilterRule{{
			Net4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			Net6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6},
		}})
	}
	publishClassifyModule(t, agent, "test", []cl3b.DestinationFilterRule{
		{Net4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4}, Net6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6}, ProtoRanges: filter.ProtoRanges{filter.NewProtoRange(6, filter.ExactSubtype(2))}, VirtualService: "syn"},
		{Net4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4}, Net6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6}, ProtoRanges: filter.ProtoRanges{filter.NewProtoRange(6, filter.ExactSubtype(16))}, VirtualService: "ack"},
	})
	for _, family := range []struct{ source, destination string }{
		{"10.0.0.1", "192.168.1.1"}, {"2001:db8:1::1", "2001:db8:2::1"},
	} {
		for _, tc := range []struct {
			name, real string
			flags      byte
		}{
			{"SYN", "172.16.0.10", 2}, {"ACK", "172.16.0.11", 16}, {"zero flags", "", 0},
		} {
			t.Run(family.source+"/"+tc.name, func(t *testing.T) {
				frame := classifyFrame(family.source, family.destination, 12345+uint16(tc.flags), 80, 6, tc.flags, nil)
				result, err := harness.HandleSegmentedPackets([][]byte{frame})
				require.NoError(t, err)
				if tc.real == "" {
					require.Empty(t, result.Output)
					require.Equal(t, [][]byte{frame}, result.Drop)
				} else {
					require.Empty(t, result.Drop)
					require.Len(t, result.Output, 1)
					info, err := framework.NewPacketParser().ParsePacket(result.Output[0])
					require.NoError(t, err)
					require.Equal(t, tc.real, info.DstIP.String())
				}
			})
		}
	}
}

// Test_L3b_SegmentedHeadersFailClosed verifies that nonresident headers reject
// while full-header controls retain their dispositions in the same batch.
//
// Raw observations expose only the head segment, not chained tail bytes.
func Test_L3b_SegmentedHeadersFailClosed(t *testing.T) {
	harness, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")
	publishClassifyService(t, agent, "svc", "172.16.0.10", []cl3bobject.SourceFilterRule{{
		Net4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		Net6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6},
	}})
	publishClassifyModule(t, agent, "test", []cl3b.DestinationFilterRule{{
		Net4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		Net6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6},
		ProtoRanges: filter.ProtoRanges{
			filter.NewProtoRange(6, filter.AnySubtype()), filter.NewProtoRange(17, filter.AnySubtype()),
			filter.NewProtoRange(1, filter.ExactSubtype(8)), filter.NewProtoRange(58, filter.ExactSubtype(128)),
		},
		VirtualService: "svc",
	}})
	for _, tc := range []struct {
		name, source, destination string
		protocol, subtype         byte
		splitOffset               int
	}{
		{"IPv4 TCP IP header", "10.0.0.1", "192.168.1.1", 6, 2, 24},
		{"IPv6 TCP IP header", "2001:db8:1::1", "2001:db8:2::1", 6, 2, 24},
		{"IPv4 TCP flags", "10.0.0.1", "192.168.1.1", 6, 2, 47},
		{"IPv6 TCP flags", "2001:db8:1::1", "2001:db8:2::1", 6, 2, 67},
		{"IPv4 TCP fixed header 19 of 20 bytes", "10.0.0.1", "192.168.1.1", 6, 2, 53},
		{"IPv6 TCP fixed header 19 of 20 bytes", "2001:db8:1::1", "2001:db8:2::1", 6, 2, 73},
		{"IPv4 UDP ports", "10.0.0.1", "192.168.1.1", 17, 0, 36},
		{"IPv6 UDP ports", "2001:db8:1::1", "2001:db8:2::1", 17, 0, 56},
		{"IPv4 UDP fixed header 7 of 8 bytes", "10.0.0.1", "192.168.1.1", 17, 0, 41},
		{"IPv6 UDP fixed header 7 of 8 bytes", "2001:db8:1::1", "2001:db8:2::1", 17, 0, 61},
		{"IPv4 ICMP echo", "10.0.0.1", "192.168.1.1", 1, 8, 36},
		{"IPv6 ICMP echo", "2001:db8:1::1", "2001:db8:2::1", 58, 128, 56},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame := classifyFrame(tc.source, tc.destination, 12345, 80, tc.protocol, tc.subtype, nil)
			result, err := harness.HandleSegmentedPackets([][]byte{frame[:tc.splitOffset], frame[tc.splitOffset:]}, [][]byte{frame})
			require.NoError(t, err)
			require.Equal(t, [][]byte{frame[:tc.splitOffset]}, result.Drop)
			require.Len(t, result.Output, 1)
			info, err := framework.NewPacketParser().ParsePacket(result.Output[0])
			require.NoError(t, err)
			if tc.protocol == 1 || tc.protocol == 58 {
				require.Equal(t, tc.destination, info.SrcIP.String())
			} else {
				require.True(t, info.IsTunneled)
				require.Equal(t, "172.16.0.10", info.DstIP.String())
			}
		})
	}
	// Echo only rewrites resident headers, making its payload chain a safe control.
	for _, tc := range []struct {
		name, source, destination string
		protocol, subtype         byte
		headerLength              int
	}{
		{"IPv4 echo payload", "10.0.0.1", "192.168.1.1", 1, 8, 42},
		{"IPv6 echo payload", "2001:db8:1::1", "2001:db8:2::1", 58, 128, 62},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := []byte{0xab, 0xcd, 1, 2}
			frame := classifyFrame(tc.source, tc.destination, 0, 0, tc.protocol, tc.subtype, payload)
			result, err := harness.HandleSegmentedPackets([][]byte{frame[:tc.headerLength], frame[tc.headerLength:]})
			require.NoError(t, err)
			require.Empty(t, result.Drop)
			require.Len(t, result.Output, 1)
			outputHead := result.Output[0]
			require.Len(t, outputHead, tc.headerLength)
			if tc.protocol == 1 {
				require.Equal(t, netip.MustParseAddr(tc.destination).AsSlice(), outputHead[26:30])
				require.Equal(t, netip.MustParseAddr(tc.source).AsSlice(), outputHead[30:34])
				require.EqualValues(t, 0, outputHead[34])
				require.EqualValues(t, len(frame)-ethHeaderLen, binary.BigEndian.Uint16(outputHead[16:18]))
			} else {
				require.Equal(t, netip.MustParseAddr(tc.destination).AsSlice(), outputHead[22:38])
				require.Equal(t, netip.MustParseAddr(tc.source).AsSlice(), outputHead[38:54])
				require.EqualValues(t, 129, outputHead[54])
				require.EqualValues(t, 8+len(payload), binary.BigEndian.Uint16(outputHead[18:20]))
			}
			require.Zero(t, outputHead[tc.headerLength-7])
			require.Equal(t, frame[tc.headerLength-4:tc.headerLength], outputHead[tc.headerLength-4:])
		})
	}
}

// Test_L3b_SegmentedICMPErrorsPassthrough verifies that readable non-Echo types
// pass unchanged, absent types drop, and following TCP still tunnels.
//
// Chained packet comparisons cover only the observable head bytes.
func Test_L3b_SegmentedICMPErrorsPassthrough(t *testing.T) {
	harness, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")
	publishClassifyService(t, agent, "svc", "172.16.0.10", []cl3bobject.SourceFilterRule{{
		Net4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		Net6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6},
	}})
	publishClassifyModule(t, agent, "test", []cl3b.DestinationFilterRule{{
		Net4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		Net6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6},
		ProtoRanges: filter.ProtoRanges{
			filter.NewProtoRange(1, filter.AnySubtype()),
			filter.NewProtoRange(58, filter.AnySubtype()),
			filter.NewProtoRange(6, filter.AnySubtype()),
		},
		VirtualService: "svc",
	}})
	for _, tc := range []struct {
		name, source, destination string
		protocol, subtype, code   byte
		networkLength             int
	}{
		{"IPv4 fragmentation needed", "10.0.0.1", "192.168.1.1", 1, 3, 4, 20},
		{"IPv6 packet too big", "2001:db8:1::1", "2001:db8:2::1", 58, 2, 0, 40},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quoted := classifyFrame(tc.destination, tc.source, 80, 12345, 6, 2, nil)
			frame := classifyFrame(
				tc.source, tc.destination, 0, 0, tc.protocol, tc.subtype,
				quoted[ethHeaderLen:],
			)
			transportOffset := ethHeaderLen + tc.networkLength
			frame[transportOffset+1] = tc.code
			binary.BigEndian.PutUint32(frame[transportOffset+4:transportOffset+8], 1280)
			packets := [][][]byte{}
			for _, headBytes := range []int{1, 4, 7} {
				splitOffset := transportOffset + headBytes
				packets = append(packets, [][]byte{frame[:splitOffset], frame[splitOffset:]})
			}
			control := classifyFrame(tc.source, tc.destination, 12345, 80, 6, 2, nil)
			packets = append(packets,
				[][]byte{frame[:transportOffset], frame[transportOffset:]},
				[][]byte{frame},
				[][]byte{control},
			)
			result, err := harness.HandleSegmentedPackets(packets...)
			require.NoError(t, err)
			require.Equal(t, [][]byte{frame[:transportOffset]}, result.Drop)
			require.Len(t, result.Output, 5)
			for idx, headBytes := range []int{1, 4, 7} {
				require.Equal(t, frame[:transportOffset+headBytes], result.Output[idx], "ICMP bytes in head: %d", headBytes)
			}
			require.Equal(t, frame, result.Output[3])
			info, err := framework.NewPacketParser().ParsePacket(result.Output[4])
			require.NoError(t, err)
			require.True(t, info.IsTunneled)
			require.Equal(t, "172.16.0.10", info.DstIP.String())
		})
	}
}

// Test_L3b_ClassifyExemptPacketsPassthrough verifies that globally short ICMP
// and unread protocols pass unchanged even with a published matching service.
//
// Admitted TCP and UDP tunnel to the real, and a readable Echo request is
// answered locally.
func Test_L3b_ClassifyExemptPacketsPassthrough(t *testing.T) {
	harness, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")
	publishClassifyService(t, agent, "svc", "172.16.0.10", []cl3bobject.SourceFilterRule{{
		Net4s:      []xnetip.Contiguous[xnetip.Network4]{xnetip.MustParseContiguous4("10.0.0.0/24")},
		Net6s:      []xnetip.BiContiguous{xnetip.MustParseBiContiguous("2001:db8:1::/48")},
		PortRanges: filter.PortRanges{{From: 0, To: 65535}},
	}})
	publishClassifyModule(t, agent, "test", []cl3b.DestinationFilterRule{{
		Net4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		Net6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6},
		ProtoRanges: filter.ProtoRanges{
			filter.NewProtoRange(1, filter.AnySubtype()),
			filter.NewProtoRange(58, filter.AnySubtype()),
			filter.NewProtoRange(47, filter.AnySubtype()),
			filter.NewProtoRange(6, filter.AnySubtype()),
			filter.NewProtoRange(17, filter.AnySubtype()),
		},
		VirtualService: "svc",
	}})
	for _, tc := range []struct {
		name, source, destination string
		protocol, subtype         byte
		networkLength             int
		transportLength           int
		splitOffset               int
	}{
		{"IPv4 short ICMP", "10.0.0.1", "192.168.1.1", 1, 0, 20, 0, 0},
		{"IPv6 short ICMP", "2001:db8:1::1", "2001:db8:2::1", 58, 0, 40, 0, 0},
		{"IPv4 truncated Echo passes through unanswered", "10.0.0.1", "192.168.1.1", 1, 8, 20, 1, 0},
		{"IPv6 truncated Echo passes through unanswered", "2001:db8:1::1", "2001:db8:2::1", 58, 128, 40, 1, 0},
		{"IPv4 one-byte non-Echo passes through", "10.0.0.1", "192.168.1.1", 1, 3, 20, 1, 0},
		{"IPv6 one-byte non-Echo passes through", "2001:db8:1::1", "2001:db8:2::1", 58, 2, 40, 1, 0},
		{"IPv4 unread protocol", "10.0.0.1", "192.168.1.1", 47, 0, 20, 0, 0},
		{"IPv6 unread protocol", "2001:db8:1::1", "2001:db8:2::1", 47, 0, 40, 0, 0},
		{"IPv4 unread protocol IP header split", "10.0.0.1", "192.168.1.1", 47, 0, 20, 0, 24},
		{"IPv6 unread protocol IP header split", "2001:db8:1::1", "2001:db8:2::1", 47, 0, 40, 0, 24},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame := classifyFrame(tc.source, tc.destination, 0, 0, tc.protocol, tc.subtype, nil)
			frame = frame[:ethHeaderLen+tc.networkLength+tc.transportLength]
			if tc.networkLength == 20 {
				binary.BigEndian.PutUint16(frame[16:18], uint16(tc.networkLength+tc.transportLength))
			} else {
				binary.BigEndian.PutUint16(frame[18:20], uint16(tc.transportLength))
			}
			input := [][][]byte{{frame}}
			if tc.splitOffset != 0 {
				input = append(input, [][]byte{frame[:tc.splitOffset], frame[tc.splitOffset:]})
			}
			incomingBefore, incomingBytesBefore := counterOf(t, harness, "svc", "incoming")
			rejectedBefore, rejectedBytesBefore := counterOf(t, harness, "svc", "filter_rejected")
			result, err := harness.HandleSegmentedPackets(input...)
			require.NoError(t, err)
			require.Empty(t, result.Drop)
			require.Len(t, result.Output, len(input))
			for idx, output := range result.Output {
				// Only the readable head is observable for chained passthrough.
				require.Equal(t, input[idx][0], output)
			}
			incomingAfter, incomingBytesAfter := counterOf(t, harness, "svc", "incoming")
			rejectedAfter, rejectedBytesAfter := counterOf(t, harness, "svc", "filter_rejected")
			require.Equal(t, incomingBefore, incomingAfter)
			require.Equal(t, incomingBytesBefore, incomingBytesAfter)
			require.Equal(t, rejectedBefore, rejectedAfter)
			require.Equal(t, rejectedBytesBefore, rejectedBytesAfter)
		})
	}
	for _, tc := range []struct {
		name, source, destination string
		protocol, subtype         byte
	}{
		{"IPv4 TCP tunnels to real", "10.0.0.1", "192.168.1.1", 6, 2},
		{"IPv6 TCP tunnels to real", "2001:db8:1::1", "2001:db8:2::1", 6, 2},
		{"IPv4 UDP tunnels to real", "10.0.0.1", "192.168.1.1", 17, 0},
		{"IPv6 UDP tunnels to real", "2001:db8:1::1", "2001:db8:2::1", 17, 0},
		{"IPv4 Echo replies locally", "10.0.0.1", "192.168.1.1", 1, 8},
		{"IPv6 Echo replies locally", "2001:db8:1::1", "2001:db8:2::1", 58, 128},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame := classifyFrame(tc.source, tc.destination, 12345, 80, tc.protocol, tc.subtype, nil)
			// Request lifetime stays below the reply value to pin a restart, not a copy.
			if tc.protocol == 1 {
				frame[22] = 1
			} else if tc.protocol == 58 {
				frame[21] = 1
			}
			incomingBefore, incomingBytesBefore := counterOf(t, harness, "svc", "incoming")
			repliedBefore, repliedBytesBefore := counterOf(t, harness, "svc", "icmp_replied")
			result, err := harness.HandleSegmentedPackets([][]byte{frame})
			require.NoError(t, err)
			require.Empty(t, result.Drop)
			require.Len(t, result.Output, 1)
			info, err := framework.NewPacketParser().ParsePacket(result.Output[0])
			require.NoError(t, err)
			incomingAfter, incomingBytesAfter := counterOf(t, harness, "svc", "incoming")
			require.Equal(t, incomingBefore+1, incomingAfter)
			require.Equal(t, incomingBytesBefore+uint64(len(frame)), incomingBytesAfter)
			if tc.protocol == 6 || tc.protocol == 17 {
				require.True(t, info.IsTunneled)
				require.Equal(t, "172.16.0.10", info.DstIP.String())
				return
			}
			require.False(t, info.IsTunneled)
			require.Equal(t, tc.destination, info.SrcIP.String())
			require.Equal(t, tc.source, info.DstIP.String())
			require.Len(t, result.Output[0], len(frame))
			if tc.protocol == 1 {
				require.EqualValues(t, 0, result.Output[0][34])
				require.EqualValues(t, 65, result.Output[0][22])
			} else {
				require.EqualValues(t, 129, result.Output[0][54])
				require.EqualValues(t, 65, result.Output[0][21])
			}
			repliedAfter, repliedBytesAfter := counterOf(t, harness, "svc", "icmp_replied")
			require.Equal(t, repliedBefore+1, repliedAfter)
			require.Equal(t, repliedBytesBefore+uint64(len(result.Output[0])), repliedBytesAfter)
		})
	}
}

// Test_L3b_ClassifyMatchesByDestination verifies that reversing source and
// destination networks does not select a service for outbound traffic.
func Test_L3b_ClassifyMatchesByDestination(t *testing.T) {
	harness, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")
	publishClassifyService(t, agent, "svc", "172.16.0.10", []cl3bobject.SourceFilterRule{{
		Net4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		Net6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6},
	}})
	publishClassifyModule(t, agent, "test", []cl3b.DestinationFilterRule{{
		Net4s:          []xnetip.Contiguous[xnetip.Network4]{xnetip.MustParseContiguous4("192.168.1.0/24")},
		Net6s:          []xnetip.BiContiguous{xnetip.MustParseBiContiguous("2001:db8:2::/48")},
		ProtoRanges:    filter.ProtoRanges{filter.NewProtoRange(6, filter.AnySubtype())},
		VirtualService: "svc",
	}})
	for _, tc := range []struct{ source, destination string }{
		{"10.0.0.1", "192.168.1.1"}, {"2001:db8:1::1", "2001:db8:2::1"},
	} {
		inbound := classifyFrame(tc.source, tc.destination, 12345, 80, 6, 2, nil)
		outbound := classifyFrame(tc.destination, tc.source, 12345, 80, 6, 2, nil)
		result, err := harness.HandleSegmentedPackets([][]byte{inbound}, [][]byte{outbound})
		require.NoError(t, err)
		require.Len(t, result.Output, 1)
		require.Equal(t, [][]byte{outbound}, result.Drop)
	}
}

// Test_L3b_RulelessConfigDropsTcp verifies that an empty destination config
// drops ordinary transport without querying an uninitialized classifier.
func Test_L3b_RulelessConfigDropsTcp(t *testing.T) {
	harness, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")
	publishClassifyModule(t, agent, "test", nil)
	frame := classifyFrame("10.0.0.1", "192.168.1.1", 12345, 80, 6, 2, nil)
	result, err := harness.HandleSegmentedPackets([][]byte{frame})
	require.NoError(t, err)
	require.Empty(t, result.Output)
	require.Equal(t, [][]byte{frame}, result.Drop)
}
