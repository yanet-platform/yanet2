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

	return []gopacket.SerializableLayer{eth, ip, icmp, gopacket.Payload(payload)}
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

	return []gopacket.SerializableLayer{eth, ip, icmp, gopacket.Payload(payload)}
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
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeDestinationUnreachable, 3), // Port unreachable
	}

	// Extract the original IP header and first 8 bytes of transport layer
	// This is what ICMP error messages typically include
	originalData := originalPacket.Data()
	// Find the IP layer start (skip Ethernet header)
	ipStart := 14 // Ethernet header size
	if ipStart < len(originalData) {
		// Include IP header + 8 bytes of transport layer (minimum for ICMP error)
		payloadLen := min(len(originalData)-ipStart, 28) // 20 (IP) + 8 (transport)
		payload := originalData[ipStart : ipStart+payloadLen]
		return []gopacket.SerializableLayer{eth, ip, icmp, gopacket.Payload(payload)}
	}

	return []gopacket.SerializableLayer{eth, ip, icmp, gopacket.Payload([]byte{})}
}

////////////////////////////////////////////////////////////////////////////////
// Test: ICMP Echo Request/Reply for IPv4
////////////////////////////////////////////////////////////////////////////////

func TestICMPv4EchoRequest(t *testing.T) {
	vsIP := IpAddr("10.1.1.1")
	clientIP := IpAddr("10.0.1.1")

	config := &balancerpb.ModuleConfig{
		SourceAddressV4: IpAddr("5.5.5.5").AsSlice(),
		SourceAddressV6: IpAddr("fe80::5").AsSlice(),
		VirtualServices: []*balancerpb.VirtualService{
			{
				Addr:  vsIP.AsSlice(),
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
		},
		SessionsTimeouts: &balancerpb.SessionsTimeouts{
			TcpSynAck: 60,
			TcpSyn:    60,
			TcpFin:    60,
			Tcp:       60,
			Udp:       60,
			Default:   60,
		},
	}

	setup, err := SetupTest(&TestConfig{
		balancer: config,
		stateConfig: &balancerpb.ModuleStateConfig{
			SessionTableCapacity: 100,
		},
	})
	require.NoError(t, err)
	defer setup.Free()

	// Create ICMP Echo Request
	packetLayers := MakeICMPv4EchoRequest(clientIP, vsIP, 1234, 1)
	packet := xpacket.LayersToPacket(t, packetLayers...)

	// Send packet
	result, err := setup.mock.HandlePackets(packet)
	require.NoError(t, err)
	require.Equal(t, 1, len(result.Output), "should have one output packet")
	require.Empty(t, result.Drop, "should not drop packet")

	// Parse response
	responsePacket := gopacket.NewPacket(result.Output[0].RawData, layers.LayerTypeEthernet, gopacket.Default)

	// Verify it's an ICMP Echo Reply
	icmpLayer := responsePacket.Layer(layers.LayerTypeICMPv4)
	require.NotNil(t, icmpLayer, "response should have ICMPv4 layer")

	icmp := icmpLayer.(*layers.ICMPv4)
	assert.Equal(t, uint8(layers.ICMPv4TypeEchoReply), uint8(icmp.TypeCode.Type()), "should be Echo Reply")
	assert.Equal(t, uint8(0), uint8(icmp.TypeCode.Code()), "code should be 0")
	assert.Equal(t, uint16(1234), icmp.Id, "ID should match request")
	assert.Equal(t, uint16(1), icmp.Seq, "sequence should match request")

	// Verify IP addresses are swapped
	ipLayer := responsePacket.Layer(layers.LayerTypeIPv4)
	require.NotNil(t, ipLayer, "response should have IPv4 layer")

	ip := ipLayer.(*layers.IPv4)
	assert.Equal(t, net.IP(vsIP.AsSlice()), ip.SrcIP, "src IP should be VS IP")
	assert.Equal(t, net.IP(clientIP.AsSlice()), ip.DstIP, "dst IP should be client IP")
	assert.Equal(t, uint8(64), ip.TTL, "TTL should be reset to 64")
}

////////////////////////////////////////////////////////////////////////////////
// Test: ICMP Echo Request/Reply for IPv6
////////////////////////////////////////////////////////////////////////////////

func TestICMPv6EchoRequest(t *testing.T) {
	vsIP := IpAddr("2001:db8::1")
	clientIP := IpAddr("2001:db8:1::1")

	config := &balancerpb.ModuleConfig{
		SourceAddressV4: IpAddr("5.5.5.5").AsSlice(),
		SourceAddressV6: IpAddr("fe80::5").AsSlice(),
		VirtualServices: []*balancerpb.VirtualService{
			{
				Addr:  vsIP.AsSlice(),
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
						SrcMask: IpAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff").AsSlice(),
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
	}

	setup, err := SetupTest(&TestConfig{
		balancer: config,
		stateConfig: &balancerpb.ModuleStateConfig{
			SessionTableCapacity: 100,
		},
	})
	require.NoError(t, err)
	defer setup.Free()

	// Create ICMPv6 Echo Request
	packetLayers := MakeICMPv6EchoRequest(clientIP, vsIP, 5678, 2)
	packet := xpacket.LayersToPacket(t, packetLayers...)

	// Send packet
	result, err := setup.mock.HandlePackets(packet)
	require.NoError(t, err)
	require.Equal(t, 1, len(result.Output), "should have one output packet")
	require.Empty(t, result.Drop, "should not drop packet")

	// Parse response
	responsePacket := gopacket.NewPacket(result.Output[0].RawData, layers.LayerTypeEthernet, gopacket.Default)

	// Verify it's an ICMPv6 Echo Reply
	icmpLayer := responsePacket.Layer(layers.LayerTypeICMPv6)
	require.NotNil(t, icmpLayer, "response should have ICMPv6 layer")

	icmp := icmpLayer.(*layers.ICMPv6)
	assert.Equal(t, uint8(layers.ICMPv6TypeEchoReply), uint8(icmp.TypeCode.Type()), "should be Echo Reply")
	assert.Equal(t, uint8(0), uint8(icmp.TypeCode.Code()), "code should be 0")

	// Verify IP addresses are swapped
	ipLayer := responsePacket.Layer(layers.LayerTypeIPv6)
	require.NotNil(t, ipLayer, "response should have IPv6 layer")

	ip := ipLayer.(*layers.IPv6)
	assert.Equal(t, net.IP(vsIP.AsSlice()), ip.SrcIP, "src IP should be VS IP")
	assert.Equal(t, net.IP(clientIP.AsSlice()), ip.DstIP, "dst IP should be client IP")
	assert.Equal(t, uint8(64), ip.HopLimit, "hop limit should be reset to 64")
}

////////////////////////////////////////////////////////////////////////////////
// Test: ICMP Error packet forwarding when session exists
////////////////////////////////////////////////////////////////////////////////

func TestICMPv4ErrorWithExistingSession(t *testing.T) {
	vsIP := IpAddr("10.1.1.1")
	realIP := IpAddr("10.2.2.2")
	clientIP := IpAddr("10.0.1.1")
	clientPort := uint16(12345)
	vsPort := uint16(80)

	config := &balancerpb.ModuleConfig{
		SourceAddressV4: IpAddr("5.5.5.5").AsSlice(),
		SourceAddressV6: IpAddr("fe80::5").AsSlice(),
		VirtualServices: []*balancerpb.VirtualService{
			{
				Addr:  vsIP.AsSlice(),
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
						DstAddr: realIP.AsSlice(),
						Weight:  1,
						SrcAddr: realIP.AsSlice(),
						SrcMask: IpAddr("255.255.255.255").AsSlice(),
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
	}

	setup, err := SetupTest(&TestConfig{
		balancer: config,
		stateConfig: &balancerpb.ModuleStateConfig{
			SessionTableCapacity: 100,
		},
	})
	require.NoError(t, err)
	defer setup.Free()

	// First, create a session by sending a TCP SYN packet
	tcpLayers := MakeTCPPacket(clientIP, clientPort, vsIP, vsPort, &layers.TCP{SYN: true})
	tcpPacket := xpacket.LayersToPacket(t, tcpLayers...)

	result, err := setup.mock.HandlePackets(tcpPacket)
	require.NoError(t, err)
	require.Equal(t, 1, len(result.Output), "TCP packet should be forwarded")

	// Now send an ICMP Destination Unreachable error containing the original packet
	// The error is sent from the VS IP back to the client
	icmpLayers := MakeICMPv4DestUnreachable(vsIP, clientIP, tcpPacket)
	icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

	result, err = setup.mock.HandlePackets(icmpPacket)
	require.NoError(t, err)

	// The ICMP error should be forwarded to the real server (tunneled)
	require.Equal(t, 1, len(result.Output), "ICMP error should be forwarded")
	require.Empty(t, result.Drop, "ICMP error should not be dropped")

	// Verify the packet is tunneled
	outputPacket := result.Output[0]
	assert.True(t, outputPacket.IsTunneled, "ICMP error should be tunneled to real")
	assert.Equal(t, net.IP(realIP.AsSlice()), outputPacket.DstIP, "should be sent to real server")
}

////////////////////////////////////////////////////////////////////////////////
// Test: ICMP Error packet drop when VS not found
////////////////////////////////////////////////////////////////////////////////

func TestICMPv4ErrorWithUnknownVS(t *testing.T) {
	vsIP := IpAddr("10.1.1.1")
	unknownVsIP := IpAddr("10.99.99.99") // Not configured
	clientIP := IpAddr("10.0.1.1")
	clientPort := uint16(12345)
	vsPort := uint16(80)

	config := &balancerpb.ModuleConfig{
		SourceAddressV4: IpAddr("5.5.5.5").AsSlice(),
		SourceAddressV6: IpAddr("fe80::5").AsSlice(),
		VirtualServices: []*balancerpb.VirtualService{
			{
				Addr:  vsIP.AsSlice(),
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
		},
		SessionsTimeouts: &balancerpb.SessionsTimeouts{
			TcpSynAck: 60,
			TcpSyn:    60,
			TcpFin:    60,
			Tcp:       60,
			Udp:       60,
			Default:   60,
		},
	}

	setup, err := SetupTest(&TestConfig{
		balancer: config,
		stateConfig: &balancerpb.ModuleStateConfig{
			SessionTableCapacity: 100,
		},
	})
	require.NoError(t, err)
	defer setup.Free()

	// Create a TCP packet to an unknown VS
	tcpLayers := MakeTCPPacket(clientIP, clientPort, unknownVsIP, vsPort, &layers.TCP{SYN: true})
	tcpPacket := xpacket.LayersToPacket(t, tcpLayers...)

	// Create ICMP error for the unknown VS
	icmpLayers := MakeICMPv4DestUnreachable(unknownVsIP, clientIP, tcpPacket)
	icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

	result, err := setup.mock.HandlePackets(icmpPacket)
	require.NoError(t, err)

	// The ICMP error should be dropped because VS is not found
	require.Empty(t, result.Output, "ICMP error should not be forwarded")
	require.Equal(t, 1, len(result.Drop), "ICMP error should be dropped")
}

////////////////////////////////////////////////////////////////////////////////
// Test: ICMP Error packet drop when session not found
////////////////////////////////////////////////////////////////////////////////

func TestICMPv4ErrorWithNoSession(t *testing.T) {
	vsIP := IpAddr("10.1.1.1")
	clientIP := IpAddr("10.0.1.1")
	clientPort := uint16(12345)
	vsPort := uint16(80)

	config := &balancerpb.ModuleConfig{
		SourceAddressV4: IpAddr("5.5.5.5").AsSlice(),
		SourceAddressV6: IpAddr("fe80::5").AsSlice(),
		VirtualServices: []*balancerpb.VirtualService{
			{
				Addr:  vsIP.AsSlice(),
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
				Peers: [][]byte{}, // No peers configured
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
	}

	setup, err := SetupTest(&TestConfig{
		balancer: config,
		stateConfig: &balancerpb.ModuleStateConfig{
			SessionTableCapacity: 100,
		},
	})
	require.NoError(t, err)
	defer setup.Free()

	// Create a TCP packet (but don't send it to create a session)
	tcpLayers := MakeTCPPacket(clientIP, clientPort, vsIP, vsPort, &layers.TCP{SYN: true})
	tcpPacket := xpacket.LayersToPacket(t, tcpLayers...)

	// Create ICMP error for a non-existent session
	icmpLayers := MakeICMPv4DestUnreachable(vsIP, clientIP, tcpPacket)
	icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

	result, err := setup.mock.HandlePackets(icmpPacket)
	require.NoError(t, err)

	// Since there's no session and no peers, the packet should be dropped
	// (In a real scenario with peers, it would be broadcast)
	require.Empty(t, result.Output, "ICMP error should not be forwarded without session")
	require.Equal(t, 1, len(result.Drop), "ICMP error should be dropped")
}
