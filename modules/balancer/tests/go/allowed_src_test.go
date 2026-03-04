package balancer_test

// TestAllowedSrc is a comprehensive test suite for the allowed_src source filtering feature.
//
// This test verifies that the balancer correctly filters packets based on the allowed_srcs
// configuration for each virtual service. It covers:
//
// # Source Filtering Behavior
// - Packets from allowed source ranges are accepted and forwarded
// - Packets from non-allowed source ranges are dropped
// - Empty allowed_srcs list denies all sources
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
// The test uses 7 virtual services with different configurations:
// - VS1: IPv4 TCP port 80 with allowed_src 10.0.1.0/24
// - VS2: IPv4 UDP port 5353 with allowed_src 10.0.2.0/24
// - VS3: IPv6 TCP port 8080 with allowed_src 2001:db8:1::/48
// - VS4: IPv6 UDP port 5353 with allowed_src 2001:db8:2::/48
// - VS5: IPv4 TCP port 443 with allowed_src 0.0.0.0/1 + 128.0.0.0/1 (allow all IPv4)
// - VS6: IPv4 TCP port 8443 with allowed_src 0.0.0.0/0 (allow all)
// - VS7: IPv4 TCP port 9443 with empty allowed_src (deny all)

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

	// VS5: IPv4 TCP with large CIDR ranges (allow all)
	allowedSrcVs5IP   = netip.MustParseAddr("10.10.5.1")
	allowedSrcVs5Port = uint16(443)

	// VS6: IPv4 TCP with 0.0.0.0/0 allowed_src (allow all)
	allowedSrcVs6IP   = netip.MustParseAddr("10.10.6.1")
	allowedSrcVs6Port = uint16(8443)

	// VS7: IPv4 TCP with empty allowed_src (deny all)
	allowedSrcVs7IP   = netip.MustParseAddr("10.10.7.1")
	allowedSrcVs7Port = uint16(9443)

	// Real servers for allowed_src tests
	allowedSrcRealIPv4 = netip.MustParseAddr("192.168.100.1")
	allowedSrcRealIPv6 = netip.MustParseAddr("fe80::100")

	// Balancer source addresses for allowed_src tests
	allowedSrcBalancerSrcIPv4 = netip.MustParseAddr("5.5.5.5")
	allowedSrcBalancerSrcIPv6 = netip.MustParseAddr("fe80::5")
)

// createAllowedSrcTestConfig creates a balancer configuration with 7 virtual services
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
					AllowedSrcs: []*balancerpb.AllowedSources{
						{
							Nets: []*balancerpb.Net{{
								Addr: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("10.0.1.0").
										AsSlice(),
								},
								Mask: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("255.255.255.0").
										AsSlice(),
								},
							}},
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
					AllowedSrcs: []*balancerpb.AllowedSources{
						{
							Nets: []*balancerpb.Net{{
								Addr: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("10.0.2.0").
										AsSlice(),
								},
								Mask: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("255.255.255.0").
										AsSlice(),
								},
							}},
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
					AllowedSrcs: []*balancerpb.AllowedSources{
						{
							Nets: []*balancerpb.Net{{
								Addr: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("2001:db8:1::").
										AsSlice(),
								},
								Mask: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("ffff:ffff:ffff::").
										AsSlice(),
								},
							}},
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
					AllowedSrcs: []*balancerpb.AllowedSources{
						{
							Nets: []*balancerpb.Net{{
								Addr: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("2001:db8:2::").
										AsSlice(),
								},
								Mask: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("ffff:ffff:ffff::").
										AsSlice(),
								},
							}},
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
					AllowedSrcs: []*balancerpb.AllowedSources{
						{
							Nets: []*balancerpb.Net{{
								Addr: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("0.0.0.0").
										AsSlice(),
								},
								Mask: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("128.0.0.0").
										AsSlice(),
								},
							}},
						},
						{
							Nets: []*balancerpb.Net{{
								Addr: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("128.0.0.0").
										AsSlice(),
								},
								Mask: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("128.0.0.0").
										AsSlice(),
								},
							}},
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
					AllowedSrcs: []*balancerpb.AllowedSources{
						{
							Nets: []*balancerpb.Net{{
								Addr: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("0.0.0.0").
										AsSlice(),
								},
								Mask: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("0.0.0.0").
										AsSlice(),
								},
							}},
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
				// VS7: IPv4 TCP with empty allowed_src (deny all)
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: allowedSrcVs7IP.AsSlice(),
						},
						Port:  uint32(allowedSrcVs7Port),
						Proto: balancerpb.TransportProto_TCP,
					},
					Scheduler:   balancerpb.VsScheduler_ROUND_ROBIN,
					AllowedSrcs: []*balancerpb.AllowedSources{},
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

	// Test empty allowed_src (deny all)
	t.Run("Empty_AllowedSrc_DeniesAll", func(t *testing.T) {
		testEmptyAllowedSrcDeniesAll(t, ts, statsRef)
	})
}

// testEmptyAllowedSrcDeniesAll tests that empty allowed_src denies all sources
func testEmptyAllowedSrcDeniesAll(
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
		allowedSrcVs7IP,
		allowedSrcVs7Port,
		balancerpb.TransportProto_TCP,
	)
	require.NotNil(t, initialVsStats, "VS7 stats should exist")

	// Send packets from various sources - all should be denied
	testSources := []string{
		"10.0.1.1",
		"10.0.99.99",
		"192.168.1.1",
		"1.2.3.4",
		"172.16.0.1",
	}

	for _, srcIP := range testSources {
		clientIP := netip.MustParseAddr(srcIP)
		clientPort := uint16(60000)

		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			allowedSrcVs7IP,
			allowedSrcVs7Port,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)

		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Empty(
			t,
			result.Output,
			"expected no output packets for source %s when allowed_src is empty",
			srcIP,
		)
		require.Equal(
			t,
			1,
			len(result.Drop),
			"expected 1 dropped packet for source %s when allowed_src is empty",
			srcIP,
		)
	}

	// Get final stats
	finalStats, err := ts.Balancer.Stats(statsRef)
	require.NoError(t, err)
	finalVsStats := findVsStats(
		finalStats,
		allowedSrcVs7IP,
		allowedSrcVs7Port,
		balancerpb.TransportProto_TCP,
	)
	require.NotNil(t, finalVsStats, "VS7 stats should exist")

	// Verify counters - all packets should be blocked
	assert.Equal(
		t,
		initialVsStats.PacketSrcNotAllowed+uint64(len(testSources)),
		finalVsStats.PacketSrcNotAllowed,
		"packet_src_not_allowed should increase by number of test sources when allowed_src is empty",
	)
	assert.Equal(t,
		initialVsStats.OutgoingPackets,
		finalVsStats.OutgoingPackets,
		"outgoing_packets should not increase when allowed_src is empty",
	)
	assert.Equal(t,
		initialVsStats.CreatedSessions,
		finalVsStats.CreatedSessions,
		"created_sessions should not increase when allowed_src is empty",
	)
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

// TestAllowedSrcWithPorts tests source filtering with port range restrictions
func TestAllowedSrcWithPorts(t *testing.T) {
	// Create configuration with port range restrictions
	config := &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: &balancerpb.Addr{
				Bytes: allowedSrcBalancerSrcIPv4.AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: allowedSrcBalancerSrcIPv6.AsSlice(),
			},
			Vs: []*balancerpb.VirtualService{
				// VS with single port range restriction
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("10.20.1.1").AsSlice(),
						},
						Port:  80,
						Proto: balancerpb.TransportProto_TCP,
					},
					Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
					AllowedSrcs: []*balancerpb.AllowedSources{
						{
							Nets: []*balancerpb.Net{{
								Addr: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("192.168.0.0").
										AsSlice(),
								},
								Mask: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("255.255.0.0").
										AsSlice(),
								},
							}},
							Ports: []*balancerpb.PortsRange{
								{
									From: 1024,
									To:   65535,
								}, // Only high ports allowed
							},
						},
					},
					Flags: &balancerpb.VsFlags{},
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
				// VS with multiple specific port ranges
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("10.20.2.1").AsSlice(),
						},
						Port:  443,
						Proto: balancerpb.TransportProto_TCP,
					},
					Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
					AllowedSrcs: []*balancerpb.AllowedSources{
						{
							Nets: []*balancerpb.Net{{
								Addr: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("10.0.0.0").
										AsSlice(),
								},
								Mask: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("255.0.0.0").
										AsSlice(),
								},
							}},
							Ports: []*balancerpb.PortsRange{
								{From: 80, To: 80},     // HTTP
								{From: 443, To: 443},   // HTTPS
								{From: 8000, To: 9000}, // Custom range
							},
						},
					},
					Flags: &balancerpb.VsFlags{},
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

	statsRef := &balancerpb.PacketHandlerRef{
		Device:   &utils.DeviceName,
		Pipeline: &utils.PipelineName,
		Function: &utils.FunctionName,
		Chain:    &utils.ChainName,
	}

	t.Run("HighPortAllowed", func(t *testing.T) {
		// Test packet from high port (within 1024-65535 range)
		vsIP := netip.MustParseAddr("10.20.1.1")
		clientIP := netip.MustParseAddr("192.168.1.100")
		clientPort := uint16(50000) // High port - should be allowed

		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			vsIP,
			80,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)

		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(
			t,
			1,
			len(result.Output),
			"expected 1 output packet for high port",
		)
		require.Empty(
			t,
			result.Drop,
			"expected no dropped packets for high port",
		)
	})

	t.Run("LowPortBlocked", func(t *testing.T) {
		// Test packet from low port (below 1024)
		vsIP := netip.MustParseAddr("10.20.1.1")
		clientIP := netip.MustParseAddr("192.168.1.100")
		clientPort := uint16(80) // Low port - should be blocked

		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			vsIP,
			80,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)

		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Empty(
			t,
			result.Output,
			"expected no output packets for low port",
		)
		require.Equal(
			t,
			1,
			len(result.Drop),
			"expected 1 dropped packet for low port",
		)

		// Verify counter increased
		stats, err := ts.Balancer.Stats(statsRef)
		require.NoError(t, err)
		vsStats := findVsStats(stats, vsIP, 80, balancerpb.TransportProto_TCP)
		require.NotNil(t, vsStats)
		assert.Greater(t, vsStats.PacketSrcNotAllowed, uint64(0),
			"packet_src_not_allowed should increase for blocked port")
	})

	t.Run("SpecificPortsAllowed", func(t *testing.T) {
		// Test packets from specific allowed ports
		vsIP := netip.MustParseAddr("10.20.2.1")
		clientIP := netip.MustParseAddr("10.1.1.100")

		allowedPorts := []uint16{80, 443, 8500} // All within allowed ranges
		for _, port := range allowedPorts {
			packetLayers := utils.MakeTCPPacket(
				clientIP,
				port,
				vsIP,
				443,
				&layers.TCP{SYN: true},
			)
			packet := xpacket.LayersToPacket(t, packetLayers...)

			result, err := ts.Mock.HandlePackets(packet)
			require.NoError(t, err)
			require.Equal(t, 1, len(result.Output),
				"expected 1 output packet for allowed port %d", port)
			require.Empty(t, result.Drop,
				"expected no dropped packets for allowed port %d", port)
		}
	})

	t.Run("SpecificPortsBlocked", func(t *testing.T) {
		// Test packets from ports outside allowed ranges
		vsIP := netip.MustParseAddr("10.20.2.1")
		clientIP := netip.MustParseAddr("10.1.1.100")

		blockedPorts := []uint16{22, 3306, 10000} // Outside allowed ranges
		for _, port := range blockedPorts {
			packetLayers := utils.MakeTCPPacket(
				clientIP,
				port,
				vsIP,
				443,
				&layers.TCP{SYN: true},
			)
			packet := xpacket.LayersToPacket(t, packetLayers...)

			result, err := ts.Mock.HandlePackets(packet)
			require.NoError(t, err)
			require.Empty(t, result.Output,
				"expected no output packets for blocked port %d", port)
			require.Equal(t, 1, len(result.Drop),
				"expected 1 dropped packet for blocked port %d", port)
		}
	})
}

// TestAllowedSrcWithTags tests that allowed_sources stats are correctly tracked per tag
func TestAllowedSrcWithTags(t *testing.T) {
	// Create configuration with multiple allowed sources with different tags
	config := &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: &balancerpb.Addr{
				Bytes: allowedSrcBalancerSrcIPv4.AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: allowedSrcBalancerSrcIPv6.AsSlice(),
			},
			Vs: []*balancerpb.VirtualService{
				// VS with multiple allowed sources with tags
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("10.30.1.1").AsSlice(),
						},
						Port:  80,
						Proto: balancerpb.TransportProto_TCP,
					},
					Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
					AllowedSrcs: []*balancerpb.AllowedSources{
						{
							// Tag 100: Internal network
							Nets: []*balancerpb.Net{{
								Addr: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("10.0.0.0").
										AsSlice(),
								},
								Mask: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("255.0.0.0").
										AsSlice(),
								},
							}},
							Tag: func() *string { s := "100"; return &s }(),
						},
						{
							// Tag 200: Partner network
							Nets: []*balancerpb.Net{{
								Addr: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("192.168.0.0").
										AsSlice(),
								},
								Mask: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("255.255.0.0").
										AsSlice(),
								},
							}},
							Tag: func() *string { s := "200"; return &s }(),
						},
						{
							// Tag 300: Public network range (for testing untracked sources)
							// Using a specific range that doesn't overlap with tags 100 and 200
							Nets: []*balancerpb.Net{{
								Addr: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("8.0.0.0").
										AsSlice(),
								},
								Mask: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("255.0.0.0").
										AsSlice(),
								},
							}},
							Tag: nil, // nil means no tracking
						},
					},
					Flags: &balancerpb.VsFlags{},
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
				// VS with multiple networks per allowed source and multiple port ranges
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("10.30.2.1").AsSlice(),
						},
						Port:  443,
						Proto: balancerpb.TransportProto_TCP,
					},
					Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
					AllowedSrcs: []*balancerpb.AllowedSources{
						{
							// Tag 300: Multiple networks with port restrictions
							Nets: []*balancerpb.Net{
								{
									Addr: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("172.16.0.0").
											AsSlice(),
									},
									Mask: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("255.240.0.0").
											AsSlice(),
									},
								},
								{
									Addr: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("172.32.0.0").
											AsSlice(),
									},
									Mask: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("255.240.0.0").
											AsSlice(),
									},
								},
							},
							Ports: []*balancerpb.PortsRange{
								{From: 1024, To: 65535}, // High ports only
							},
							Tag: func() *string { s := "300"; return &s }(),
						},
						{
							// Tag 400: Different network with different port ranges
							Nets: []*balancerpb.Net{{
								Addr: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("203.0.113.0").
										AsSlice(),
								},
								Mask: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("255.255.255.0").
										AsSlice(),
								},
							}},
							Ports: []*balancerpb.PortsRange{
								{From: 80, To: 80},
								{From: 443, To: 443},
								{From: 8080, To: 8080},
							},
							Tag: func() *string { s := "400"; return &s }(),
						},
					},
					Flags: &balancerpb.VsFlags{},
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

	ts, err := utils.Make(&utils.TestConfig{
		Mock:     utils.SingleWorkerMockConfig(256*datasize.MB, 4*datasize.MB),
		Balancer: config,
		AgentMemory: func() *datasize.ByteSize {
			memory := 128 * datasize.MB
			return &memory
		}(),
	})
	require.NoError(t, err)
	defer ts.Free()

	// Enable all reals
	utils.EnableAllReals(t, ts)

	statsRef := &balancerpb.PacketHandlerRef{
		Device:   &utils.DeviceName,
		Pipeline: &utils.PipelineName,
		Function: &utils.FunctionName,
		Chain:    &utils.ChainName,
	}

	t.Run("TaggedSourcesTracking", func(t *testing.T) {
		vsIP := netip.MustParseAddr("10.30.1.1")
		vsPort := uint16(80)

		// Get initial stats
		initialStats, err := ts.Balancer.Stats(statsRef)
		require.NoError(t, err)

		var initialVsStats *balancerpb.NamedVsStats
		for _, vs := range initialStats.Vs {
			addr, _ := netip.AddrFromSlice(vs.Vs.Addr.Bytes)
			if addr == vsIP && vs.Vs.Port == uint32(vsPort) {
				initialVsStats = vs
				break
			}
		}
		require.NotNil(t, initialVsStats, "VS stats should exist")

		// Send packets from different sources
		testCases := []struct {
			name        string
			srcIP       string
			srcPort     uint16
			expectedTag uint32
		}{
			{"InternalNetwork", "10.5.5.5", 50000, 100},
			{"PartnerNetwork", "192.168.10.10", 50001, 200},
			{"PublicNetwork", "8.8.8.8", 50002, 0}, // Tag 0 - no tracking
		}

		for _, tc := range testCases {
			clientIP := netip.MustParseAddr(tc.srcIP)
			packetLayers := utils.MakeTCPPacket(
				clientIP,
				tc.srcPort,
				vsIP,
				vsPort,
				&layers.TCP{SYN: true},
			)
			packet := xpacket.LayersToPacket(t, packetLayers...)

			result, err := ts.Mock.HandlePackets(packet)
			require.NoError(
				t,
				err,
				"packet from %s should be processed",
				tc.name,
			)
			require.Equal(
				t,
				1,
				len(result.Output),
				"expected 1 output packet for %s",
				tc.name,
			)
		}

		// Get final stats
		finalStats, err := ts.Balancer.Stats(statsRef)
		require.NoError(t, err)

		var finalVsStats *balancerpb.NamedVsStats
		for _, vs := range finalStats.Vs {
			addr, _ := netip.AddrFromSlice(vs.Vs.Addr.Bytes)
			if addr == vsIP && vs.Vs.Port == uint32(vsPort) {
				finalVsStats = vs
				break
			}
		}
		require.NotNil(t, finalVsStats, "VS stats should exist")

		// Verify allowed_sources stats
		require.NotNil(
			t,
			finalVsStats.AllowedSources,
			"allowed_sources stats should exist",
		)

		// Build a map of tag -> passes for easier verification
		tagStats := make(map[string]uint64)
		for _, allowedSrc := range finalVsStats.AllowedSources {
			tagStats[allowedSrc.Tag] = allowedSrc.Passes
		}

		// Verify tag 100 (Internal network) has 1 pass
		assert.Equal(t, uint64(1), tagStats["100"], "tag 100 should have 1 pass")

		// Verify tag 200 (Partner network) has 1 pass
		assert.Equal(t, uint64(1), tagStats["200"], "tag 200 should have 1 pass")

		// Verify nil tag (Public network) is NOT tracked
		_, exists := tagStats[""]
		assert.False(t, exists, "nil tag should not be tracked in stats")
	})

	t.Run("MultipleNetworksAndPorts", func(t *testing.T) {
		vsIP := netip.MustParseAddr("10.30.2.1")
		vsPort := uint16(443)

		// Get initial stats
		initialStats, err := ts.Balancer.Stats(statsRef)
		require.NoError(t, err)

		var initialVsStats *balancerpb.NamedVsStats
		for _, vs := range initialStats.Vs {
			addr, _ := netip.AddrFromSlice(vs.Vs.Addr.Bytes)
			if addr == vsIP && vs.Vs.Port == uint32(vsPort) {
				initialVsStats = vs
				break
			}
		}
		require.NotNil(t, initialVsStats, "VS stats should exist")

		// Test packets from different networks and ports
		testCases := []struct {
			name        string
			srcIP       string
			srcPort     uint16
			shouldPass  bool
			expectedTag uint32
		}{
			// Tag 300 tests (172.16.0.0/12 and 172.32.0.0/12 with high ports)
			{"Network1HighPort", "172.16.1.1", 50000, true, 300},
			{"Network2HighPort", "172.32.1.1", 50001, true, 300},
			{"Network1LowPort", "172.16.1.1", 80, false, 0}, // Low port blocked

			// Tag 400 tests (203.0.113.0/24 with specific ports)
			{"Network3Port80", "203.0.113.10", 80, true, 400},
			{"Network3Port443", "203.0.113.10", 443, true, 400},
			{"Network3Port8080", "203.0.113.10", 8080, true, 400},
			{
				"Network3Port22",
				"203.0.113.10",
				22,
				false,
				0,
			}, // Port not in allowed list
		}

		passedPackets := make(map[uint32]int)
		for _, tc := range testCases {
			clientIP := netip.MustParseAddr(tc.srcIP)
			packetLayers := utils.MakeTCPPacket(
				clientIP,
				tc.srcPort,
				vsIP,
				vsPort,
				&layers.TCP{SYN: true},
			)
			packet := xpacket.LayersToPacket(t, packetLayers...)

			result, err := ts.Mock.HandlePackets(packet)
			require.NoError(t, err, "packet processing failed for %s", tc.name)

			if tc.shouldPass {
				require.Equal(
					t,
					1,
					len(result.Output),
					"expected 1 output packet for %s",
					tc.name,
				)
				require.Empty(
					t,
					result.Drop,
					"expected no dropped packets for %s",
					tc.name,
				)
				passedPackets[tc.expectedTag]++
			} else {
				require.Empty(t, result.Output, "expected no output packets for %s", tc.name)
				require.Equal(t, 1, len(result.Drop), "expected 1 dropped packet for %s", tc.name)
			}
		}

		// Get final stats
		finalStats, err := ts.Balancer.Stats(statsRef)
		require.NoError(t, err)

		var finalVsStats *balancerpb.NamedVsStats
		for _, vs := range finalStats.Vs {
			addr, _ := netip.AddrFromSlice(vs.Vs.Addr.Bytes)
			if addr == vsIP && vs.Vs.Port == uint32(vsPort) {
				finalVsStats = vs
				break
			}
		}
		require.NotNil(t, finalVsStats, "VS stats should exist")

		// Verify allowed_sources stats
		require.NotNil(
			t,
			finalVsStats.AllowedSources,
			"allowed_sources stats should exist",
		)

		// Build a map of tag -> passes
		tagStats := make(map[string]uint64)
		for _, allowedSrc := range finalVsStats.AllowedSources {
			tagStats[allowedSrc.Tag] = allowedSrc.Passes
		}

		// Verify tag 300 has correct number of passes (2 packets from different networks)
		assert.Equal(t, uint64(passedPackets[300]), tagStats["300"],
			"tag 300 should have %d passes", passedPackets[300])

		// Verify tag 400 has correct number of passes (3 packets from different ports)
		assert.Equal(t, uint64(passedPackets[400]), tagStats["400"],
			"tag 400 should have %d passes", passedPackets[400])
	})
}

// TestAllowedSrcMultipleNetworksWithTags tests ACL behavior with multiple networks per allowed source
// and verifies stats tracking using tags
func TestAllowedSrcMultipleNetworksWithTags(t *testing.T) {
	// Create configuration with allowed sources containing multiple networks each
	config := &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: &balancerpb.Addr{
				Bytes: allowedSrcBalancerSrcIPv4.AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: allowedSrcBalancerSrcIPv6.AsSlice(),
			},
			Vs: []*balancerpb.VirtualService{
				// VS with multiple allowed sources, each containing multiple networks
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("10.40.1.1").AsSlice(),
						},
						Port:  80,
						Proto: balancerpb.TransportProto_TCP,
					},
					Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
					AllowedSrcs: []*balancerpb.AllowedSources{
						{
							// Tag 500: Corporate networks (4 different subnets)
							Nets: []*balancerpb.Net{
								{
									Addr: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("10.10.0.0").
											AsSlice(),
									},
									Mask: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("255.255.0.0").
											AsSlice(),
									},
								},
								{
									Addr: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("10.20.0.0").
											AsSlice(),
									},
									Mask: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("255.255.0.0").
											AsSlice(),
									},
								},
								{
									Addr: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("10.30.0.0").
											AsSlice(),
									},
									Mask: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("255.255.0.0").
											AsSlice(),
									},
								},
								{
									Addr: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("10.40.0.0").
											AsSlice(),
									},
									Mask: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("255.255.0.0").
											AsSlice(),
									},
								},
							},
							Tag: func() *string { s := "500"; return &s }(),
						},
						{
							// Tag 600: Partner networks (3 different subnets)
							Nets: []*balancerpb.Net{
								{
									Addr: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("192.168.1.0").
											AsSlice(),
									},
									Mask: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("255.255.255.0").
											AsSlice(),
									},
								},
								{
									Addr: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("192.168.2.0").
											AsSlice(),
									},
									Mask: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("255.255.255.0").
											AsSlice(),
									},
								},
								{
									Addr: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("192.168.3.0").
											AsSlice(),
									},
									Mask: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("255.255.255.0").
											AsSlice(),
									},
								},
							},
							Tag: func() *string { s := "600"; return &s }(),
						},
						{
							// Tag 700: External networks (4 different subnets with port restrictions)
							Nets: []*balancerpb.Net{
								{
									Addr: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("203.0.113.0").
											AsSlice(),
									},
									Mask: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("255.255.255.0").
											AsSlice(),
									},
								},
								{
									Addr: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("198.51.100.0").
											AsSlice(),
									},
									Mask: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("255.255.255.0").
											AsSlice(),
									},
								},
								{
									Addr: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("198.18.0.0").
											AsSlice(),
									},
									Mask: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("255.254.0.0").
											AsSlice(),
									},
								},
								{
									Addr: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("100.64.0.0").
											AsSlice(),
									},
									Mask: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("255.192.0.0").
											AsSlice(),
									},
								},
							},
							Ports: []*balancerpb.PortsRange{
								{From: 1024, To: 65535}, // Only high ports
							},
							Tag: func() *string { s := "700"; return &s }(),
						},
					},
					Flags: &balancerpb.VsFlags{},
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

	ts, err := utils.Make(&utils.TestConfig{
		Mock:     utils.SingleWorkerMockConfig(256*datasize.MB, 4*datasize.MB),
		Balancer: config,
		AgentMemory: func() *datasize.ByteSize {
			memory := 128 * datasize.MB
			return &memory
		}(),
	})
	require.NoError(t, err)
	defer ts.Free()

	// Enable all reals
	utils.EnableAllReals(t, ts)

	statsRef := &balancerpb.PacketHandlerRef{
		Device:   &utils.DeviceName,
		Pipeline: &utils.PipelineName,
		Function: &utils.FunctionName,
		Chain:    &utils.ChainName,
	}

	vsIP := netip.MustParseAddr("10.40.1.1")
	vsPort := uint16(80)

	t.Run("CorporateNetworks_Tag500", func(t *testing.T) {
		// Test packets from all 4 corporate networks
		testCases := []struct {
			name    string
			srcIP   string
			srcPort uint16
		}{
			{"Network1", "10.10.5.5", 50000},
			{"Network2", "10.20.10.10", 50001},
			{"Network3", "10.30.15.15", 50002},
			{"Network4", "10.40.20.20", 50003},
		}

		for _, tc := range testCases {
			clientIP := netip.MustParseAddr(tc.srcIP)
			packetLayers := utils.MakeTCPPacket(
				clientIP,
				tc.srcPort,
				vsIP,
				vsPort,
				&layers.TCP{SYN: true},
			)
			packet := xpacket.LayersToPacket(t, packetLayers...)

			result, err := ts.Mock.HandlePackets(packet)
			require.NoError(
				t,
				err,
				"packet from %s should be processed",
				tc.name,
			)
			require.Equal(
				t,
				1,
				len(result.Output),
				"expected 1 output packet for %s",
				tc.name,
			)
			require.Empty(
				t,
				result.Drop,
				"expected no dropped packets for %s",
				tc.name,
			)
		}

		// Verify stats for tag 500
		stats, err := ts.Balancer.Stats(statsRef)
		require.NoError(t, err)

		var vsStats *balancerpb.NamedVsStats
		for _, vs := range stats.Vs {
			addr, _ := netip.AddrFromSlice(vs.Vs.Addr.Bytes)
			if addr == vsIP && vs.Vs.Port == uint32(vsPort) {
				vsStats = vs
				break
			}
		}
		require.NotNil(t, vsStats, "VS stats should exist")

		// Build tag stats map
		tagStats := make(map[string]uint64)
		for _, allowedSrc := range vsStats.AllowedSources {
			tagStats[allowedSrc.Tag] = allowedSrc.Passes
		}

		// Verify tag 500 has 4 passes (one from each network)
		assert.Equal(
			t,
			uint64(4),
			tagStats["500"],
			"tag 500 should have 4 passes",
		)
	})

	t.Run("PartnerNetworks_Tag600", func(t *testing.T) {
		// Test packets from all 3 partner networks
		testCases := []struct {
			name    string
			srcIP   string
			srcPort uint16
		}{
			{"Partner1", "192.168.1.100", 51000},
			{"Partner2", "192.168.2.200", 51001},
			{"Partner3", "192.168.3.50", 51002},
		}

		for _, tc := range testCases {
			clientIP := netip.MustParseAddr(tc.srcIP)
			packetLayers := utils.MakeTCPPacket(
				clientIP,
				tc.srcPort,
				vsIP,
				vsPort,
				&layers.TCP{SYN: true},
			)
			packet := xpacket.LayersToPacket(t, packetLayers...)

			result, err := ts.Mock.HandlePackets(packet)
			require.NoError(
				t,
				err,
				"packet from %s should be processed",
				tc.name,
			)
			require.Equal(
				t,
				1,
				len(result.Output),
				"expected 1 output packet for %s",
				tc.name,
			)
			require.Empty(
				t,
				result.Drop,
				"expected no dropped packets for %s",
				tc.name,
			)
		}

		// Verify stats for tag 600
		stats, err := ts.Balancer.Stats(statsRef)
		require.NoError(t, err)

		var vsStats *balancerpb.NamedVsStats
		for _, vs := range stats.Vs {
			addr, _ := netip.AddrFromSlice(vs.Vs.Addr.Bytes)
			if addr == vsIP && vs.Vs.Port == uint32(vsPort) {
				vsStats = vs
				break
			}
		}
		require.NotNil(t, vsStats, "VS stats should exist")

		// Build tag stats map
		tagStats := make(map[string]uint64)
		for _, allowedSrc := range vsStats.AllowedSources {
			tagStats[allowedSrc.Tag] = allowedSrc.Passes
		}

		// Verify tag 600 has 3 passes (one from each partner network)
		assert.Equal(
			t,
			uint64(3),
			tagStats["600"],
			"tag 600 should have 3 passes",
		)
	})

	t.Run("ExternalNetworks_Tag700_WithPortFiltering", func(t *testing.T) {
		// Test packets from all 4 external networks with different port scenarios
		testCases := []struct {
			name       string
			srcIP      string
			srcPort    uint16
			shouldPass bool
		}{
			// High ports - should pass
			{"External1_HighPort", "203.0.113.10", 50000, true},
			{"External2_HighPort", "198.51.100.20", 50001, true},
			{"External3_HighPort", "198.18.5.5", 50002, true},
			{"External4_HighPort", "100.64.10.10", 50003, true},

			// Low ports - should be blocked
			{"External1_LowPort", "203.0.113.11", 80, false},
			{"External2_LowPort", "198.51.100.21", 443, false},
			{"External3_LowPort", "198.18.5.6", 22, false},
			{"External4_LowPort", "100.64.10.11", 1023, false},
		}

		passedCount := 0
		for _, tc := range testCases {
			clientIP := netip.MustParseAddr(tc.srcIP)
			packetLayers := utils.MakeTCPPacket(
				clientIP,
				tc.srcPort,
				vsIP,
				vsPort,
				&layers.TCP{SYN: true},
			)
			packet := xpacket.LayersToPacket(t, packetLayers...)

			result, err := ts.Mock.HandlePackets(packet)
			require.NoError(t, err, "packet processing failed for %s", tc.name)

			if tc.shouldPass {
				require.Equal(
					t,
					1,
					len(result.Output),
					"expected 1 output packet for %s",
					tc.name,
				)
				require.Empty(
					t,
					result.Drop,
					"expected no dropped packets for %s",
					tc.name,
				)
				passedCount++
			} else {
				require.Empty(t, result.Output, "expected no output packets for %s", tc.name)
				require.Equal(t, 1, len(result.Drop), "expected 1 dropped packet for %s", tc.name)
			}
		}

		// Verify stats for tag 700
		stats, err := ts.Balancer.Stats(statsRef)
		require.NoError(t, err)

		var vsStats *balancerpb.NamedVsStats
		for _, vs := range stats.Vs {
			addr, _ := netip.AddrFromSlice(vs.Vs.Addr.Bytes)
			if addr == vsIP && vs.Vs.Port == uint32(vsPort) {
				vsStats = vs
				break
			}
		}
		require.NotNil(t, vsStats, "VS stats should exist")

		// Build tag stats map
		tagStats := make(map[string]uint64)
		for _, allowedSrc := range vsStats.AllowedSources {
			tagStats[allowedSrc.Tag] = allowedSrc.Passes
		}

		// Verify tag 700 has correct number of passes (only high port packets)
		assert.Equal(
			t,
			uint64(passedCount),
			tagStats["700"],
			"tag 700 should have %d passes (only high port packets)",
			passedCount,
		)
	})

	t.Run("BlockedSources", func(t *testing.T) {
		// Test packets from networks not in any allowed source
		testCases := []struct {
			name    string
			srcIP   string
			srcPort uint16
		}{
			{"Blocked1", "172.16.1.1", 50000},
			{"Blocked2", "8.8.8.8", 50001},
			{"Blocked3", "1.1.1.1", 50002},
		}

		for _, tc := range testCases {
			clientIP := netip.MustParseAddr(tc.srcIP)
			packetLayers := utils.MakeTCPPacket(
				clientIP,
				tc.srcPort,
				vsIP,
				vsPort,
				&layers.TCP{SYN: true},
			)
			packet := xpacket.LayersToPacket(t, packetLayers...)

			result, err := ts.Mock.HandlePackets(packet)
			require.NoError(t, err, "packet processing failed for %s", tc.name)
			require.Empty(
				t,
				result.Output,
				"expected no output packets for %s",
				tc.name,
			)
			require.Equal(
				t,
				1,
				len(result.Drop),
				"expected 1 dropped packet for %s",
				tc.name,
			)
		}

		// Verify these blocked packets don't appear in any tag stats
		stats, err := ts.Balancer.Stats(statsRef)
		require.NoError(t, err)

		var vsStats *balancerpb.NamedVsStats
		for _, vs := range stats.Vs {
			addr, _ := netip.AddrFromSlice(vs.Vs.Addr.Bytes)
			if addr == vsIP && vs.Vs.Port == uint32(vsPort) {
				vsStats = vs
				break
			}
		}
		require.NotNil(t, vsStats, "VS stats should exist")

		// Verify packet_src_not_allowed counter increased
		assert.Greater(t, vsStats.Stats.PacketSrcNotAllowed, uint64(0),
			"packet_src_not_allowed should be greater than 0")
	})

	t.Run("VerifyAllTagsPresent", func(t *testing.T) {
		// Final verification that all tags are present with correct counts
		stats, err := ts.Balancer.Stats(statsRef)
		require.NoError(t, err)

		var vsStats *balancerpb.NamedVsStats
		for _, vs := range stats.Vs {
			addr, _ := netip.AddrFromSlice(vs.Vs.Addr.Bytes)
			if addr == vsIP && vs.Vs.Port == uint32(vsPort) {
				vsStats = vs
				break
			}
		}
		require.NotNil(t, vsStats, "VS stats should exist")

		// Build tag stats map
		tagStats := make(map[string]uint64)
		for _, allowedSrc := range vsStats.AllowedSources {
			tagStats[allowedSrc.Tag] = allowedSrc.Passes
		}

		// Verify all three tags are present
		assert.Contains(t, tagStats, "500", "tag 500 should be present")
		assert.Contains(t, tagStats, "600", "tag 600 should be present")
		assert.Contains(t, tagStats, "700", "tag 700 should be present")

		// Verify tag 500 (4 corporate networks)
		assert.Equal(
			t,
			uint64(4),
			tagStats["500"],
			"tag 500 should have 4 passes",
		)

		// Verify tag 600 (3 partner networks)
		assert.Equal(
			t,
			uint64(3),
			tagStats["600"],
			"tag 600 should have 3 passes",
		)

		// Verify tag 700 (4 external networks with port filtering - only high ports)
		assert.Equal(
			t,
			uint64(4),
			tagStats["700"],
			"tag 700 should have 4 passes",
		)

		// Verify total allowed sources count
		assert.Equal(t, 3, len(vsStats.AllowedSources),
			"should have exactly 3 allowed source stats entries")
	})
}
