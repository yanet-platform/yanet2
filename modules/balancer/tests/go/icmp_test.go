package balancer_test

// TestICMP is a comprehensive test suite for ICMP packet handling in the balancer module that covers:
//
// # ICMP Echo Request/Reply
// - IPv4 and IPv6 echo request handling
// - Proper echo reply generation
// - IP address swapping in responses
// - TTL/HopLimit reset to 64
//
// # ICMP Echo to Non-Virtual Service
// - Dropping echo requests to non-configured VS IPs
// - Proper response to valid VS IPs
// - IPv4 and IPv6 validation
//
// # ICMP Error Packet Forwarding
// - Forwarding ICMP errors when session exists
// - Tunneling ICMP errors to real servers
// - IPv4 Destination Unreachable handling
// - IPv6 Destination Unreachable handling
//
// # ICMP Error Packet Dropping
// - Dropping ICMP errors for unknown virtual services
// - Dropping ICMP errors when no session exists
// - Broadcasting ICMP errors to peers when no session found
//
// The test validates:
// - Correct ICMP packet type and code
// - Proper IP address handling
// - Session-based ICMP error forwarding
// - Peer broadcasting for unknown sessions
// - Packet tunneling to real servers

import (
	"net"
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/tests/go/utils"
	"google.golang.org/protobuf/types/known/durationpb"
)

////////////////////////////////////////////////////////////////////////////////
// Test: ICMP Echo Request/Reply for IPv4 and IPv6
////////////////////////////////////////////////////////////////////////////////

func TestICMPEchoRequest(t *testing.T) {
	// Define test addresses
	vsIPv4 := netip.MustParseAddr("10.1.1.1")
	clientIPv4 := netip.MustParseAddr("10.0.1.1")
	vsIPv6 := netip.MustParseAddr("2001:db8::1")
	clientIPv6 := netip.MustParseAddr("2001:db8:1::1")

	// Create balancer configuration
	config := &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("5.5.5.5").AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("fe80::5").AsSlice(),
			},
			Vs: []*balancerpb.VirtualService{
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: vsIPv4.AsSlice(),
						},
						Port:  80,
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
									Bytes: netip.MustParseAddr("10.2.2.2").
										AsSlice(),
								},
								Port: 0,
							},
							Weight: 1,
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("10.2.2.2").
									AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.255").
									AsSlice(),
							},
						},
					},
					Peers: []*balancerpb.Addr{},
				},
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: vsIPv6.AsSlice(),
						},
						Port:  80,
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
									Bytes: netip.MustParseAddr("2001:db8:2::2").
										AsSlice(),
								},
								Port: 0,
							},
							Weight: 1,
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("2001:db8:2::2").
									AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff").
									AsSlice(),
							},
						},
					},
					Peers: []*balancerpb.Addr{},
				},
			},
			DecapAddresses: []*balancerpb.Addr{},
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

	// Setup test
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

	t.Run("IPv4", func(t *testing.T) {
		// Create ICMP Echo Request
		packetLayers := utils.MakeICMPv4EchoRequest(clientIPv4, vsIPv4, 1234, 1)
		packet := xpacket.LayersToPacket(t, packetLayers...)

		// Send packet
		result, err := ts.Mock.HandlePackets(packet)
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
		packetLayers := utils.MakeICMPv6EchoRequest(clientIPv6, vsIPv6, 5678, 2)
		packet := xpacket.LayersToPacket(t, packetLayers...)

		// Send packet
		result, err := ts.Mock.HandlePackets(packet)
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
	vsIPv4 := netip.MustParseAddr("10.1.1.1")
	nonVsIPv4 := netip.MustParseAddr("10.99.99.99") // Not configured as VS
	clientIPv4 := netip.MustParseAddr("10.0.1.1")

	vsIPv6 := netip.MustParseAddr("2001:db8::1")
	nonVsIPv6 := netip.MustParseAddr("2001:db8:99::99") // Not configured as VS
	clientIPv6 := netip.MustParseAddr("2001:db8:1::1")

	config := &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("5.5.5.5").AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("fe80::5").AsSlice(),
			},
			Vs: []*balancerpb.VirtualService{
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: vsIPv4.AsSlice(),
						},
						Port:  80,
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
									Bytes: netip.MustParseAddr("10.2.2.2").
										AsSlice(),
								},
								Port: 0,
							},
							Weight: 1,
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("10.2.2.2").
									AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.255").
									AsSlice(),
							},
						},
					},
					Peers: []*balancerpb.Addr{},
				},
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: vsIPv6.AsSlice(),
						},
						Port:  80,
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
									Bytes: netip.MustParseAddr("2001:db8:2::2").
										AsSlice(),
								},
								Port: 0,
							},
							Weight: 1,
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("2001:db8:2::2").
									AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff").
									AsSlice(),
							},
						},
					},
					Peers: []*balancerpb.Addr{},
				},
			},
			DecapAddresses: []*balancerpb.Addr{},
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

	ts, err := utils.Make(&utils.TestConfig{
		Mock:     utils.SingleWorkerMockConfig(64*datasize.MB, 4*datasize.MB),
		Balancer: config,
		AgentMemory: func() *datasize.ByteSize {
			value := 16 * datasize.MB
			return &value
		}(),
	})
	require.NoError(t, err)
	defer ts.Free()

	t.Run("IPv4_NonVS_ShouldDrop", func(t *testing.T) {
		// Create ICMP Echo Request to non-VS IP
		packetLayers := utils.MakeICMPv4EchoRequest(
			clientIPv4,
			nonVsIPv4,
			1234,
			1,
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)

		// Send packet
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)

		// Packet should be dropped, not responded to
		require.Empty(t, result.Output, "should not respond to non-VS IP")
		require.Equal(t, 1, len(result.Drop), "should drop packet")
	})

	t.Run("IPv6_NonVS_ShouldDrop", func(t *testing.T) {
		// Create ICMPv6 Echo Request to non-VS IP
		packetLayers := utils.MakeICMPv6EchoRequest(
			clientIPv6,
			nonVsIPv6,
			5678,
			2,
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)

		// Send packet
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)

		// Packet should be dropped, not responded to
		require.Empty(t, result.Output, "should not respond to non-VS IP")
		require.Equal(t, 1, len(result.Drop), "should drop packet")
	})

	t.Run("IPv4_ValidVS_ShouldRespond", func(t *testing.T) {
		// Create ICMP Echo Request to valid VS IP
		packetLayers := utils.MakeICMPv4EchoRequest(clientIPv4, vsIPv4, 1234, 1)
		packet := xpacket.LayersToPacket(t, packetLayers...)

		// Send packet
		result, err := ts.Mock.HandlePackets(packet)
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
	vsIPv4 := netip.MustParseAddr("10.1.1.1")
	realIPv4 := netip.MustParseAddr("10.2.2.2")
	clientIPv4 := netip.MustParseAddr("10.0.1.1")

	vsIPv6 := netip.MustParseAddr("2001:db8::1")
	realIPv6 := netip.MustParseAddr("2001:db8:2::2")
	clientIPv6 := netip.MustParseAddr("2001:db8:1::1")

	clientPort := uint16(12345)
	vsPort := uint16(80)

	config := &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("5.5.5.5").AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("fe80::5").AsSlice(),
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
					Peers: []*balancerpb.Addr{},
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
					Peers: []*balancerpb.Addr{},
				},
			},
			DecapAddresses: []*balancerpb.Addr{},
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

	ts, err := utils.Make(&utils.TestConfig{
		Mock:     utils.SingleWorkerMockConfig(64*datasize.MB, 4*datasize.MB),
		Balancer: config,
		AgentMemory: func() *datasize.ByteSize {
			value := 16 * datasize.MB
			return &value
		}(),
	})
	require.NoError(t, err)
	defer ts.Free()

	// Enable all reals
	utils.EnableAllReals(t, ts)

	t.Run("IPv4", func(t *testing.T) {
		// First, create a session by sending a TCP SYN packet
		tcpLayers := utils.MakeTCPPacket(
			clientIPv4,
			clientPort,
			vsIPv4,
			vsPort,
			&layers.TCP{SYN: true},
		)
		tcpPacket := xpacket.LayersToPacket(t, tcpLayers...)

		result, err := ts.Mock.HandlePackets(tcpPacket)
		require.NoError(t, err)
		require.Equal(
			t,
			1,
			len(result.Output),
			"TCP packet should be forwarded",
		)

		// Now simulate the real server's response packet (which would trigger an ICMP error)
		// The real server responds with src=vsIP (as configured), dst=clientIP
		responsePacket := utils.MakeTCPPacket(
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
		icmpLayers := utils.MakeICMPv4DestUnreachable(
			clientIPv4,
			vsIPv4,
			responsePacketData,
		)
		icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

		result, err = ts.Mock.HandlePackets(icmpPacket)
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
		tcpLayers := utils.MakeTCPPacket(
			clientIPv6,
			clientPort,
			vsIPv6,
			vsPort,
			&layers.TCP{SYN: true},
		)
		tcpPacket := xpacket.LayersToPacket(t, tcpLayers...)

		result, err := ts.Mock.HandlePackets(tcpPacket)
		require.NoError(t, err)
		require.Equal(
			t,
			1,
			len(result.Output),
			"TCP packet should be forwarded",
		)

		// Now simulate the real server's response packet (which would trigger an ICMPv6 error)
		// The real server responds with src=vsIP (as configured), dst=clientIP
		responsePacket := utils.MakeTCPPacket(
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
		icmpLayers := utils.MakeICMPv6DestUnreachable(
			clientIPv6,
			vsIPv6,
			responsePacketData,
		)
		icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

		result, err = ts.Mock.HandlePackets(icmpPacket)
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
	vsIPv4 := netip.MustParseAddr("10.1.1.1")
	unknownVsIPv4 := netip.MustParseAddr("10.99.99.99") // Not configured
	clientIPv4 := netip.MustParseAddr("10.0.1.1")

	vsIPv6 := netip.MustParseAddr("2001:db8::1")
	unknownVsIPv6 := netip.MustParseAddr("2001:db8:99::99") // Not configured
	clientIPv6 := netip.MustParseAddr("2001:db8:1::1")

	clientPort := uint16(12345)
	vsPort := uint16(80)

	config := &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("5.5.5.5").AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("fe80::5").AsSlice(),
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
									Bytes: netip.MustParseAddr("10.2.2.2").
										AsSlice(),
								},
								Port: 0,
							},
							Weight: 1,
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("10.2.2.2").
									AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.255").
									AsSlice(),
							},
						},
					},
					Peers: []*balancerpb.Addr{},
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
									Bytes: netip.MustParseAddr("2001:db8:2::2").
										AsSlice(),
								},
								Port: 0,
							},
							Weight: 1,
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("2001:db8:2::2").
									AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff").
									AsSlice(),
							},
						},
					},
					Peers: []*balancerpb.Addr{},
				},
			},
			DecapAddresses: []*balancerpb.Addr{},
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

	ts, err := utils.Make(&utils.TestConfig{
		Mock:     utils.SingleWorkerMockConfig(64*datasize.MB, 4*datasize.MB),
		Balancer: config,
		AgentMemory: func() *datasize.ByteSize {
			value := 16 * datasize.MB
			return &value
		}(),
	})
	require.NoError(t, err)
	defer ts.Free()

	t.Run("IPv4", func(t *testing.T) {
		// Create a TCP packet to an unknown VS
		tcpLayers := utils.MakeTCPPacket(
			unknownVsIPv4,
			vsPort,
			clientIPv4,
			clientPort,
			&layers.TCP{SYN: true},
		)
		tcpPacket := xpacket.LayersToPacket(t, tcpLayers...)

		// Create ICMP error for the unknown VS
		icmpLayers := utils.MakeICMPv4DestUnreachable(
			clientIPv4,
			unknownVsIPv4,
			tcpPacket,
		)
		icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

		result, err := ts.Mock.HandlePackets(icmpPacket)
		require.NoError(t, err)

		// The ICMP error should be dropped because VS is not found
		require.Empty(t, result.Output, "ICMP error should not be forwarded")
		require.Equal(t, 1, len(result.Drop), "ICMP error should be dropped")
	})

	t.Run("IPv6", func(t *testing.T) {
		// Create a TCP packet to an unknown VS
		tcpLayers := utils.MakeTCPPacket(
			unknownVsIPv6,
			vsPort,
			clientIPv6,
			clientPort,
			&layers.TCP{SYN: true},
		)
		tcpPacket := xpacket.LayersToPacket(t, tcpLayers...)

		// Create ICMPv6 error for the unknown VS
		icmpLayers := utils.MakeICMPv6DestUnreachable(
			clientIPv6,
			unknownVsIPv6,
			tcpPacket,
		)
		icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

		result, err := ts.Mock.HandlePackets(icmpPacket)
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
	vsIPv4 := netip.MustParseAddr("10.1.1.1")
	clientIPv4 := netip.MustParseAddr("10.0.1.1")

	vsIPv6 := netip.MustParseAddr("2001:db8::1")
	clientIPv6 := netip.MustParseAddr("2001:db8:1::1")

	clientPort := uint16(12345)
	vsPort := uint16(80)

	peer1 := netip.MustParseAddr("10.12.11.13")
	peer2 := netip.MustParseAddr("fe80::11")

	config := &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("5.5.5.5").AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("fe80::5").AsSlice(),
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
									Bytes: netip.MustParseAddr("10.2.2.2").
										AsSlice(),
								},
								Port: 0,
							},
							Weight: 1,
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("10.2.2.2").
									AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.255").
									AsSlice(),
							},
						},
					},
					Peers: []*balancerpb.Addr{
						{Bytes: peer1.AsSlice()},
						{Bytes: peer2.AsSlice()},
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
									Bytes: netip.MustParseAddr("2001:db8:2::2").
										AsSlice(),
								},
								Port: 0,
							},
							Weight: 1,
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("2001:db8:2::2").
									AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff").
									AsSlice(),
							},
						},
					},
					Peers: []*balancerpb.Addr{
						{Bytes: peer1.AsSlice()},
						{Bytes: peer2.AsSlice()},
					},
				},
			},
			DecapAddresses: []*balancerpb.Addr{},
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

	ts, err := utils.Make(&utils.TestConfig{
		Mock:     utils.SingleWorkerMockConfig(64*datasize.MB, 4*datasize.MB),
		Balancer: config,
		AgentMemory: func() *datasize.ByteSize {
			value := 16 * datasize.MB
			return &value
		}(),
	})
	require.NoError(t, err)
	defer ts.Free()

	t.Run("IPv4", func(t *testing.T) {
		// Create a TCP packet (but don't send it to create a session)
		tcpLayers := utils.MakeTCPPacket(
			vsIPv4,
			vsPort,
			clientIPv4,
			clientPort,
			&layers.TCP{SYN: true},
		)
		tcpPacket := xpacket.LayersToPacket(t, tcpLayers...)

		// Create ICMP error for a non-existent session
		icmpLayers := utils.MakeICMPv4DestUnreachable(
			clientIPv4,
			vsIPv4,
			tcpPacket,
		)
		icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

		result, err := ts.Mock.HandlePackets(icmpPacket)
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
		tcpLayers := utils.MakeTCPPacket(
			vsIPv6,
			vsPort,
			clientIPv6,
			clientPort,
			&layers.TCP{SYN: true},
		)
		tcpPacket := xpacket.LayersToPacket(t, tcpLayers...)

		// Create ICMPv6 error for a non-existent session
		icmpLayers := utils.MakeICMPv6DestUnreachable(
			clientIPv6,
			vsIPv6,
			tcpPacket,
		)
		icmpPacket := xpacket.LayersToPacket(t, icmpLayers...)

		result, err := ts.Mock.HandlePackets(icmpPacket)
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
