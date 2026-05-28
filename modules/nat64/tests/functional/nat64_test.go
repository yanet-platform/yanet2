package nat64_test

import (
	"encoding/binary"
	"net"
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	nat64 "github.com/yanet-platform/yanet2/modules/nat64/controlplane"
	"github.com/yanet-platform/yanet2/modules/nat64/controlplane/nat64pb"
)

const (
	nat64CPSize  = 64 * datasize.MB
	nat64DPSize  = 4 * datasize.MB
	nat64MemSize = 8 * datasize.MB
)

var nat64Prefix96 = [12]byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0}

func setupNAT64Harness(t *testing.T) (*dataplaneut.Harness, *nat64.NAT64Service) {
	t.Helper()

	h, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(nat64CPSize),
		DPMemory:      uint64(nat64DPSize),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		Modules:       []string{"nat64"},
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

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: name,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: name + "_chain",
				Modules: []ffi.ChainModuleConfig{{
					Type: "nat64",
					Name: name,
				}},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: name, Functions: []string{name}}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "dummy"}))
	require.NoError(t, agent.UpdatePlainDevices([]ffi.DeviceConfig{{
		Name:   "port0",
		Input:  []ffi.DevicePipelineConfig{{Name: name, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}}))
}

func mustAddPrefix(t *testing.T, svc *nat64.NAT64Service, name string, prefix [12]byte) {
	t.Helper()
	_, err := svc.AddPrefix(t.Context(), &nat64pb.AddPrefixRequest{Name: name, Prefix: prefix[:]})
	require.NoError(t, err)
}

func mustAddMapping(t *testing.T, svc *nat64.NAT64Service, name string, ip4, ip6 string, prefixIdx uint32) {
	t.Helper()
	_, err := svc.AddMapping(t.Context(), &nat64pb.AddMappingRequest{
		Name:        name,
		Ipv4:        commonpb.NewIPAddressFromAddr(netip.MustParseAddr(ip4)),
		Ipv6:        commonpb.NewIPAddressFromAddr(netip.MustParseAddr(ip6)),
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

func mappedV6(prefix [12]byte, ip4 netip.Addr) net.IP {
	ip4b := ip4.As4()
	out := make([]byte, 16)
	copy(out[:12], prefix[:])
	copy(out[12:], ip4b[:])
	return out
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
