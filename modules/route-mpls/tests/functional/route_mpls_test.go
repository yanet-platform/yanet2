package route_mpls_test

import (
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
	"github.com/yanet-platform/yanet2/modules/route-mpls/bindings/go/croutempls"
	routempls "github.com/yanet-platform/yanet2/modules/route-mpls/controlplane"
)

// Memory sizes for the route-mpls functional harness.
const (
	mplsCPSize  = 64 * datasize.MB
	mplsDPSize  = 4 * datasize.MB
	mplsMemSize = 16 * datasize.MB
)

// MPLS-over-UDP framing contract the handler emits (RFC 7510).
//
// The IANA destination port, the source-port base the packet hash is folded
// into, the fixed label-stack TTL and the outer IP TTL/hop limit.
const (
	mplsUDPPort     = 6635
	mplsSrcPortBase = 0xc000
	mplsSrcPortMask = 0x3fff
	mplsStackTTL    = 10
	tunnelTTL       = 64
)

// Topology names shared by every test: one ingress device and one
// route-mpls config chained into a forward sink on that device.
const (
	mplsDevice     = "port0"
	mplsConfigName = "test"
)

// innerPayload pads every test frame past the 60-byte minimum so the harness
// never zero-pads an output frame and byte-exact comparisons hold.
var innerPayload = []byte("route-mpls functional test payload")

// oddInnerPayload is innerPayload plus one byte, giving the inner packet an
// odd length so the UDP checksum has to fold a trailing single byte.
var oddInnerPayload = append(append([]byte{}, innerPayload...), '!')

// tunnel is one MPLS-over-UDP nexthop: outer endpoints, the label pushed and
// the counter the handler accounts the nexthop under.
type tunnel struct {
	src     netip.Addr
	dst     netip.Addr
	label   uint32
	counter string
}

// nexthop returns the tunnel as a weighted tunnel nexthop.
func (m tunnel) nexthop(weight uint64) croutempls.Nexthop {
	return croutempls.Nexthop{
		Kind:        croutempls.KindTun,
		Source:      m.src,
		Destination: m.dst,
		MPLSLabel:   m.label,
		Weight:      weight,
		Counter:     m.counter,
	}
}

var (
	tunnel4 = tunnel{
		src:     netip.MustParseAddr("4.2.4.2"),
		dst:     netip.MustParseAddr("10.12.1.1"),
		label:   45,
		counter: "tun4",
	}
	tunnel4Alt = tunnel{
		src:     netip.MustParseAddr("4.2.4.2"),
		dst:     netip.MustParseAddr("10.12.1.2"),
		label:   46,
		counter: "tun4alt",
	}
	tunnel6 = tunnel{
		src:     netip.MustParseAddr("2424::1212"),
		dst:     netip.MustParseAddr("ccee::11"),
		label:   47,
		counter: "tun6",
	}
)

// rule4 builds a rule matching one IPv4 destination prefix.
func rule4(prefix string, nexthops ...croutempls.Nexthop) croutempls.Rule {
	return croutempls.Rule{
		Dst4s:    []xnetip.Contiguous[xnetip.Network4]{xnetip.MustParseContiguous4(prefix)},
		Nexthops: nexthops,
	}
}

// rule6 builds a rule matching one IPv6 destination prefix.
func rule6(prefix string, nexthops ...croutempls.Nexthop) croutempls.Rule {
	return croutempls.Rule{
		Dst6s:    []xnetip.BiContiguous{xnetip.MustParseBiContiguous(prefix)},
		Nexthops: nexthops,
	}
}

// innerFrame is one test input: the serialized frame plus its layers.
//
// Keeping the layers lets the expected tunnel frame be re-serialized around
// the very same inner headers the input was built from.
type innerFrame struct {
	packet gopacket.Packet
	eth    layers.Ethernet
	// stack holds the network layer and everything above it.
	stack []gopacket.SerializableLayer
}

// size returns the frame length in bytes, which is what the per-nexthop byte
// counter accounts before encapsulation.
func (m innerFrame) size() uint64 {
	return uint64(len(m.packet.Data()))
}

// l3 returns the frame from its network header on — the bytes the handler
// must carry unchanged below the label stack.
func (m innerFrame) l3() []byte {
	return m.packet.LinkLayer().LayerPayload()
}

func newEthernet(ethernetType layers.EthernetType) layers.Ethernet {
	return layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: ethernetType,
	}
}

// serializeFrame serializes eth and stack with gopacket fixing every length
// and checksum, and returns the frame both parsed and as its layer list.
func serializeFrame(
	t *testing.T,
	eth layers.Ethernet,
	stack []gopacket.SerializableLayer,
) innerFrame {
	t.Helper()

	all := append([]gopacket.SerializableLayer{&eth}, stack...)
	packet := xpacket.LayersToPacket(t, all...)
	return innerFrame{packet: packet, eth: eth, stack: stack}
}

// ip4Frame builds an IPv4 ICMP echo request addressed to dst carrying the
// standard payload.
func ip4Frame(t *testing.T, dst string) innerFrame {
	t.Helper()

	return ip4FrameWithPayload(t, dst, innerPayload)
}

// ip4FrameWithPayload builds an IPv4 ICMP echo request addressed to dst
// carrying payload.
func ip4FrameWithPayload(t *testing.T, dst string, payload []byte) innerFrame {
	t.Helper()

	ip4 := &layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    net.ParseIP("192.0.2.100").To4(),
		DstIP:    net.ParseIP(dst).To4(),
	}
	icmp := &layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
	}
	return serializeFrame(
		t,
		newEthernet(layers.EthernetTypeIPv4),
		[]gopacket.SerializableLayer{ip4, icmp, gopacket.Payload(payload)},
	)
}

// ip6Frame builds an IPv6 ICMPv6 echo request addressed to dst carrying the
// standard payload.
func ip6Frame(t *testing.T, dst string) innerFrame {
	t.Helper()

	return ip6FrameWithPayload(t, dst, innerPayload)
}

// ip6FrameWithPayload builds an IPv6 ICMPv6 echo request addressed to dst
// carrying payload.
func ip6FrameWithPayload(t *testing.T, dst string, payload []byte) innerFrame {
	t.Helper()

	ip6 := &layers.IPv6{
		Version:    6,
		HopLimit:   64,
		NextHeader: layers.IPProtocolICMPv6,
		SrcIP:      net.ParseIP("2001:db8::100"),
		DstIP:      net.ParseIP(dst),
	}
	icmp6 := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
	}
	require.NoError(t, icmp6.SetNetworkLayerForChecksum(ip6))
	echo := &layers.ICMPv6Echo{Identifier: 1, SeqNumber: 1}
	return serializeFrame(
		t,
		newEthernet(layers.EthernetTypeIPv6),
		[]gopacket.SerializableLayer{ip6, icmp6, echo, gopacket.Payload(payload)},
	)
}

// arpFrame builds a broadcast ARP request, padded to the minimum frame size
// so the harness hands it back byte-exact.
func arpFrame(t *testing.T) gopacket.Packet {
	t.Helper()

	eth := newEthernet(layers.EthernetTypeARP)
	eth.DstMAC = xerror.Unwrap(net.ParseMAC("ff:ff:ff:ff:ff:ff"))
	arp := &layers.ARP{
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
	padding := make([]byte, 18)
	return xpacket.LayersToPacket(t, &eth, arp, gopacket.Payload(padding))
}

// expectedTunnelFrame serializes the frame the handler must emit when it
// carries frame over tun for a packet with the given hash.
//
// The reference keeps the original Ethernet addressing, prepends an outer IPv4
// or IPv6 header with UDP, one bottom-of-stack label and the untouched inner
// packet. gopacket computes every length and checksum, so a byte-exact match
// against it also validates the handler's outer IP and UDP checksums.
func expectedTunnelFrame(
	t *testing.T,
	frame innerFrame,
	tun tunnel,
	hash uint32,
) []byte {
	t.Helper()

	udp := &layers.UDP{
		SrcPort: layers.UDPPort(mplsSrcPortBase | (hash & mplsSrcPortMask)),
		DstPort: mplsUDPPort,
	}
	mpls := &layers.MPLS{
		Label:       tun.label,
		StackBottom: true,
		TTL:         mplsStackTTL,
	}

	eth := frame.eth
	var outer gopacket.SerializableLayer
	if tun.dst.Is4() {
		eth.EthernetType = layers.EthernetTypeIPv4
		ip4 := &layers.IPv4{
			Version:  4,
			TTL:      tunnelTTL,
			Protocol: layers.IPProtocolUDP,
			SrcIP:    tun.src.AsSlice(),
			DstIP:    tun.dst.AsSlice(),
		}
		require.NoError(t, udp.SetNetworkLayerForChecksum(ip4))
		outer = ip4
	} else {
		eth.EthernetType = layers.EthernetTypeIPv6
		ip6 := &layers.IPv6{
			Version:    6,
			HopLimit:   tunnelTTL,
			NextHeader: layers.IPProtocolUDP,
			SrcIP:      tun.src.AsSlice(),
			DstIP:      tun.dst.AsSlice(),
		}
		require.NoError(t, udp.SetNetworkLayerForChecksum(ip6))
		outer = ip6
	}

	stack := append([]gopacket.SerializableLayer{&eth, outer, udp, mpls}, frame.stack...)
	buffer := gopacket.NewSerializeBuffer()
	options := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
	require.NoError(t, gopacket.SerializeLayers(buffer, options, stack...))
	return buffer.Bytes()
}

// tunnelFrame is the decoded shape of one MPLS-over-UDP frame.
type tunnelFrame struct {
	outerSrc    netip.Addr
	outerDst    netip.Addr
	outerTTL    uint8
	srcPort     uint16
	dstPort     uint16
	label       uint32
	stackBottom bool
	labelTTL    uint8
	inner       []byte
}

// decodeTunnelFrame parses raw as Ethernet / outer IP / UDP / one MPLS label /
// inner packet, failing the test on any other shape.
func decodeTunnelFrame(t *testing.T, raw []byte) tunnelFrame {
	t.Helper()

	packet := gopacket.NewPacket(raw, layers.LayerTypeEthernet, gopacket.Default)
	require.Nil(t, packet.ErrorLayer(), "output frame must decode cleanly")

	var decoded tunnelFrame
	switch network := packet.NetworkLayer().(type) {
	case *layers.IPv4:
		decoded.outerSrc = netip.AddrFrom4([4]byte(network.SrcIP.To4()))
		decoded.outerDst = netip.AddrFrom4([4]byte(network.DstIP.To4()))
		decoded.outerTTL = network.TTL
		require.Equal(t, layers.IPProtocolUDP, network.Protocol, "outer IPv4 must carry UDP")
	case *layers.IPv6:
		decoded.outerSrc = netip.AddrFrom16([16]byte(network.SrcIP.To16()))
		decoded.outerDst = netip.AddrFrom16([16]byte(network.DstIP.To16()))
		decoded.outerTTL = network.HopLimit
		require.Equal(t, layers.IPProtocolUDP, network.NextHeader, "outer IPv6 must carry UDP")
	default:
		require.Failf(t, "unexpected outer header", "network layer %T", network)
	}

	udp, ok := packet.Layer(layers.LayerTypeUDP).(*layers.UDP)
	require.True(t, ok, "output frame must carry a UDP header")
	decoded.srcPort = uint16(udp.SrcPort)
	decoded.dstPort = uint16(udp.DstPort)

	labelStack := gopacket.NewPacket(udp.Payload, layers.LayerTypeMPLS, gopacket.Default)
	mpls, ok := labelStack.Layer(layers.LayerTypeMPLS).(*layers.MPLS)
	require.True(t, ok, "UDP payload must start with an MPLS label")
	decoded.label = mpls.Label
	decoded.stackBottom = mpls.StackBottom
	decoded.labelTTL = mpls.TTL
	decoded.inner = mpls.Payload
	return decoded
}

// requireTunnelFrame asserts that raw is frame carried over tun for a packet
// with the given hash.
//
// Fields are checked one by one first for diagnosable failures, then the whole
// frame byte for byte against the gopacket reference for lengths and checksums.
func requireTunnelFrame(
	t *testing.T,
	raw []byte,
	frame innerFrame,
	tun tunnel,
	hash uint32,
) {
	t.Helper()

	decoded := decodeTunnelFrame(t, raw)
	require.Equal(t, tun.src, decoded.outerSrc, "outer source must be the tunnel source")
	require.Equal(t, tun.dst, decoded.outerDst, "outer destination must be the tunnel destination")
	require.Equal(t, uint8(tunnelTTL), decoded.outerTTL, "outer TTL must be the default")
	require.Equal(t, uint16(mplsUDPPort), decoded.dstPort, "UDP destination must be the MPLS-in-UDP port")
	require.Equal(
		t,
		uint16(mplsSrcPortBase|(hash&mplsSrcPortMask)),
		decoded.srcPort,
		"UDP source port must fold the packet hash into the ephemeral range",
	)
	require.Equal(t, tun.label, decoded.label, "label must be the nexthop label")
	require.True(t, decoded.stackBottom, "single label must be bottom of stack")
	require.Equal(t, uint8(mplsStackTTL), decoded.labelTTL, "label TTL must be the fixed stack TTL")
	require.Equal(t, frame.l3(), decoded.inner, "inner packet must be carried unchanged")

	require.Equal(
		t,
		expectedTunnelFrame(t, frame, tun, hash),
		raw,
		"frame must match the gopacket reference byte for byte (lengths and checksums included)",
	)
}

// requirePassthrough asserts that raw is the input frame, untouched.
func requirePassthrough(t *testing.T, raw []byte, input gopacket.Packet) {
	t.Helper()

	require.Equal(t, input.Data(), raw, "frame must pass through unchanged")
}

// catchAllForwardRules returns forward rules that send every packet type
// (IPv4, IPv6 and non-IP L2) to the given device's output stage.
//
// The input entry point is not allowed to transmit directly, so route-mpls
// output needs a catch-all egress rule behind it to reach the output stage.
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
		{
			Target:  device,
			Mode:    cforward.ModeOut,
			Counter: "sink_l2",
			Devices: filter.Devices{{Name: device}},
		},
	}
}

// deployRules builds a one-device harness with route-mpls and forward loaded,
// publishes rules as the route-mpls config and wires the ingress topology.
//
// The chain is route-mpls followed by a forward sink, behind one function,
// one pipeline and one plain device. All teardown is registered through
// t.Cleanup in LIFO order.
func deployRules(t *testing.T, rules []croutempls.Rule) *dataplaneut.Harness {
	t.Helper()

	config := dataplaneut.Config{
		CPMemory:      uint64(mplsCPSize),
		DPMemory:      uint64(mplsDPSize),
		WorkerCount:   1,
		Devices:       []string{mplsDevice},
		Modules:       []string{"route_mpls", "forward"},
		DevicesToLoad: []string{"plain"},
	}
	harness, err := dataplaneut.NewHarness(config)
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	agent, err := harness.SharedMemory().AgentAttach("mpls-test", 0, mplsMemSize)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	handle, err := routempls.NewBackend(agent).UpdateModule(mplsConfigName, rules)
	require.NoError(t, err)
	t.Cleanup(func() { _ = handle.Free() })

	sinkName := mplsConfigName + "-sink"
	sinkHandle, err := forward.NewBackend(agent).UpdateModule(sinkName, catchAllForwardRules(mplsDevice))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sinkHandle.Free() })

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: mplsConfigName,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: mplsConfigName + "_chain",
				Modules: []ffi.ChainModuleConfig{
					{Type: "route-mpls", Name: mplsConfigName},
					{Type: "forward", Name: sinkName},
				},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      mplsConfigName,
		Functions: []string{mplsConfigName},
	}))
	// A pipeline with no functions passes egress packets straight through.
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name: "dummy",
	}))
	_, err = plain.UpdateDevices(agent, []ffi.DeviceConfig{{
		Name:   mplsDevice,
		Input:  []ffi.DevicePipelineConfig{{Name: mplsConfigName, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}})
	require.NoError(t, err)

	return harness
}

// counterPath locates the route-mpls module inside the deployed topology.
func counterPath() dataplaneut.CounterPath {
	return dataplaneut.CounterPath{
		Device:     mplsDevice,
		Pipeline:   mplsConfigName,
		Function:   mplsConfigName,
		Chain:      mplsConfigName + "_chain",
		ModuleType: "route-mpls",
		ModuleName: mplsConfigName,
	}
}

// requireNexthopCounter asserts the per-nexthop counter named name holds
// wantPackets and wantBytes.
func requireNexthopCounter(
	t *testing.T,
	harness *dataplaneut.Harness,
	name string,
	wantPackets, wantBytes uint64,
) {
	t.Helper()

	dataplaneut.RequireRuleCounter(t, harness, counterPath(), name, wantPackets, wantBytes)
}

// Test_RouteMPLS_Encap verifies that a packet matching a tunnel nexthop is
// wrapped in outer IP / UDP / one label with the inner packet untouched.
//
// Every inner/outer family combination is covered, with even and odd inner
// lengths, and the nexthop counter must account the original frame.
func Test_RouteMPLS_Encap(t *testing.T) {
	// A hash above the 14-bit source-port mask pins that only the low bits
	// reach the port.
	const hash = 0x1234abcd

	cases := []struct {
		name  string
		frame func(t *testing.T) innerFrame
		rules []croutempls.Rule
		tun   tunnel
	}{
		{
			name:  "IPv4 over IPv4 tunnel",
			frame: func(t *testing.T) innerFrame { return ip4Frame(t, "10.1.1.5") },
			rules: []croutempls.Rule{rule4("10.0.0.0/8", tunnel4.nexthop(1))},
			tun:   tunnel4,
		},
		{
			name:  "IPv4 over IPv6 tunnel",
			frame: func(t *testing.T) innerFrame { return ip4Frame(t, "10.1.1.5") },
			rules: []croutempls.Rule{rule4("10.0.0.0/8", tunnel6.nexthop(1))},
			tun:   tunnel6,
		},
		{
			name:  "IPv6 over IPv4 tunnel",
			frame: func(t *testing.T) innerFrame { return ip6Frame(t, "2001:db8:1::5") },
			rules: []croutempls.Rule{rule6("2001:db8::/32", tunnel4.nexthop(1))},
			tun:   tunnel4,
		},
		{
			name:  "IPv6 over IPv6 tunnel",
			frame: func(t *testing.T) innerFrame { return ip6Frame(t, "2001:db8:1::5") },
			rules: []croutempls.Rule{rule6("2001:db8::/32", tunnel6.nexthop(1))},
			tun:   tunnel6,
		},
		{
			name: "odd-length inner over IPv4 tunnel",
			frame: func(t *testing.T) innerFrame {
				return ip4FrameWithPayload(t, "10.1.1.5", oddInnerPayload)
			},
			rules: []croutempls.Rule{rule4("10.0.0.0/8", tunnel4.nexthop(1))},
			tun:   tunnel4,
		},
		{
			name: "odd-length inner over IPv6 tunnel",
			frame: func(t *testing.T) innerFrame {
				return ip6FrameWithPayload(t, "2001:db8:1::5", oddInnerPayload)
			},
			rules: []croutempls.Rule{rule6("2001:db8::/32", tunnel6.nexthop(1))},
			tun:   tunnel6,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frame := tc.frame(t)
			harness := deployRules(t, tc.rules)

			result, err := harness.HandlePacketsWithHashes([]uint32{hash}, frame.packet)
			require.NoError(t, err)
			require.Len(t, result.Output, 1, "encapsulated packet must reach egress")
			require.Empty(t, result.Drop, "encapsulation must not drop")

			requireTunnelFrame(t, result.Output[0].RawData, frame, tc.tun, hash)
			requireNexthopCounter(t, harness, tc.tun.counter, 1, frame.size())
		})
	}
}

// Test_RouteMPLS_NoMatch_PassesThrough verifies that a packet no rule matches
// leaves the module unchanged and touches no nexthop counter.
//
// Covers a destination outside every prefix, a family with no rules at all,
// and a frame that is not IP.
func Test_RouteMPLS_NoMatch_PassesThrough(t *testing.T) {
	rules := []croutempls.Rule{rule4("10.0.0.0/8", tunnel4.nexthop(1))}

	cases := []struct {
		name  string
		input func(t *testing.T) gopacket.Packet
	}{
		{
			name:  "IPv4 outside every prefix",
			input: func(t *testing.T) gopacket.Packet { return ip4Frame(t, "192.168.1.1").packet },
		},
		{
			name:  "IPv6 when only IPv4 rules exist",
			input: func(t *testing.T) gopacket.Packet { return ip6Frame(t, "2001:db8::1").packet },
		},
		{
			name:  "non-IP frame",
			input: arpFrame,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := tc.input(t)
			harness := deployRules(t, rules)

			result, err := harness.HandlePackets(input)
			require.NoError(t, err)
			require.Len(t, result.Output, 1, "unmatched packet must pass through")
			require.Empty(t, result.Drop, "unmatched packet must not be dropped")

			requirePassthrough(t, result.Output[0].RawData, input)
			requireNexthopCounter(t, harness, tunnel4.counter, 0, 0)
		})
	}
}

// Test_RouteMPLS_NoneNexthop_CountsAndPassesThrough verifies that a matching
// rule whose nexthop has no tunnel accounts the packet and forwards it as is.
//
// This is the shape the control plane uses for its default "no route" entries.
func Test_RouteMPLS_NoneNexthop_CountsAndPassesThrough(t *testing.T) {
	frame := ip4Frame(t, "10.1.1.5")
	harness := deployRules(t, []croutempls.Rule{
		rule4("0.0.0.0/0", croutempls.Nexthop{
			Kind:    croutempls.KindNone,
			Weight:  1,
			Counter: "no route",
		}),
	})

	result, err := harness.HandlePackets(frame.packet)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "packet must pass through")
	require.Empty(t, result.Drop, "packet must not be dropped")

	requirePassthrough(t, result.Output[0].RawData, frame.packet)
	requireNexthopCounter(t, harness, "no route", 1, frame.size())
}

// Test_RouteMPLS_ZeroWeightNexthops_PassThrough verifies that a matching rule
// whose nexthops all weigh zero forwards the packet unchanged and unaccounted.
//
// Zero total weight leaves the selection map empty, so no nexthop can be
// chosen and no counter may move.
func Test_RouteMPLS_ZeroWeightNexthops_PassThrough(t *testing.T) {
	frame := ip4Frame(t, "10.1.1.5")
	harness := deployRules(t, []croutempls.Rule{
		rule4("10.0.0.0/8", tunnel4.nexthop(0), tunnel4Alt.nexthop(0)),
	})

	result, err := harness.HandlePackets(frame.packet)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "packet must pass through")
	require.Empty(t, result.Drop, "packet must not be dropped")

	requirePassthrough(t, result.Output[0].RawData, frame.packet)
	requireNexthopCounter(t, harness, tunnel4.counter, 0, 0)
	requireNexthopCounter(t, harness, tunnel4Alt.counter, 0, 0)
}

// Test_RouteMPLS_ECMP_SelectsByWeightedHash verifies that the packet hash
// modulo the weight sum selects the nexthop and reaches the UDP source port.
//
// With weights 1 and 3 the hash walks a four-slot map, and each nexthop
// counter must see exactly the packets routed to it.
func Test_RouteMPLS_ECMP_SelectsByWeightedHash(t *testing.T) {
	frame := ip4Frame(t, "10.1.1.5")
	harness := deployRules(t, []croutempls.Rule{
		rule4("10.0.0.0/8", tunnel4.nexthop(1), tunnel4Alt.nexthop(3)),
	})

	// Two full passes over the 4-slot selection map.
	hashes := []uint32{0, 1, 2, 3, 4, 5, 6, 7}
	wantTunnels := []tunnel{
		tunnel4, tunnel4Alt, tunnel4Alt, tunnel4Alt,
		tunnel4, tunnel4Alt, tunnel4Alt, tunnel4Alt,
	}

	packets := make([]gopacket.Packet, len(hashes))
	for idx := range packets {
		packets[idx] = frame.packet
	}

	result, err := harness.HandlePacketsWithHashes(hashes, packets...)
	require.NoError(t, err)
	require.Len(t, result.Output, len(hashes), "every packet must reach egress")
	require.Empty(t, result.Drop, "no packet must be dropped")

	for idx, output := range result.Output {
		requireTunnelFrame(t, output.RawData, frame, wantTunnels[idx], hashes[idx])
	}
	requireNexthopCounter(t, harness, tunnel4.counter, 2, 2*frame.size())
	requireNexthopCounter(t, harness, tunnel4Alt.counter, 6, 6*frame.size())
}

// Test_RouteMPLS_OverlappingPrefixes_FirstRuleWins verifies that when two
// rules cover a destination the earlier rule wins regardless of prefix length.
//
// This is the ordering contract the control plane relies on when it emits
// rules most-specific first.
func Test_RouteMPLS_OverlappingPrefixes_FirstRuleWins(t *testing.T) {
	specific := rule4("10.1.1.0/24", tunnel4.nexthop(1))
	broad := rule4("10.0.0.0/8", tunnel4Alt.nexthop(1))

	cases := []struct {
		name  string
		rules []croutempls.Rule
		dst   string
		want  tunnel
		other tunnel
	}{
		{
			name:  "specific rule first routes its prefix",
			rules: []croutempls.Rule{specific, broad},
			dst:   "10.1.1.5",
			want:  tunnel4,
			other: tunnel4Alt,
		},
		{
			name:  "specific rule first leaves the rest to the broad rule",
			rules: []croutempls.Rule{specific, broad},
			dst:   "10.2.0.1",
			want:  tunnel4Alt,
			other: tunnel4,
		},
		{
			name:  "broad rule first shadows the specific one",
			rules: []croutempls.Rule{broad, specific},
			dst:   "10.1.1.5",
			want:  tunnel4Alt,
			other: tunnel4,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frame := ip4Frame(t, tc.dst)
			harness := deployRules(t, tc.rules)

			result, err := harness.HandlePacketsWithHashes([]uint32{0}, frame.packet)
			require.NoError(t, err)
			require.Len(t, result.Output, 1, "packet must reach egress")
			require.Empty(t, result.Drop, "packet must not be dropped")

			requireTunnelFrame(t, result.Output[0].RawData, frame, tc.want, 0)
			requireNexthopCounter(t, harness, tc.want.counter, 1, frame.size())
			requireNexthopCounter(t, harness, tc.other.counter, 0, 0)
		})
	}
}

// Test_RouteMPLS_MixedBurst_KeepsOrderAndPerFamilyLookups verifies that a
// mixed burst comes out in input order with exactly the routed frames wrapped.
//
// The burst mixes routed and unrouted IPv4, IPv6 and non-IP frames.
// The handler classifies each family into its own lookup batch and walks
// the results back in a second pass, so an unmatched or non-IP frame in the
// middle must not shift which result the following frames consume.
func Test_RouteMPLS_MixedBurst_KeepsOrderAndPerFamilyLookups(t *testing.T) {
	harness := deployRules(t, []croutempls.Rule{
		rule4("10.0.0.0/8", tunnel4.nexthop(1)),
		rule6("2001:db8::/32", tunnel6.nexthop(1)),
	})

	routed4 := ip4Frame(t, "10.1.1.5")
	unrouted6 := ip6Frame(t, "2001:db9::1")
	unrouted4 := ip4Frame(t, "192.168.1.1")
	arp := arpFrame(t)
	routed6 := ip6Frame(t, "2001:db8:1::5")
	routed4Again := ip4Frame(t, "10.2.2.2")

	hashes := []uint32{1, 2, 3, 4, 5, 6}
	result, err := harness.HandlePacketsWithHashes(
		hashes,
		routed4.packet,
		unrouted6.packet,
		unrouted4.packet,
		arp,
		routed6.packet,
		routed4Again.packet,
	)
	require.NoError(t, err)
	require.Len(t, result.Output, 6, "every frame must reach egress")
	require.Empty(t, result.Drop, "no frame must be dropped")

	requireTunnelFrame(t, result.Output[0].RawData, routed4, tunnel4, hashes[0])
	requirePassthrough(t, result.Output[1].RawData, unrouted6.packet)
	requirePassthrough(t, result.Output[2].RawData, unrouted4.packet)
	requirePassthrough(t, result.Output[3].RawData, arp)
	requireTunnelFrame(t, result.Output[4].RawData, routed6, tunnel6, hashes[4])
	requireTunnelFrame(t, result.Output[5].RawData, routed4Again, tunnel4, hashes[5])

	requireNexthopCounter(t, harness, tunnel4.counter, 2, routed4.size()+routed4Again.size())
	requireNexthopCounter(t, harness, tunnel6.counter, 1, routed6.size())
}

// Test_RouteMPLS_EmptyRound verifies that a force-poll round with no packets
// passes through the handler harmlessly and leaves every counter at zero.
//
// The worker invokes the handler on every tick even with an empty front;
// the handler sizes its per-family batches from the front length, so this
// round pins the zero-length path that #2065 made safe.
func Test_RouteMPLS_EmptyRound(t *testing.T) {
	harness := deployRules(t, []croutempls.Rule{
		rule4("10.0.0.0/8", tunnel4.nexthop(1)),
	})

	result, err := harness.HandlePackets()
	require.NoError(t, err)
	require.Empty(t, result.Output, "empty round produces no output")
	require.Empty(t, result.Drop, "empty round produces no drops")
	requireNexthopCounter(t, harness, tunnel4.counter, 0, 0)
}
