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
	plain "github.com/yanet-platform/yanet2/devices/plain/controlplane"
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	forward "github.com/yanet-platform/yanet2/modules/forward/controlplane"
	"github.com/yanet-platform/yanet2/modules/l3b/bindings/go/cl3b"
	l3b "github.com/yanet-platform/yanet2/modules/l3b/controlplane"
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

// newVirtualService creates a session table and a virtual service borrowing
// it, leaving the scheduler ring and the publish to the caller.
//
// Tearndown is wired so cleanup attempts to free the service before the table
// it borrows.
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
func ipv4TestFrame(df, mf bool, offsetUnits uint16, payload []byte) []byte {
	frame := make([]byte, ethHeaderLen+20+len(payload))
	copy(frame[0:6], []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66})
	copy(frame[6:12], []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff})
	binary.BigEndian.PutUint16(frame[12:14], 0x0800)
	frame[14] = 0x45
	binary.BigEndian.PutUint16(frame[16:18], uint16(20+len(payload)))
	fragmentField := offsetUnits
	if mf {
		fragmentField |= 1 << 13
	}
	if df {
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
	mf bool,
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
	if mf {
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

// loadEchoPCAPPairs adapts pinned Ethernet fixtures without rewriting L3 bytes.
//
// VLAN tags are removed; reply MACs follow the request in the local harness.
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
				name      string
				weights   []uint32
				legacy    string
				disabled  bool
				unmatched bool
				wantDrop  bool
				wantReply uint64
				wantInput uint64
			}
			cases := []stateCase{
				{name: "empty configured list drops unchanged", wantDrop: true, wantInput: 1},
				{name: "enabled configured reals reply", weights: []uint32{1}, legacy: enabledBatch, wantReply: 1, wantInput: 1},
				{name: "all configured reals disabled reply", weights: []uint32{1, 1}, disabled: true, wantReply: 1, wantInput: 1},
				{name: "all configured weights zero reply", weights: []uint32{0, 0}, wantReply: 1, wantInput: 1},
				{name: "unmatched destination passes unchanged", weights: []uint32{1, 1}, unmatched: true},
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
					require.GreaterOrEqual(t, len(seedSessions), 2)
					require.ElementsMatch(t, expectedSessions, seedSessions)
					reals = reals[:len(tc.weights)]
					serviceConfig.RealServers = reals
					serviceConfig.SourceFilterRules[0].PortRanges = filter.PortRanges{{From: 0, To: 65535}}
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
								&layers.IPv4{Version: 4, TTL: 64, Protocol: layers.IPProtocolICMPv4, SrcIP: destination, DstIP: net.ParseIP("10.0.0.1")},
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
							replyIP := layers.IPv6{Version: 6, HopLimit: 64, NextHeader: layers.IPProtocolICMPv6, SrcIP: destination, DstIP: net.ParseIP("2001:db8::1")}
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
