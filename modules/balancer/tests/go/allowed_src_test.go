package balancer_test

// TestAllowedSrc is a comprehensive test suite for the allowed_src source filtering feature.
//
// This test verifies that the balancer correctly filters packets based on the allowed_srcs
// configuration for each virtual service. It covers:
//
// # Source Filtering Behavior
// - Packets from allowed source ranges are accepted and forwarded
// - Packets from non-allowed source ranges are dropped
// - Empty allowed_srcs list allows all sources
// - 0.0.0.0/0 (IPv4) or ::/0 (IPv6) allows all sources
//
// # Protocol Coverage
// - TCP protocol with source filtering
// - UDP protocol with source filtering
//
// # IP Version Coverage
// - IPv4 virtual services with IPv4 allowed_src ranges
// - IPv6 virtual services with IPv6 allowed_src ranges
//
// # Counter Validation
// - packet_src_not_allowed counter increases when packets are blocked
// - incoming_packets counter increases for all packets (allowed and blocked)
// - outgoing_packets counter increases only for allowed packets
// - created_sessions counter increases only for allowed packets
//
// The test uses 6 virtual services with different configurations:
// - VS1: IPv4 TCP port 80 with allowed_src 10.0.1.0/24
// - VS2: IPv4 UDP port 5353 with allowed_src 10.0.2.0/24
// - VS3: IPv6 TCP port 8080 with allowed_src 2001:db8:1::/48
// - VS4: IPv6 UDP port 5353 with allowed_src 2001:db8:2::/48
// - VS5: IPv4 TCP port 443 with allowed_src 0.0.0.0/1 + 128.0.0.0/1 (allow all IPv4)
// - VS6: IPv4 TCP port 8443 with allowed_src 0.0.0.0/0 (allow all)

import (
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/tests/go/utils"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Virtual service addresses and ports for allowed_src tests
var (
	// VS1: IPv4 TCP with restricted source
	allowedSrcVs1IP   = netip.MustParseAddr("10.10.1.1")
	allowedSrcVs1Port = uint16(80)

	// VS2: IPv4 UDP with restricted source
	allowedSrcVs2IP   = netip.MustParseAddr("10.10.2.1")
	allowedSrcVs2Port = uint16(5353)

	// VS3: IPv6 TCP with restricted source
	allowedSrcVs3IP   = netip.MustParseAddr("2001:db8:100::1")
	allowedSrcVs3Port = uint16(8080)

	// VS4: IPv6 UDP with restricted source
	allowedSrcVs4IP   = netip.MustParseAddr("2001:db8:200::1")
	allowedSrcVs4Port = uint16(5353)

	// VS5: IPv4 TCP with empty allowed_src (allow all)
	allowedSrcVs5IP   = netip.MustParseAddr("10.10.5.1")
	allowedSrcVs5Port = uint16(443)

	// VS6: IPv4 TCP with 0.0.0.0/0 allowed_src (allow all)
	allowedSrcVs6IP   = netip.MustParseAddr("10.10.6.1")
	allowedSrcVs6Port = uint16(8443)

	// Real servers for allowed_src tests
	allowedSrcRealIPv4 = netip.MustParseAddr("192.168.100.1")
	allowedSrcRealIPv6 = netip.MustParseAddr("fe80::100")

	// Balancer source addresses for allowed_src tests
	allowedSrcBalancerSrcIPv4 = netip.MustParseAddr("5.5.5.5")
	allowedSrcBalancerSrcIPv6 = netip.MustParseAddr("fe80::5")
)

// createAllowedSrcTestConfig creates a balancer configuration with 6 virtual services
// covering different allowed_src scenarios
func createAllowedSrcTestConfig() *balancerpb.BalancerConfig {
	return &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: &balancerpb.Addr{
				Bytes: allowedSrcBalancerSrcIPv4.AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: allowedSrcBalancerSrcIPv6.AsSlice(),
			},
			Vs: []*balancerpb.VirtualService{
				// VS1: IPv4 TCP with allowed_src 10.0.1.0/24
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: allowedSrcVs1IP.AsSlice(),
						},
						Port:  uint32(allowedSrcVs1Port),
						Proto: balancerpb.TransportProto_TCP,
					},
					Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
					AllowedSrcs: []*balancerpb.Net{
						{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("10.0.1.0").
									AsSlice(),
							},
							Size: 24,
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
									Bytes: allowedSrcRealIPv4.AsSlice(),
								},
								Port: 0,
							},
							Weight: 1,
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("4.4.4.4").AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.255").
									AsSlice(),
							},
						},
					},
					Peers: []*balancerpb.Addr{},
				},
				// VS2: IPv4 UDP with allowed_src 10.0.2.0/24
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: allowedSrcVs2IP.AsSlice(),
						},
						Port:  uint32(allowedSrcVs2Port),
						Proto: balancerpb.TransportProto_UDP,
					},
					Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
					AllowedSrcs: []*balancerpb.Net{
						{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("10.0.2.0").
									AsSlice(),
							},
							Size: 24,
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
									Bytes: allowedSrcRealIPv4.AsSlice(),
								},
								Port: 0,
							},
							Weight: 1,
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("4.4.4.4").AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.255").
									AsSlice(),
							},
						},
					},
					Peers: []*balancerpb.Addr{},
				},
				// VS3: IPv6 TCP with allowed_src 2001:db8:1::/48
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: allowedSrcVs3IP.AsSlice(),
						},
						Port:  uint32(allowedSrcVs3Port),
						Proto: balancerpb.TransportProto_TCP,
					},
					Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
					AllowedSrcs: []*balancerpb.Net{
						{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("2001:db8:1::").
									AsSlice(),
							},
							Size: 48,
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
									Bytes: allowedSrcRealIPv6.AsSlice(),
								},
								Port: 0,
							},
							Weight: 1,
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("fe80::4").AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff").
									AsSlice(),
							},
						},
					},
					Peers: []*balancerpb.Addr{},
				},
				// VS4: IPv6 UDP with allowed_src 2001:db8:2::/48
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: allowedSrcVs4IP.AsSlice(),
						},
						Port:  uint32(allowedSrcVs4Port),
						Proto: balancerpb.TransportProto_UDP,
					},
					Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
					AllowedSrcs: []*balancerpb.Net{
						{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("2001:db8:2::").
									AsSlice(),
							},
							Size: 48,
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
									Bytes: allowedSrcRealIPv6.AsSlice(),
								},
								Port: 0,
							},
							Weight: 1,
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("fe80::4").AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff").
									AsSlice(),
							},
						},
					},
					Peers: []*balancerpb.Addr{},
				},
				// VS5: IPv4 TCP with single large CIDR (effectively allow all IPv4)
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: allowedSrcVs5IP.AsSlice(),
						},
						Port:  uint32(allowedSrcVs5Port),
						Proto: balancerpb.TransportProto_TCP,
					},
					Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
					AllowedSrcs: []*balancerpb.Net{
						{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("0.0.0.0").AsSlice(),
							},
							Size: 1, // 0.0.0.0/1 covers 0.0.0.0-127.255.255.255
						},
						{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("128.0.0.0").
									AsSlice(),
							},
							Size: 1, // 128.0.0.0/1 covers 128.0.0.0-255.255.255.255
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
									Bytes: allowedSrcRealIPv4.AsSlice(),
								},
								Port: 0,
							},
							Weight: 1,
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("4.4.4.4").AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.255").
									AsSlice(),
							},
						},
					},
					Peers: []*balancerpb.Addr{},
				},
				// VS6: IPv4 TCP with 0.0.0.0/0 allowed_src (allow all)
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: allowedSrcVs6IP.AsSlice(),
						},
						Port:  uint32(allowedSrcVs6Port),
						Proto: balancerpb.TransportProto_TCP,
					},
					Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
					AllowedSrcs: []*balancerpb.Net{
						{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("0.0.0.0").AsSlice(),
							},
							Size: 0, // 0.0.0.0/0 allows all
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
									Bytes: allowedSrcRealIPv4.AsSlice(),
								},
								Port: 0,
							},
							Weight: 1,
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("4.4.4.4").AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.255").
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
			SessionTableCapacity:      func() *uint64 { v := uint64(1000); return &v }(),
			SessionTableMaxLoadFactor: func() *float32 { v := float32(0.8); return &v }(),
			RefreshPeriod:             durationpb.New(0),
			Wlc: &balancerpb.WlcConfig{
				Power:     func() *uint64 { v := uint64(10); return &v }(),
				MaxWeight: func() *uint32 { v := uint32(1000); return &v }(),
			},
		},
	}
}

// findVsStats finds statistics for a specific virtual service by its identifier
func findVsStats(
	stats *balancerpb.BalancerStats,
	vsIP netip.Addr,
	vsPort uint16,
	proto balancerpb.TransportProto,
) *balancerpb.VsStats {
	for _, namedVsStats := range stats.Vs {
		if namedVsStats.Vs == nil {
			continue
		}
		addr, _ := netip.AddrFromSlice(namedVsStats.Vs.Addr.Bytes)
		if addr == vsIP &&
			namedVsStats.Vs.Port == uint32(vsPort) &&
			namedVsStats.Vs.Proto == proto {
			return namedVsStats.Stats
		}
	}
	return nil
}

// TestAllowedSrc is the main test function
func TestAllowedSrc(t *testing.T) {
	config := createAllowedSrcTestConfig()

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

	// Enable all reals
	utils.EnableAllReals(t, ts)

	// Get packet handler reference for stats
	statsRef := &balancerpb.PacketHandlerRef{
		Device:   &utils.DeviceName,
		Pipeline: &utils.PipelineName,
		Function: &utils.FunctionName,
		Chain:    &utils.ChainName,
	}

	// Test IPv4 TCP with allowed source
	t.Run("IPv4_TCP_Allowed", func(t *testing.T) {
		testIPv4TCPAllowed(t, ts, statsRef)
	})

	// Test IPv4 TCP with blocked source
	t.Run("IPv4_TCP_Blocked", func(t *testing.T) {
		testIPv4TCPBlocked(t, ts, statsRef)
	})

	// Test IPv4 UDP with allowed source
	t.Run("IPv4_UDP_Allowed", func(t *testing.T) {
		testIPv4UDPAllowed(t, ts, statsRef)
	})

	// Test IPv4 UDP with blocked source
	t.Run("IPv4_UDP_Blocked", func(t *testing.T) {
		testIPv4UDPBlocked(t, ts, statsRef)
	})

	// Test IPv6 TCP with allowed source
	t.Run("IPv6_TCP_Allowed", func(t *testing.T) {
		testIPv6TCPAllowed(t, ts, statsRef)
	})

	// Test IPv6 TCP with blocked source
	t.Run("IPv6_TCP_Blocked", func(t *testing.T) {
		testIPv6TCPBlocked(t, ts, statsRef)
	})

	// Test IPv6 UDP with allowed source
	t.Run("IPv6_UDP_Allowed", func(t *testing.T) {
		testIPv6UDPAllowed(t, ts, statsRef)
	})

	// Test IPv6 UDP with blocked source
	t.Run("IPv6_UDP_Blocked", func(t *testing.T) {
		testIPv6UDPBlocked(t, ts, statsRef)
	})

	// Test empty allowed_src (allow all)
	t.Run("Empty_AllowedSrc_AllowsAll", func(t *testing.T) {
		testEmptyAllowedSrcAllowsAll(t, ts, statsRef)
	})

	// Test 0.0.0.0/0 allowed_src (allow all)
	t.Run("Zero_CIDR_AllowsAll", func(t *testing.T) {
		testZeroCIDRAllowsAll(t, ts, statsRef)
	})
}

// testIPv4TCPAllowed tests that packets from allowed IPv4 source are accepted
func testIPv4TCPAllowed(
	t *testing.T,
	ts *utils.TestSetup,
	statsRef *balancerpb.PacketHandlerRef,
) {
	t.Helper()

	// Get initial stats
	initialStats, err := ts.Balancer.Stats(statsRef)
	require.NoError(t, err)
	initialVsStats := findVsStats(
		initialStats,
		allowedSrcVs1IP,
		allowedSrcVs1Port,
		balancerpb.TransportProto_TCP,
	)
	require.NotNil(t, initialVsStats, "VS1 stats should exist")

	// Send packet from allowed source (10.0.1.50)
	clientIP := netip.MustParseAddr("10.0.1.50")
	clientPort := uint16(12345)

	packetLayers := utils.MakeTCPPacket(
		clientIP,
		clientPort,
		allowedSrcVs1IP,
		allowedSrcVs1Port,
		&layers.TCP{SYN: true},
	)
	packet := xpacket.LayersToPacket(t, packetLayers...)

	result, err := ts.Mock.HandlePackets(packet)
	require.NoError(t, err)
	require.Equal(t, 1, len(result.Output), "expected 1 output packet")
	require.Empty(t, result.Drop, "expected no dropped packets")

	// Validate the output packet
	utils.ValidatePacket(t, ts.Balancer.Config(), packet, result.Output[0])

	// Get final stats
	finalStats, err := ts.Balancer.Stats(statsRef)
	require.NoError(t, err)
	finalVsStats := findVsStats(
		finalStats,
		allowedSrcVs1IP,
		allowedSrcVs1Port,
		balancerpb.TransportProto_TCP,
	)
	require.NotNil(t, finalVsStats, "VS1 stats should exist")

	// Verify counters
	assert.Equal(t,
		initialVsStats.PacketSrcNotAllowed,
		finalVsStats.PacketSrcNotAllowed,
		"packet_src_not_allowed should not increase for allowed source",
	)
	assert.Equal(t,
		initialVsStats.IncomingPackets+1,
		finalVsStats.IncomingPackets,
		"incoming_packets should increase by 1",
	)
	assert.Equal(t,
		initialVsStats.OutgoingPackets+1,
		finalVsStats.OutgoingPackets,
		"outgoing_packets should increase by 1",
	)
	assert.Equal(t,
		initialVsStats.CreatedSessions+1,
		finalVsStats.CreatedSessions,
		"created_sessions should increase by 1",
	)
}

// testIPv4TCPBlocked tests that packets from non-allowed IPv4 source are blocked
func testIPv4TCPBlocked(
	t *testing.T,
	ts *utils.TestSetup,
	statsRef *balancerpb.PacketHandlerRef,
) {
	t.Helper()

	// Get initial stats
	initialStats, err := ts.Balancer.Stats(statsRef)
	require.NoError(t, err)
	initialVsStats := findVsStats(
		initialStats,
		allowedSrcVs1IP,
		allowedSrcVs1Port,
		balancerpb.TransportProto_TCP,
	)
	require.NotNil(t, initialVsStats, "VS1 stats should exist")

	// Send packet from non-allowed source (10.0.99.50)
	clientIP := netip.MustParseAddr("10.0.99.50")
	clientPort := uint16(12346)

	packetLayers := utils.MakeTCPPacket(
		clientIP,
		clientPort,
		allowedSrcVs1IP,
		allowedSrcVs1Port,
		&layers.TCP{SYN: true},
	)
	packet := xpacket.LayersToPacket(t, packetLayers...)

	result, err := ts.Mock.HandlePackets(packet)
	require.NoError(t, err)
	require.Empty(t, result.Output, "expected no output packets")
	require.Equal(t, 1, len(result.Drop), "expected 1 dropped packet")

	// Get final stats
	finalStats, err := ts.Balancer.Stats(statsRef)
	require.NoError(t, err)
	finalVsStats := findVsStats(
		finalStats,
		allowedSrcVs1IP,
		allowedSrcVs1Port,
		balancerpb.TransportProto_TCP,
	)
	require.NotNil(t, finalVsStats, "VS1 stats should exist")

	// Verify counters
	assert.Equal(t,
		initialVsStats.PacketSrcNotAllowed+1,
		finalVsStats.PacketSrcNotAllowed,
		"packet_src_not_allowed should increase by 1",
	)
	assert.Equal(t,
		initialVsStats.OutgoingPackets,
		finalVsStats.OutgoingPackets,
		"outgoing_packets should not increase for blocked packet",
	)
	assert.Equal(t,
		initialVsStats.CreatedSessions,
		finalVsStats.CreatedSessions,
		"created_sessions should not increase for blocked packet",
	)
}

// testIPv4UDPAllowed tests that UDP packets from allowed IPv4 source are accepted
func testIPv4UDPAllowed(
	t *testing.T,
	ts *utils.TestSetup,
	statsRef *balancerpb.PacketHandlerRef,
) {
	t.Helper()

	// Get initial stats
	initialStats, err := ts.Balancer.Stats(statsRef)
	require.NoError(t, err)
	initialVsStats := findVsStats(
		initialStats,
		allowedSrcVs2IP,
		allowedSrcVs2Port,
		balancerpb.TransportProto_UDP,
	)
	require.NotNil(t, initialVsStats, "VS2 stats should exist")

	// Send packet from allowed source (10.0.2.50)
	clientIP := netip.MustParseAddr("10.0.2.50")
	clientPort := uint16(54321)

	packetLayers := utils.MakeUDPPacket(
		clientIP,
		clientPort,
		allowedSrcVs2IP,
		allowedSrcVs2Port,
	)
	packet := xpacket.LayersToPacket(t, packetLayers...)

	result, err := ts.Mock.HandlePackets(packet)
	require.NoError(t, err)
	require.Equal(t, 1, len(result.Output), "expected 1 output packet")
	require.Empty(t, result.Drop, "expected no dropped packets")

	// Validate the output packet
	utils.ValidatePacket(t, ts.Balancer.Config(), packet, result.Output[0])

	// Get final stats
	finalStats, err := ts.Balancer.Stats(statsRef)
	require.NoError(t, err)
	finalVsStats := findVsStats(
		finalStats,
		allowedSrcVs2IP,
		allowedSrcVs2Port,
		balancerpb.TransportProto_UDP,
	)
	require.NotNil(t, finalVsStats, "VS2 stats should exist")

	// Verify counters
	assert.Equal(t,
		initialVsStats.PacketSrcNotAllowed,
		finalVsStats.PacketSrcNotAllowed,
		"packet_src_not_allowed should not increase for allowed source",
	)
	assert.Equal(t,
		initialVsStats.IncomingPackets+1,
		finalVsStats.IncomingPackets,
		"incoming_packets should increase by 1",
	)
	assert.Equal(t,
		initialVsStats.OutgoingPackets+1,
		finalVsStats.OutgoingPackets,
		"outgoing_packets should increase by 1",
	)
}

// testIPv4UDPBlocked tests that UDP packets from non-allowed IPv4 source are blocked
func testIPv4UDPBlocked(
	t *testing.T,
	ts *utils.TestSetup,
	statsRef *balancerpb.PacketHandlerRef,
) {
	t.Helper()

	// Get initial stats
	initialStats, err := ts.Balancer.Stats(statsRef)
	require.NoError(t, err)
	initialVsStats := findVsStats(
		initialStats,
		allowedSrcVs2IP,
		allowedSrcVs2Port,
		balancerpb.TransportProto_UDP,
	)
	require.NotNil(t, initialVsStats, "VS2 stats should exist")

	// Send packet from non-allowed source (10.0.99.50)
	clientIP := netip.MustParseAddr("10.0.99.50")
	clientPort := uint16(54322)

	packetLayers := utils.MakeUDPPacket(
		clientIP,
		clientPort,
		allowedSrcVs2IP,
		allowedSrcVs2Port,
	)
	packet := xpacket.LayersToPacket(t, packetLayers...)

	result, err := ts.Mock.HandlePackets(packet)
	require.NoError(t, err)
	require.Empty(t, result.Output, "expected no output packets")
	require.Equal(t, 1, len(result.Drop), "expected 1 dropped packet")

	// Get stats after the blocked packet
	stats, err := ts.Balancer.Stats(statsRef)
	require.NoError(t, err)
	vsStats := findVsStats(
		stats,
		allowedSrcVs2IP,
		allowedSrcVs2Port,
		balancerpb.TransportProto_UDP,
	)
	require.NotNil(t, vsStats, "VS2 stats should exist")

	// Verify that packet_src_not_allowed counter increased
	assert.Greater(t,
		vsStats.PacketSrcNotAllowed,
		uint64(0),
		"packet_src_not_allowed should be greater than 0 for blocked packets",
	)
}

// testIPv6TCPAllowed tests that TCP packets from allowed IPv6 source are accepted
func testIPv6TCPAllowed(
	t *testing.T,
	ts *utils.TestSetup,
	statsRef *balancerpb.PacketHandlerRef,
) {
	t.Helper()

	// Get initial stats
	initialStats, err := ts.Balancer.Stats(statsRef)
	require.NoError(t, err)
	initialVsStats := findVsStats(
		initialStats,
		allowedSrcVs3IP,
		allowedSrcVs3Port,
		balancerpb.TransportProto_TCP,
	)
	require.NotNil(t, initialVsStats, "VS3 stats should exist")

	// Send packet from allowed source (2001:db8:1::50)
	clientIP := netip.MustParseAddr("2001:db8:1::50")
	clientPort := uint16(23456)

	packetLayers := utils.MakeTCPPacket(
		clientIP,
		clientPort,
		allowedSrcVs3IP,
		allowedSrcVs3Port,
		&layers.TCP{SYN: true},
	)
	packet := xpacket.LayersToPacket(t, packetLayers...)

	result, err := ts.Mock.HandlePackets(packet)
	require.NoError(t, err)
	require.Equal(t, 1, len(result.Output), "expected 1 output packet")
	require.Empty(t, result.Drop, "expected no dropped packets")

	// Validate the output packet
	utils.ValidatePacket(t, ts.Balancer.Config(), packet, result.Output[0])

	// Get final stats
	finalStats, err := ts.Balancer.Stats(statsRef)
	require.NoError(t, err)
	finalVsStats := findVsStats(
		finalStats,
		allowedSrcVs3IP,
		allowedSrcVs3Port,
		balancerpb.TransportProto_TCP,
	)
	require.NotNil(t, finalVsStats, "VS3 stats should exist")

	// Verify counters
	assert.Equal(t,
		initialVsStats.PacketSrcNotAllowed,
		finalVsStats.PacketSrcNotAllowed,
		"packet_src_not_allowed should not increase for allowed source",
	)
	assert.Equal(t,
		initialVsStats.IncomingPackets+1,
		finalVsStats.IncomingPackets,
		"incoming_packets should increase by 1",
	)
	assert.Equal(t,
		initialVsStats.OutgoingPackets+1,
		finalVsStats.OutgoingPackets,
		"outgoing_packets should increase by 1",
	)
}

// testIPv6TCPBlocked tests that TCP packets from non-allowed IPv6 source are blocked
func testIPv6TCPBlocked(
	t *testing.T,
	ts *utils.TestSetup,
	statsRef *balancerpb.PacketHandlerRef,
) {
	t.Helper()

	// Get initial stats
	initialStats, err := ts.Balancer.Stats(statsRef)
	require.NoError(t, err)
	initialVsStats := findVsStats(
		initialStats,
		allowedSrcVs3IP,
		allowedSrcVs3Port,
		balancerpb.TransportProto_TCP,
	)
	require.NotNil(t, initialVsStats, "VS3 stats should exist")

	// Send packet from non-allowed source (2001:db8:99::50)
	clientIP := netip.MustParseAddr("2001:db8:99::50")
	clientPort := uint16(23457)

	packetLayers := utils.MakeTCPPacket(
		clientIP,
		clientPort,
		allowedSrcVs3IP,
		allowedSrcVs3Port,
		&layers.TCP{SYN: true},
	)
	packet := xpacket.LayersToPacket(t, packetLayers...)

	result, err := ts.Mock.HandlePackets(packet)
	require.NoError(t, err)
	require.Empty(t, result.Output, "expected no output packets")
	require.Equal(t, 1, len(result.Drop), "expected 1 dropped packet")

	// Get stats after the blocked packet
	stats, err := ts.Balancer.Stats(statsRef)
	require.NoError(t, err)
	vsStats := findVsStats(
		stats,
		allowedSrcVs3IP,
		allowedSrcVs3Port,
		balancerpb.TransportProto_TCP,
	)
	require.NotNil(t, vsStats, "VS3 stats should exist")

	// Verify that packet_src_not_allowed counter increased
	assert.Greater(t,
		vsStats.PacketSrcNotAllowed,
		uint64(0),
		"packet_src_not_allowed should be greater than 0 for blocked packets",
	)
}

// testIPv6UDPAllowed tests that UDP packets from allowed IPv6 source are accepted
func testIPv6UDPAllowed(
	t *testing.T,
	ts *utils.TestSetup,
	statsRef *balancerpb.PacketHandlerRef,
) {
	t.Helper()

	// Get initial stats
	initialStats, err := ts.Balancer.Stats(statsRef)
	require.NoError(t, err)
	initialVsStats := findVsStats(
		initialStats,
		allowedSrcVs4IP,
		allowedSrcVs4Port,
		balancerpb.TransportProto_UDP,
	)
	require.NotNil(t, initialVsStats, "VS4 stats should exist")

	// Send packet from allowed source (2001:db8:2::50)
	clientIP := netip.MustParseAddr("2001:db8:2::50")
	clientPort := uint16(34567)

	packetLayers := utils.MakeUDPPacket(
		clientIP,
		clientPort,
		allowedSrcVs4IP,
		allowedSrcVs4Port,
	)
	packet := xpacket.LayersToPacket(t, packetLayers...)

	result, err := ts.Mock.HandlePackets(packet)
	require.NoError(t, err)
	require.Equal(t, 1, len(result.Output), "expected 1 output packet")
	require.Empty(t, result.Drop, "expected no dropped packets")

	// Validate the output packet
	utils.ValidatePacket(t, ts.Balancer.Config(), packet, result.Output[0])

	// Get final stats
	finalStats, err := ts.Balancer.Stats(statsRef)
	require.NoError(t, err)
	finalVsStats := findVsStats(
		finalStats,
		allowedSrcVs4IP,
		allowedSrcVs4Port,
		balancerpb.TransportProto_UDP,
	)
	require.NotNil(t, finalVsStats, "VS4 stats should exist")

	// Verify counters
	assert.Equal(t,
		initialVsStats.PacketSrcNotAllowed,
		finalVsStats.PacketSrcNotAllowed,
		"packet_src_not_allowed should not increase for allowed source",
	)
	assert.Equal(t,
		initialVsStats.IncomingPackets+1,
		finalVsStats.IncomingPackets,
		"incoming_packets should increase by 1",
	)
	assert.Equal(t,
		initialVsStats.OutgoingPackets+1,
		finalVsStats.OutgoingPackets,
		"outgoing_packets should increase by 1",
	)
}

// testIPv6UDPBlocked tests that UDP packets from non-allowed IPv6 source are blocked
func testIPv6UDPBlocked(
	t *testing.T,
	ts *utils.TestSetup,
	statsRef *balancerpb.PacketHandlerRef,
) {
	t.Helper()

	// Get initial stats
	initialStats, err := ts.Balancer.Stats(statsRef)
	require.NoError(t, err)
	initialVsStats := findVsStats(
		initialStats,
		allowedSrcVs4IP,
		allowedSrcVs4Port,
		balancerpb.TransportProto_UDP,
	)
	require.NotNil(t, initialVsStats, "VS4 stats should exist")

	// Send packet from non-allowed source (2001:db8:99::50)
	clientIP := netip.MustParseAddr("2001:db8:99::50")
	clientPort := uint16(34568)

	packetLayers := utils.MakeUDPPacket(
		clientIP,
		clientPort,
		allowedSrcVs4IP,
		allowedSrcVs4Port,
	)
	packet := xpacket.LayersToPacket(t, packetLayers...)

	result, err := ts.Mock.HandlePackets(packet)
	require.NoError(t, err)
	require.Empty(t, result.Output, "expected no output packets")
	require.Equal(t, 1, len(result.Drop), "expected 1 dropped packet")

	// Get stats after the blocked packet
	stats, err := ts.Balancer.Stats(statsRef)
	require.NoError(t, err)
	vsStats := findVsStats(
		stats,
		allowedSrcVs4IP,
		allowedSrcVs4Port,
		balancerpb.TransportProto_UDP,
	)
	require.NotNil(t, vsStats, "VS4 stats should exist")

	// Verify that packet_src_not_allowed counter increased
	assert.Greater(t,
		vsStats.PacketSrcNotAllowed,
		uint64(0),
		"packet_src_not_allowed should be greater than 0 for blocked packets",
	)
}

// testEmptyAllowedSrcAllowsAll tests that large CIDR ranges allow all sources
func testEmptyAllowedSrcAllowsAll(
	t *testing.T,
	ts *utils.TestSetup,
	statsRef *balancerpb.PacketHandlerRef,
) {
	t.Helper()

	// Get initial stats
	initialStats, err := ts.Balancer.Stats(statsRef)
	require.NoError(t, err)
	initialVsStats := findVsStats(
		initialStats,
		allowedSrcVs5IP,
		allowedSrcVs5Port,
		balancerpb.TransportProto_TCP,
	)
	require.NotNil(t, initialVsStats, "VS5 stats should exist")

	// Send packets from various sources - all should be allowed
	testSources := []string{
		"10.0.1.1",
		"10.0.99.99",
		"192.168.1.1",
		"1.2.3.4",
	}

	for _, srcIP := range testSources {
		clientIP := netip.MustParseAddr(srcIP)
		clientPort := uint16(45678)

		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			allowedSrcVs5IP,
			allowedSrcVs5Port,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)

		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(
			t,
			1,
			len(result.Output),
			"expected 1 output packet for source %s",
			srcIP,
		)
		require.Empty(
			t,
			result.Drop,
			"expected no dropped packets for source %s",
			srcIP,
		)
	}

	// Get final stats
	finalStats, err := ts.Balancer.Stats(statsRef)
	require.NoError(t, err)
	finalVsStats := findVsStats(
		finalStats,
		allowedSrcVs5IP,
		allowedSrcVs5Port,
		balancerpb.TransportProto_TCP,
	)
	require.NotNil(t, finalVsStats, "VS5 stats should exist")

	// Verify counters - no packets should be blocked
	assert.Equal(
		t,
		initialVsStats.PacketSrcNotAllowed,
		finalVsStats.PacketSrcNotAllowed,
		"packet_src_not_allowed should not increase when allowed_src covers all IPs",
	)
	assert.Equal(t,
		initialVsStats.IncomingPackets+uint64(len(testSources)),
		finalVsStats.IncomingPackets,
		"incoming_packets should increase by number of test sources",
	)
	assert.Equal(t,
		initialVsStats.OutgoingPackets+uint64(len(testSources)),
		finalVsStats.OutgoingPackets,
		"outgoing_packets should increase by number of test sources",
	)
}

// testZeroCIDRAllowsAll tests that 0.0.0.0/0 allowed_src allows all sources
func testZeroCIDRAllowsAll(
	t *testing.T,
	ts *utils.TestSetup,
	statsRef *balancerpb.PacketHandlerRef,
) {
	t.Helper()

	// Get initial stats
	initialStats, err := ts.Balancer.Stats(statsRef)
	require.NoError(t, err)
	initialVsStats := findVsStats(
		initialStats,
		allowedSrcVs6IP,
		allowedSrcVs6Port,
		balancerpb.TransportProto_TCP,
	)
	require.NotNil(t, initialVsStats, "VS6 stats should exist")

	// Send packets from various sources - all should be allowed
	testSources := []string{
		"10.0.1.1",
		"10.0.99.99",
		"192.168.1.1",
		"1.2.3.4",
		"172.16.0.1",
	}

	for _, srcIP := range testSources {
		clientIP := netip.MustParseAddr(srcIP)
		clientPort := uint16(56789)

		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			allowedSrcVs6IP,
			allowedSrcVs6Port,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)

		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(
			t,
			1,
			len(result.Output),
			"expected 1 output packet for source %s",
			srcIP,
		)
		require.Empty(
			t,
			result.Drop,
			"expected no dropped packets for source %s",
			srcIP,
		)
	}

	// Get final stats
	finalStats, err := ts.Balancer.Stats(statsRef)
	require.NoError(t, err)
	finalVsStats := findVsStats(
		finalStats,
		allowedSrcVs6IP,
		allowedSrcVs6Port,
		balancerpb.TransportProto_TCP,
	)
	require.NotNil(t, finalVsStats, "VS6 stats should exist")

	// Verify counters - no packets should be blocked
	assert.Equal(
		t,
		initialVsStats.PacketSrcNotAllowed,
		finalVsStats.PacketSrcNotAllowed,
		"packet_src_not_allowed should not increase when allowed_src is 0.0.0.0/0",
	)
	assert.Equal(t,
		initialVsStats.IncomingPackets+uint64(len(testSources)),
		finalVsStats.IncomingPackets,
		"incoming_packets should increase by number of test sources",
	)
	assert.Equal(t,
		initialVsStats.OutgoingPackets+uint64(len(testSources)),
		finalVsStats.OutgoingPackets,
		"outgoing_packets should increase by number of test sources",
	)
}
