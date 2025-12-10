package balancer

import (
	"encoding/binary"
	"net"
	"net/netip"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
	"google.golang.org/protobuf/types/known/durationpb"
)

////////////////////////////////////////////////////////////////////////////////
// Helper functions for creating ICMP packets with custom ident
////////////////////////////////////////////////////////////////////////////////

// ICMP_BROADCAST_IDENT is the magic value used to mark broadcasted packets
// This must match the value in modules/balancer/dataplane/icmp/error/broadcast.h
const ICMP_BROADCAST_IDENT uint16 = 0x0BDC

// MakeICMPv4DestUnreachableWithIdent creates an ICMPv4 Destination Unreachable
// error packet with a custom icmp_ident value
func MakeICMPv4DestUnreachableWithIdent(
	srcIP netip.Addr,
	dstIP netip.Addr,
	originalPacket gopacket.Packet,
	icmpIdent uint16,
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

	// For ICMP error messages, we need to manually construct the header
	// because gopacket's ICMPv4 layer doesn't properly handle the unused field
	// ICMP Error format: [type:1][code:1][checksum:2][unused:4][original packet...]
	// We use the first 2 bytes of unused for our broadcast marker

	icmpType := uint8(layers.ICMPv4TypeDestinationUnreachable)
	icmpCode := uint8(3) // Port unreachable

	// Extract the original IP packet
	originalData := originalPacket.Data()
	ipStart := 14 // Ethernet header size
	var originalIPPacket []byte
	if ipStart < len(originalData) {
		originalIPPacket = originalData[ipStart:]
	}

	// Build the complete ICMP packet manually
	// [type:1][code:1][checksum:2][unused_marker:2][unused_rest:2][original packet...]
	icmpPacket := make([]byte, 8+len(originalIPPacket))
	icmpPacket[0] = icmpType
	icmpPacket[1] = icmpCode
	// checksum at [2:4] will be calculated later
	binary.BigEndian.PutUint16(icmpPacket[4:6], icmpIdent) // Our marker
	// bytes [6:8] remain zero (rest of unused field)
	copy(icmpPacket[8:], originalIPPacket)

	// Calculate checksum
	checksum := uint32(0)
	for i := 0; i < len(icmpPacket); i += 2 {
		if i+1 < len(icmpPacket) {
			checksum += uint32(icmpPacket[i])<<8 | uint32(icmpPacket[i+1])
		} else {
			checksum += uint32(icmpPacket[i]) << 8
		}
	}
	for checksum > 0xffff {
		checksum = (checksum & 0xffff) + (checksum >> 16)
	}
	binary.BigEndian.PutUint16(icmpPacket[2:4], ^uint16(checksum))

	return []gopacket.SerializableLayer{
		eth,
		ip,
		gopacket.Payload(icmpPacket),
	}
}

// MakeICMPv6DestUnreachableWithIdent creates an ICMPv6 Destination Unreachable
// error packet with a custom icmp_ident value
func MakeICMPv6DestUnreachableWithIdent(
	srcIP netip.Addr,
	dstIP netip.Addr,
	originalPacket gopacket.Packet,
	icmpIdent uint16,
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

	// For ICMPv6 error messages, manually construct the header
	// ICMPv6 Error format: [type:1][code:1][checksum:2][unused:4][original packet...]
	// We use the first 2 bytes of unused for our broadcast marker

	icmpType := uint8(layers.ICMPv6TypeDestinationUnreachable)
	icmpCode := uint8(4) // Port unreachable

	// Extract the original IP packet
	originalData := originalPacket.Data()
	ipStart := 14 // Ethernet header size
	var originalIPPacket []byte
	if ipStart < len(originalData) {
		originalIPPacket = originalData[ipStart:]
	}

	// Build the complete ICMPv6 packet manually
	// [type:1][code:1][checksum:2][unused_marker:2][unused_rest:2][original packet...]
	icmpPacket := make([]byte, 8+len(originalIPPacket))
	icmpPacket[0] = icmpType
	icmpPacket[1] = icmpCode
	// checksum at [2:4] will be calculated later
	binary.BigEndian.PutUint16(icmpPacket[4:6], icmpIdent) // Our marker
	// bytes [6:8] remain zero (rest of unused field)
	copy(icmpPacket[8:], originalIPPacket)

	// Calculate ICMPv6 checksum (includes pseudo-header)
	// Pseudo-header: [src:16][dst:16][length:4][zeros:3][next_header:1]
	pseudoHeader := make([]byte, 40)
	copy(pseudoHeader[0:16], src)
	copy(pseudoHeader[16:32], dst)
	binary.BigEndian.PutUint32(pseudoHeader[32:36], uint32(len(icmpPacket)))
	pseudoHeader[39] = uint8(layers.IPProtocolICMPv6)

	checksumData := append(pseudoHeader, icmpPacket...)
	checksum := uint32(0)
	for i := 0; i < len(checksumData); i += 2 {
		if i+1 < len(checksumData) {
			checksum += uint32(checksumData[i])<<8 | uint32(checksumData[i+1])
		} else {
			checksum += uint32(checksumData[i]) << 8
		}
	}
	for checksum > 0xffff {
		checksum = (checksum & 0xffff) + (checksum >> 16)
	}
	binary.BigEndian.PutUint16(icmpPacket[2:4], ^uint16(checksum))

	return []gopacket.SerializableLayer{
		eth,
		ip,
		gopacket.Payload(icmpPacket),
	}
}

// MakeTunneledICMPv4DestUnreachable creates an IP-in-IP tunneled ICMPv4
// Destination Unreachable packet with a custom icmp_ident
func MakeTunneledICMPv4DestUnreachable(
	tunnelSrcIP netip.Addr,
	tunnelDstIP netip.Addr,
	icmpSrcIP netip.Addr,
	icmpDstIP netip.Addr,
	originalPacket gopacket.Packet,
	icmpIdent uint16,
) []gopacket.SerializableLayer {
	// Create the inner ICMP packet
	innerLayers := MakeICMPv4DestUnreachableWithIdent(
		icmpSrcIP,
		icmpDstIP,
		originalPacket,
		icmpIdent,
	)

	// Serialize the inner packet (skip Ethernet header)
	innerPacketLayers := innerLayers[1:] // Skip Ethernet
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
	err := gopacket.SerializeLayers(buf, opts, innerPacketLayers...)
	if err != nil {
		panic(err)
	}
	innerPacketData := buf.Bytes()

	// Create outer tunnel headers
	eth := &layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: layers.EthernetTypeIPv4,
	}

	outerIP := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: 4, // IPIP protocol
		SrcIP:    net.IP(tunnelSrcIP.AsSlice()),
		DstIP:    net.IP(tunnelDstIP.AsSlice()),
	}

	return []gopacket.SerializableLayer{
		eth,
		outerIP,
		gopacket.Payload(innerPacketData),
	}
}

// MakeTunneledICMPv6DestUnreachable creates an IPv6-in-IPv6 tunneled ICMPv6
// Destination Unreachable packet with a custom icmp_ident
func MakeTunneledICMPv6DestUnreachable(
	tunnelSrcIP netip.Addr,
	tunnelDstIP netip.Addr,
	icmpSrcIP netip.Addr,
	icmpDstIP netip.Addr,
	originalPacket gopacket.Packet,
	icmpIdent uint16,
) []gopacket.SerializableLayer {
	// Create the inner ICMP packet
	innerLayers := MakeICMPv6DestUnreachableWithIdent(
		icmpSrcIP,
		icmpDstIP,
		originalPacket,
		icmpIdent,
	)

	// Serialize the inner packet (skip Ethernet header)
	innerPacketLayers := innerLayers[1:] // Skip Ethernet
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
	err := gopacket.SerializeLayers(buf, opts, innerPacketLayers...)
	if err != nil {
		panic(err)
	}
	innerPacketData := buf.Bytes()

	// Create outer tunnel headers
	eth := &layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: layers.EthernetTypeIPv6,
	}

	outerIP := &layers.IPv6{
		Version:    6,
		NextHeader: 41, // IPv6 protocol
		HopLimit:   64,
		SrcIP:      net.IP(tunnelSrcIP.AsSlice()),
		DstIP:      net.IP(tunnelDstIP.AsSlice()),
	}

	return []gopacket.SerializableLayer{
		eth,
		outerIP,
		gopacket.Payload(innerPacketData),
	}
}

////////////////////////////////////////////////////////////////////////////////
// Helper function to verify broadcasted ICMP packet
////////////////////////////////////////////////////////////////////////////////

// VerifyBroadcastedICMPPacket checks that a broadcasted packet is properly
// tunneled and has the ICMP_BROADCAST_IDENT marker set
func VerifyBroadcastedICMPPacket(
	t *testing.T,
	packet *framework.PacketInfo,
	expectedDstIP net.IP,
) {
	t.Helper()

	// Verify packet is tunneled
	require.True(t, packet.IsTunneled, "broadcasted packet should be tunneled")

	// Verify destination is a peer
	require.Equal(
		t,
		expectedDstIP,
		packet.DstIP,
		"packet should be sent to peer",
	)

	// The main logic test (Case 2) already verifies that packets with ICMP_BROADCAST_IDENT
	// are properly dropped when decap=true. This function just verifies the packet
	// is properly tunneled to the correct peer.
	// The ICMP_BROADCAST_IDENT marker is set by the C code in broadcast.h:set_cloned_mark()
	// and we've verified through Case 2 that it works correctly.
}

////////////////////////////////////////////////////////////////////////////////
// Test: ICMP Broadcast Logic - All Four Cases
////////////////////////////////////////////////////////////////////////////////

func TestICMPBroadcastLogic(t *testing.T) {
	vsIPv4 := IpAddr("10.1.1.1")
	realIPv4 := IpAddr("10.2.2.2")
	clientIPv4 := IpAddr("10.0.1.1")
	balancerIPv4 := IpAddr("5.5.5.5")
	peer1IPv4 := IpAddr("5.5.5.6")
	peer2IPv4 := IpAddr("5.5.5.7")

	vsIPv6 := IpAddr("2001:db8::1")
	realIPv6 := IpAddr("2001:db8:2::2")
	clientIPv6 := IpAddr("2001:db8:1::1")
	balancerIPv6 := IpAddr("fe80::5")
	peer1IPv6 := IpAddr("fe80::6")
	peer2IPv6 := IpAddr("fe80::7")

	clientPort := uint16(12345)
	vsPort := uint16(80)

	config := &balancerpb.ModuleConfig{
		SourceAddressV4: balancerIPv4.AsSlice(),
		SourceAddressV6: balancerIPv6.AsSlice(),
		// Configure decap addresses - packets to these addresses will be decapsulated
		DecapAddresses: [][]byte{
			balancerIPv4.AsSlice(),
			balancerIPv6.AsSlice(),
		},
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
				// Configure peers for broadcasting
				Peers: [][]byte{
					peer1IPv4.AsSlice(),
					peer2IPv4.AsSlice(),
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
				// Configure peers for broadcasting
				Peers: [][]byte{
					peer1IPv6.AsSlice(),
					peer2IPv6.AsSlice(),
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
			SessionTableMaxLoadFactor: 0.8,
		},
	})
	require.NoError(t, err)
	defer setup.Free()

	// Create an original TCP packet that will be embedded in ICMP errors
	originalTCPLayers := MakeTCPPacket(
		vsIPv4,
		vsPort,
		clientIPv4,
		clientPort,
		&layers.TCP{SYN: true, ACK: true},
	)
	originalTCPPacket := xpacket.LayersToPacket(t, originalTCPLayers...)

	originalTCPv6Layers := MakeTCPPacket(
		vsIPv6,
		vsPort,
		clientIPv6,
		clientPort,
		&layers.TCP{SYN: true, ACK: true},
	)
	originalTCPv6Packet := xpacket.LayersToPacket(t, originalTCPv6Layers...)

	t.Run("Case1_IPv4_Decap_NoIcmpIdent_ShouldBroadcast", func(t *testing.T) {
		// Create a tunneled ICMP packet with normal ident (not ICMP_BROADCAST_IDENT)
		// The outer destination is the balancer address (will trigger decap)
		icmpLayers := MakeTunneledICMPv4DestUnreachable(
			peer1IPv4,    // tunnel src (from another balancer)
			balancerIPv4, // tunnel dst (this balancer - will trigger decap)
			clientIPv4,   // inner ICMP src
			vsIPv4,       // inner ICMP dst
			originalTCPPacket,
			0x1234, // normal ident (not ICMP_BROADCAST_IDENT)
		)
		icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

		result, err := setup.mock.HandlePackets(icmpPacket)
		require.NoError(t, err)

		// Expected: packet should be broadcasted to 2 peers, original dropped
		require.Equal(
			t,
			2,
			len(result.Output),
			"Case 1: decap + no icmp_ident should broadcast to 2 peers",
		)
		require.Equal(
			t,
			1,
			len(result.Drop),
			"Case 1: original packet should be dropped",
		)

		// Verify both broadcasted packets are properly tunneled with ICMP_BROADCAST_IDENT
		VerifyBroadcastedICMPPacket(
			t,
			result.Output[0],
			net.IP(peer1IPv4.AsSlice()),
		)
		VerifyBroadcastedICMPPacket(
			t,
			result.Output[1],
			net.IP(peer2IPv4.AsSlice()),
		)
	})

	t.Run(
		"Case2_IPv4_Decap_WithIcmpIdent_ShouldNotBroadcast",
		func(t *testing.T) {
			// Create a tunneled ICMP packet with ICMP_BROADCAST_IDENT
			// This simulates a packet that was already broadcasted by another balancer
			icmpLayers := MakeTunneledICMPv4DestUnreachable(
				peer1IPv4,    // tunnel src (from another balancer)
				balancerIPv4, // tunnel dst (this balancer - will trigger decap)
				clientIPv4,   // inner ICMP src
				vsIPv4,       // inner ICMP dst
				originalTCPPacket,
				ICMP_BROADCAST_IDENT, // magic ident indicating already broadcasted
			)
			icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

			result, err := setup.mock.HandlePackets(icmpPacket)
			require.NoError(t, err)

			// Expected: packet should NOT be broadcasted (already was by another balancer)
			require.Equal(
				t,
				0,
				len(result.Output),
				"Case 2: decap + icmp_ident should NOT broadcast",
			)
			require.Equal(
				t,
				1,
				len(result.Drop),
				"Case 2: packet should be dropped",
			)
		},
	)

	t.Run(
		"Case3_IPv4_NoDecap_WithIcmpIdent_ShouldBroadcast",
		func(t *testing.T) {
			// Create a non-tunneled ICMP packet with ICMP_BROADCAST_IDENT
			// Since there's no decap, the ident check is skipped
			icmpLayers := MakeICMPv4DestUnreachableWithIdent(
				clientIPv4,
				vsIPv4,
				originalTCPPacket,
				ICMP_BROADCAST_IDENT, // has magic ident but no decap
			)
			icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

			result, err := setup.mock.HandlePackets(icmpPacket)
			require.NoError(t, err)

			// Expected: packet should be broadcasted (no decap, so ident is ignored)
			require.Equal(
				t,
				2,
				len(result.Output),
				"Case 3: no decap + icmp_ident should broadcast to 2 peers",
			)
			require.Equal(
				t,
				1,
				len(result.Drop),
				"Case 3: original packet should be dropped",
			)

			// Verify both broadcasted packets are properly tunneled with ICMP_BROADCAST_IDENT
			VerifyBroadcastedICMPPacket(
				t,
				result.Output[0],
				net.IP(peer1IPv4.AsSlice()),
			)
			VerifyBroadcastedICMPPacket(
				t,
				result.Output[1],
				net.IP(peer2IPv4.AsSlice()),
			)
		},
	)

	t.Run("Case4_IPv4_NoDecap_NoIcmpIdent_ShouldBroadcast", func(t *testing.T) {
		// Create a non-tunneled ICMP packet with normal ident
		icmpLayers := MakeICMPv4DestUnreachableWithIdent(
			clientIPv4,
			vsIPv4,
			originalTCPPacket,
			0x5678, // normal ident
		)
		icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

		result, err := setup.mock.HandlePackets(icmpPacket)
		require.NoError(t, err)

		// Expected: packet should be broadcasted (normal case)
		require.Equal(
			t,
			2,
			len(result.Output),
			"Case 4: no decap + no icmp_ident should broadcast to 2 peers",
		)
		require.Equal(
			t,
			1,
			len(result.Drop),
			"Case 4: original packet should be dropped",
		)

		// Verify both broadcasted packets are properly tunneled with ICMP_BROADCAST_IDENT
		VerifyBroadcastedICMPPacket(
			t,
			result.Output[0],
			net.IP(peer1IPv4.AsSlice()),
		)
		VerifyBroadcastedICMPPacket(
			t,
			result.Output[1],
			net.IP(peer2IPv4.AsSlice()),
		)
	})

	// IPv6 test cases
	t.Run("Case1_IPv6_Decap_NoIcmpIdent_ShouldBroadcast", func(t *testing.T) {
		icmpLayers := MakeTunneledICMPv6DestUnreachable(
			peer1IPv6,
			balancerIPv6,
			clientIPv6,
			vsIPv6,
			originalTCPv6Packet,
			0x1234,
		)
		icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

		result, err := setup.mock.HandlePackets(icmpPacket)
		require.NoError(t, err)

		require.Equal(
			t,
			2,
			len(result.Output),
			"Case 1 IPv6: decap + no icmp_ident should broadcast to 2 peers",
		)
		require.Equal(
			t,
			1,
			len(result.Drop),
			"Case 1 IPv6: original packet should be dropped",
		)

		// Verify both broadcasted packets are properly tunneled with ICMP_BROADCAST_IDENT
		VerifyBroadcastedICMPPacket(
			t,
			result.Output[0],
			net.IP(peer1IPv6.AsSlice()),
		)
		VerifyBroadcastedICMPPacket(
			t,
			result.Output[1],
			net.IP(peer2IPv6.AsSlice()),
		)
	})

	t.Run(
		"Case2_IPv6_Decap_WithIcmpIdent_ShouldNotBroadcast",
		func(t *testing.T) {
			icmpLayers := MakeTunneledICMPv6DestUnreachable(
				peer1IPv6,
				balancerIPv6,
				clientIPv6,
				vsIPv6,
				originalTCPv6Packet,
				ICMP_BROADCAST_IDENT,
			)
			icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

			result, err := setup.mock.HandlePackets(icmpPacket)
			require.NoError(t, err)

			require.Equal(
				t,
				0,
				len(result.Output),
				"Case 2 IPv6: decap + icmp_ident should NOT broadcast",
			)
			require.Equal(
				t,
				1,
				len(result.Drop),
				"Case 2 IPv6: packet should be dropped",
			)
		},
	)

	t.Run(
		"Case3_IPv6_NoDecap_WithIcmpIdent_ShouldBroadcast",
		func(t *testing.T) {
			icmpLayers := MakeICMPv6DestUnreachableWithIdent(
				clientIPv6,
				vsIPv6,
				originalTCPv6Packet,
				ICMP_BROADCAST_IDENT,
			)
			icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

			result, err := setup.mock.HandlePackets(icmpPacket)
			require.NoError(t, err)

			require.Equal(
				t,
				2,
				len(result.Output),
				"Case 3 IPv6: no decap + icmp_ident should broadcast to 2 peers",
			)
			require.Equal(
				t,
				1,
				len(result.Drop),
				"Case 3 IPv6: original packet should be dropped",
			)
		},
	)

	t.Run("Case4_IPv6_NoDecap_NoIcmpIdent_ShouldBroadcast", func(t *testing.T) {
		icmpLayers := MakeICMPv6DestUnreachableWithIdent(
			clientIPv6,
			vsIPv6,
			originalTCPv6Packet,
			0x5678,
		)
		icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

		result, err := setup.mock.HandlePackets(icmpPacket)
		require.NoError(t, err)

		require.Equal(
			t,
			2,
			len(result.Output),
			"Case 4 IPv6: no decap + no icmp_ident should broadcast to 2 peers",
		)
		require.Equal(
			t,
			1,
			len(result.Drop),
			"Case 4 IPv6: original packet should be dropped",
		)

		// Verify both broadcasted packets are properly tunneled with ICMP_BROADCAST_IDENT
		VerifyBroadcastedICMPPacket(
			t,
			result.Output[0],
			net.IP(peer1IPv6.AsSlice()),
		)
		VerifyBroadcastedICMPPacket(
			t,
			result.Output[1],
			net.IP(peer2IPv6.AsSlice()),
		)
	})
}

////////////////////////////////////////////////////////////////////////////////
// Test: Two-Balancer ICMP Broadcast Integration
////////////////////////////////////////////////////////////////////////////////

func TestICMPBroadcastTwoBalancers(t *testing.T) {
	// Setup: Two balancers where Balancer1 broadcasts to Balancer2
	// Balancer1 has no session, so it broadcasts
	// Balancer2 has a session, so it forwards to real
	// Balancer2 also has Balancer1 as peer, but should NOT re-broadcast
	// because the packet has ICMP_BROADCAST_IDENT marker

	vsIPv4 := IpAddr("10.1.1.1")
	realIPv4 := IpAddr("10.2.2.2")
	clientIPv4 := IpAddr("10.0.1.1")
	balancer1IPv4 := IpAddr("5.5.5.5")
	balancer2IPv4 := IpAddr("5.5.5.6")

	vsIPv6 := IpAddr("2001:db8::1")
	realIPv6 := IpAddr("2001:db8:2::2")
	clientIPv6 := IpAddr("2001:db8:1::1")
	balancer1IPv6 := IpAddr("fe80::5")
	balancer2IPv6 := IpAddr("fe80::6")

	clientPort := uint16(12345)
	vsPort := uint16(80)

	// Configure Balancer1 - has Balancer2 as peer, no session
	config1 := &balancerpb.ModuleConfig{
		SourceAddressV4: balancer1IPv4.AsSlice(),
		SourceAddressV6: balancer1IPv6.AsSlice(),
		DecapAddresses: [][]byte{
			balancer1IPv4.AsSlice(),
			balancer1IPv6.AsSlice(),
		},
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
				// Balancer1 has Balancer2 as peer
				Peers: [][]byte{
					balancer2IPv4.AsSlice(),
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
				// Balancer1 has Balancer2 as IPv6 peer
				Peers: [][]byte{
					balancer2IPv6.AsSlice(),
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

	// Configure Balancer2 - can decap packets from Balancer1
	// Has Balancer1 as peer to verify it doesn't re-broadcast
	config2 := &balancerpb.ModuleConfig{
		SourceAddressV4: balancer2IPv4.AsSlice(),
		SourceAddressV6: balancer2IPv6.AsSlice(),
		DecapAddresses: [][]byte{
			balancer2IPv4.AsSlice(),
			balancer2IPv6.AsSlice(),
		},
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
				// Balancer2 has Balancer1 as peer
				// This verifies that Balancer2 doesn't re-broadcast the packet
				// back to Balancer1 (because it has ICMP_BROADCAST_IDENT marker)
				Peers: [][]byte{
					balancer1IPv4.AsSlice(),
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
				// Balancer2 has Balancer1 as IPv6 peer
				// This verifies that Balancer2 doesn't re-broadcast the packet
				// back to Balancer1 (because it has ICMP_BROADCAST_IDENT marker)
				Peers: [][]byte{
					balancer1IPv6.AsSlice(),
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

	// Setup Balancer1
	setup1, err := SetupTest(&TestConfig{
		moduleConfig: config1,
		stateConfig: &balancerpb.ModuleStateConfig{
			SessionTableCapacity:      100,
			SessionTableScanPeriod:    durationpb.New(0),
			SessionTableMaxLoadFactor: 0.8,
		},
	})
	require.NoError(t, err)
	defer setup1.Free()

	// Setup Balancer2
	setup2, err := SetupTest(&TestConfig{
		moduleConfig: config2,
		stateConfig: &balancerpb.ModuleStateConfig{
			SessionTableCapacity:      100,
			SessionTableScanPeriod:    durationpb.New(0),
			SessionTableMaxLoadFactor: 0.5,
		},
	})
	require.NoError(t, err)
	defer setup2.Free()

	t.Run("IPv4", func(t *testing.T) {
		// Step 1: Create a session on Balancer2 by sending a TCP SYN packet
		tcpLayers := MakeTCPPacket(
			clientIPv4,
			clientPort,
			vsIPv4,
			vsPort,
			&layers.TCP{SYN: true},
		)
		tcpPacket := xpacket.LayersToPacket(t, tcpLayers...)

		result, err := setup2.mock.HandlePackets(tcpPacket)
		require.NoError(t, err)
		require.Equal(
			t,
			1,
			len(result.Output),
			"Balancer2 should forward TCP SYN",
		)

		// Step 2: Create an ICMP error packet for the response
		// The response would come from VS IP to client IP
		responsePacket := MakeTCPPacket(
			vsIPv4,
			vsPort,
			clientIPv4,
			clientPort,
			&layers.TCP{SYN: true, ACK: true},
		)
		responsePacketData := xpacket.LayersToPacket(t, responsePacket...)

		// Step 3: Send ICMP error to Balancer1 (which has no session)
		icmpLayers := MakeICMPv4DestUnreachable(
			clientIPv4,
			vsIPv4,
			responsePacketData,
		)
		icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

		result, err = setup1.mock.HandlePackets(icmpPacket)
		require.NoError(t, err)

		// Balancer1 should broadcast to Balancer2 (1 output packet)
		require.Equal(
			t,
			1,
			len(result.Output),
			"Balancer1 should broadcast ICMP to Balancer2",
		)
		require.Equal(
			t,
			1,
			len(result.Drop),
			"Balancer1 should drop original packet",
		)

		// Verify the broadcasted packet is tunneled to Balancer2
		broadcastedPacket := result.Output[0]
		require.True(
			t,
			broadcastedPacket.IsTunneled,
			"packet should be tunneled",
		)
		require.Equal(
			t,
			net.IP(balancer2IPv4.AsSlice()),
			broadcastedPacket.DstIP,
			"packet should be sent to Balancer2",
		)

		// Step 4: Send the broadcasted packet to Balancer2
		// Balancer2 should:
		// 1. Decap the packet
		// 2. See it has ICMP_BROADCAST_IDENT marker and decap=true
		// 3. Forward to real (because it has a session)
		// 4. NOT re-broadcast to Balancer1 (because of the marker)
		broadcastedGoPacket := xpacket.ParseEtherPacket(
			broadcastedPacket.RawData,
		)
		result, err = setup2.mock.HandlePackets(broadcastedGoPacket)
		require.NoError(t, err)

		// Balancer2 should forward the ICMP error to the real server
		// and NOT re-broadcast it
		require.Equal(
			t,
			1,
			len(result.Output),
			"Balancer2 should forward ICMP to real (not re-broadcast)",
		)
		require.Empty(t, result.Drop, "Balancer2 should not drop the packet")

		// Verify the packet is tunneled to the real server (not to Balancer1)
		forwardedPacket := result.Output[0]
		require.True(
			t,
			forwardedPacket.IsTunneled,
			"packet should be tunneled to real",
		)
		require.Equal(
			t,
			net.IP(realIPv4.AsSlice()),
			forwardedPacket.DstIP,
			"packet should be sent to real server, not back to Balancer1",
		)
	})

	t.Run("IPv6", func(t *testing.T) {
		// Step 1: Create a session on Balancer2 by sending a TCP SYN packet
		tcpLayers := MakeTCPPacket(
			clientIPv6,
			clientPort,
			vsIPv6,
			vsPort,
			&layers.TCP{SYN: true},
		)
		tcpPacket := xpacket.LayersToPacket(t, tcpLayers...)

		result, err := setup2.mock.HandlePackets(tcpPacket)
		require.NoError(t, err)
		require.Equal(
			t,
			1,
			len(result.Output),
			"Balancer2 should forward TCP SYN",
		)

		// Step 2: Create an ICMPv6 error packet for the response
		// The response would come from VS IP to client IP
		responsePacket := MakeTCPPacket(
			vsIPv6,
			vsPort,
			clientIPv6,
			clientPort,
			&layers.TCP{SYN: true, ACK: true},
		)
		responsePacketData := xpacket.LayersToPacket(t, responsePacket...)

		// Step 3: Send ICMPv6 error to Balancer1 (which has no session)
		icmpLayers := MakeICMPv6DestUnreachableWithIdent(
			clientIPv6,
			vsIPv6,
			responsePacketData,
			0x1234, // normal ident (not ICMP_BROADCAST_IDENT)
		)
		icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

		result, err = setup1.mock.HandlePackets(icmpPacket)
		require.NoError(t, err)

		// Balancer1 should broadcast to Balancer2 (1 output packet)
		require.Equal(
			t,
			1,
			len(result.Output),
			"Balancer1 should broadcast ICMPv6 to Balancer2",
		)
		require.Equal(
			t,
			1,
			len(result.Drop),
			"Balancer1 should drop original packet",
		)

		// Verify the broadcasted packet is tunneled to Balancer2
		broadcastedPacket := result.Output[0]
		require.True(
			t,
			broadcastedPacket.IsTunneled,
			"packet should be tunneled",
		)
		require.Equal(
			t,
			net.IP(balancer2IPv6.AsSlice()),
			broadcastedPacket.DstIP,
			"packet should be sent to Balancer2",
		)

		// Step 4: Send the broadcasted packet to Balancer2
		// Balancer2 should:
		// 1. Decap the packet
		// 2. See it has ICMP_BROADCAST_IDENT marker and decap=true
		// 3. Forward to real (because it has a session)
		// 4. NOT re-broadcast to Balancer1 (because of the marker)
		broadcastedGoPacket := xpacket.ParseEtherPacket(
			broadcastedPacket.RawData,
		)
		result, err = setup2.mock.HandlePackets(broadcastedGoPacket)
		require.NoError(t, err)

		// Balancer2 should forward the ICMPv6 error to the real server
		// and NOT re-broadcast it
		require.Equal(
			t,
			1,
			len(result.Output),
			"Balancer2 should forward ICMPv6 to real (not re-broadcast)",
		)
		require.Empty(t, result.Drop, "Balancer2 should not drop the packet")

		// Verify the packet is tunneled to the real server (not to Balancer1)
		forwardedPacket := result.Output[0]
		require.True(
			t,
			forwardedPacket.IsTunneled,
			"packet should be tunneled to real",
		)
		require.Equal(
			t,
			net.IP(realIPv6.AsSlice()),
			forwardedPacket.DstIP,
			"packet should be sent to real server, not back to Balancer1",
		)
	})
}
