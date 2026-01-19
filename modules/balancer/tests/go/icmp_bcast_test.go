package balancer_test

// TestICMPBroadcast is a comprehensive test suite for ICMP broadcast functionality in the balancer module that covers:
//
// # ICMP Broadcast Logic - Four Cases
// - Case 1: Decap + No ICMP_BROADCAST_IDENT → Should broadcast to peers
// - Case 2: Decap + ICMP_BROADCAST_IDENT → Should NOT broadcast (already broadcasted)
// - Case 3: No Decap + ICMP_BROADCAST_IDENT → Should broadcast (ident check skipped)
// - Case 4: No Decap + No ICMP_BROADCAST_IDENT → Should broadcast (normal case)
//
// # ICMP Broadcast Marker
// - ICMP_BROADCAST_IDENT (0x0BDC) magic value to prevent re-broadcasting
// - Marker set in the unused field of ICMP error messages
// - Prevents broadcast loops between multiple balancers
//
// # Tunneled ICMP Packets
// - IP-in-IP tunneling for IPv4 ICMP errors
// - IPv6-in-IPv6 tunneling for ICMPv6 errors
// - Proper decapsulation and marker checking
//
// # Two-Balancer Integration
// - Balancer1 broadcasts ICMP error to Balancer2
// - Balancer2 receives broadcasted packet with marker
// - Balancer2 forwards to real server (has session)
// - Balancer2 does NOT re-broadcast to Balancer1 (marker prevents loop)
//
// The test validates:
// - Correct broadcast behavior based on decap and marker presence
// - Prevention of broadcast loops using ICMP_BROADCAST_IDENT
// - Proper tunneling and decapsulation
// - Session-based forwarding after broadcast reception
// - IPv4 and IPv6 support for all scenarios

import (
	"net"
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/tests/go/utils"
	"google.golang.org/protobuf/types/known/durationpb"
)

////////////////////////////////////////////////////////////////////////////////
// Test: ICMP Broadcast Logic - All Four Cases
////////////////////////////////////////////////////////////////////////////////

func TestICMPBroadcastLogic(t *testing.T) {
	vsIPv4 := netip.MustParseAddr("10.1.1.1")
	realIPv4 := netip.MustParseAddr("10.2.2.2")
	clientIPv4 := netip.MustParseAddr("10.0.1.1")
	balancerIPv4 := netip.MustParseAddr("5.5.5.5")
	peer1IPv4 := netip.MustParseAddr("5.5.5.6")
	peer2IPv4 := netip.MustParseAddr("5.5.5.7")

	vsIPv6 := netip.MustParseAddr("2001:db8::1")
	realIPv6 := netip.MustParseAddr("2001:db8:2::2")
	clientIPv6 := netip.MustParseAddr("2001:db8:1::1")
	balancerIPv6 := netip.MustParseAddr("fe80::5")
	peer1IPv6 := netip.MustParseAddr("fe80::6")
	peer2IPv6 := netip.MustParseAddr("fe80::7")

	clientPort := uint16(12345)
	vsPort := uint16(80)

	config := &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: &balancerpb.Addr{
				Bytes: balancerIPv4.AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: balancerIPv6.AsSlice(),
			},
			// Configure decap addresses - packets to these addresses will be decapsulated
			DecapAddresses: []*balancerpb.Addr{
				{Bytes: balancerIPv4.AsSlice()},
				{Bytes: balancerIPv6.AsSlice()},
			},
			Vs: []*balancerpb.VirtualService{
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: vsIPv4.AsSlice(),
						},
						Port:  uint32(vsPort),
						Proto: balancerpb.TransportProto_TCP,
					},
					Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
					AllowedSrcs: []*balancerpb.Net{
						{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("10.0.0.0").
									AsSlice(),
							},
							Size: 8,
						},
					},
					Flags: &balancerpb.VsFlags{
						Gre:    false,
						FixMss: false,
						Ops:    false,
						PureL3: false,
						Wlc:    false,
					},
					Reals: []*balancerpb.Real{
						{
							Id: &balancerpb.RelativeRealIdentifier{
								Ip: &balancerpb.Addr{
									Bytes: realIPv4.AsSlice(),
								},
								Port: 0,
							},
							Weight: 1,
							SrcAddr: &balancerpb.Addr{
								Bytes: realIPv4.AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.255").
									AsSlice(),
							},
						},
					},
					// Configure peers for broadcasting
					Peers: []*balancerpb.Addr{
						{Bytes: peer1IPv4.AsSlice()},
						{Bytes: peer2IPv4.AsSlice()},
					},
				},
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: vsIPv6.AsSlice(),
						},
						Port:  uint32(vsPort),
						Proto: balancerpb.TransportProto_TCP,
					},
					Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
					AllowedSrcs: []*balancerpb.Net{
						{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("2001:db8::").
									AsSlice(),
							},
							Size: 32,
						},
					},
					Flags: &balancerpb.VsFlags{
						Gre:    false,
						FixMss: false,
						Ops:    false,
						PureL3: false,
						Wlc:    false,
					},
					Reals: []*balancerpb.Real{
						{
							Id: &balancerpb.RelativeRealIdentifier{
								Ip: &balancerpb.Addr{
									Bytes: realIPv6.AsSlice(),
								},
								Port: 0,
							},
							Weight: 1,
							SrcAddr: &balancerpb.Addr{
								Bytes: realIPv6.AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff").
									AsSlice(),
							},
						},
					},
					// Configure peers for broadcasting
					Peers: []*balancerpb.Addr{
						{Bytes: peer1IPv6.AsSlice()},
						{Bytes: peer2IPv6.AsSlice()},
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
		},
		State: &balancerpb.StateConfig{
			SessionTableCapacity:      func() *uint64 { v := uint64(100); return &v }(),
			SessionTableMaxLoadFactor: func() *float32 { v := float32(0.8); return &v }(),
			RefreshPeriod:             durationpb.New(0),
			Wlc: &balancerpb.WlcConfig{
				Power:     func() *uint64 { v := uint64(10); return &v }(),
				MaxWeight: func() *uint32 { v := uint32(1000); return &v }(),
			},
		},
	}

	ts, err := utils.Make(&utils.TestConfig{
		Mock:     utils.SingleWorkerMockConfig(64*datasize.MB, 4*datasize.MB),
		Balancer: config,
		AgentMemory: func() *datasize.ByteSize {
			memory := 16 * datasize.MB
			return &memory
		}(),
	})
	require.NoError(t, err)
	defer ts.Free()

	// Create an original TCP packet that will be embedded in ICMP errors
	originalTCPLayers := utils.MakeTCPPacket(
		vsIPv4,
		vsPort,
		clientIPv4,
		clientPort,
		&layers.TCP{SYN: true, ACK: true},
	)
	originalTCPPacket := xpacket.LayersToPacket(t, originalTCPLayers...)

	originalTCPv6Layers := utils.MakeTCPPacket(
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
		icmpLayers := utils.MakeTunneledICMPv4DestUnreachable(
			peer1IPv4,    // tunnel src (from another balancer)
			balancerIPv4, // tunnel dst (this balancer - will trigger decap)
			clientIPv4,   // inner ICMP src
			vsIPv4,       // inner ICMP dst
			originalTCPPacket,
			0x1234, // normal ident (not ICMP_BROADCAST_IDENT)
		)
		icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

		result, err := ts.Mock.HandlePackets(icmpPacket)
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
		utils.VerifyBroadcastedICMPPacket(
			t,
			result.Output[0],
			net.IP(peer1IPv4.AsSlice()),
		)
		utils.VerifyBroadcastedICMPPacket(
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
			icmpLayers := utils.MakeTunneledICMPv4DestUnreachable(
				peer1IPv4,    // tunnel src (from another balancer)
				balancerIPv4, // tunnel dst (this balancer - will trigger decap)
				clientIPv4,   // inner ICMP src
				vsIPv4,       // inner ICMP dst
				originalTCPPacket,
				utils.ICMP_BROADCAST_IDENT, // magic ident indicating already broadcasted
			)
			icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

			result, err := ts.Mock.HandlePackets(icmpPacket)
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
			icmpLayers := utils.MakeICMPv4DestUnreachableWithIdent(
				clientIPv4,
				vsIPv4,
				originalTCPPacket,
				utils.ICMP_BROADCAST_IDENT, // has magic ident but no decap
			)
			icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

			result, err := ts.Mock.HandlePackets(icmpPacket)
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
			utils.VerifyBroadcastedICMPPacket(
				t,
				result.Output[0],
				net.IP(peer1IPv4.AsSlice()),
			)
			utils.VerifyBroadcastedICMPPacket(
				t,
				result.Output[1],
				net.IP(peer2IPv4.AsSlice()),
			)
		},
	)

	t.Run("Case4_IPv4_NoDecap_NoIcmpIdent_ShouldBroadcast", func(t *testing.T) {
		// Create a non-tunneled ICMP packet with normal ident
		icmpLayers := utils.MakeICMPv4DestUnreachableWithIdent(
			clientIPv4,
			vsIPv4,
			originalTCPPacket,
			0x5678, // normal ident
		)
		icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

		result, err := ts.Mock.HandlePackets(icmpPacket)
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
		utils.VerifyBroadcastedICMPPacket(
			t,
			result.Output[0],
			net.IP(peer1IPv4.AsSlice()),
		)
		utils.VerifyBroadcastedICMPPacket(
			t,
			result.Output[1],
			net.IP(peer2IPv4.AsSlice()),
		)
	})

	// IPv6 test cases
	t.Run("Case1_IPv6_Decap_NoIcmpIdent_ShouldBroadcast", func(t *testing.T) {
		icmpLayers := utils.MakeTunneledICMPv6DestUnreachable(
			peer1IPv6,
			balancerIPv6,
			clientIPv6,
			vsIPv6,
			originalTCPv6Packet,
			0x1234,
		)
		icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

		result, err := ts.Mock.HandlePackets(icmpPacket)
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
		utils.VerifyBroadcastedICMPPacket(
			t,
			result.Output[0],
			net.IP(peer1IPv6.AsSlice()),
		)
		utils.VerifyBroadcastedICMPPacket(
			t,
			result.Output[1],
			net.IP(peer2IPv6.AsSlice()),
		)
	})

	t.Run(
		"Case2_IPv6_Decap_WithIcmpIdent_ShouldNotBroadcast",
		func(t *testing.T) {
			icmpLayers := utils.MakeTunneledICMPv6DestUnreachable(
				peer1IPv6,
				balancerIPv6,
				clientIPv6,
				vsIPv6,
				originalTCPv6Packet,
				utils.ICMP_BROADCAST_IDENT,
			)
			icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

			result, err := ts.Mock.HandlePackets(icmpPacket)
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
			icmpLayers := utils.MakeICMPv6DestUnreachableWithIdent(
				clientIPv6,
				vsIPv6,
				originalTCPv6Packet,
				utils.ICMP_BROADCAST_IDENT,
			)
			icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

			result, err := ts.Mock.HandlePackets(icmpPacket)
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
		icmpLayers := utils.MakeICMPv6DestUnreachableWithIdent(
			clientIPv6,
			vsIPv6,
			originalTCPv6Packet,
			0x5678,
		)
		icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

		result, err := ts.Mock.HandlePackets(icmpPacket)
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
		utils.VerifyBroadcastedICMPPacket(
			t,
			result.Output[0],
			net.IP(peer1IPv6.AsSlice()),
		)
		utils.VerifyBroadcastedICMPPacket(
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

	vsIPv4 := netip.MustParseAddr("10.1.1.1")
	realIPv4 := netip.MustParseAddr("10.2.2.2")
	clientIPv4 := netip.MustParseAddr("10.0.1.1")
	balancer1IPv4 := netip.MustParseAddr("5.5.5.5")
	balancer2IPv4 := netip.MustParseAddr("5.5.5.6")

	vsIPv6 := netip.MustParseAddr("2001:db8::1")
	realIPv6 := netip.MustParseAddr("2001:db8:2::2")
	clientIPv6 := netip.MustParseAddr("2001:db8:1::1")
	balancer1IPv6 := netip.MustParseAddr("fe80::5")
	balancer2IPv6 := netip.MustParseAddr("fe80::6")

	clientPort := uint16(12345)
	vsPort := uint16(80)

	// Configure Balancer1 - has Balancer2 as peer, no session
	config1 := &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: &balancerpb.Addr{
				Bytes: balancer1IPv4.AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: balancer1IPv6.AsSlice(),
			},
			DecapAddresses: []*balancerpb.Addr{
				{Bytes: balancer1IPv4.AsSlice()},
				{Bytes: balancer1IPv6.AsSlice()},
			},
			Vs: []*balancerpb.VirtualService{
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: vsIPv4.AsSlice(),
						},
						Port:  uint32(vsPort),
						Proto: balancerpb.TransportProto_TCP,
					},
					Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
					AllowedSrcs: []*balancerpb.Net{
						{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("10.0.0.0").
									AsSlice(),
							},
							Size: 8,
						},
					},
					Flags: &balancerpb.VsFlags{
						Gre:    false,
						FixMss: false,
						Ops:    false,
						PureL3: false,
						Wlc:    false,
					},
					Reals: []*balancerpb.Real{
						{
							Id: &balancerpb.RelativeRealIdentifier{
								Ip: &balancerpb.Addr{
									Bytes: realIPv4.AsSlice(),
								},
								Port: 0,
							},
							Weight: 1,
							SrcAddr: &balancerpb.Addr{
								Bytes: realIPv4.AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.255").
									AsSlice(),
							},
						},
					},
					// Balancer1 has Balancer2 as peer
					Peers: []*balancerpb.Addr{
						{Bytes: balancer2IPv4.AsSlice()},
					},
				},
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: vsIPv6.AsSlice(),
						},
						Port:  uint32(vsPort),
						Proto: balancerpb.TransportProto_TCP,
					},
					Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
					AllowedSrcs: []*balancerpb.Net{
						{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("2001:db8::").
									AsSlice(),
							},
							Size: 32,
						},
					},
					Flags: &balancerpb.VsFlags{
						Gre:    false,
						FixMss: false,
						Ops:    false,
						PureL3: false,
						Wlc:    false,
					},
					Reals: []*balancerpb.Real{
						{
							Id: &balancerpb.RelativeRealIdentifier{
								Ip: &balancerpb.Addr{
									Bytes: realIPv6.AsSlice(),
								},
								Port: 0,
							},
							Weight: 1,
							SrcAddr: &balancerpb.Addr{
								Bytes: realIPv6.AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff").
									AsSlice(),
							},
						},
					},
					// Balancer1 has Balancer2 as IPv6 peer
					Peers: []*balancerpb.Addr{
						{Bytes: balancer2IPv6.AsSlice()},
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
		},
		State: &balancerpb.StateConfig{
			SessionTableCapacity:      func() *uint64 { v := uint64(100); return &v }(),
			SessionTableMaxLoadFactor: func() *float32 { v := float32(0.8); return &v }(),
			RefreshPeriod:             durationpb.New(0),
			Wlc: &balancerpb.WlcConfig{
				Power:     func() *uint64 { v := uint64(10); return &v }(),
				MaxWeight: func() *uint32 { v := uint32(1000); return &v }(),
			},
		},
	}

	// Configure Balancer2 - can decap packets from Balancer1
	// Has Balancer1 as peer to verify it doesn't re-broadcast
	config2 := &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: &balancerpb.Addr{
				Bytes: balancer2IPv4.AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: balancer2IPv6.AsSlice(),
			},
			DecapAddresses: []*balancerpb.Addr{
				{Bytes: balancer2IPv4.AsSlice()},
				{Bytes: balancer2IPv6.AsSlice()},
			},
			Vs: []*balancerpb.VirtualService{
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: vsIPv4.AsSlice(),
						},
						Port:  uint32(vsPort),
						Proto: balancerpb.TransportProto_TCP,
					},
					Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
					AllowedSrcs: []*balancerpb.Net{
						{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("10.0.0.0").
									AsSlice(),
							},
							Size: 8,
						},
					},
					Flags: &balancerpb.VsFlags{
						Gre:    false,
						FixMss: false,
						Ops:    false,
						PureL3: false,
						Wlc:    false,
					},
					Reals: []*balancerpb.Real{
						{
							Id: &balancerpb.RelativeRealIdentifier{
								Ip: &balancerpb.Addr{
									Bytes: realIPv4.AsSlice(),
								},
								Port: 0,
							},
							Weight: 1,
							SrcAddr: &balancerpb.Addr{
								Bytes: realIPv4.AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.255").
									AsSlice(),
							},
						},
					},
					// Balancer2 has Balancer1 as peer
					// This verifies that Balancer2 doesn't re-broadcast the packet
					// back to Balancer1 (because it has ICMP_BROADCAST_IDENT marker)
					Peers: []*balancerpb.Addr{
						{Bytes: balancer1IPv4.AsSlice()},
					},
				},
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: vsIPv6.AsSlice(),
						},
						Port:  uint32(vsPort),
						Proto: balancerpb.TransportProto_TCP,
					},
					Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
					AllowedSrcs: []*balancerpb.Net{
						{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("2001:db8::").
									AsSlice(),
							},
							Size: 32,
						},
					},
					Flags: &balancerpb.VsFlags{
						Gre:    false,
						FixMss: false,
						Ops:    false,
						PureL3: false,
						Wlc:    false,
					},
					Reals: []*balancerpb.Real{
						{
							Id: &balancerpb.RelativeRealIdentifier{
								Ip: &balancerpb.Addr{
									Bytes: realIPv6.AsSlice(),
								},
								Port: 0,
							},
							Weight: 1,
							SrcAddr: &balancerpb.Addr{
								Bytes: realIPv6.AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff").
									AsSlice(),
							},
						},
					},
					// Balancer2 has Balancer1 as IPv6 peer
					// This verifies that Balancer2 doesn't re-broadcast the packet
					// back to Balancer1 (because it has ICMP_BROADCAST_IDENT marker)
					Peers: []*balancerpb.Addr{
						{Bytes: balancer1IPv6.AsSlice()},
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
		},
		State: &balancerpb.StateConfig{
			SessionTableCapacity:      func() *uint64 { v := uint64(100); return &v }(),
			SessionTableMaxLoadFactor: func() *float32 { v := float32(0.5); return &v }(),
			RefreshPeriod:             durationpb.New(0),
			Wlc: &balancerpb.WlcConfig{
				Power:     func() *uint64 { v := uint64(10); return &v }(),
				MaxWeight: func() *uint32 { v := uint32(1000); return &v }(),
			},
		},
	}

	// Setup Balancer1
	setup1, err := utils.Make(&utils.TestConfig{
		Mock:     utils.SingleWorkerMockConfig(64*datasize.MB, 4*datasize.MB),
		Balancer: config1,
		AgentMemory: func() *datasize.ByteSize {
			value := 16 * datasize.MB
			return &value
		}(),
	})
	require.NoError(t, err)
	defer setup1.Free()

	// Setup Balancer2
	setup2, err := utils.Make(&utils.TestConfig{
		Mock:     utils.SingleWorkerMockConfig(64*datasize.MB, 4*datasize.MB),
		Balancer: config2,
		AgentMemory: func() *datasize.ByteSize {
			value := 16 * datasize.MB
			return &value
		}(),
	})
	require.NoError(t, err)
	defer setup2.Free()

	// Enable all reals on both balancers
	utils.EnableAllReals(t, setup1)
	utils.EnableAllReals(t, setup2)

	t.Run("IPv4", func(t *testing.T) {
		// Step 1: Create a session on Balancer2 by sending a TCP SYN packet
		tcpLayers := utils.MakeTCPPacket(
			clientIPv4,
			clientPort,
			vsIPv4,
			vsPort,
			&layers.TCP{SYN: true},
		)
		tcpPacket := xpacket.LayersToPacket(t, tcpLayers...)

		result, err := setup2.Mock.HandlePackets(tcpPacket)
		require.NoError(t, err)
		require.Equal(
			t,
			1,
			len(result.Output),
			"Balancer2 should forward TCP SYN",
		)

		// Step 2: Create an ICMP error packet for the response
		// The response would come from VS IP to client IP
		responsePacket := utils.MakeTCPPacket(
			vsIPv4,
			vsPort,
			clientIPv4,
			clientPort,
			&layers.TCP{SYN: true, ACK: true},
		)
		responsePacketData := xpacket.LayersToPacket(t, responsePacket...)

		// Step 3: Send ICMP error to Balancer1 (which has no session)
		icmpLayers := utils.MakeICMPv4DestUnreachable(
			clientIPv4,
			vsIPv4,
			responsePacketData,
		)
		icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

		result, err = setup1.Mock.HandlePackets(icmpPacket)
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
		result, err = setup2.Mock.HandlePackets(broadcastedGoPacket)
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
		tcpLayers := utils.MakeTCPPacket(
			clientIPv6,
			clientPort,
			vsIPv6,
			vsPort,
			&layers.TCP{SYN: true},
		)
		tcpPacket := xpacket.LayersToPacket(t, tcpLayers...)

		result, err := setup2.Mock.HandlePackets(tcpPacket)
		require.NoError(t, err)
		require.Equal(
			t,
			1,
			len(result.Output),
			"Balancer2 should forward TCP SYN",
		)

		// Step 2: Create an ICMPv6 error packet for the response
		// The response would come from VS IP to client IP
		responsePacket := utils.MakeTCPPacket(
			vsIPv6,
			vsPort,
			clientIPv6,
			clientPort,
			&layers.TCP{SYN: true, ACK: true},
		)
		responsePacketData := xpacket.LayersToPacket(t, responsePacket...)

		// Step 3: Send ICMPv6 error to Balancer1 (which has no session)
		icmpLayers := utils.MakeICMPv6DestUnreachableWithIdent(
			clientIPv6,
			vsIPv6,
			responsePacketData,
			0x1234, // normal ident (not ICMP_BROADCAST_IDENT)
		)
		icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

		result, err = setup1.Mock.HandlePackets(icmpPacket)
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
		result, err = setup2.Mock.HandlePackets(broadcastedGoPacket)
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
