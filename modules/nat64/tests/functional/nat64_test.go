package nat64_test

import (
	"encoding/binary"
	"github.com/yanet-platform/xnetip"
	"net"
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	cplain "github.com/yanet-platform/yanet2/devices/plain/bindings/go/cplain"
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	forward "github.com/yanet-platform/yanet2/modules/forward/controlplane"
	"github.com/yanet-platform/yanet2/modules/nat64/bindings/go/cnat64"
	nat64 "github.com/yanet-platform/yanet2/modules/nat64/controlplane"
	nat64pb "github.com/yanet-platform/yanet2/modules/nat64/controlplane/nat64pb/v1"
)

const (
	nat64CPSize  = 64 * datasize.MB
	nat64DPSize  = 4 * datasize.MB
	nat64MemSize = 8 * datasize.MB
)

var nat64Prefix96 = netip.MustParsePrefix("2001:db8::/96")

func setupNAT64Harness(t *testing.T) (*dataplaneut.Harness, *nat64.NAT64Service) {
	t.Helper()

	h, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(nat64CPSize),
		DPMemory:      uint64(nat64DPSize),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		Modules:       []string{"nat64", "forward"},
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(h.Free)

	agent, err := h.SharedMemory().AgentAttach("nat64-func-test", 0, nat64MemSize)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	svc := nat64.NewNAT64Service(nat64.NewBackend(agent))
	mustSetDropUnknown(t, svc, "nat64-test", false, false)
	wireNAT64Pipeline(t, agent, "nat64-test")
	return h, svc
}

func wireNAT64Pipeline(t *testing.T, agent *ffi.Agent, name string) {
	t.Helper()

	sinkName := name + "-sink"
	sinkRules := []cforward.ForwardRule{
		{
			Target:  "port0",
			Mode:    cforward.ModeOut,
			Counter: "sink4",
			Src4s:   []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			Dst4s:   []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		},
		{
			Target:  "port0",
			Mode:    cforward.ModeOut,
			Counter: "sink6",
			Src6s:   []xnetip.BiContiguous{filter.UnspecifiedIPv6},
			Dst6s:   []xnetip.BiContiguous{filter.UnspecifiedIPv6},
		},
	}
	sinkHandle, err := forward.NewBackend(agent).UpdateModule(sinkName, sinkRules)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sinkHandle.Free() })

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: name,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: name + "_chain",
				Modules: []ffi.ChainModuleConfig{
					{Type: "nat64", Name: name},
					{Type: "forward", Name: sinkName},
				},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: name, Functions: []string{name}}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "dummy"}))
	_, err = cplain.UpdateDevices(agent, []ffi.DeviceConfig{{
		Name:   "port0",
		Input:  []ffi.DevicePipelineConfig{{Name: name, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}})
	require.NoError(t, err)
}

func mustAddPrefix(t *testing.T, svc *nat64.NAT64Service, name string, prefix netip.Prefix) {
	t.Helper()

	wirePrefix, err := commonpb.NewIPv6PrefixFromPrefix(prefix)
	require.NoError(t, err)

	_, err = svc.AddPrefix(t.Context(), &nat64pb.AddPrefixRequest{
		Name:   name,
		Prefix: wirePrefix,
	})
	require.NoError(t, err)
}

func mustAddMapping(t *testing.T, svc *nat64.NAT64Service, name string, ip4, ip6 string, prefixIdx uint32) {
	t.Helper()
	_, err := svc.AddMapping(t.Context(), &nat64pb.AddMappingRequest{
		Name:        name,
		Ipv4:        commonpb.NewIPv4Address(netip.MustParseAddr(ip4).As4()),
		Ipv6:        commonpb.NewIPv6Address(netip.MustParseAddr(ip6).As16()),
		PrefixIndex: prefixIdx,
	})
	require.NoError(t, err)
}

func mustSetDropUnknown(t *testing.T, svc *nat64.NAT64Service, name string, dropPrefix, dropMapping bool) {
	t.Helper()
	_, err := svc.SetDropUnknown(t.Context(), &nat64pb.SetDropUnknownRequest{
		Name:               name,
		DropUnknownPrefix:  dropPrefix,
		DropUnknownMapping: dropMapping,
	})
	require.NoError(t, err)
}

func parseOneOutput(t *testing.T, result *dataplaneut.Result) gopacket.Packet {
	t.Helper()
	require.Empty(t, result.Drop)
	require.Len(t, result.Output, 1)
	return xpacket.ParseEtherPacket(result.Output[0].RawData)
}

func mappedV6(prefix netip.Prefix, ip4 netip.Addr) net.IP {
	out := prefix.Masked().Addr().As16()
	ip4b := ip4.As4()
	copy(out[12:], ip4b[:])
	return out[:]
}

func packetWithIPv6RawExt(
	t *testing.T,
	srcIP, dstIP net.IP,
	extFirst byte,
	extPayload []byte,
) gopacket.Packet {
	t.Helper()
	ip6 := layers.IPv6{
		Version:    6,
		HopLimit:   64,
		NextHeader: layers.IPProtocol(extFirst),
		SrcIP:      srcIP,
		DstIP:      dstIP,
	}
	buf := gopacket.NewSerializeBuffer()
	require.NoError(t, gopacket.SerializeLayers(
		buf,
		gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true},
		&layers.Ethernet{
			SrcMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
			DstMAC:       xerror.Unwrap(net.ParseMAC("66:77:88:99:aa:bb")),
			EthernetType: layers.EthernetTypeIPv6,
		},
		&ip6,
		gopacket.Payload(extPayload),
	))
	return gopacket.NewPacket(buf.Bytes(), layers.LayerTypeEthernet, gopacket.Default)
}

func packetWithIPv4RawPayload(
	t *testing.T,
	srcIP, dstIP net.IP,
	proto layers.IPProtocol,
	payload []byte,
) gopacket.Packet {
	t.Helper()
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: proto,
		SrcIP:    srcIP,
		DstIP:    dstIP,
	}
	buf := gopacket.NewSerializeBuffer()
	require.NoError(t, gopacket.SerializeLayers(
		buf,
		gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true},
		&layers.Ethernet{
			SrcMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
			DstMAC:       xerror.Unwrap(net.ParseMAC("66:77:88:99:aa:bb")),
			EthernetType: layers.EthernetTypeIPv4,
		},
		&ip4,
		gopacket.Payload(payload),
	))
	return gopacket.NewPacket(buf.Bytes(), layers.LayerTypeEthernet, gopacket.Default)
}

func mustSetMTU(t *testing.T, svc *nat64.NAT64Service, name string, v4, v6 uint32) {
	t.Helper()
	_, err := svc.SetMTU(t.Context(), &nat64pb.SetMTURequest{
		Name: name,
		Mtu: &nat64pb.MTUConfig{
			Ipv4Mtu: v4,
			Ipv6Mtu: v6,
		},
	})
	require.NoError(t, err)
}

// TestNAT64_ICMPv6ToIPv4Echo verifies ICMP echo request translation from IPv6
// to IPv4 using service-driven NAT64 configuration.
func TestNAT64_ICMPv6ToIPv4Echo(t *testing.T) {
	h, svc := setupNAT64Harness(t)
	mustAddPrefix(t, svc, "nat64-test", nat64Prefix96)
	mustAddMapping(t, svc, "nat64-test", "192.0.2.10", "2001:db8::10", 0)

	ip6 := layers.IPv6{
		Version:    6,
		HopLimit:   64,
		NextHeader: layers.IPProtocolICMPv6,
		SrcIP:      net.ParseIP("2001:db8::10"),
		DstIP:      net.ParseIP("2001:db8::192.0.2.20"),
	}
	icmp6 := layers.ICMPv6{TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0)}
	icmp6.SetNetworkLayerForChecksum(&ip6)
	echo6 := layers.ICMPv6Echo{Identifier: 123, SeqNumber: 456}
	payload := gopacket.Payload([]byte("nat64-v6-to-v4"))
	pkt := xpacket.LayersToPacket(t,
		&layers.Ethernet{
			SrcMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
			DstMAC:       xerror.Unwrap(net.ParseMAC("66:77:88:99:aa:bb")),
			EthernetType: layers.EthernetTypeIPv6,
		},
		&ip6, &icmp6, &echo6, payload,
	)

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	out := parseOneOutput(t, result)

	ip4Layer := out.Layer(layers.LayerTypeIPv4)
	require.NotNil(t, ip4Layer)
	ip4 := ip4Layer.(*layers.IPv4)
	require.Equal(t, net.ParseIP("192.0.2.10").To4(), ip4.SrcIP.To4())
	require.Equal(t, net.ParseIP("192.0.2.20").To4(), ip4.DstIP.To4())
	require.Equal(t, layers.IPProtocolICMPv4, ip4.Protocol)

	icmp4Layer := out.Layer(layers.LayerTypeICMPv4)
	require.NotNil(t, icmp4Layer)
	icmp4 := icmp4Layer.(*layers.ICMPv4)
	require.Equal(t, uint16(echo6.Identifier), icmp4.Id)
	require.Equal(t, uint16(echo6.SeqNumber), icmp4.Seq)

	app := out.ApplicationLayer()
	require.NotNil(t, app)
	require.Equal(t, []byte(payload), app.Payload())
}

// TestNAT64_ICMPv4ToIPv6Echo verifies ICMP echo request translation from IPv4
// to IPv6 using NAT64 prefix synthesis and mapping lookup.
func TestNAT64_ICMPv4ToIPv6Echo(t *testing.T) {
	h, svc := setupNAT64Harness(t)
	mustAddPrefix(t, svc, "nat64-test", nat64Prefix96)
	mustAddMapping(t, svc, "nat64-test", "192.0.2.30", "2001:db8::30", 0)

	ip4 := layers.IPv4{Version: 4, TTL: 64, Protocol: layers.IPProtocolICMPv4, SrcIP: net.ParseIP("192.0.2.20"), DstIP: net.ParseIP("192.0.2.30")}
	icmp4 := layers.ICMPv4{TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0), Id: 7, Seq: 9}
	payload := gopacket.Payload([]byte("nat64-v4-to-v6"))
	pkt := xpacket.LayersToPacket(t,
		&layers.Ethernet{
			SrcMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
			DstMAC:       xerror.Unwrap(net.ParseMAC("66:77:88:99:aa:bb")),
			EthernetType: layers.EthernetTypeIPv4,
		},
		&ip4, &icmp4, payload,
	)

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	out := parseOneOutput(t, result)

	ip6Layer := out.Layer(layers.LayerTypeIPv6)
	require.NotNil(t, ip6Layer)
	ip6 := ip6Layer.(*layers.IPv6)
	require.Equal(t, mappedV6(nat64Prefix96, netip.MustParseAddr("192.0.2.20")), ip6.SrcIP)
	require.Equal(t, net.ParseIP("2001:db8::30"), ip6.DstIP)
	require.Equal(t, layers.IPProtocolICMPv6, ip6.NextHeader)
}

// TestNAT64_UDPv6ToIPv4 verifies UDP translation from IPv6 to IPv4.
func TestNAT64_UDPv6ToIPv4(t *testing.T) {
	h, svc := setupNAT64Harness(t)
	mustAddPrefix(t, svc, "nat64-test", nat64Prefix96)
	mustAddMapping(t, svc, "nat64-test", "192.0.2.10", "2001:db8::10", 0)

	ip6 := layers.IPv6{
		Version:    6,
		HopLimit:   64,
		NextHeader: layers.IPProtocolUDP,
		SrcIP:      net.ParseIP("2001:db8::10"),
		DstIP:      net.ParseIP("2001:db8::198.51.100.2"),
	}
	udp := layers.UDP{SrcPort: 12345, DstPort: 10053}
	require.NoError(t, udp.SetNetworkLayerForChecksum(&ip6))
	pkt := xpacket.LayersToPacket(t,
		&layers.Ethernet{
			SrcMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
			DstMAC:       xerror.Unwrap(net.ParseMAC("66:77:88:99:aa:bb")),
			EthernetType: layers.EthernetTypeIPv6,
		},
		&ip6, &udp, gopacket.Payload([]byte{1, 2, 3, 4}),
	)

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	out := parseOneOutput(t, result)
	require.NotNil(t, out.Layer(layers.LayerTypeUDP))

	ip4 := out.Layer(layers.LayerTypeIPv4).(*layers.IPv4)
	require.Equal(t, net.ParseIP("192.0.2.10").To4(), ip4.SrcIP.To4())
	require.Equal(t, net.ParseIP("198.51.100.2").To4(), ip4.DstIP.To4())
	udpOut := out.Layer(layers.LayerTypeUDP).(*layers.UDP)
	require.Equal(t, layers.UDPPort(12345), udpOut.SrcPort)
	require.Equal(t, layers.UDPPort(10053), udpOut.DstPort)
}

// TestNAT64_UDPv6NonFirstFragmentToIPv4 verifies non-first IPv6 UDP fragments
// are translated without transport-layer rewrites.
func TestNAT64_UDPv6NonFirstFragmentToIPv4(t *testing.T) {
	h, svc := setupNAT64Harness(t)
	mustAddPrefix(t, svc, "nat64-test", nat64Prefix96)
	mustAddMapping(t, svc, "nat64-test", "192.0.2.10", "2001:db8::10", 0)

	ip6 := layers.IPv6{
		Version:    6,
		HopLimit:   64,
		NextHeader: layers.IPProtocolIPv6Fragment,
		SrcIP:      net.ParseIP("2001:db8::10"),
		DstIP:      net.ParseIP("2001:db8::198.51.100.2"),
	}
	frag := layers.IPv6Fragment{
		NextHeader:     layers.IPProtocolUDP,
		FragmentOffset: 2,
		MoreFragments:  true,
		Identification: 0x11223344,
	}
	fragmentPayload := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	pkt := xpacket.LayersToPacket(
		t,
		&layers.Ethernet{
			SrcMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
			DstMAC:       xerror.Unwrap(net.ParseMAC("66:77:88:99:aa:bb")),
			EthernetType: layers.EthernetTypeIPv6,
		},
		&ip6,
		&frag,
		gopacket.Payload(fragmentPayload),
	)

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	out := parseOneOutput(t, result)

	ip4 := out.Layer(layers.LayerTypeIPv4).(*layers.IPv4)
	require.Equal(t, net.ParseIP("192.0.2.10").To4(), ip4.SrcIP.To4())
	require.Equal(t, net.ParseIP("198.51.100.2").To4(), ip4.DstIP.To4())
	require.Equal(t, layers.IPProtocolUDP, ip4.Protocol)
	require.Equal(t, uint16(28), ip4.Length)
	require.Equal(t, uint16(2), ip4.FragOffset)
	require.True(t, ip4.Flags&layers.IPv4MoreFragments != 0)
	require.Equal(t, fragmentPayload, ip4.Payload)
}

// TestNAT64_IPv6DestinationOptionsBeforeUDP verifies IPv6 packets with a valid
// destination options header before UDP are translated to IPv4 UDP.
func TestNAT64_IPv6DestinationOptionsBeforeUDP(t *testing.T) {
	h, svc := setupNAT64Harness(t)
	mustAddPrefix(t, svc, "nat64-test", nat64Prefix96)
	mustAddMapping(t, svc, "nat64-test", "192.0.2.10", "2001:db8::10", 0)

	srcIP := net.ParseIP("2001:db8::10")
	dstIP := net.ParseIP("2001:db8::198.51.100.2")
	ip6ForChecksum := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolUDP,
		HopLimit:   64,
		SrcIP:      srcIP,
		DstIP:      dstIP,
	}
	udp := layers.UDP{SrcPort: 12345, DstPort: 10053}
	require.NoError(t, udp.SetNetworkLayerForChecksum(&ip6ForChecksum))

	udpBuf := gopacket.NewSerializeBuffer()
	require.NoError(t, gopacket.SerializeLayers(
		udpBuf,
		gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true},
		&udp,
		gopacket.Payload([]byte{1, 2, 3, 4}),
	))

	// Destination options header: next header UDP(17), hdr_ext_len=0 (8 bytes).
	dstOptsHdr := []byte{
		byte(layers.IPProtocolUDP), 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00,
	}
	pkt := packetWithIPv6RawExt(
		t,
		srcIP,
		dstIP,
		byte(layers.IPProtocolIPv6Destination),
		append(dstOptsHdr, udpBuf.Bytes()...),
	)

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	out := parseOneOutput(t, result)
	require.NotNil(t, out.Layer(layers.LayerTypeUDP))

	ip4 := out.Layer(layers.LayerTypeIPv4).(*layers.IPv4)
	require.Equal(t, net.ParseIP("192.0.2.10").To4(), ip4.SrcIP.To4())
	require.Equal(t, net.ParseIP("198.51.100.2").To4(), ip4.DstIP.To4())
	require.Equal(t, layers.IPProtocolUDP, ip4.Protocol)
	require.Equal(t, uint16(32), ip4.Length)

	udpOut := out.Layer(layers.LayerTypeUDP).(*layers.UDP)
	require.Equal(t, layers.UDPPort(12345), udpOut.SrcPort)
	require.Equal(t, layers.UDPPort(10053), udpOut.DstPort)
}

// TestNAT64_TCPv4ToIPv6 verifies TCP translation from IPv4 to IPv6.
func TestNAT64_TCPv4ToIPv6(t *testing.T) {
	h, svc := setupNAT64Harness(t)
	mustAddPrefix(t, svc, "nat64-test", nat64Prefix96)
	mustAddMapping(t, svc, "nat64-test", "198.51.100.9", "2001:db8::9", 0)

	ip4 := layers.IPv4{Version: 4, TTL: 64, Protocol: layers.IPProtocolTCP, SrcIP: net.ParseIP("203.0.113.4"), DstIP: net.ParseIP("198.51.100.9")}
	tcp := layers.TCP{SrcPort: 40000, DstPort: 443, SYN: true, Seq: 100}
	require.NoError(t, tcp.SetNetworkLayerForChecksum(&ip4))
	pkt := xpacket.LayersToPacket(t,
		&layers.Ethernet{
			SrcMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
			DstMAC:       xerror.Unwrap(net.ParseMAC("66:77:88:99:aa:bb")),
			EthernetType: layers.EthernetTypeIPv4,
		},
		&ip4, &tcp,
	)

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	out := parseOneOutput(t, result)
	require.NotNil(t, out.Layer(layers.LayerTypeTCP))

	ip6 := out.Layer(layers.LayerTypeIPv6).(*layers.IPv6)
	require.Equal(t, mappedV6(nat64Prefix96, netip.MustParseAddr("203.0.113.4")), ip6.SrcIP)
	require.Equal(t, net.ParseIP("2001:db8::9"), ip6.DstIP)
	tcpOut := out.Layer(layers.LayerTypeTCP).(*layers.TCP)
	require.Equal(t, layers.TCPPort(40000), tcpOut.SrcPort)
	require.Equal(t, layers.TCPPort(443), tcpOut.DstPort)
	require.True(t, tcpOut.SYN)
}

// TestNAT64_TCPv6ToIPv4 verifies TCP translation from IPv6 to IPv4.
func TestNAT64_TCPv6ToIPv4(t *testing.T) {
	h, svc := setupNAT64Harness(t)
	mustAddPrefix(t, svc, "nat64-test", nat64Prefix96)
	mustAddMapping(t, svc, "nat64-test", "192.0.2.10", "2001:db8::10", 0)

	ip6 := layers.IPv6{
		Version:    6,
		HopLimit:   64,
		NextHeader: layers.IPProtocolTCP,
		SrcIP:      net.ParseIP("2001:db8::10"),
		DstIP:      net.ParseIP("2001:db8::198.51.100.2"),
	}
	tcp := layers.TCP{SrcPort: 25000, DstPort: 8443, ACK: true, Seq: 200, Ack: 100}
	require.NoError(t, tcp.SetNetworkLayerForChecksum(&ip6))
	pkt := xpacket.LayersToPacket(t,
		&layers.Ethernet{
			SrcMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
			DstMAC:       xerror.Unwrap(net.ParseMAC("66:77:88:99:aa:bb")),
			EthernetType: layers.EthernetTypeIPv6,
		},
		&ip6, &tcp,
	)

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	out := parseOneOutput(t, result)
	require.NotNil(t, out.Layer(layers.LayerTypeTCP))

	ip4 := out.Layer(layers.LayerTypeIPv4).(*layers.IPv4)
	require.Equal(t, net.ParseIP("192.0.2.10").To4(), ip4.SrcIP.To4())
	require.Equal(t, net.ParseIP("198.51.100.2").To4(), ip4.DstIP.To4())
	require.Equal(t, layers.IPProtocolTCP, ip4.Protocol)

	tcpOut := out.Layer(layers.LayerTypeTCP).(*layers.TCP)
	require.Equal(t, layers.TCPPort(25000), tcpOut.SrcPort)
	require.Equal(t, layers.TCPPort(8443), tcpOut.DstPort)
	require.True(t, tcpOut.ACK)
}

// TestNAT64_UDPv4ToIPv6 verifies UDP translation from IPv4 to IPv6.
func TestNAT64_UDPv4ToIPv6(t *testing.T) {
	h, svc := setupNAT64Harness(t)
	mustAddPrefix(t, svc, "nat64-test", nat64Prefix96)
	mustAddMapping(t, svc, "nat64-test", "198.51.100.9", "2001:db8::9", 0)

	ip4 := layers.IPv4{Version: 4, TTL: 64, Protocol: layers.IPProtocolUDP, SrcIP: net.ParseIP("203.0.113.4"), DstIP: net.ParseIP("198.51.100.9")}
	udp := layers.UDP{SrcPort: 12000, DstPort: 5353}
	require.NoError(t, udp.SetNetworkLayerForChecksum(&ip4))
	pkt := xpacket.LayersToPacket(t,
		&layers.Ethernet{
			SrcMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
			DstMAC:       xerror.Unwrap(net.ParseMAC("66:77:88:99:aa:bb")),
			EthernetType: layers.EthernetTypeIPv4,
		},
		&ip4, &udp, gopacket.Payload([]byte{9, 8, 7, 6}),
	)

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	out := parseOneOutput(t, result)
	require.NotNil(t, out.Layer(layers.LayerTypeUDP))

	ip6 := out.Layer(layers.LayerTypeIPv6).(*layers.IPv6)
	require.Equal(t, mappedV6(nat64Prefix96, netip.MustParseAddr("203.0.113.4")), ip6.SrcIP)
	require.Equal(t, net.ParseIP("2001:db8::9"), ip6.DstIP)
	require.Equal(t, layers.IPProtocolUDP, ip6.NextHeader)

	udpOut := out.Layer(layers.LayerTypeUDP).(*layers.UDP)
	require.Equal(t, layers.UDPPort(12000), udpOut.SrcPort)
	require.Equal(t, layers.UDPPort(5353), udpOut.DstPort)
}

// TestNAT64_UnknownMappingPassThrough verifies mapping-miss pass-through when
// drop_unknown_mapping is disabled.
func TestNAT64_UnknownMappingPassThrough(t *testing.T) {
	h, svc := setupNAT64Harness(t)
	mustAddPrefix(t, svc, "nat64-test", nat64Prefix96)

	ip6 := layers.IPv6{
		Version:    6,
		HopLimit:   64,
		NextHeader: layers.IPProtocolUDP,
		SrcIP:      net.ParseIP("2001:db8::dead"),
		DstIP:      net.ParseIP("2001:db8::203.0.113.2"),
	}
	udp := layers.UDP{SrcPort: 1111, DstPort: 2222}
	require.NoError(t, udp.SetNetworkLayerForChecksum(&ip6))
	pkt := xpacket.LayersToPacket(t,
		&layers.Ethernet{
			SrcMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
			DstMAC:       xerror.Unwrap(net.ParseMAC("66:77:88:99:aa:bb")),
			EthernetType: layers.EthernetTypeIPv6,
		},
		&ip6, &udp,
	)

	mustSetDropUnknown(t, svc, "nat64-test", false, false)
	passRes, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	out := parseOneOutput(t, passRes)
	ip6Out := out.Layer(layers.LayerTypeIPv6)
	require.NotNil(t, ip6Out)
	ip6Parsed := ip6Out.(*layers.IPv6)
	require.Equal(t, ip6.SrcIP, ip6Parsed.SrcIP)
	require.Equal(t, ip6.DstIP, ip6Parsed.DstIP)
	require.Equal(t, layers.IPProtocolUDP, ip6Parsed.NextHeader)
}

// TestNAT64_UnknownMappingDrop verifies mapping-miss drop when
// drop_unknown_mapping is enabled.
func TestNAT64_UnknownMappingDrop(t *testing.T) {
	h, svc := setupNAT64Harness(t)
	mustAddPrefix(t, svc, "nat64-test", nat64Prefix96)
	mustSetDropUnknown(t, svc, "nat64-test", false, true)

	ip6 := layers.IPv6{
		Version:    6,
		HopLimit:   64,
		NextHeader: layers.IPProtocolUDP,
		SrcIP:      net.ParseIP("2001:db8::dead"),
		DstIP:      net.ParseIP("2001:db8::203.0.113.2"),
	}
	udp := layers.UDP{SrcPort: 1111, DstPort: 2222}
	require.NoError(t, udp.SetNetworkLayerForChecksum(&ip6))
	pkt := xpacket.LayersToPacket(t,
		&layers.Ethernet{
			SrcMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
			DstMAC:       xerror.Unwrap(net.ParseMAC("66:77:88:99:aa:bb")),
			EthernetType: layers.EthernetTypeIPv6,
		},
		&ip6, &udp,
	)

	dropRes, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Empty(t, dropRes.Output)
	require.Len(t, dropRes.Drop, 1)
}

// TestNAT64_DropUnknownPrefix verifies drop_unknown_prefix applies to source
// IPv6 prefix when source mapping is missing in v6->v4 processing.
func TestNAT64_DropUnknownPrefix(t *testing.T) {
	h, svc := setupNAT64Harness(t)
	mustAddPrefix(t, svc, "nat64-test", nat64Prefix96)
	mustSetDropUnknown(t, svc, "nat64-test", true, false)

	ip6 := layers.IPv6{
		Version:    6,
		HopLimit:   64,
		NextHeader: layers.IPProtocolUDP,
		SrcIP:      net.ParseIP("2001:db9::dead"),
		DstIP:      net.ParseIP("2001:db8::203.0.113.2"),
	}
	udp := layers.UDP{SrcPort: 1111, DstPort: 2222}
	require.NoError(t, udp.SetNetworkLayerForChecksum(&ip6))
	pkt := xpacket.LayersToPacket(t,
		&layers.Ethernet{
			SrcMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
			DstMAC:       xerror.Unwrap(net.ParseMAC("66:77:88:99:aa:bb")),
			EthernetType: layers.EthernetTypeIPv6,
		},
		&ip6, &udp,
	)

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Empty(t, result.Output)
	require.Len(t, result.Drop, 1)
}

// TestNAT64_RepresentativeDropPaths verifies non-IP, fragmented ICMP, and IPv4
// source-route packets hit NAT64 drop paths.
func TestNAT64_RepresentativeDropPaths(t *testing.T) {
	h, svc := setupNAT64Harness(t)
	mustAddPrefix(t, svc, "nat64-test", nat64Prefix96)
	mustAddMapping(t, svc, "nat64-test", "192.0.2.30", "2001:db8::30", 0)

	nonIP := xpacket.LayersToPacket(t,
		&layers.Ethernet{
			SrcMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
			DstMAC:       xerror.Unwrap(net.ParseMAC("66:77:88:99:aa:bb")),
			EthernetType: layers.EthernetTypeARP,
		},
		gopacket.Payload([]byte{0, 1, 2}),
	)
	nonIPRes, err := h.HandlePackets(nonIP)
	require.NoError(t, err)
	require.Len(t, nonIPRes.Drop, 1)

	ip4FragICMP := layers.IPv4{Version: 4, TTL: 64, Flags: layers.IPv4MoreFragments, FragOffset: 1, Protocol: layers.IPProtocolICMPv4, SrcIP: net.ParseIP("192.0.2.20"), DstIP: net.ParseIP("192.0.2.30")}
	fragICMPPkt := xpacket.LayersToPacket(t,
		&layers.Ethernet{
			SrcMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
			DstMAC:       xerror.Unwrap(net.ParseMAC("66:77:88:99:aa:bb")),
			EthernetType: layers.EthernetTypeIPv4,
		},
		&ip4FragICMP,
		&layers.ICMPv4{TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0)},
		gopacket.Payload([]byte{0, 1, 2, 3, 4, 5, 6, 7}),
	)
	fragRes, err := h.HandlePackets(fragICMPPkt)
	require.NoError(t, err)
	require.Len(t, fragRes.Drop, 1)

	ip4WithSR := layers.IPv4{
		Version:  4,
		TTL:      64,
		IHL:      6,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    net.ParseIP("192.0.2.20"),
		DstIP:    net.ParseIP("192.0.2.30"),
		Options:  []layers.IPv4Option{{OptionType: 0x83, OptionLength: 4, OptionData: []byte{0, 0}}},
	}
	udp := layers.UDP{SrcPort: 1010, DstPort: 2020}
	require.NoError(t, udp.SetNetworkLayerForChecksum(&ip4WithSR))
	srPkt := xpacket.LayersToPacket(t,
		&layers.Ethernet{
			SrcMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
			DstMAC:       xerror.Unwrap(net.ParseMAC("66:77:88:99:aa:bb")),
			EthernetType: layers.EthernetTypeIPv4,
		},
		&ip4WithSR,
		&udp,
	)
	srRes, err := h.HandlePackets(srPkt)
	require.NoError(t, err)
	require.Len(t, srRes.Drop, 1)
}

// TestNAT64_IPv6ESPHeader_Dropped verifies packets with IPsec ESP extension
// header are dropped in IPv6 extension-header handling.
func TestNAT64_IPv6ESPHeader_Dropped(t *testing.T) {
	h, svc := setupNAT64Harness(t)
	mustAddPrefix(t, svc, "nat64-test", nat64Prefix96)
	mustAddMapping(t, svc, "nat64-test", "192.0.2.10", "2001:db8::10", 0)

	srcIP := net.ParseIP("2001:db8::10")
	dstIP := net.ParseIP("2001:db8::198.51.100.2")
	espPayload := []byte{0xde, 0xad, 0xbe, 0xef, 0, 0, 0, 1}
	pkt := packetWithIPv6RawExt(t,
		srcIP,
		dstIP,
		byte(layers.IPProtocolESP),
		espPayload,
	)

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Empty(t, result.Output)
	require.Len(t, result.Drop, 1)
}

// TestNAT64_IPv6RoutingHeaderType0_Dropped verifies Type 0 routing header is
// rejected by IPv6 extension-header processing.
func TestNAT64_IPv6RoutingHeaderType0_Dropped(t *testing.T) {
	h, svc := setupNAT64Harness(t)
	mustAddPrefix(t, svc, "nat64-test", nat64Prefix96)
	mustAddMapping(t, svc, "nat64-test", "192.0.2.10", "2001:db8::10", 0)

	srcIP := net.ParseIP("2001:db8::10")
	dstIP := net.ParseIP("2001:db8::198.51.100.2")
	udp := layers.UDP{SrcPort: 2000, DstPort: 3000}
	ip6ForChecksum := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolUDP,
		HopLimit:   64,
		SrcIP:      srcIP,
		DstIP:      dstIP,
	}
	require.NoError(t, udp.SetNetworkLayerForChecksum(&ip6ForChecksum))
	udpBuf := gopacket.NewSerializeBuffer()
	require.NoError(t, gopacket.SerializeLayers(
		udpBuf,
		gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true},
		&udp,
		gopacket.Payload([]byte{0xaa, 0xbb, 0xcc, 0xdd}),
	))
	// Routing header: next header UDP(17), hdr_ext_len=0, routing type=0.
	routingType0 := []byte{
		byte(layers.IPProtocolUDP), 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
	}
	pkt := packetWithIPv6RawExt(
		t,
		srcIP,
		dstIP,
		byte(layers.IPProtocolIPv6Routing),
		append(routingType0, udpBuf.Bytes()...),
	)

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Empty(t, result.Output)
	require.Len(t, result.Drop, 1)
}

// TestNAT64_ICMPv4FragNeededToICMPv6PacketTooBig verifies ICMPv4
// Fragmentation Needed is translated to ICMPv6 Packet Too Big.
func TestNAT64_ICMPv4FragNeededToICMPv6PacketTooBig(t *testing.T) {
	h, svc := setupNAT64Harness(t)
	mustAddPrefix(t, svc, "nat64-test", nat64Prefix96)
	mustAddMapping(t, svc, "nat64-test", "192.0.2.30", "2001:db8::30", 0)
	mustSetMTU(t, svc, "nat64-test", 1400, 1500)

	innerIP4 := layers.IPv4{
		Version:  4,
		TTL:      32,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    net.ParseIP("192.0.2.30"),
		DstIP:    net.ParseIP("198.51.100.20"),
	}
	innerUDP := layers.UDP{SrcPort: 1234, DstPort: 4321}
	require.NoError(t, innerUDP.SetNetworkLayerForChecksum(&innerIP4))
	innerBuf := gopacket.NewSerializeBuffer()
	require.NoError(t, gopacket.SerializeLayers(
		innerBuf,
		gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true},
		&innerIP4,
		&innerUDP,
	))
	inner := innerBuf.Bytes()
	require.GreaterOrEqual(t, len(inner), 28)

	icmp := make([]byte, 8+28)
	icmp[0] = byte(layers.ICMPv4TypeDestinationUnreachable)
	icmp[1] = 4
	binary.BigEndian.PutUint16(icmp[6:8], 1380)
	copy(icmp[8:], inner[:28])
	pkt := packetWithIPv4RawPayload(
		t,
		net.ParseIP("198.51.100.2"),
		net.ParseIP("192.0.2.30"),
		layers.IPProtocolICMPv4,
		icmp,
	)

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	out := parseOneOutput(t, result)
	ip6 := out.Layer(layers.LayerTypeIPv6).(*layers.IPv6)
	require.Equal(t, mappedV6(nat64Prefix96, netip.MustParseAddr("198.51.100.2")), ip6.SrcIP)
	require.Equal(t, net.ParseIP("2001:db8::30"), ip6.DstIP)

	l4 := ip6.Payload
	require.GreaterOrEqual(t, len(l4), 8)
	require.Equal(t, byte(2), l4[0]) // ICMPv6 Packet Too Big
	require.Equal(t, byte(0), l4[1])
	require.Equal(t, uint32(1400), binary.BigEndian.Uint32(l4[4:8]))
}

// TestNAT64_ICMPv6PacketTooBigToICMPv4FragNeeded verifies ICMPv6 Packet Too
// Big is translated to ICMPv4 Fragmentation Needed.
func TestNAT64_ICMPv6PacketTooBigToICMPv4FragNeeded(t *testing.T) {
	h, svc := setupNAT64Harness(t)
	mustAddPrefix(t, svc, "nat64-test", nat64Prefix96)
	mustAddMapping(t, svc, "nat64-test", "192.0.2.30", "2001:db8::30", 0)
	mustSetMTU(t, svc, "nat64-test", 1450, 1280)

	innerIP6 := layers.IPv6{
		Version:    6,
		HopLimit:   32,
		NextHeader: layers.IPProtocolUDP,
		SrcIP:      net.ParseIP("2001:db8::30"),
		DstIP:      net.ParseIP("2001:db8::198.51.100.2"),
	}
	innerUDP := layers.UDP{SrcPort: 2233, DstPort: 3344}
	require.NoError(t, innerUDP.SetNetworkLayerForChecksum(&innerIP6))
	innerBuf := gopacket.NewSerializeBuffer()
	require.NoError(t, gopacket.SerializeLayers(
		innerBuf,
		gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true},
		&innerIP6,
		&innerUDP,
	))
	inner := innerBuf.Bytes()
	require.GreaterOrEqual(t, len(inner), 48)

	icmp6 := make([]byte, 8+48)
	icmp6[0] = 2 // Packet Too Big
	icmp6[1] = 0
	binary.BigEndian.PutUint32(icmp6[4:8], 1400)
	copy(icmp6[8:], inner[:48])
	pkt := packetWithIPv6RawExt(
		t,
		net.ParseIP("2001:db8::30"),
		net.ParseIP("2001:db8::198.51.100.2"),
		byte(layers.IPProtocolICMPv6),
		icmp6,
	)

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	out := parseOneOutput(t, result)
	ip4 := out.Layer(layers.LayerTypeIPv4).(*layers.IPv4)
	require.Equal(t, net.ParseIP("192.0.2.30").To4(), ip4.SrcIP.To4())
	require.Equal(t, net.ParseIP("198.51.100.2").To4(), ip4.DstIP.To4())

	l4 := ip4.Payload
	require.GreaterOrEqual(t, len(l4), 8)
	require.Equal(t, byte(layers.ICMPv4TypeDestinationUnreachable), l4[0])
	require.Equal(t, byte(4), l4[1])
	require.Equal(t, uint16(1260), binary.BigEndian.Uint16(l4[6:8]))
}

// TestNAT64_ICMPv6PacketTooBigMTUFloor verifies that the ICMPv4 next-hop MTU
// derived from an ICMPv6 Packet Too Big never underflows below the IPv4
// minimum, regardless of the attacker-controlled wire MTU value.
func TestNAT64_ICMPv6PacketTooBigMTUFloor(t *testing.T) {
	tests := []struct {
		name    string
		wireMTU uint32
		cfg4    uint32
		cfg6    uint32
		want    uint16
	}{
		{name: "underflow_1", wireMTU: 1, cfg4: 1450, cfg6: 1280, want: 68},
		{name: "underflow_19", wireMTU: 19, cfg4: 1450, cfg6: 1280, want: 68},
		{name: "equals_header_delta_20", wireMTU: 20, cfg4: 1450, cfg6: 1280, want: 68},
		{name: "below_ipv4_minimum_21", wireMTU: 21, cfg4: 1450, cfg6: 1280, want: 68},
		{name: "exactly_at_floor_88", wireMTU: 88, cfg4: 1450, cfg6: 1280, want: 68},
		{name: "just_above_floor_89", wireMTU: 89, cfg4: 1450, cfg6: 1280, want: 69},
		{name: "normal_100", wireMTU: 100, cfg4: 1450, cfg6: 1280, want: 80},
		{name: "clamped_by_ipv6_mtu_1400", wireMTU: 1400, cfg4: 1450, cfg6: 1280, want: 1260},
		{name: "rfc1191_remap_wire0", wireMTU: 0, cfg4: 1450, cfg6: 1280, want: 1260},
		{name: "ipv4_unset_wire0_falls_back_to_ipv6", wireMTU: 0, cfg4: 0, cfg6: 1280, want: 1260},
		{name: "both_config_zero_wire0", wireMTU: 0, cfg4: 0, cfg6: 0, want: 68},
		{name: "cap_at_uint16_max", wireMTU: 100000, cfg4: 0, cfg6: 0, want: 65535},
		{name: "tiny_ipv6_config_clamps_to_floor", wireMTU: 1400, cfg4: 1450, cfg6: 10, want: 68},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, svc := setupNAT64Harness(t)
			mustAddPrefix(t, svc, "nat64-test", nat64Prefix96)
			mustAddMapping(t, svc, "nat64-test", "192.0.2.30", "2001:db8::30", 0)
			mustSetMTU(t, svc, "nat64-test", tt.cfg4, tt.cfg6)

			innerIP6 := layers.IPv6{
				Version:    6,
				HopLimit:   32,
				NextHeader: layers.IPProtocolUDP,
				SrcIP:      net.ParseIP("2001:db8::30"),
				DstIP:      net.ParseIP("2001:db8::198.51.100.2"),
			}
			innerUDP := layers.UDP{SrcPort: 2233, DstPort: 3344}
			require.NoError(t, innerUDP.SetNetworkLayerForChecksum(&innerIP6))
			innerBuf := gopacket.NewSerializeBuffer()
			require.NoError(t, gopacket.SerializeLayers(
				innerBuf,
				gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true},
				&innerIP6,
				&innerUDP,
			))
			inner := innerBuf.Bytes()
			require.GreaterOrEqual(t, len(inner), 48)

			icmp6 := make([]byte, 8+48)
			icmp6[0] = 2 // Packet Too Big
			icmp6[1] = 0
			binary.BigEndian.PutUint32(icmp6[4:8], tt.wireMTU)
			copy(icmp6[8:], inner[:48])
			pkt := packetWithIPv6RawExt(
				t,
				net.ParseIP("2001:db8::30"),
				net.ParseIP("2001:db8::198.51.100.2"),
				byte(layers.IPProtocolICMPv6),
				icmp6,
			)

			result, err := h.HandlePackets(pkt)
			require.NoError(t, err)
			out := parseOneOutput(t, result)
			ip4 := out.Layer(layers.LayerTypeIPv4).(*layers.IPv4)
			require.Equal(t, net.ParseIP("192.0.2.30").To4(), ip4.SrcIP.To4())
			require.Equal(t, net.ParseIP("198.51.100.2").To4(), ip4.DstIP.To4())

			l4 := ip4.Payload
			require.GreaterOrEqual(t, len(l4), 8)
			require.Equal(t, byte(layers.ICMPv4TypeDestinationUnreachable), l4[0])
			require.Equal(t, byte(4), l4[1])
			// RFC792 unused field must always be zero.
			require.Equal(t, uint16(0), binary.BigEndian.Uint16(l4[4:6]))
			require.Equal(t, tt.want, binary.BigEndian.Uint16(l4[6:8]))
		})
	}
}

const (
	ipv6ProtocolHopOptions              = 0
	ipv6ProtocolFragment                = 44
	ipv6ProtocolNone                    = 59
	ipProtocolICMPv6                    = 58
	ipProtocolUDP                       = 17
	ipProtocolTCP                       = 6
	icmpv6DestinationUnreachable        = 1
	icmpv6DestinationUnreachableNoRoute = 0
	icmpv6EchoRequest                   = 128
	etherTypeIPv6                       = 0x86dd
)

var (
	nat64EmbeddedOuterIPv4 = [4]byte{192, 0, 2, 34}
	nat64EmbeddedMappings  = []struct {
		IPv4 netip.Addr
		IPv6 netip.Addr
	}{
		{netip.MustParseAddr("198.51.100.1"), netip.MustParseAddr("2001:db8::4")},
		{netip.MustParseAddr("198.51.100.2"), netip.MustParseAddr("2001:db8::3")},
		{netip.MustParseAddr("198.51.100.3"), netip.MustParseAddr("2001:db8::2")},
		{netip.MustParseAddr("198.51.100.4"), netip.MustParseAddr("2001:db8::1")},
		{netip.MustParseAddr("198.51.100.5"), netip.MustParseAddr("2001:db8::8")},
		{netip.MustParseAddr("198.51.100.6"), netip.MustParseAddr("2001:db8::7")},
		{netip.MustParseAddr("198.51.100.7"), netip.MustParseAddr("2001:db8::6")},
		{netip.MustParseAddr("198.51.100.8"), netip.MustParseAddr("2001:db8::5")},
	}
)

// setupNAT64EmbeddedHarness publishes eight mappings under a documentation prefix.
func setupNAT64EmbeddedHarness(t *testing.T) (*dataplaneut.Harness, *nat64.NAT64Service) {
	t.Helper()
	harness, service := setupNAT64Harness(t)
	mustAddPrefix(t, service, "nat64-test", nat64Prefix96)
	for _, mapping := range nat64EmbeddedMappings {
		mustAddMapping(t, service, "nat64-test", mapping.IPv4.String(), mapping.IPv6.String(), 0)
	}
	return harness, service
}

// nat64EmbeddedOuterDestination embeds the external IPv4 address in the prefix.
func nat64EmbeddedOuterDestination() [16]byte {
	address := nat64Prefix96.Addr().As16()
	copy(address[12:], nat64EmbeddedOuterIPv4[:])
	return address
}

// runNAT64EmbeddedPacket preserves raw bytes and checks native pool ownership.
func runNAT64EmbeddedPacket(t *testing.T, harness *dataplaneut.Harness, packet []byte) *dataplaneut.RawResult {
	t.Helper()
	outstanding := harness.OutstandingMbufs()
	result, err := harness.HandleSegmentedPackets([][]byte{packet})
	require.NoError(t, err)
	require.Equal(t, outstanding, harness.OutstandingMbufs())
	return result
}

// readNAT64MalformedPackets reads the active module's malformed diagnostic.
func readNAT64MalformedPackets(t *testing.T, harness *dataplaneut.Harness) uint64 {
	t.Helper()
	value, err := cnat64.MalformedPackets(harness.SharedMemory(), 0, "nat64-test")
	require.NoError(t, err)
	return value
}

// putEther appends a broadcast IPv6 frame header with a fixed source address.
func putEther(buffer []byte) []byte {
	buffer = append(buffer, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff)
	buffer = append(buffer, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00)
	return binary.BigEndian.AppendUint16(buffer, etherTypeIPv6)
}

// putIPv6 appends a fixed-hop-limit header with the declared payload length.
func putIPv6(buffer []byte, payloadLength uint16, protocol uint8, source, destination [16]byte) []byte {
	buffer = binary.BigEndian.AppendUint32(buffer, 0x60000000)
	buffer = binary.BigEndian.AppendUint16(buffer, payloadLength)
	buffer = append(buffer, protocol, 64)
	buffer = append(buffer, source[:]...)
	buffer = append(buffer, destination[:]...)
	return buffer
}

// putICMPv6Header appends eight bytes with zero checksum and reserved fields.
//
// Packet parsing accepts the unchecked checksum, preserving malformed payloads.
func putICMPv6Header(buffer []byte, icmpType, code uint8) []byte {
	buffer = append(buffer, icmpType, code)
	buffer = binary.BigEndian.AppendUint16(buffer, 0)
	return binary.BigEndian.AppendUint32(buffer, 0)
}

// putICMPv6Error wraps an unchanged embedded packet in an IPv6 error frame.
func putICMPv6Error(buffer, embedded []byte) []byte {
	buffer = putEther(buffer)
	buffer = putIPv6(buffer, uint16(8+len(embedded)), ipProtocolICMPv6, nat64EmbeddedMappings[0].IPv6.As16(), nat64EmbeddedOuterDestination())
	buffer = putICMPv6Header(buffer, icmpv6DestinationUnreachable, icmpv6DestinationUnreachableNoRoute)
	return append(buffer, embedded...)
}

// Test_NAT64_ICMPv6EmbeddedFragTruncatedDropped verifies that a one-byte
// embedded Fragment header is dropped and counted as malformed.
func Test_NAT64_ICMPv6EmbeddedFragTruncatedDropped(t *testing.T) {
	harness, _ := setupNAT64EmbeddedHarness(t)

	// Embedded original packet: an IPv6 header announcing a Fragment
	// header, followed by exactly one byte of it.
	embedded := putIPv6(nil, 1, ipv6ProtocolFragment, nat64EmbeddedMappings[0].IPv6.As16(), nat64EmbeddedMappings[1].IPv6.As16())
	embedded = append(embedded, ipv6ProtocolNone)
	require.Len(t, embedded, 41)

	packet := putEther(nil)
	packet = putIPv6(packet, uint16(8+len(embedded)), ipProtocolICMPv6, nat64EmbeddedMappings[0].IPv6.As16(), nat64EmbeddedOuterDestination())
	packet = putICMPv6Header(packet, icmpv6DestinationUnreachable, icmpv6DestinationUnreachableNoRoute)
	packet = append(packet, embedded...)

	result := runNAT64EmbeddedPacket(t, harness, packet)

	require.Empty(t, result.Output, "truncated embedded Fragment header must not be forwarded")
	require.Len(t, result.Drop, 1, "expected a drop for the truncated embedded Fragment header")
	require.EqualValues(t, 1, readNAT64MalformedPackets(t, harness))
}

// Test_NAT64_ICMPv6EmbeddedExtTruncatedCountedMalformed verifies that a
// one-byte embedded Hop-by-Hop header is dropped and counted as malformed.
//
// Dropping alone does not prove that the bounds guard rejected the short header.
func Test_NAT64_ICMPv6EmbeddedExtTruncatedCountedMalformed(t *testing.T) {
	harness, _ := setupNAT64EmbeddedHarness(t)

	embedded := putIPv6(nil, 1, ipv6ProtocolHopOptions, nat64EmbeddedMappings[0].IPv6.As16(), nat64EmbeddedMappings[1].IPv6.As16())
	embedded = append(embedded, 0)
	require.Len(t, embedded, 41)

	packet := putEther(nil)
	packet = putIPv6(packet, uint16(8+len(embedded)), ipProtocolICMPv6, nat64EmbeddedMappings[0].IPv6.As16(), nat64EmbeddedOuterDestination())
	packet = putICMPv6Header(packet, icmpv6DestinationUnreachable, icmpv6DestinationUnreachableNoRoute)
	packet = append(packet, embedded...)

	result := runNAT64EmbeddedPacket(t, harness, packet)

	require.Empty(t, result.Output, "truncated embedded extension header must not be forwarded")
	require.Len(t, result.Drop, 1, "expected a drop for the truncated embedded extension header")
	require.EqualValues(t, 1, readNAT64MalformedPackets(t, harness))
}

// Test_NAT64_ICMPv6EmbeddedICMPv6TruncatedCountedMalformed verifies that
// a complete Hop-by-Hop header followed by four ICMPv6 bytes is malformed.
func Test_NAT64_ICMPv6EmbeddedICMPv6TruncatedCountedMalformed(t *testing.T) {
	harness, _ := setupNAT64EmbeddedHarness(t)

	// A complete Hop-by-Hop header reaches the embedded ICMPv6 guard.
	embedded := putIPv6(nil, 12, ipv6ProtocolHopOptions, nat64EmbeddedMappings[0].IPv6.As16(), nat64EmbeddedMappings[1].IPv6.As16())
	embedded = append(embedded, ipProtocolICMPv6, 0, 0, 0, 0, 0, 0, 0)
	embedded = append(embedded, icmpv6EchoRequest, 0, 0, 0)
	require.Len(t, embedded, 52)

	packet := putICMPv6Error(nil, embedded)
	result := runNAT64EmbeddedPacket(t, harness, packet)

	require.Empty(t, result.Output, "truncated embedded ICMPv6 header must not be forwarded")
	require.Len(t, result.Drop, 1, "expected a drop for the truncated embedded ICMPv6 header")
	require.EqualValues(t, 1, readNAT64MalformedPackets(t, harness))
}

// Test_NAT64_ICMPv6EmbeddedUDPTruncatedCountedMalformed verifies that
// a four-byte embedded UDP header is dropped and counted as malformed.
func Test_NAT64_ICMPv6EmbeddedUDPTruncatedCountedMalformed(t *testing.T) {
	harness, _ := setupNAT64EmbeddedHarness(t)

	embedded := putIPv6(nil, 4, ipProtocolUDP, nat64EmbeddedMappings[0].IPv6.As16(), nat64EmbeddedMappings[1].IPv6.As16())
	embedded = append(embedded, 0, 0, 0, 0)
	require.Len(t, embedded, 44)

	packet := putICMPv6Error(nil, embedded)
	result := runNAT64EmbeddedPacket(t, harness, packet)

	require.Empty(t, result.Output, "truncated embedded UDP header must not be forwarded")
	require.Len(t, result.Drop, 1, "expected a drop for the truncated embedded UDP header")
	require.EqualValues(t, 1, readNAT64MalformedPackets(t, harness))
}

// Test_NAT64_ICMPv6EmbeddedTCPTruncatedCountedMalformed verifies that
// a twelve-byte embedded TCP header is dropped and counted as malformed.
func Test_NAT64_ICMPv6EmbeddedTCPTruncatedCountedMalformed(t *testing.T) {
	harness, _ := setupNAT64EmbeddedHarness(t)

	embedded := putIPv6(nil, 12, ipProtocolTCP, nat64EmbeddedMappings[0].IPv6.As16(), nat64EmbeddedMappings[1].IPv6.As16())
	embedded = append(embedded, make([]byte, 12)...)
	require.Len(t, embedded, 52)

	packet := putICMPv6Error(nil, embedded)
	result := runNAT64EmbeddedPacket(t, harness, packet)

	require.Empty(t, result.Output, "truncated embedded TCP header must not be forwarded")
	require.Len(t, result.Drop, 1, "expected a drop for the truncated embedded TCP header")
	require.EqualValues(t, 1, readNAT64MalformedPackets(t, harness))
}

// Test_NAT64_ICMPv6EmbeddedExtFragTranslated verifies that complete
// embedded extensions preserve translated addresses and fragment fields.
func Test_NAT64_ICMPv6EmbeddedExtFragTranslated(t *testing.T) {
	harness, _ := setupNAT64EmbeddedHarness(t)

	// Six option bytes complete an eight-byte Hop-by-Hop header.
	hopOptions := []byte{ipv6ProtocolFragment, 0, 0, 0, 0, 0, 0, 0}

	fragment := []byte{ipProtocolICMPv6, 0}
	fragment = binary.BigEndian.AppendUint16(fragment, (2<<3)|1)
	fragment = binary.BigEndian.AppendUint32(fragment, 0x12345678)

	embeddedICMPv6 := putICMPv6Header(nil, icmpv6EchoRequest, 0)

	embeddedPayload := append(append([]byte{}, hopOptions...), fragment...)
	embeddedPayload = append(embeddedPayload, embeddedICMPv6...)

	embedded := putIPv6(nil, uint16(len(embeddedPayload)), ipv6ProtocolHopOptions, nat64EmbeddedMappings[0].IPv6.As16(), nat64EmbeddedMappings[1].IPv6.As16())
	embedded = append(embedded, embeddedPayload...)

	packet := putEther(nil)
	packet = putIPv6(packet, uint16(8+len(embedded)), ipProtocolICMPv6, nat64EmbeddedMappings[0].IPv6.As16(), nat64EmbeddedOuterDestination())
	packet = putICMPv6Header(packet, icmpv6DestinationUnreachable, icmpv6DestinationUnreachableNoRoute)
	packet = append(packet, embedded...)

	result := runNAT64EmbeddedPacket(t, harness, packet)

	require.Len(t, result.Output, 1, "expected the translated ICMPv6 error to be forwarded")
	require.Empty(t, result.Drop, "well-formed embedded extension and Fragment headers must not drop")
	require.EqualValues(t, 0, readNAT64MalformedPackets(t, harness))

	output := result.Output[0]
	require.GreaterOrEqual(t, len(output), 14+20)

	// The outer translated IPv4 header follows the 14-byte Ethernet header;
	// its source and destination are its last 8 bytes.
	outerIPv4 := output[14 : 14+20]
	require.Equal(t, []byte{198, 51, 100, 1}, outerIPv4[12:16], "translated outer IPv4 source is wrong")
	require.Equal(t, nat64EmbeddedOuterIPv4[:], outerIPv4[16:20], "translated outer IPv4 destination is wrong")

	// The translated embedded IPv4 header follows ether(14) + ipv4(20) +
	// icmp(8) in the output packet.
	const embeddedIPv4Offset = 14 + 20 + 8
	require.GreaterOrEqual(t, len(output), embeddedIPv4Offset+20)

	embeddedIPv4 := output[embeddedIPv4Offset : embeddedIPv4Offset+20]
	packetID := binary.BigEndian.Uint16(embeddedIPv4[4:6])
	fragmentOffset := binary.BigEndian.Uint16(embeddedIPv4[6:8])

	// These expectations preserve existing fragment-encoding deviations during
	// the harness migration.
	//
	// Identification narrowing retains the high-order half, and the offset uses
	// bytes instead of eight-octet units. Correcting either protocol deviation is
	// a separate behavior change and must update these expectations.
	require.EqualValues(t, 0x1234, packetID, "translated embedded Fragment identification is wrong")
	require.EqualValues(t, (2<<3)|0x2000, fragmentOffset, "translated embedded Fragment offset is wrong")
}

// Test_NAT64_MalformedDiagnosticFollowsPublishedConfig verifies that replacing
// a module resets its diagnostic and subsequent packets count on the new config.
func Test_NAT64_MalformedDiagnosticFollowsPublishedConfig(t *testing.T) {
	harness, service := setupNAT64EmbeddedHarness(t)
	embedded := putIPv6(nil, 4, ipProtocolUDP, nat64EmbeddedMappings[0].IPv6.As16(), nat64EmbeddedMappings[1].IPv6.As16())
	embedded = append(embedded, 0, 0, 0, 0)
	packet := putICMPv6Error(nil, embedded)

	result := runNAT64EmbeddedPacket(t, harness, packet)
	require.Empty(t, result.Output)
	require.Len(t, result.Drop, 1)
	require.EqualValues(t, 1, readNAT64MalformedPackets(t, harness))

	generation := harness.PublishedGeneration()
	mustSetMTU(t, service, "nat64-test", 1500, 1500)
	require.Greater(t, harness.PublishedGeneration(), generation)
	require.Zero(t, readNAT64MalformedPackets(t, harness))

	result = runNAT64EmbeddedPacket(t, harness, packet)
	require.Empty(t, result.Output)
	require.Len(t, result.Drop, 1)
	require.EqualValues(t, 1, readNAT64MalformedPackets(t, harness))
}
