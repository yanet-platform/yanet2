package vxlan_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"
	"net"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/google/go-cmp/cmp"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	vxlan "github.com/yanet-platform/yanet2/devices/vxlan/controlplane"
	"github.com/yanet-platform/yanet2/devices/vxlan/controlplane/vxlanpb/v1"
	"github.com/yanet-platform/yanet2/modules/blackhole/bindings/go/cblackhole"
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	forward "github.com/yanet-platform/yanet2/modules/forward/controlplane"
)

const (
	vxlanCPSize  = 64 * datasize.MB
	vxlanDPSize  = 4 * datasize.MB
	vxlanMemSize = 16 * datasize.MB

	tunnelVNI     = uint32(4242)
	tunnelDstPort = uint32(4789)
	tunnelSrcMAC  = "02:00:00:00:00:01"
	tunnelDstMAC  = "02:00:00:00:00:02"
	tunnelSrcIP   = "10.0.0.1"
	tunnelDstIP   = "10.0.0.2"

	// vxlanSrcPortBase and vxlanSrcPortRange bound the outer UDP source
	// port the dataplane derives from the inner packet hash, per RFC 7348.
	vxlanSrcPortBase  = 49152
	vxlanSrcPortRange = 16384
	vxlanSrcPortLimit = 65535

	// vxlanEncapLen is the size of the outer Ethernet + IPv4 + UDP + VXLAN
	// header stack the device prepends on output.
	vxlanEncapLen = 50

	// etherHeaderLen is the outer Ethernet header the IPv4 total_length
	// excludes: the device writes the IP datagram length, not the frame
	// length.
	etherHeaderLen = 14

	// vxlanUdpOverhead is the UDP payload the outer header stack adds on
	// top of the inner frame: the UDP and VXLAN headers themselves.
	vxlanUdpOverhead = 16

	// IPv4 fragment_offset field bits, mirroring DPDK's
	// RTE_IPV4_HDR_MF_FLAG and RTE_IPV4_HDR_OFFSET_MASK.
	ipv4MFFlag     = 0x2000
	ipv4OffsetMask = 0x1FFF
)

// setupVxlanHarness builds a harness that loads the plain and vxlan device
// types and the forward and blackhole modules, plus an agent and a vxlan
// service.
//
// The plain device type is required because the harness resolves the
// physical port to it. The blackhole module terminates the chains whose
// forward stage must match every packet it emits.
func setupVxlanHarness(t *testing.T) (*dataplaneut.Harness, *ffi.Agent, *vxlan.DeviceVxlanService) {
	t.Helper()

	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(vxlanCPSize),
		DPMemory:      uint64(vxlanDPSize),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		Modules:       []string{"forward", "blackhole"},
		DevicesToLoad: []string{"plain", "vxlan"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	agent, err := harness.SharedMemory().AgentAttach("vxlan-test", 0, vxlanMemSize)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	return harness, agent, vxlan.NewDeviceVxlanService(agent)
}

// createTunnel installs the "tun0" device through the control-plane service.
//
// Decapsulated frames arriving from the tunnel are routed into the "tun-sink"
// pipeline, and encapsulated frames leaving it pass through the empty
// "tun-dummy" pipeline into the round output.
func createTunnel(t *testing.T, service *vxlan.DeviceVxlanService) {
	t.Helper()

	createTunnelWithOutput(t, service, "tun-dummy")
}

// createTunnelWithOutput installs the "tun0" device with the given pipeline on
// its output side, so a test can inspect encapsulated packets through a
// non-trivial stage before they leave the device.
func createTunnelWithOutput(
	t *testing.T,
	service *vxlan.DeviceVxlanService,
	outputPipeline string,
) {
	t.Helper()

	createTunnelWithPipelines(t, service, "tun-sink", outputPipeline)
}

// createTunnelWithPipelines installs the "tun0" device with the given input
// and output pipelines.
func createTunnelWithPipelines(
	t *testing.T,
	service *vxlan.DeviceVxlanService,
	inputPipeline string,
	outputPipeline string,
) {
	t.Helper()

	request := &vxlanpb.UpdateDeviceVxlanRequest{
		Name: "tun0",
		Device: &commonpb.Device{
			Input:  []*commonpb.DevicePipeline{{Name: inputPipeline, Weight: 1}},
			Output: []*commonpb.DevicePipeline{{Name: outputPipeline, Weight: 1}},
		},
		Vni:     tunnelVNI,
		DstPort: tunnelDstPort,
		SrcMac:  tunnelSrcMAC,
		DstMac:  tunnelDstMAC,
		SrcIp:   tunnelSrcIP,
		DstIp:   tunnelDstIP,
	}

	_, err := service.UpdateDevice(t.Context(), request)
	require.NoError(t, err)
}

// wireVxlanPipelines installs the forward modules, functions and pipelines
// the tunnel tests route through.
//
// Forward target names are recorded by name and resolved when the devices
// are registered later, so the modules may be created before "tun0" and
// "port0" exist.
func wireVxlanPipelines(t *testing.T, agent *ffi.Agent) {
	t.Helper()

	sinkHandle, err := forward.NewBackend(agent).UpdateModule("sink", []cforward.ForwardRule{
		{
			Target:  "port0",
			Mode:    cforward.ModeOut,
			Counter: "sink4",
			Src4s:   filter.IPNets{filter.UnspecifiedIPv4},
			Dst4s:   filter.IPNets{filter.UnspecifiedIPv4},
		},
		{
			Target:  "port0",
			Mode:    cforward.ModeOut,
			Counter: "sink6",
			Src6s:   filter.IPNets{filter.UnspecifiedIPv6},
			Dst6s:   filter.IPNets{filter.UnspecifiedIPv6},
		},
	})
	require.NoError(t, err)
	t.Cleanup(sinkHandle.Free)

	toTunInHandle, err := forward.NewBackend(agent).UpdateModule("to-tun-in", []cforward.ForwardRule{
		{
			Target:  "tun0",
			Mode:    cforward.ModeIn,
			Counter: "to_tun_in4",
			Src4s:   filter.IPNets{filter.UnspecifiedIPv4},
			Dst4s:   filter.IPNets{filter.UnspecifiedIPv4},
		},
		{
			Target:  "tun0",
			Mode:    cforward.ModeIn,
			Counter: "to_tun_in6",
			Src6s:   filter.IPNets{filter.UnspecifiedIPv6},
			Dst6s:   filter.IPNets{filter.UnspecifiedIPv6},
		},
	})
	require.NoError(t, err)
	t.Cleanup(toTunInHandle.Free)

	toTunOutHandle, err := forward.NewBackend(agent).UpdateModule("to-tun-out", []cforward.ForwardRule{
		{
			Target:  "tun0",
			Mode:    cforward.ModeOut,
			Counter: "to_tun_out4",
			Src4s:   filter.IPNets{filter.UnspecifiedIPv4},
			Dst4s:   filter.IPNets{filter.UnspecifiedIPv4},
		},
		{
			Target:  "tun0",
			Mode:    cforward.ModeOut,
			Counter: "to_tun_out6",
			Src6s:   filter.IPNets{filter.UnspecifiedIPv6},
			Dst6s:   filter.IPNets{filter.UnspecifiedIPv6},
		},
	})
	require.NoError(t, err)
	t.Cleanup(toTunOutHandle.Free)

	chains := []struct {
		function string
		pipeline string
		module   string
	}{
		{function: "tun-sink-func", pipeline: "tun-sink", module: "sink"},
		{function: "vxlan-in-func", pipeline: "vxlan-in", module: "to-tun-in"},
		{function: "vxlan-out-func", pipeline: "vxlan-out", module: "to-tun-out"},
	}
	for _, chain := range chains {
		require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
			Name: chain.function,
			Chains: []ffi.FunctionChainConfig{{
				Weight: 1,
				Chain: ffi.ChainConfig{
					Name:    chain.function + "_chain",
					Modules: []ffi.ChainModuleConfig{{Type: "forward", Name: chain.module}},
				},
			}},
		}))
		require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
			Name:      chain.pipeline,
			Functions: []string{chain.function},
		}))
	}

	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "tun-dummy"}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "port-dummy"}))
}

// wirePortInput binds port0's input to the given pipeline.
func wirePortInput(t *testing.T, agent *ffi.Agent, pipeline string) {
	t.Helper()

	require.NoError(t, agent.UpdatePlainDevices([]ffi.DeviceConfig{{
		Name:   "port0",
		Input:  []ffi.DevicePipelineConfig{{Name: pipeline, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "port-dummy", Weight: 1}},
	}}))
}

// wireUnderlayPipeline installs the "tun-underlay" pipeline for the tunnel's
// output side: a forward stage that matches only the outer underlay source
// address, followed by a blackhole stage.
//
// A matched packet is routed out of port0 and emitted; an unmatched one falls
// through the forward stage and is dropped by the blackhole, so the pipeline
// turns "the metadata described the outer headers" into an observable emit.
func wireUnderlayPipeline(t *testing.T, agent *ffi.Agent) {
	t.Helper()

	underlayHandle, err := forward.NewBackend(agent).UpdateModule("underlay-check", []cforward.ForwardRule{
		{
			Target:  "port0",
			Mode:    cforward.ModeOut,
			Counter: "underlay4",
			Src4s:   filter.IPNets{filter.MustParseIPNet(tunnelSrcIP + "/32")},
			Dst4s:   filter.IPNets{filter.UnspecifiedIPv4},
		},
		{
			// Never matches: it only keeps the ip6 filter non-empty,
			// since the outer header bytes read as an IPv6 source can
			// never start with 2001:db8:7654:3210.
			Target:  "port0",
			Mode:    cforward.ModeOut,
			Counter: "underlay6",
			Src6s:   filter.IPNets{filter.MustParseIPNet("2001:db8:7654:3210::/64")},
			Dst6s:   filter.IPNets{filter.UnspecifiedIPv6},
		},
	})
	require.NoError(t, err)
	t.Cleanup(underlayHandle.Free)

	blackholeConfig, err := cblackhole.NewModuleConfig(agent, "underlay-sink")
	require.NoError(t, err)
	t.Cleanup(blackholeConfig.Free)
	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{blackholeConfig.AsFFIModule()}))

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: "tun-underlay-func",
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: "tun-underlay-func_chain",
				Modules: []ffi.ChainModuleConfig{
					{Type: "forward", Name: "underlay-check"},
					{Type: "blackhole", Name: "underlay-sink"},
				},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      "tun-underlay",
		Functions: []string{"tun-underlay-func"},
	}))
}

// wireUntaggedPipeline installs the "tun-untagged" pipeline for the tunnel's
// output side: a forward stage whose only rule matches untagged packet
// metadata (vlan id 0), followed by a blackhole stage.
//
// The vlan filter reads the packet metadata the encap path leaves behind, not
// the outer frame bytes, so a matched packet is routed out of port0 and
// emitted while an unmatched one falls through and is dropped by the
// blackhole.
func wireUntaggedPipeline(t *testing.T, agent *ffi.Agent) {
	t.Helper()

	untaggedHandle, err := forward.NewBackend(agent).UpdateModule("untagged-check", []cforward.ForwardRule{
		{
			Target:     "port0",
			Mode:       cforward.ModeOut,
			Counter:    "untagged_hit",
			VlanRanges: filter.VlanRanges{{From: 0, To: 0}},
		},
	})
	require.NoError(t, err)
	t.Cleanup(untaggedHandle.Free)

	blackholeConfig, err := cblackhole.NewModuleConfig(agent, "untagged-sink")
	require.NoError(t, err)
	t.Cleanup(blackholeConfig.Free)
	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{blackholeConfig.AsFFIModule()}))

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: "tun-untagged-func",
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: "tun-untagged-func_chain",
				Modules: []ffi.ChainModuleConfig{
					{Type: "forward", Name: "untagged-check"},
					{Type: "blackhole", Name: "untagged-sink"},
				},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      "tun-untagged",
		Functions: []string{"tun-untagged-func"},
	}))
}

// wireL2SinkPipeline installs the "tun-l2" pipeline for the tunnel's input
// side: a forward stage whose single rule carries no match criteria, so it
// qualifies for the L2 filter and routes every packet — including non-IP
// inners the ip4 and ip6 filters never see — out of port0.
//
// The round drops a device input handler's output unless a module routes it
// into a device entry, so the decapsulated non-IP inner needs this catch-all
// to reach the emitted output.
func wireL2SinkPipeline(t *testing.T, agent *ffi.Agent) {
	t.Helper()

	l2SinkHandle, err := forward.NewBackend(agent).UpdateModule("l2-sink", []cforward.ForwardRule{
		{
			Target:  "port0",
			Mode:    cforward.ModeOut,
			Counter: "l2_sink",
		},
	})
	require.NoError(t, err)
	t.Cleanup(l2SinkHandle.Free)

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: "tun-l2-func",
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name:    "tun-l2-func_chain",
				Modules: []ffi.ChainModuleConfig{{Type: "forward", Name: "l2-sink"}},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      "tun-l2",
		Functions: []string{"tun-l2-func"},
	}))
}

// innerFrame builds the Ethernet frame the tunnel carries inside.
func innerFrame(t *testing.T) gopacket.Packet {
	t.Helper()

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:  4,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    net.ParseIP("192.168.1.1"),
		DstIP:    net.ParseIP("192.168.1.2"),
	}
	icmp4 := layers.ICMPv4{TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0)}
	payload := gopacket.Payload([]byte("inner echo payload for the vxlan tunnel"))

	return xpacket.LayersToPacket(t, &eth, &ip4, &icmp4, payload)
}

// arpInnerFrame builds the Ethernet+ARP request frame a tunnel carries inside
// as its non-IP inner: a complete, parseable frame whose parse stops at the
// network layer.
func arpInnerFrame(t *testing.T) gopacket.Packet {
	t.Helper()

	srcMAC := xerror.Unwrap(net.ParseMAC("00:00:00:00:00:01"))
	eth := layers.Ethernet{
		SrcMAC:       srcMAC,
		DstMAC:       xerror.Unwrap(net.ParseMAC("ff:ff:ff:ff:ff:ff")),
		EthernetType: layers.EthernetTypeARP,
	}
	arp := layers.ARP{
		AddrType:          layers.LinkTypeEthernet,
		Protocol:          layers.EthernetTypeIPv4,
		HwAddressSize:     6,
		ProtAddressSize:   4,
		Operation:         layers.ARPRequest,
		SourceHwAddress:   srcMAC,
		SourceProtAddress: net.ParseIP("192.168.1.1").To4(),
		DstHwAddress:      net.HardwareAddr{0, 0, 0, 0, 0, 0},
		DstProtAddress:    net.ParseIP("192.168.1.2").To4(),
	}

	return xpacket.LayersToPacket(t, &eth, &arp)
}

// outerLayers builds the default outer tunnel headers: IPv4, UDP to the
// configured destination port, and a valid VXLAN header with the configured
// VNI. The returned layers may be modified before use in vxlanFrame.
func outerLayers() (*layers.IPv4, *layers.UDP, *layers.VXLAN) {
	ip4 := &layers.IPv4{
		Version:  4,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    net.ParseIP("203.0.113.1"),
		DstIP:    net.ParseIP("198.51.100.1"),
	}
	udp := &layers.UDP{SrcPort: 1111, DstPort: layers.UDPPort(tunnelDstPort)}
	udp.SetNetworkLayerForChecksum(ip4)
	vxlanHeader := &layers.VXLAN{ValidIDFlag: true, VNI: tunnelVNI}

	return ip4, udp, vxlanHeader
}

// vxlanFrame wraps the inner frame's bytes in the given outer headers.
func vxlanFrame(
	t *testing.T,
	ip4 *layers.IPv4,
	udp *layers.UDP,
	vxlanHeader *layers.VXLAN,
	inner gopacket.Packet,
) gopacket.Packet {
	t.Helper()

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:01")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:02")),
		EthernetType: layers.EthernetTypeIPv4,
	}

	return xpacket.LayersToPacket(t, &eth, ip4, udp, vxlanHeader, gopacket.Payload(inner.Data()))
}

// requireSingleOutputEquals asserts the result carries exactly one output
// packet equal to expected and no drops.
func requireSingleOutputEquals(t *testing.T, result *dataplaneut.Result, expected gopacket.Packet) {
	t.Helper()

	require.Len(t, result.Output, 1)
	require.Empty(t, result.Drop)
	actual := xpacket.ParseEtherPacket(result.Output[0].RawData)
	diff := cmp.Diff(expected.Layers(), actual.Layers())
	require.Empty(t, diff)
}

// TestVxlan_Decap verifies that a matching VXLAN frame fed to the tunnel
// input yields the decapsulated inner frame.
func TestVxlan_Decap(t *testing.T) {
	harness, agent, service := setupVxlanHarness(t)
	wireVxlanPipelines(t, agent)
	createTunnel(t, service)
	wirePortInput(t, agent, "vxlan-in")

	expected := innerFrame(t)
	ip4, udp, vxlanHeader := outerLayers()
	frame := vxlanFrame(t, ip4, udp, vxlanHeader, expected)

	result, err := harness.HandlePackets(frame)
	require.NoError(t, err)
	requireSingleOutputEquals(t, result, expected)
}

// TestVxlan_Encap verifies that a plain frame routed to the tunnel output
// comes out encapsulated with the configured outer headers.
func TestVxlan_Encap(t *testing.T) {
	harness, agent, service := setupVxlanHarness(t)
	wireVxlanPipelines(t, agent)
	createTunnel(t, service)
	wirePortInput(t, agent, "vxlan-out")

	inner := innerFrame(t)

	result, err := harness.HandlePackets(inner)
	require.NoError(t, err)
	require.Len(t, result.Output, 1)
	require.Empty(t, result.Drop)

	outer := xpacket.ParseEtherPacket(result.Output[0].RawData)

	ethLayer := outer.Layer(layers.LayerTypeEthernet).(*layers.Ethernet)
	require.Equal(t, tunnelDstMAC, ethLayer.DstMAC.String())
	require.Equal(t, tunnelSrcMAC, ethLayer.SrcMAC.String())
	require.Equal(t, layers.EthernetTypeIPv4, ethLayer.EthernetType)

	ip4Layer := outer.Layer(layers.LayerTypeIPv4).(*layers.IPv4)
	require.Equal(t, tunnelSrcIP, ip4Layer.SrcIP.String())
	require.Equal(t, tunnelDstIP, ip4Layer.DstIP.String())
	require.Equal(t, layers.IPProtocolUDP, ip4Layer.Protocol)
	require.Equal(t, layers.IPv4DontFragment, ip4Layer.Flags)
	require.Equal(t, uint8(64), ip4Layer.TTL)
	require.Equal(
		t,
		uint16(len(result.Output[0].RawData)-etherHeaderLen),
		ip4Layer.Length,
	)

	udpLayer := outer.Layer(layers.LayerTypeUDP).(*layers.UDP)
	require.Equal(t, layers.UDPPort(tunnelDstPort), udpLayer.DstPort)
	require.GreaterOrEqual(t, udpLayer.SrcPort, layers.UDPPort(vxlanSrcPortBase))
	require.LessOrEqual(t, udpLayer.SrcPort, layers.UDPPort(vxlanSrcPortLimit))
	require.Equal(t, uint16(vxlanUdpOverhead+len(inner.Data())), udpLayer.Length)
	require.Zero(t, udpLayer.Checksum)

	vxlanLayer := outer.Layer(layers.LayerTypeVXLAN).(*layers.VXLAN)
	require.True(t, vxlanLayer.ValidIDFlag)
	require.Equal(t, tunnelVNI, vxlanLayer.VNI)

	require.Equal(
		t,
		inner.Data(),
		result.Output[0].RawData[vxlanEncapLen:vxlanEncapLen+len(inner.Data())],
	)
}

// outerEther builds the outer Ethernet header of a tunnel frame.
func outerEther() *layers.Ethernet {
	return &layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:01")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:02")),
		EthernetType: layers.EthernetTypeIPv4,
	}
}

// rawVxlanFrame assembles a complete matching tunnel frame around inner
// with an outer IPv4 header of ipHdrLen bytes (options left zero) and the
// given fragment_offset field value.
//
// The bytes mirror the C behavioural test's frame builder: outer
// Ethernet, IPv4, UDP to the configured port, and a valid VXLAN header
// with the configured VNI, all at IHL-derived offsets. The header
// checksum stays zero because no stage on the decap path verifies it.
func rawVxlanFrame(ipHdrLen int, fragmentOffset uint16, inner []byte) []byte {
	frame := make([]byte, etherHeaderLen+ipHdrLen+16+len(inner))
	putUint16 := func(offset int, value uint16) {
		binary.BigEndian.PutUint16(frame[offset:], value)
	}

	copy(frame[0:6], outerEther().DstMAC)
	copy(frame[6:12], outerEther().SrcMAC)
	putUint16(12, uint16(layers.EthernetTypeIPv4))

	frame[etherHeaderLen] = byte(0x40 | ipHdrLen/4)
	putUint16(etherHeaderLen+2, uint16(ipHdrLen+16+len(inner)))
	putUint16(etherHeaderLen+6, fragmentOffset)
	frame[etherHeaderLen+8] = 64
	frame[etherHeaderLen+9] = byte(layers.IPProtocolUDP)
	copy(frame[etherHeaderLen+12:], net.ParseIP("203.0.113.1").To4())
	copy(frame[etherHeaderLen+16:], net.ParseIP("198.51.100.1").To4())

	udpOffset := etherHeaderLen + ipHdrLen
	putUint16(udpOffset, 1111)
	putUint16(udpOffset+2, uint16(tunnelDstPort))
	putUint16(udpOffset+4, uint16(16+len(inner)))

	vxlanOffset := udpOffset + 8
	frame[vxlanOffset] = 0x08
	putUint16(vxlanOffset+4, uint16(tunnelVNI>>8))
	frame[vxlanOffset+6] = byte(tunnelVNI & 0xFF)

	copy(frame[vxlanOffset+8:], inner)
	return frame
}

// patchBE16 overwrites a big-endian 16-bit field of a raw frame in place.
func patchBE16(frame []byte, offset int, value uint16) {
	binary.BigEndian.PutUint16(frame[offset:], value)
}

// lazyPacket wraps raw frame bytes so the malformed-frame table can feed
// them through its gopacket-based runner without decoding them.
func lazyPacket(frame []byte) gopacket.Packet {
	return gopacket.NewPacket(frame, layers.LayerTypeEthernet, gopacket.Lazy)
}

// TestVxlan_InputDropsMalformedFrames verifies that frames failing any outer
// validation are dropped instead of passed through: the device terminates
// the tunnel, so a non-matching frame has nowhere else to go.
func TestVxlan_InputDropsMalformedFrames(t *testing.T) {
	harness, agent, service := setupVxlanHarness(t)
	wireVxlanPipelines(t, agent)
	createTunnel(t, service)
	wirePortInput(t, agent, "vxlan-in")

	inner := innerFrame(t)

	cases := []struct {
		name  string
		frame func(t *testing.T) gopacket.Packet
	}{
		{
			name: "bad_flags",
			frame: func(t *testing.T) gopacket.Packet {
				ip4, udp, vxlanHeader := outerLayers()
				vxlanHeader.ValidIDFlag = false
				return vxlanFrame(t, ip4, udp, vxlanHeader, inner)
			},
		},
		{
			name: "wrong_vni",
			frame: func(t *testing.T) gopacket.Packet {
				ip4, udp, vxlanHeader := outerLayers()
				vxlanHeader.VNI = tunnelVNI + 1
				return vxlanFrame(t, ip4, udp, vxlanHeader, inner)
			},
		},
		{
			name: "wrong_dst_port",
			frame: func(t *testing.T) gopacket.Packet {
				ip4, udp, vxlanHeader := outerLayers()
				udp.DstPort = layers.UDPPort(tunnelDstPort + 1)
				return vxlanFrame(t, ip4, udp, vxlanHeader, inner)
			},
		},
		{
			// A frame whose UDP payload stops inside the VXLAN
			// header. Lengths stay consistent so the frame parses
			// and reaches the device handler; gopacket refuses to
			// decode the short VXLAN header, so the frame is built
			// without re-parsing.
			name: "truncated_vxlan_header",
			frame: func(t *testing.T) gopacket.Packet {
				ip4, udp, _ := outerLayers()
				buf := gopacket.NewSerializeBuffer()
				opts := gopacket.SerializeOptions{
					FixLengths:       true,
					ComputeChecksums: true,
				}
				require.NoError(t, gopacket.SerializeLayers(
					buf,
					opts,
					outerEther(),
					ip4,
					udp,
					gopacket.Payload([]byte{0x08, 0x00, 0x00, 0x00}),
				))

				return gopacket.NewPacket(
					buf.Bytes(),
					layers.LayerTypeEthernet,
					gopacket.Lazy,
				)
			},
		},
		{
			// An MF-set first fragment of an otherwise matching frame:
			// the tunnel packet continues in later fragments, so
			// decapsulating it would emit a truncated inner frame.
			name: "fragmented_outer_mf",
			frame: func(t *testing.T) gopacket.Packet {
				return lazyPacket(rawVxlanFrame(20, ipv4MFFlag, inner.Data()))
			},
		},
		{
			// A non-first fragment whose payload sits where the UDP and
			// VXLAN headers would be, genuinely matching the configured
			// port, flags and VNI — an attacker-craftable look-alike.
			name: "fragmented_outer_offset",
			frame: func(t *testing.T) gopacket.Packet {
				return lazyPacket(rawVxlanFrame(20, ipv4OffsetMask, inner.Data()))
			},
		},
		{
			name: "ipv6_outer_ethertype",
			frame: func(t *testing.T) gopacket.Packet {
				eth := outerEther()
				eth.EthernetType = layers.EthernetTypeIPv6
				ip6 := layers.IPv6{
					Version:    6,
					NextHeader: layers.IPProtocolUDP,
					HopLimit:   64,
					SrcIP:      net.ParseIP("2001:db8:1::1"),
					DstIP:      net.ParseIP("2001:db8:2::1"),
				}
				udp := layers.UDP{SrcPort: 1111, DstPort: layers.UDPPort(tunnelDstPort)}
				udp.SetNetworkLayerForChecksum(&ip6)
				vxlanHeader := &layers.VXLAN{ValidIDFlag: true, VNI: tunnelVNI}

				return xpacket.LayersToPacket(
					t,
					eth,
					&ip6,
					&udp,
					vxlanHeader,
					gopacket.Payload(inner.Data()),
				)
			},
		},
		{
			name: "non_udp_ipv4_protocol",
			frame: func(t *testing.T) gopacket.Packet {
				ip4, _, _ := outerLayers()
				ip4.Protocol = layers.IPProtocolICMPv4
				icmp4 := layers.ICMPv4{TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0)}

				return xpacket.LayersToPacket(t, outerEther(), ip4, &icmp4)
			},
		},
		{
			// The declared IPv4 length stops at the end of the UDP
			// header, while the frame continues with a VXLAN-looking
			// header and the inner frame as trailing padding: the UDP
			// datagram would have to reach past the IPv4 end.
			name: "total_length_ends_at_udp_header",
			frame: func(t *testing.T) gopacket.Packet {
				frame := rawVxlanFrame(20, 0, inner.Data())
				patchBE16(frame, etherHeaderLen+2, 20+8)
				patchBE16(frame, etherHeaderLen+20+4, 16)
				return lazyPacket(frame)
			},
		},
		{
			// The UDP length claims a bare UDP header, with the VXLAN
			// header and inner frame as trailing bytes: the datagram
			// cannot cover the VXLAN header.
			name: "dgram_len_udp_header_only",
			frame: func(t *testing.T) gopacket.Packet {
				frame := rawVxlanFrame(20, 0, inner.Data())
				patchBE16(frame, etherHeaderLen+20+4, 8)
				return lazyPacket(frame)
			},
		},
		{
			// The UDP length runs past the declared IPv4 end although
			// both stay inside the frame: the datagram envelope must
			// stay within the IPv4 datagram.
			//
			// A total_length inflated past the frame itself cannot
			// be expressed here: the ingress parse enforces the same
			// bound before the device sees the packet, so the harness
			// refuses to build such a frame.
			name: "dgram_len_overruns_ip_end",
			frame: func(t *testing.T) gopacket.Packet {
				frame := rawVxlanFrame(20, 0, inner.Data())
				patchBE16(frame, etherHeaderLen+20+4, uint16(16+len(inner.Data())+100))
				return lazyPacket(frame)
			},
		},
	}

	frames := make([]gopacket.Packet, 0, len(cases))
	for _, tc := range cases {
		frames = append(frames, tc.frame(t))
	}

	segments := make([][][]byte, 0, len(frames))
	for _, frame := range frames {
		segments = append(segments, [][]byte{frame.Data()})
	}

	result, err := harness.HandleSegmentedPackets(segments...)
	require.NoError(t, err)
	require.Empty(t, result.Output)
	require.Len(t, result.Drop, len(cases))

	for idx, tc := range cases {
		require.Equalf(
			t,
			frames[idx].Data(),
			result.Drop[idx],
			"dropped frame %q must come out unchanged",
			tc.name,
		)
	}
}

// TestVxlan_DecapOuterIpOptions verifies decapsulation honours the outer
// IPv4 IHL: with options present the UDP header sits further in, and the
// handler must find it at the IHL-derived offset rather than at a fixed
// 20-byte one.
func TestVxlan_DecapOuterIpOptions(t *testing.T) {
	harness, agent, service := setupVxlanHarness(t)
	wireVxlanPipelines(t, agent)
	createTunnel(t, service)
	wirePortInput(t, agent, "vxlan-in")

	inner := innerFrame(t)

	for _, ipHdrLen := range []int{20, 24} {
		t.Run(fmt.Sprintf("ihl_%d", ipHdrLen/4), func(t *testing.T) {
			frame := rawVxlanFrame(ipHdrLen, 0, inner.Data())

			result, err := harness.HandleSegmentedPackets([][]byte{frame})
			require.NoError(t, err)
			require.Empty(t, result.Drop)
			require.Len(t, result.Output, 1)
			require.Equal(t, inner.Data(), result.Output[0])
		})
	}
}

// TestVxlan_DecapTrimsTrailingPadding verifies that bytes beyond the declared
// IPv4 length are stripped on decapsulation: the emitted inner frame ends at
// the UDP payload end, without the trailing padding the frame carried.
func TestVxlan_DecapTrimsTrailingPadding(t *testing.T) {
	harness, agent, service := setupVxlanHarness(t)
	wireVxlanPipelines(t, agent)
	createTunnel(t, service)
	wirePortInput(t, agent, "vxlan-in")

	inner := innerFrame(t)
	frame := append(rawVxlanFrame(20, 0, inner.Data()), bytes.Repeat([]byte{0xAB}, 20)...)

	result, err := harness.HandleSegmentedPackets([][]byte{frame})
	require.NoError(t, err)
	require.Empty(t, result.Drop)
	require.Len(t, result.Output, 1)
	require.Equal(t, inner.Data(), result.Output[0])
}

// TestVxlan_DecapDropsInnerBeyondUdpDatagram verifies that a frame whose UDP
// datagram declares only the tunnel headers is dropped even when the IPv4
// envelope covers a complete inner-looking frame beyond the UDP end.
//
// The trim to the declared UDP end empties the frame once the outer headers
// are stripped, so the re-parse fails and the undeclared payload is never
// injected. The input pipeline routes every decapsulated frame out through an
// L2 catch-all, so an improperly injected inner would surface as an output.
func TestVxlan_DecapDropsInnerBeyondUdpDatagram(t *testing.T) {
	harness, agent, service := setupVxlanHarness(t)
	wireVxlanPipelines(t, agent)
	wireL2SinkPipeline(t, agent)
	createTunnelWithPipelines(t, service, "tun-l2", "tun-dummy")
	wirePortInput(t, agent, "vxlan-in")

	// The IPv4 envelope (rawVxlanFrame's total_length) covers the whole
	// frame including the complete Ethernet+ARP payload, while the UDP
	// datagram ends right after the VXLAN header.
	frame := rawVxlanFrame(20, 0, arpInnerFrame(t).Data())
	patchBE16(frame, etherHeaderLen+20+4, uint16(vxlanUdpOverhead))

	result, err := harness.HandleSegmentedPackets([][]byte{frame})
	require.NoError(t, err)
	require.Empty(t, result.Output)
	require.Len(t, result.Drop, 1)
}

// TestVxlan_DecapTrimsToUdpDatagramEnd verifies the decap trim target is the
// UDP datagram end, not the IPv4 end: padding declared inside the IPv4
// envelope but past the UDP datagram is stripped, and the emitted inner frame
// ends exactly at the UDP payload boundary.
func TestVxlan_DecapTrimsToUdpDatagramEnd(t *testing.T) {
	harness, agent, service := setupVxlanHarness(t)
	wireVxlanPipelines(t, agent)
	createTunnel(t, service)
	wirePortInput(t, agent, "vxlan-in")

	inner := innerFrame(t)
	const paddingLen = 16
	frame := append(rawVxlanFrame(20, 0, inner.Data()), bytes.Repeat([]byte{0xCD}, paddingLen)...)

	// Widen the IPv4 envelope over the padding while the UDP datagram
	// still ends at the inner frame's last byte.
	patchBE16(frame, etherHeaderLen+2, uint16(20+vxlanUdpOverhead+len(inner.Data())+paddingLen))

	result, err := harness.HandleSegmentedPackets([][]byte{frame})
	require.NoError(t, err)
	require.Empty(t, result.Drop)
	require.Len(t, result.Output, 1)
	require.Equal(t, inner.Data(), result.Output[0])
}

// TestVxlan_DecapNonIpInner verifies that a tunnel frame carrying a non-IP
// inner (ARP) decapsulates and re-parses cleanly: the inner frame reaches the
// output intact.
//
// The transport-metadata reset that accompanies this path has no Go-level
// gate: forward rules carry no proto or port match in their C ABI, and the
// acl module applies its proto and port filters only to packets it buckets as
// IPv4 or IPv6, which an ARP-typed inner never is. No loadable stage reads
// the transport metadata of a non-IP packet, so the reset itself cannot be
// asserted from Go.
func TestVxlan_DecapNonIpInner(t *testing.T) {
	harness, agent, service := setupVxlanHarness(t)
	wireVxlanPipelines(t, agent)
	wireL2SinkPipeline(t, agent)
	createTunnelWithPipelines(t, service, "tun-l2", "tun-dummy")
	wirePortInput(t, agent, "vxlan-in")

	expected := arpInnerFrame(t)
	ip4, udp, vxlanHeader := outerLayers()
	frame := vxlanFrame(t, ip4, udp, vxlanHeader, expected)

	result, err := harness.HandlePackets(frame)
	require.NoError(t, err)
	requireSingleOutputEquals(t, result, expected)
}

// TestVxlan_DecapMultiSegment verifies decapsulation of a frame whose inner
// frame starts in the head segment and continues into the second one.
//
// The result collector reports only the head segment's bytes, so the assertion
// covers the inner frame's prefix the head carries after the outer headers are
// stripped.
func TestVxlan_DecapMultiSegment(t *testing.T) {
	harness, agent, service := setupVxlanHarness(t)
	wireVxlanPipelines(t, agent)
	createTunnel(t, service)
	wirePortInput(t, agent, "vxlan-in")

	inner := innerFrame(t)
	frame := rawVxlanFrame(20, 0, inner.Data())

	// 40 inner bytes stay in the head: enough for the re-parse to read the
	// inner Ethernet and IPv4 headers resident there.
	headResident := 40
	head := frame[:vxlanEncapLen+headResident]
	tail := frame[vxlanEncapLen+headResident:]

	result, err := harness.HandleSegmentedPackets([][]byte{head, tail})
	require.NoError(t, err)
	require.Empty(t, result.Drop)
	require.Len(t, result.Output, 1)
	require.Equal(t, inner.Data()[:headResident], result.Output[0])
}

// TestVxlan_DecapDropsNonResidentInnerHeader verifies that a chained frame
// whose head segment ends exactly at the VXLAN header is dropped.
//
// The re-parse after the strip reads through the head segment while bounding
// its reads by the whole-packet length, so an inner Ethernet header that starts
// in the second segment would be read outside the head segment's data.
func TestVxlan_DecapDropsNonResidentInnerHeader(t *testing.T) {
	harness, agent, service := setupVxlanHarness(t)
	wireVxlanPipelines(t, agent)
	createTunnel(t, service)
	wirePortInput(t, agent, "vxlan-in")

	inner := innerFrame(t)
	frame := rawVxlanFrame(20, 0, inner.Data())

	head := frame[:vxlanEncapLen]
	tail := frame[vxlanEncapLen:]

	result, err := harness.HandleSegmentedPackets([][]byte{head, tail})
	require.NoError(t, err)
	require.Empty(t, result.Output)
	require.Len(t, result.Drop, 1)
	require.Equal(t, head, result.Drop[0])
}

// crc32Castagnoli backs the CRC-32C register chain replicating the
// dataplane's packet hash (common/crc32.h).
var crc32Castagnoli = crc32.MakeTable(crc32.Castagnoli)

// crc32cChain continues the hardware CRC-32C register hash over data.
// Go's Update complements its input and output (Update(c, b) equals
// ^chain(^c, b)), so the raw register chain needs both complements.
func crc32cChain(seed uint32, data []byte) uint32 {
	return ^crc32.Update(^seed, crc32Castagnoli, data)
}

// udpPortBytes serialises a UDP port as the two wire bytes the hash
// chains over.
func udpPortBytes(port layers.UDPPort) []byte {
	return binary.BigEndian.AppendUint16(nil, uint16(port))
}

// innerPacketHash recomputes the ingress parse hash of an Ethernet/IPv4/UDP
// frame: CRC-32C over the source and destination addresses and both UDP
// ports, chained field by field.
func innerPacketHash(ip4 *layers.IPv4, udp *layers.UDP) uint32 {
	hash := crc32cChain(0, ip4.SrcIP.To4())
	hash = crc32cChain(hash, ip4.DstIP.To4())
	hash = crc32cChain(hash, udpPortBytes(udp.SrcPort))
	hash = crc32cChain(hash, udpPortBytes(udp.DstPort))
	return hash
}

// fixedInnerFrame builds the inner frame the C behavioural test pins the
// deterministic source port with: fixed addresses, ports and payload, so
// the derived outer source port stays reproducible across both suites.
func fixedInnerFrame(t *testing.T) gopacket.Packet {
	t.Helper()

	mac := xerror.Unwrap(net.ParseMAC("ab:ab:ab:ab:ab:ab"))
	eth := layers.Ethernet{SrcMAC: mac, DstMAC: mac, EthernetType: layers.EthernetTypeIPv4}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    net.ParseIP("192.168.1.1"),
		DstIP:    net.ParseIP("192.168.1.2"),
	}
	udp := layers.UDP{SrcPort: 1234, DstPort: 5678}
	udp.SetNetworkLayerForChecksum(&ip4)

	payload := make([]byte, 32)
	for idx := range payload {
		payload[idx] = byte(idx*7 + 3)
	}

	return xpacket.LayersToPacket(t, &eth, &ip4, &udp, gopacket.Payload(payload))
}

// TestVxlan_EncapSourcePortDeterministic pins the exact outer UDP source
// port for the fixed inner frame, deriving the expectation from an
// independent Go replication of the hash the C dataplane computes at
// parse time.
func TestVxlan_EncapSourcePortDeterministic(t *testing.T) {
	harness, agent, service := setupVxlanHarness(t)
	wireVxlanPipelines(t, agent)
	createTunnel(t, service)
	wirePortInput(t, agent, "vxlan-out")

	inner := fixedInnerFrame(t)
	ip4Layer := inner.Layer(layers.LayerTypeIPv4).(*layers.IPv4)
	udpLayer := inner.Layer(layers.LayerTypeUDP).(*layers.UDP)

	hash := innerPacketHash(ip4Layer, udpLayer)
	expectedPort := layers.UDPPort(vxlanSrcPortBase + hash%vxlanSrcPortRange)
	require.Equal(t, layers.UDPPort(62756), expectedPort)

	result, err := harness.HandlePackets(inner)
	require.NoError(t, err)
	require.Len(t, result.Output, 1)
	require.Empty(t, result.Drop)

	outer := xpacket.ParseEtherPacket(result.Output[0].RawData)
	outerUDP := outer.Layer(layers.LayerTypeUDP).(*layers.UDP)
	require.Equal(t, expectedPort, outerUDP.SrcPort)
}

// jumboInnerSegments builds an inner frame of the given total length as two
// segments: the fixed header part in the head, zero padding after it.
//
// The headers stay in the head segment so the ingress parse reads only bytes
// resident there, and so the encap prepend keeps the head segment's data_len
// inside 16 bits. The result collector reports only the head segment's bytes.
func jumboInnerSegments(t *testing.T, length int) ([][]byte, []byte) {
	t.Helper()

	head := fixedInnerFrame(t).Data()
	frame := make([]byte, length)
	copy(frame, head)
	return [][]byte{frame[:len(head)], frame[len(head):]}, head
}

// TestVxlan_EncapJumboBoundary pins both sides of the encapsulation size
// limit: the outer IPv4 total length excludes the outer Ethernet header, so an
// inner frame may be as large as 65535 - 36 bytes, and one byte more cannot be
// encapsulated at all.
func TestVxlan_EncapJumboBoundary(t *testing.T) {
	harness, agent, service := setupVxlanHarness(t)
	wireVxlanPipelines(t, agent)
	createTunnel(t, service)
	wirePortInput(t, agent, "vxlan-out")

	// The largest inner frame that still fits: 36 outer bytes (IPv4 + UDP +
	// VXLAN) on top of it reach exactly the 16-bit total length maximum.
	largest := math.MaxUint16 - 36
	segments, head := jumboInnerSegments(t, largest)

	result, err := harness.HandleSegmentedPackets(segments)
	require.NoError(t, err)
	require.Empty(t, result.Drop)
	require.Len(t, result.Output, 1)

	raw := result.Output[0]
	require.Len(t, raw, vxlanEncapLen+len(head))

	outer := xpacket.ParseEtherPacket(raw)
	require.Nil(t, outer.ErrorLayer())

	ip4Layer := outer.Layer(layers.LayerTypeIPv4).(*layers.IPv4)
	require.Equal(t, uint16(math.MaxUint16), ip4Layer.Length)
	require.Equal(t, layers.IPv4DontFragment, ip4Layer.Flags)
	require.Equal(t, tunnelSrcIP, ip4Layer.SrcIP.String())
	require.Equal(t, tunnelDstIP, ip4Layer.DstIP.String())

	udpLayer := outer.Layer(layers.LayerTypeUDP).(*layers.UDP)
	require.Equal(t, uint16(math.MaxUint16-20), udpLayer.Length)
	require.Equal(t, layers.UDPPort(tunnelDstPort), udpLayer.DstPort)

	// The inner frame's head-carried bytes follow the outer headers intact.
	require.Equal(t, head, raw[vxlanEncapLen:])

	// One byte over: the outer total length would not fit 16 bits, and DF
	// forbids fragmenting the datagram, so the frame is dropped unchanged.
	tooLargeSegments, tooLargeHead := jumboInnerSegments(t, largest+1)

	result, err = harness.HandleSegmentedPackets(tooLargeSegments)
	require.NoError(t, err)
	require.Empty(t, result.Output)
	require.Len(t, result.Drop, 1)
	require.Equal(t, tooLargeHead, result.Drop[0])
}

// innerIPv6Frame builds an Ethernet/IPv6/UDP frame the tunnel carries inside.
//
// The metadata test needs an inner frame whose network type differs from the
// outer IPv4: after the encap prepend, parse metadata left over from the inner
// frame would still describe an IPv6 packet, while rebased metadata describes
// the outer IPv4 header.
func innerIPv6Frame(t *testing.T) gopacket.Packet {
	t.Helper()

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: layers.EthernetTypeIPv6,
	}
	ip6 := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolUDP,
		HopLimit:   64,
		SrcIP:      net.ParseIP("2001:db8:1::1"),
		DstIP:      net.ParseIP("2001:db8:2::1"),
	}
	udp := layers.UDP{SrcPort: 1234, DstPort: 5678}
	udp.SetNetworkLayerForChecksum(&ip6)
	payload := gopacket.Payload([]byte("inner ipv6 payload for the vxlan tunnel"))

	return xpacket.LayersToPacket(t, &eth, &ip6, &udp, payload)
}

// vlanInnerFrame builds an 802.1Q-tagged Ethernet/IPv4/UDP frame the tunnel
// carries inside.
//
// The metadata test needs an inner frame whose parse leaves the vlan field set
// to the tag's id, while the outer frame the encap builds is untagged.
func vlanInnerFrame(t *testing.T) gopacket.Packet {
	t.Helper()

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: layers.EthernetTypeDot1Q,
	}
	dot1q := layers.Dot1Q{VLANIdentifier: 100, Type: layers.EthernetTypeIPv4}
	ip4 := layers.IPv4{
		Version:  4,
		Id:       7,
		TTL:      64,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    net.ParseIP("192.168.1.1"),
		DstIP:    net.ParseIP("192.168.1.2"),
	}
	udp := layers.UDP{SrcPort: 4321, DstPort: 8765}
	udp.SetNetworkLayerForChecksum(&ip4)
	payload := gopacket.Payload([]byte("tagged inner payload for the vxlan tunnel"))

	return xpacket.LayersToPacket(t, &eth, &dot1q, &ip4, &udp, payload)
}

// TestVxlan_EncapRebasesParseMetadata verifies that encap re-pins the parse
// metadata onto the outer headers before the packet enters the device's output
// pipeline.
//
// The "tun-underlay" output pipeline forwards only packets whose IPv4 source is
// the configured underlay address, and drops the rest. An IPv6 inner frame
// makes the two metadata states observable: with rebased metadata the forward
// stage reads the outer IPv4 source and emits the packet, while metadata left
// over from the inner frame still types the packet as IPv6, fails the match and
// ends in the blackhole.
func TestVxlan_EncapRebasesParseMetadata(t *testing.T) {
	harness, agent, service := setupVxlanHarness(t)
	wireVxlanPipelines(t, agent)
	wireUnderlayPipeline(t, agent)
	createTunnelWithOutput(t, service, "tun-underlay")
	wirePortInput(t, agent, "vxlan-out")

	inner := innerIPv6Frame(t)

	result, err := harness.HandlePackets(inner)
	require.NoError(t, err)
	require.Empty(t, result.Drop)
	require.Len(t, result.Output, 1)

	raw := result.Output[0].RawData
	require.Len(t, raw, vxlanEncapLen+len(inner.Data()))

	outer := xpacket.ParseEtherPacket(raw)

	ethLayer := outer.Layer(layers.LayerTypeEthernet).(*layers.Ethernet)
	require.Equal(t, tunnelDstMAC, ethLayer.DstMAC.String())
	require.Equal(t, tunnelSrcMAC, ethLayer.SrcMAC.String())

	ip4Layer := outer.Layer(layers.LayerTypeIPv4).(*layers.IPv4)
	require.Equal(t, tunnelSrcIP, ip4Layer.SrcIP.String())
	require.Equal(t, tunnelDstIP, ip4Layer.DstIP.String())
	require.Equal(t, uint16(len(raw)-etherHeaderLen), ip4Layer.Length)

	udpLayer := outer.Layer(layers.LayerTypeUDP).(*layers.UDP)
	require.Equal(t, layers.UDPPort(tunnelDstPort), udpLayer.DstPort)
	require.Equal(t, uint16(vxlanUdpOverhead+len(inner.Data())), udpLayer.Length)

	vxlanLayer := outer.Layer(layers.LayerTypeVXLAN).(*layers.VXLAN)
	require.True(t, vxlanLayer.ValidIDFlag)
	require.Equal(t, tunnelVNI, vxlanLayer.VNI)

	require.Equal(
		t,
		inner.Data(),
		raw[vxlanEncapLen:vxlanEncapLen+len(inner.Data())],
	)
}

// TestVxlan_EncapResetsVlanMetadata verifies that encap presents untagged
// metadata to the output pipeline even for a VLAN-tagged inner frame.
//
// The "tun-untagged" output pipeline forwards only packets whose vlan metadata
// is 0 and drops the rest, so an inner frame tagged with vlan 100 makes the
// two metadata states observable: with the vlan reset the encapped packet is
// emitted, while vlan left over from the inner frame fails the match and ends
// in the blackhole.
func TestVxlan_EncapResetsVlanMetadata(t *testing.T) {
	harness, agent, service := setupVxlanHarness(t)
	wireVxlanPipelines(t, agent)
	wireUntaggedPipeline(t, agent)
	createTunnelWithOutput(t, service, "tun-untagged")
	wirePortInput(t, agent, "vxlan-out")

	inner := vlanInnerFrame(t)

	result, err := harness.HandlePackets(inner)
	require.NoError(t, err)
	require.Empty(t, result.Drop)
	require.Len(t, result.Output, 1)

	raw := result.Output[0].RawData
	require.Len(t, raw, vxlanEncapLen+len(inner.Data()))

	// The outer frame is untagged on the wire, and the tagged inner frame
	// rides the UDP payload untouched.
	outer := xpacket.ParseEtherPacket(raw)
	ethLayer := outer.Layer(layers.LayerTypeEthernet).(*layers.Ethernet)
	require.Equal(t, layers.EthernetTypeIPv4, ethLayer.EthernetType)
	require.Equal(
		t,
		inner.Data(),
		raw[vxlanEncapLen:vxlanEncapLen+len(inner.Data())],
	)

	// Control: an untagged inner frame takes the same emitted path, so the
	// match above pins the vlan reset rather than a stage that emits
	// nothing.
	untaggedResult, err := harness.HandlePackets(innerFrame(t))
	require.NoError(t, err)
	require.Empty(t, untaggedResult.Drop)
	require.Len(t, untaggedResult.Output, 1)
}
