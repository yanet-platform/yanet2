package balancer

import (
	"net"
	"net/netip"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"google.golang.org/protobuf/types/known/durationpb"
)

////////////////////////////////////////////////////////////////////////////////
// Helper functions for creating ICMP packets
////////////////////////////////////////////////////////////////////////////////

// MakeICMPv4EchoRequest creates an ICMPv4 Echo Request packet
func MakeICMPv4EchoRequest(
	srcIP netip.Addr,
	dstIP netip.Addr,
	id uint16,
	seq uint16,
) []gopacket.SerializableLayer {
	src := net.IP(srcIP.AsSlice())
	dst := net.IP(dstIP.AsSlice())

	eth := &layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: layers.EthernetTypeIPv4,
	}

	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    src,
		DstIP:    dst,
	}

	icmp := &layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
		Id:       id,
		Seq:      seq,
	}

	payload := []byte("ICMP Echo Request Payload")

	return []gopacket.SerializableLayer{
		eth,
		ip,
		icmp,
		gopacket.Payload(payload),
	}
}

// MakeICMPv6EchoRequest creates an ICMPv6 Echo Request packet
func MakeICMPv6EchoRequest(
	srcIP netip.Addr,
	dstIP netip.Addr,
	id uint16,
	seq uint16,
) []gopacket.SerializableLayer {
	src := net.IP(srcIP.AsSlice())
	dst := net.IP(dstIP.AsSlice())

	eth := &layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: layers.EthernetTypeIPv6,
	}

	ip := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   64,
		SrcIP:      src,
		DstIP:      dst,
	}

	icmp := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
	}
	icmp.SetNetworkLayerForChecksum(ip)

	// ICMPv6 Echo uses the same ID/Seq format as ICMPv4
	payload := make([]byte, 4+len("ICMP Echo Request Payload"))
	payload[0] = byte(id >> 8)
	payload[1] = byte(id)
	payload[2] = byte(seq >> 8)
	payload[3] = byte(seq)
	copy(payload[4:], []byte("ICMP Echo Request Payload"))

	return []gopacket.SerializableLayer{
		eth,
		ip,
		icmp,
		gopacket.Payload(payload),
	}
}

// MakeICMPv4DestUnreachable creates an ICMPv4 Destination Unreachable error packet
// containing the original packet that triggered the error
func MakeICMPv4DestUnreachable(
	srcIP netip.Addr,
	dstIP netip.Addr,
	originalPacket gopacket.Packet,
) []gopacket.SerializableLayer {
	src := net.IP(srcIP.AsSlice())
	dst := net.IP(dstIP.AsSlice())

	eth := &layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: layers.EthernetTypeIPv4,
	}

	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    src,
		DstIP:    dst,
	}

	icmp := &layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(
			layers.ICMPv4TypeDestinationUnreachable,
			3,
		), // Port unreachable
	}

	// Extract the full original IP packet for the balancer to process
	// The balancer needs the complete packet to validate and look up sessions
	originalData := originalPacket.Data()
	// Find the IP layer start (skip Ethernet header)
	ipStart := 14 // Ethernet header size
	if ipStart < len(originalData) {
		// Include the entire IP packet
		payload := originalData[ipStart:]
		return []gopacket.SerializableLayer{
			eth,
			ip,
			icmp,
			gopacket.Payload(payload),
		}
	}

	return []gopacket.SerializableLayer{
		eth,
		ip,
		icmp,
		gopacket.Payload([]byte{}),
	}
}

// MakeICMPv6DestUnreachable creates an ICMPv6 Destination Unreachable error packet
// containing the original packet that triggered the error
func MakeICMPv6DestUnreachable(
	srcIP netip.Addr,
	dstIP netip.Addr,
	originalPacket gopacket.Packet,
) []gopacket.SerializableLayer {
	src := net.IP(srcIP.AsSlice())
	dst := net.IP(dstIP.AsSlice())

	eth := &layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: layers.EthernetTypeIPv6,
	}

	ip := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   64,
		SrcIP:      src,
		DstIP:      dst,
	}

	icmp := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(
			layers.ICMPv6TypeDestinationUnreachable,
			4,
		), // Port unreachable
	}
	icmp.SetNetworkLayerForChecksum(ip)

	// Extract the full original IP packet for the balancer to process
	// The balancer needs the complete packet to validate and look up sessions
	originalData := originalPacket.Data()
	// Find the IP layer start (skip Ethernet header)
	ipStart := 14 // Ethernet header size
	if ipStart < len(originalData) {
		// ICMPv6 error messages have a 4-byte unused field after the ICMP header
		// before the original packet data (RFC 4443)
		unused := []byte{0, 0, 0, 0}
		// Include the entire IP packet
		originalIPPacket := originalData[ipStart:]
		payload := append(unused, originalIPPacket...)
		return []gopacket.SerializableLayer{
			eth,
			ip,
			icmp,
			gopacket.Payload(payload),
		}
	}

	return []gopacket.SerializableLayer{
		eth,
		ip,
		icmp,
		gopacket.Payload([]byte{}),
	}
}

////////////////////////////////////////////////////////////////////////////////
// Test: ICMP Echo Request/Reply for IPv4 and IPv6
////////////////////////////////////////////////////////////////////////////////

func TestICMPEchoRequest(t *testing.T) {
	vsIPv4 := IpAddr("10.1.1.1")
	clientIPv4 := IpAddr("10.0.1.1")
	vsIPv6 := IpAddr("2001:db8::1")
	clientIPv6 := IpAddr("2001:db8:1::1")

	config := &balancerpb.ModuleConfig{
		SourceAddressV4: IpAddr("5.5.5.5").AsSlice(),
		SourceAddressV6: IpAddr("fe80::5").AsSlice(),
		VirtualServices: []*balancerpb.VirtualService{
			{
				Addr:  vsIPv4.AsSlice(),
				Port:  80,
				Proto: balancerpb.TransportProto_TCP,
				AllowedSrcs: []*balancerpb.Subnet{
					{
						Addr: IpAddr("10.0.0.0").AsSlice(),
						Size: 8,
					},
				},
				Scheduler: balancerpb.VsScheduler_PRR,
				Flags: &balancerpb.VsFlags{
					Gre:    false,
					FixMss: false,
					Ops:    false,
					PureL3: false,
				},
				Reals: []*balancerpb.Real{
					{
						DstAddr: IpAddr("10.2.2.2").AsSlice(),
						Weight:  1,
						SrcAddr: IpAddr("10.2.2.2").AsSlice(),
						SrcMask: IpAddr("255.255.255.255").AsSlice(),
						Enabled: true,
					},
				},
			},
			{
				Addr:  vsIPv6.AsSlice(),
				Port:  80,
				Proto: balancerpb.TransportProto_TCP,
				AllowedSrcs: []*balancerpb.Subnet{
					{
						Addr: IpAddr("2001:db8::").AsSlice(),
						Size: 32,
					},
				},
				Scheduler: balancerpb.VsScheduler_PRR,
				Flags: &balancerpb.VsFlags{
					Gre:    false,
					FixMss: false,
					Ops:    false,
					PureL3: false,
				},
				Reals: []*balancerpb.Real{
					{
						DstAddr: IpAddr("2001:db8:2::2").AsSlice(),
						Weight:  1,
						SrcAddr: IpAddr("2001:db8:2::2").AsSlice(),
						SrcMask: IpAddr(
							"ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff",
						).AsSlice(),
						Enabled: true,
					},
				},
			},
		},
		SessionsTimeouts: &balancerpb.SessionsTimeouts{
			TcpSynAck: 60,
			TcpSyn:    60,
			TcpFin:    60,
			Tcp:       60,
			Udp:       60,
			Default:   60,
		},
		Wlc: &balancerpb.WlcConfig{
			WlcPower:      10,
			MaxRealWeight: 1000,
			UpdatePeriod:  durationpb.New(0),
		},
	}

	setup, err := SetupTest(&TestConfig{
		moduleConfig: config,
		stateConfig: &balancerpb.ModuleStateConfig{
			SessionTableCapacity:      100,
			SessionTableScanPeriod:    durationpb.New(0),
			SessionTableMaxLoadFactor: 0.5,
		},
	})
	require.NoError(t, err)
	defer setup.Free()

	t.Run("IPv4", func(t *testing.T) {
		// Create ICMP Echo Request
		packetLayers := MakeICMPv4EchoRequest(clientIPv4, vsIPv4, 1234, 1)
		packet := xpacket.LayersToPacket(t, packetLayers...)

		// Send packet
		result, err := setup.mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output), "should have one output packet")
		require.Empty(t, result.Drop, "should not drop packet")

		// Parse response
		responsePacket := gopacket.NewPacket(
			result.Output[0].RawData,
			layers.LayerTypeEthernet,
			gopacket.Default,
		)

		// Verify it's an ICMP Echo Reply
		icmpLayer := responsePacket.Layer(layers.LayerTypeICMPv4)
		require.NotNil(t, icmpLayer, "response should have ICMPv4 layer")

		icmp := icmpLayer.(*layers.ICMPv4)
		assert.Equal(
			t,
			uint8(layers.ICMPv4TypeEchoReply),
			uint8(icmp.TypeCode.Type()),
			"should be Echo Reply",
		)
		assert.Equal(
			t,
			uint8(0),
			uint8(icmp.TypeCode.Code()),
			"code should be 0",
		)
		assert.Equal(t, uint16(1234), icmp.Id, "ID should match request")
		assert.Equal(t, uint16(1), icmp.Seq, "sequence should match request")

		// Verify IP addresses are swapped
		ipLayer := responsePacket.Layer(layers.LayerTypeIPv4)
		require.NotNil(t, ipLayer, "response should have IPv4 layer")

		ip := ipLayer.(*layers.IPv4)
		assert.Equal(
			t,
			net.IP(vsIPv4.AsSlice()),
			ip.SrcIP,
			"src IP should be VS IP",
		)
		assert.Equal(
			t,
			net.IP(clientIPv4.AsSlice()),
			ip.DstIP,
			"dst IP should be client IP",
		)
		assert.Equal(t, uint8(64), ip.TTL, "TTL should be reset to 64")
	})

	t.Run("IPv6", func(t *testing.T) {
		// Create ICMPv6 Echo Request
		packetLayers := MakeICMPv6EchoRequest(clientIPv6, vsIPv6, 5678, 2)
		packet := xpacket.LayersToPacket(t, packetLayers...)

		// Send packet
		result, err := setup.mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output), "should have one output packet")
		require.Empty(t, result.Drop, "should not drop packet")

		// Parse response
		responsePacket := gopacket.NewPacket(
			result.Output[0].RawData,
			layers.LayerTypeEthernet,
			gopacket.Default,
		)

		// Verify it's an ICMPv6 Echo Reply
		icmpLayer := responsePacket.Layer(layers.LayerTypeICMPv6)
		require.NotNil(t, icmpLayer, "response should have ICMPv6 layer")

		icmp := icmpLayer.(*layers.ICMPv6)
		assert.Equal(
			t,
			uint8(layers.ICMPv6TypeEchoReply),
			uint8(icmp.TypeCode.Type()),
			"should be Echo Reply",
		)
		assert.Equal(
			t,
			uint8(0),
			uint8(icmp.TypeCode.Code()),
			"code should be 0",
		)

		// Verify IP addresses are swapped
		ipLayer := responsePacket.Layer(layers.LayerTypeIPv6)
		require.NotNil(t, ipLayer, "response should have IPv6 layer")

		ip := ipLayer.(*layers.IPv6)
		assert.Equal(
			t,
			net.IP(vsIPv6.AsSlice()),
			ip.SrcIP,
			"src IP should be VS IP",
		)
		assert.Equal(
			t,
			net.IP(clientIPv6.AsSlice()),
			ip.DstIP,
			"dst IP should be client IP",
		)
		assert.Equal(
			t,
			uint8(64),
			ip.HopLimit,
			"hop limit should be reset to 64",
		)
	})
}

////////////////////////////////////////////////////////////////////////////////
// Test: ICMP Echo Request to non-virtual service IP should be dropped
////////////////////////////////////////////////////////////////////////////////

func TestICMPEchoRequestToNonVirtualService(t *testing.T) {
	vsIPv4 := IpAddr("10.1.1.1")
	nonVsIPv4 := IpAddr("10.99.99.99") // Not configured as VS
	clientIPv4 := IpAddr("10.0.1.1")

	vsIPv6 := IpAddr("2001:db8::1")
	nonVsIPv6 := IpAddr("2001:db8:99::99") // Not configured as VS
	clientIPv6 := IpAddr("2001:db8:1::1")

	config := &balancerpb.ModuleConfig{
		SourceAddressV4: IpAddr("5.5.5.5").AsSlice(),
		SourceAddressV6: IpAddr("fe80::5").AsSlice(),
		VirtualServices: []*balancerpb.VirtualService{
			{
				Addr:  vsIPv4.AsSlice(),
				Port:  80,
				Proto: balancerpb.TransportProto_TCP,
				AllowedSrcs: []*balancerpb.Subnet{
					{
						Addr: IpAddr("10.0.0.0").AsSlice(),
						Size: 8,
					},
				},
				Scheduler: balancerpb.VsScheduler_PRR,
				Flags: &balancerpb.VsFlags{
					Gre:    false,
					FixMss: false,
					Ops:    false,
					PureL3: false,
				},
				Reals: []*balancerpb.Real{
					{
						DstAddr: IpAddr("10.2.2.2").AsSlice(),
						Weight:  1,
						SrcAddr: IpAddr("10.2.2.2").AsSlice(),
						SrcMask: IpAddr("255.255.255.255").AsSlice(),
						Enabled: true,
					},
				},
			},
			{
				Addr:  vsIPv6.AsSlice(),
				Port:  80,
				Proto: balancerpb.TransportProto_TCP,
				AllowedSrcs: []*balancerpb.Subnet{
					{
						Addr: IpAddr("2001:db8::").AsSlice(),
						Size: 32,
					},
				},
				Scheduler: balancerpb.VsScheduler_PRR,
				Flags: &balancerpb.VsFlags{
					Gre:    false,
					FixMss: false,
					Ops:    false,
					PureL3: false,
				},
				Reals: []*balancerpb.Real{
					{
						DstAddr: IpAddr("2001:db8:2::2").AsSlice(),
						Weight:  1,
						SrcAddr: IpAddr("2001:db8:2::2").AsSlice(),
						SrcMask: IpAddr(
							"ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff",
						).AsSlice(),
						Enabled: true,
					},
				},
			},
		},
		SessionsTimeouts: &balancerpb.SessionsTimeouts{
			TcpSynAck: 60,
			TcpSyn:    60,
			TcpFin:    60,
			Tcp:       60,
			Udp:       60,
			Default:   60,
		},
		Wlc: &balancerpb.WlcConfig{
			WlcPower:      10,
			MaxRealWeight: 1000,
			UpdatePeriod:  durationpb.New(0),
		},
	}

	setup, err := SetupTest(&TestConfig{
		moduleConfig: config,
		stateConfig: &balancerpb.ModuleStateConfig{
			SessionTableCapacity:      100,
			SessionTableScanPeriod:    durationpb.New(0),
			SessionTableMaxLoadFactor: 0.5,
		},
	})
	require.NoError(t, err)
	defer setup.Free()

	t.Run("IPv4_NonVS_ShouldDrop", func(t *testing.T) {
		// Create ICMP Echo Request to non-VS IP
		packetLayers := MakeICMPv4EchoRequest(clientIPv4, nonVsIPv4, 1234, 1)
		packet := xpacket.LayersToPacket(t, packetLayers...)

		// Send packet
		result, err := setup.mock.HandlePackets(packet)
		require.NoError(t, err)

		// BUG: Currently this test will FAIL because the balancer responds
		// to ANY ICMP echo request, even if the destination is not a VS.
		// After the fix, this should pass.

		// Packet should be dropped, not responded to
		require.Empty(t, result.Output, "should not respond to non-VS IP")
		require.Equal(t, 1, len(result.Drop), "should drop packet")
	})

	t.Run("IPv6_NonVS_ShouldDrop", func(t *testing.T) {
		// Create ICMPv6 Echo Request to non-VS IP
		packetLayers := MakeICMPv6EchoRequest(clientIPv6, nonVsIPv6, 5678, 2)
		packet := xpacket.LayersToPacket(t, packetLayers...)

		// Send packet
		result, err := setup.mock.HandlePackets(packet)
		require.NoError(t, err)

		// BUG: Currently this test will FAIL because the balancer responds
		// to ANY ICMPv6 echo request, even if the destination is not a VS.
		// After the fix, this should pass.

		// Packet should be dropped, not responded to
		require.Empty(t, result.Output, "should not respond to non-VS IP")
		require.Equal(t, 1, len(result.Drop), "should drop packet")
	})

	t.Run("IPv4_ValidVS_ShouldRespond", func(t *testing.T) {
		// Create ICMP Echo Request to valid VS IP
		packetLayers := MakeICMPv4EchoRequest(clientIPv4, vsIPv4, 1234, 1)
		packet := xpacket.LayersToPacket(t, packetLayers...)

		// Send packet
		result, err := setup.mock.HandlePackets(packet)
		require.NoError(t, err)

		// Should respond to valid VS IP
		require.Equal(t, 1, len(result.Output), "should respond to VS IP")
		require.Empty(t, result.Drop, "should not drop packet")

		// Verify it's an ICMP Echo Reply
		responsePacket := gopacket.NewPacket(
			result.Output[0].RawData,
			layers.LayerTypeEthernet,
			gopacket.Default,
		)
		icmpLayer := responsePacket.Layer(layers.LayerTypeICMPv4)
		require.NotNil(t, icmpLayer, "response should have ICMPv4 layer")
		icmp := icmpLayer.(*layers.ICMPv4)
		assert.Equal(
			t,
			uint8(layers.ICMPv4TypeEchoReply),
			uint8(icmp.TypeCode.Type()),
			"should be Echo Reply",
		)
	})
}

////////////////////////////////////////////////////////////////////////////////
// Test: ICMP Error packet forwarding when session exists
////////////////////////////////////////////////////////////////////////////////

func TestICMPErrorWithExistingSession(t *testing.T) {
	vsIPv4 := IpAddr("10.1.1.1")
	realIPv4 := IpAddr("10.2.2.2")
	clientIPv4 := IpAddr("10.0.1.1")

	vsIPv6 := IpAddr("2001:db8::1")
	realIPv6 := IpAddr("2001:db8:2::2")
	clientIPv6 := IpAddr("2001:db8:1::1")

	clientPort := uint16(12345)
	vsPort := uint16(80)

	config := &balancerpb.ModuleConfig{
		SourceAddressV4: IpAddr("5.5.5.5").AsSlice(),
		SourceAddressV6: IpAddr("fe80::5").AsSlice(),
		VirtualServices: []*balancerpb.VirtualService{
			{
				Addr:  vsIPv4.AsSlice(),
				Port:  uint32(vsPort),
				Proto: balancerpb.TransportProto_TCP,
				AllowedSrcs: []*balancerpb.Subnet{
					{
						Addr: IpAddr("10.0.0.0").AsSlice(),
						Size: 8,
					},
				},
				Scheduler: balancerpb.VsScheduler_PRR,
				Flags: &balancerpb.VsFlags{
					Gre:    false,
					FixMss: false,
					Ops:    false,
					PureL3: false,
				},
				Reals: []*balancerpb.Real{
					{
						DstAddr: realIPv4.AsSlice(),
						Weight:  1,
						SrcAddr: realIPv4.AsSlice(),
						SrcMask: IpAddr("255.255.255.255").AsSlice(),
						Enabled: true,
					},
				},
			},
			{
				Addr:  vsIPv6.AsSlice(),
				Port:  uint32(vsPort),
				Proto: balancerpb.TransportProto_TCP,
				AllowedSrcs: []*balancerpb.Subnet{
					{
						Addr: IpAddr("2001:db8::").AsSlice(),
						Size: 32,
					},
				},
				Scheduler: balancerpb.VsScheduler_PRR,
				Flags: &balancerpb.VsFlags{
					Gre:    false,
					FixMss: false,
					Ops:    false,
					PureL3: false,
				},
				Reals: []*balancerpb.Real{
					{
						DstAddr: realIPv6.AsSlice(),
						Weight:  1,
						SrcAddr: realIPv6.AsSlice(),
						SrcMask: IpAddr(
							"ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff",
						).AsSlice(),
						Enabled: true,
					},
				},
			},
		},
		SessionsTimeouts: &balancerpb.SessionsTimeouts{
			TcpSynAck: 60,
			TcpSyn:    60,
			TcpFin:    60,
			Tcp:       60,
			Udp:       60,
			Default:   60,
		},
		Wlc: &balancerpb.WlcConfig{
			WlcPower:      10,
			MaxRealWeight: 1000,
			UpdatePeriod:  durationpb.New(0),
		},
	}

	setup, err := SetupTest(&TestConfig{
		moduleConfig: config,
		stateConfig: &balancerpb.ModuleStateConfig{
			SessionTableCapacity:      100,
			SessionTableScanPeriod:    durationpb.New(0),
			SessionTableMaxLoadFactor: 0.5,
		},
	})
	require.NoError(t, err)
	defer setup.Free()

	t.Run("IPv4", func(t *testing.T) {
		// First, create a session by sending a TCP SYN packet
		tcpLayers := MakeTCPPacket(
			clientIPv4,
			clientPort,
			vsIPv4,
			vsPort,
			&layers.TCP{SYN: true},
		)
		tcpPacket := xpacket.LayersToPacket(t, tcpLayers...)

		result, err := setup.mock.HandlePackets(tcpPacket)
		require.NoError(t, err)
		require.Equal(
			t,
			1,
			len(result.Output),
			"TCP packet should be forwarded",
		)

		// Now simulate the real server's response packet (which would trigger an ICMP error)
		// The real server responds with src=vsIP (as configured), dst=clientIP
		responsePacket := MakeTCPPacket(
			vsIPv4,
			vsPort,
			clientIPv4,
			clientPort,
			&layers.TCP{SYN: true, ACK: true},
		)
		responsePacketData := xpacket.LayersToPacket(t, responsePacket...)

		// Now send an ICMP Destination Unreachable error containing the response packet
		// The ICMP error comes from the client network to the VS IP (balancer)
		// because the response packet had src=vsIP
		icmpLayers := MakeICMPv4DestUnreachable(
			clientIPv4,
			vsIPv4,
			responsePacketData,
		)
		icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

		result, err = setup.mock.HandlePackets(icmpPacket)
		require.NoError(t, err)

		// The ICMP error should be forwarded to the real server (tunneled)
		require.Equal(
			t,
			1,
			len(result.Output),
			"ICMP error should be forwarded",
		)
		require.Empty(t, result.Drop, "ICMP error should not be dropped")

		// Verify the packet is tunneled
		outputPacket := result.Output[0]
		assert.True(
			t,
			outputPacket.IsTunneled,
			"ICMP error should be tunneled to real",
		)
		assert.Equal(
			t,
			net.IP(realIPv4.AsSlice()),
			outputPacket.DstIP,
			"should be sent to real server",
		)
	})

	t.Run("IPv6", func(t *testing.T) {
		// First, create a session by sending a TCP SYN packet
		tcpLayers := MakeTCPPacket(
			clientIPv6,
			clientPort,
			vsIPv6,
			vsPort,
			&layers.TCP{SYN: true},
		)
		tcpPacket := xpacket.LayersToPacket(t, tcpLayers...)

		result, err := setup.mock.HandlePackets(tcpPacket)
		require.NoError(t, err)
		require.Equal(
			t,
			1,
			len(result.Output),
			"TCP packet should be forwarded",
		)

		// Now simulate the real server's response packet (which would trigger an ICMPv6 error)
		// The real server responds with src=vsIP (as configured), dst=clientIP
		responsePacket := MakeTCPPacket(
			vsIPv6,
			vsPort,
			clientIPv6,
			clientPort,
			&layers.TCP{SYN: true, ACK: true},
		)
		responsePacketData := xpacket.LayersToPacket(t, responsePacket...)

		// Now send an ICMPv6 Destination Unreachable error containing the response packet
		// The ICMPv6 error comes from the client network to the VS IP (balancer)
		// because the response packet had src=vsIP
		icmpLayers := MakeICMPv6DestUnreachable(
			clientIPv6,
			vsIPv6,
			responsePacketData,
		)
		icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

		result, err = setup.mock.HandlePackets(icmpPacket)
		require.NoError(t, err)

		// The ICMPv6 error should be forwarded to the real server (tunneled)
		require.Equal(
			t,
			1,
			len(result.Output),
			"ICMPv6 error should be forwarded",
		)
		require.Empty(t, result.Drop, "ICMPv6 error should not be dropped")

		// Verify the packet is tunneled
		outputPacket := result.Output[0]
		assert.True(
			t,
			outputPacket.IsTunneled,
			"ICMPv6 error should be tunneled to real",
		)
		assert.Equal(
			t,
			net.IP(realIPv6.AsSlice()),
			outputPacket.DstIP,
			"should be sent to real server",
		)
	})
}

////////////////////////////////////////////////////////////////////////////////
// Test: ICMP Error packet drop when VS not found
////////////////////////////////////////////////////////////////////////////////

func TestICMPErrorWithUnknownVS(t *testing.T) {
	vsIPv4 := IpAddr("10.1.1.1")
	unknownVsIPv4 := IpAddr("10.99.99.99") // Not configured
	clientIPv4 := IpAddr("10.0.1.1")

	vsIPv6 := IpAddr("2001:db8::1")
	unknownVsIPv6 := IpAddr("2001:db8:99::99") // Not configured
	clientIPv6 := IpAddr("2001:db8:1::1")

	clientPort := uint16(12345)
	vsPort := uint16(80)

	config := &balancerpb.ModuleConfig{
		SourceAddressV4: IpAddr("5.5.5.5").AsSlice(),
		SourceAddressV6: IpAddr("fe80::5").AsSlice(),
		VirtualServices: []*balancerpb.VirtualService{
			{
				Addr:  vsIPv4.AsSlice(),
				Port:  uint32(vsPort),
				Proto: balancerpb.TransportProto_TCP,
				AllowedSrcs: []*balancerpb.Subnet{
					{
						Addr: IpAddr("10.0.0.0").AsSlice(),
						Size: 8,
					},
				},
				Scheduler: balancerpb.VsScheduler_PRR,
				Flags: &balancerpb.VsFlags{
					Gre:    false,
					FixMss: false,
					Ops:    false,
					PureL3: false,
				},
				Reals: []*balancerpb.Real{
					{
						DstAddr: IpAddr("10.2.2.2").AsSlice(),
						Weight:  1,
						SrcAddr: IpAddr("10.2.2.2").AsSlice(),
						SrcMask: IpAddr("255.255.255.255").AsSlice(),
						Enabled: true,
					},
				},
			},
			{
				Addr:  vsIPv6.AsSlice(),
				Port:  uint32(vsPort),
				Proto: balancerpb.TransportProto_TCP,
				AllowedSrcs: []*balancerpb.Subnet{
					{
						Addr: IpAddr("2001:db8::").AsSlice(),
						Size: 32,
					},
				},
				Scheduler: balancerpb.VsScheduler_PRR,
				Flags: &balancerpb.VsFlags{
					Gre:    false,
					FixMss: false,
					Ops:    false,
					PureL3: false,
				},
				Reals: []*balancerpb.Real{
					{
						DstAddr: IpAddr("2001:db8:2::2").AsSlice(),
						Weight:  1,
						SrcAddr: IpAddr("2001:db8:2::2").AsSlice(),
						SrcMask: IpAddr(
							"ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff",
						).AsSlice(),
						Enabled: true,
					},
				},
			},
		},
		SessionsTimeouts: &balancerpb.SessionsTimeouts{
			TcpSynAck: 60,
			TcpSyn:    60,
			TcpFin:    60,
			Tcp:       60,
			Udp:       60,
			Default:   60,
		},
		Wlc: &balancerpb.WlcConfig{
			WlcPower:      10,
			MaxRealWeight: 1000,
			UpdatePeriod:  durationpb.New(0),
		},
	}

	setup, err := SetupTest(&TestConfig{
		moduleConfig: config,
		stateConfig: &balancerpb.ModuleStateConfig{
			SessionTableCapacity:      100,
			SessionTableScanPeriod:    durationpb.New(0),
			SessionTableMaxLoadFactor: 0.5,
		},
	})
	require.NoError(t, err)
	defer setup.Free()

	t.Run("IPv4", func(t *testing.T) {
		// Create a TCP packet to an unknown VS
		tcpLayers := MakeTCPPacket(
			unknownVsIPv4,
			vsPort,
			clientIPv4,
			clientPort,
			&layers.TCP{SYN: true},
		)
		tcpPacket := xpacket.LayersToPacket(t, tcpLayers...)

		// Create ICMP error for the unknown VS
		icmpLayers := MakeICMPv4DestUnreachable(
			clientIPv4,
			unknownVsIPv4,
			tcpPacket,
		)
		icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

		result, err := setup.mock.HandlePackets(icmpPacket)
		require.NoError(t, err)

		// The ICMP error should be dropped because VS is not found
		require.Empty(t, result.Output, "ICMP error should not be forwarded")
		require.Equal(t, 1, len(result.Drop), "ICMP error should be dropped")
	})

	t.Run("IPv6", func(t *testing.T) {
		// Create a TCP packet to an unknown VS
		tcpLayers := MakeTCPPacket(
			unknownVsIPv6,
			vsPort,
			clientIPv6,
			clientPort,
			&layers.TCP{SYN: true},
		)
		tcpPacket := xpacket.LayersToPacket(t, tcpLayers...)

		// Create ICMPv6 error for the unknown VS
		icmpLayers := MakeICMPv6DestUnreachable(
			clientIPv6,
			unknownVsIPv6,
			tcpPacket,
		)
		icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

		result, err := setup.mock.HandlePackets(icmpPacket)
		require.NoError(t, err)

		// The ICMPv6 error should be dropped because VS is not found
		require.Empty(t, result.Output, "ICMPv6 error should not be forwarded")
		require.Equal(t, 1, len(result.Drop), "ICMPv6 error should be dropped")
	})
}

////////////////////////////////////////////////////////////////////////////////
// Test: ICMP Error packet drop when session not found
////////////////////////////////////////////////////////////////////////////////

func TestICMPErrorWithNoSession(t *testing.T) {
	// In this test packet must be broadcasted to peers
	vsIPv4 := IpAddr("10.1.1.1")
	clientIPv4 := IpAddr("10.0.1.1")

	vsIPv6 := IpAddr("2001:db8::1")
	clientIPv6 := IpAddr("2001:db8:1::1")

	clientPort := uint16(12345)
	vsPort := uint16(80)

	peer1 := IpAddr("10.12.11.13")
	peer2 := IpAddr("fe80::11")

	config := &balancerpb.ModuleConfig{
		SourceAddressV4: IpAddr("5.5.5.5").AsSlice(),
		SourceAddressV6: IpAddr("fe80::5").AsSlice(),
		VirtualServices: []*balancerpb.VirtualService{
			{
				Addr:  vsIPv4.AsSlice(),
				Port:  uint32(vsPort),
				Proto: balancerpb.TransportProto_TCP,
				AllowedSrcs: []*balancerpb.Subnet{
					{
						Addr: IpAddr("10.0.0.0").AsSlice(),
						Size: 8,
					},
				},
				Scheduler: balancerpb.VsScheduler_PRR,
				Flags: &balancerpb.VsFlags{
					Gre:    false,
					FixMss: false,
					Ops:    false,
					PureL3: false,
				},
				Reals: []*balancerpb.Real{
					{
						DstAddr: IpAddr("10.2.2.2").AsSlice(),
						Weight:  1,
						SrcAddr: IpAddr("10.2.2.2").AsSlice(),
						SrcMask: IpAddr("255.255.255.255").AsSlice(),
						Enabled: true,
					},
				},
				Peers: [][]byte{
					peer1.AsSlice(), peer2.AsSlice(),
				},
			},
			{
				Addr:  vsIPv6.AsSlice(),
				Port:  uint32(vsPort),
				Proto: balancerpb.TransportProto_TCP,
				AllowedSrcs: []*balancerpb.Subnet{
					{
						Addr: IpAddr("2001:db8::").AsSlice(),
						Size: 32,
					},
				},
				Scheduler: balancerpb.VsScheduler_PRR,
				Flags: &balancerpb.VsFlags{
					Gre:    false,
					FixMss: false,
					Ops:    false,
					PureL3: false,
				},
				Reals: []*balancerpb.Real{
					{
						DstAddr: IpAddr("2001:db8:2::2").AsSlice(),
						Weight:  1,
						SrcAddr: IpAddr("2001:db8:2::2").AsSlice(),
						SrcMask: IpAddr(
							"ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff",
						).AsSlice(),
						Enabled: true,
					},
				},
				Peers: [][]byte{
					peer1.AsSlice(), peer2.AsSlice(),
				},
			},
		},
		SessionsTimeouts: &balancerpb.SessionsTimeouts{
			TcpSynAck: 60,
			TcpSyn:    60,
			TcpFin:    60,
			Tcp:       60,
			Udp:       60,
			Default:   60,
		},
		Wlc: &balancerpb.WlcConfig{
			WlcPower:      10,
			MaxRealWeight: 1000,
			UpdatePeriod:  durationpb.New(0),
		},
	}

	setup, err := SetupTest(&TestConfig{
		moduleConfig: config,
		stateConfig: &balancerpb.ModuleStateConfig{
			SessionTableCapacity:      100,
			SessionTableScanPeriod:    durationpb.New(0),
			SessionTableMaxLoadFactor: 0.5,
		},
	})
	require.NoError(t, err)
	defer setup.Free()

	t.Run("IPv4", func(t *testing.T) {
		// Create a TCP packet (but don't send it to create a session)
		tcpLayers := MakeTCPPacket(
			vsIPv4,
			vsPort,
			clientIPv4,
			clientPort,
			&layers.TCP{SYN: true},
		)
		tcpPacket := xpacket.LayersToPacket(t, tcpLayers...)

		// Create ICMP error for a non-existent session
		icmpLayers := MakeICMPv4DestUnreachable(clientIPv4, vsIPv4, tcpPacket)
		icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

		result, err := setup.mock.HandlePackets(icmpPacket)
		require.NoError(t, err)

		// Since there's no session, the packet should be broadcasted to peers
		require.Equal(
			t,
			2,
			len(result.Output),
			"ICMP error clone must be broadcasted to both peers",
		)
		require.Equal(
			t,
			1,
			len(result.Drop),
			"The original packet must be dropped",
		)
	})

	t.Run("IPv6", func(t *testing.T) {
		// Create a TCP packet (but don't send it to create a session)
		tcpLayers := MakeTCPPacket(
			vsIPv6,
			vsPort,
			clientIPv6,
			clientPort,
			&layers.TCP{SYN: true},
		)
		tcpPacket := xpacket.LayersToPacket(t, tcpLayers...)

		// Create ICMPv6 error for a non-existent session
		icmpLayers := MakeICMPv6DestUnreachable(clientIPv6, vsIPv6, tcpPacket)
		icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

		result, err := setup.mock.HandlePackets(icmpPacket)
		require.NoError(t, err)

		// Since there's no session, the packet should be broadcasted to peers
		require.Equal(
			t,
			2,
			len(result.Output),
			"ICMPv6 error clone must be broadcasted to both peers",
		)
		require.Equal(
			t,
			1,
			len(result.Drop),
			"The original packet must be dropped",
		)
	})
}
