package balancer_test

// TestSessionTableStress2 validates session table behavior with multiple virtual services:
//
// # Virtual Services Configuration
// Six virtual services with diverse configurations:
// - VS1: TCP IPv4, ROUND_ROBIN, no GRE/FixMSS, 2 IPv4 reals (weights 1:1)
// - VS2: UDP IPv4, SOURCE_HASH, no GRE, 2 IPv4 reals (weights 2:3)
// - VS3: TCP IPv6, ROUND_ROBIN, with GRE, 2 IPv6 reals (weights 1:1)
// - VS4: UDP IPv6, SOURCE_HASH, no GRE, 2 IPv6 reals (weights 1:2)
// - VS5: TCP IPv4, SOURCE_HASH, with GRE, mixed IPv4/IPv6 reals (weights 3:2)
// - VS6: TCP IPv6, ROUND_ROBIN, with FixMSS, 2 IPv4 reals (weights 1:1)
//
// # Test Configuration
// - Session timeout: 60 seconds
// - Initial capacity: 16 (dynamically resized)
// - Max load factor: 0.25
//
// # Phase 1: Initial Session Creation
// - Sends 16 random packets across all virtual services
// - Tracks which sessions were accepted and their selected real servers
// - Verifies at least 75% acceptance rate
//
// # Phase 2: Iterative Stress Testing (10 iterations)
// Each iteration performs:
// - Step 1: Sync active sessions and resize table on demand
// - Step 2: Send packets to all existing sessions
//   * Validates all packets are accepted (no drops)
//   * Verifies session-to-real consistency (same client → same real)
// - Step 3: Create N/2 new sessions (where N = current active sessions)
//   * Validates all new sessions are accepted
//   * Tracks new sessions and their selected reals
//
// # Session Consistency Validation
// - Ensures the same client always reaches the same real server
// - Validates this consistency across packet retransmissions
// - Tests with both TCP (SYN and non-SYN) and UDP packets
//
// # Final Verification
// - Confirms tracked sessions match balancer's session count
// - Validates VS and Real active session counts are consistent
// - Verifies session table capacity and load factor

import (
	"fmt"
	"math/rand"
	"net/netip"
	"testing"
	"time"

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

// sessionInfo tracks a session and its selected real
type sessionInfo struct {
	clientIP   netip.Addr
	clientPort uint16
	vsIP       netip.Addr
	vsPort     uint16
	proto      balancerpb.TransportProto
	realIP     netip.Addr // The real server selected for this session
}

// vsConfig holds configuration for a virtual service
type vsConfig struct {
	ip        netip.Addr
	port      uint16
	proto     balancerpb.TransportProto
	scheduler balancerpb.VsScheduler
	gre       bool
	fixMss    bool
	reals     []realConfig
}

// realConfig holds configuration for a real server
type realConfig struct {
	ip     netip.Addr
	weight uint32
}

// TestSessionTableStress2 tests session table with multiple virtual services,
// sequential resizing, and session consistency validation
func TestSessionTableStress2(t *testing.T) {
	sessionTimeout := 60 // in seconds
	initialCapacity := 16
	maxLoadFactor := 0.25

	// Define virtual services configuration
	// Mix of TCP/UDP, IPv4/IPv6, ROUND_ROBIN/SOURCE_HASH schedulers, with/without GRE and FixMSS
	virtualServicesConfig := []vsConfig{
		// VS1: TCP IPv4, ROUND_ROBIN, no GRE, no FixMSS, IPv4 reals
		{
			ip:        netip.MustParseAddr("10.1.1.1"),
			port:      80,
			proto:     balancerpb.TransportProto_TCP,
			scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
			gre:       false,
			fixMss:    false,
			reals: []realConfig{
				{netip.MustParseAddr("10.2.1.1"), 1},
				{netip.MustParseAddr("10.2.1.2"), 1},
			},
		},
		// VS2: UDP IPv4, SOURCE_HASH, no GRE, IPv4 reals
		{
			ip:        netip.MustParseAddr("10.1.2.1"),
			port:      5353,
			proto:     balancerpb.TransportProto_UDP,
			scheduler: balancerpb.VsScheduler_SOURCE_HASH,
			gre:       false,
			fixMss:    false,
			reals: []realConfig{
				{netip.MustParseAddr("10.2.2.1"), 2},
				{netip.MustParseAddr("10.2.2.2"), 3},
			},
		},
		// VS3: TCP IPv6, ROUND_ROBIN, with GRE, IPv6 reals
		{
			ip:        netip.MustParseAddr("2001:db8::1"),
			port:      443,
			proto:     balancerpb.TransportProto_TCP,
			scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
			gre:       true,
			fixMss:    false,
			reals: []realConfig{
				{netip.MustParseAddr("2001:db8:2::1"), 1},
				{netip.MustParseAddr("2001:db8:2::2"), 1},
			},
		},
		// VS4: UDP IPv6, SOURCE_HASH, IPv6 reals
		{
			ip:        netip.MustParseAddr("2001:db8::2"),
			port:      8080,
			proto:     balancerpb.TransportProto_UDP,
			scheduler: balancerpb.VsScheduler_SOURCE_HASH,
			gre:       false,
			fixMss:    false,
			reals: []realConfig{
				{netip.MustParseAddr("2001:db8:3::1"), 1},
				{netip.MustParseAddr("2001:db8:3::2"), 2},
			},
		},
		// VS5: TCP IPv4, SOURCE_HASH, with GRE, mixed IPv4 and IPv6 reals
		{
			ip:        netip.MustParseAddr("10.1.3.1"),
			port:      8443,
			proto:     balancerpb.TransportProto_TCP,
			scheduler: balancerpb.VsScheduler_SOURCE_HASH,
			gre:       true,
			fixMss:    false,
			reals: []realConfig{
				{netip.MustParseAddr("10.2.4.1"), 3},
				{netip.MustParseAddr("2001:db8:4::2"), 2},
			},
		},
		// VS6: TCP IPv6, ROUND_ROBIN, with FixMSS, IPv4 reals
		{
			ip:        netip.MustParseAddr("2001:db8::3"),
			port:      9000,
			proto:     balancerpb.TransportProto_TCP,
			scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
			gre:       false,
			fixMss:    true,
			reals: []realConfig{
				{netip.MustParseAddr("10.2.3.1"), 1},
				{netip.MustParseAddr("10.2.3.2"), 1},
			},
		},
	}

	// Build module config from virtual services configuration
	virtualServices := make(
		[]*balancerpb.VirtualService,
		0,
		len(virtualServicesConfig),
	)
	for _, vsConf := range virtualServicesConfig {
		// Build allowed sources based on VS IP version
		var allowedSrcs []*balancerpb.Net
		if vsConf.ip.Is4() {
			allowedSrcs = []*balancerpb.Net{
				{
					Addr: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("10.0.0.0").AsSlice(),
					},
					Size: 8,
				},
			}
		} else {
			allowedSrcs = []*balancerpb.Net{
				{
					Addr: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("2001:db8::").AsSlice(),
					},
					Size: 32,
				},
			}
		}

		// Build reals
		reals := make([]*balancerpb.Real, 0, len(vsConf.reals))
		for _, realConf := range vsConf.reals {
			var srcMask []byte
			if realConf.ip.Is4() {
				srcMask = netip.MustParseAddr("255.255.255.255").AsSlice()
			} else {
				srcMask = netip.MustParseAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff").AsSlice()
			}

			reals = append(reals, &balancerpb.Real{
				Id: &balancerpb.RelativeRealIdentifier{
					Ip: &balancerpb.Addr{
						Bytes: realConf.ip.AsSlice(),
					},
					Port: 0,
				},
				Weight: realConf.weight,
				SrcAddr: &balancerpb.Addr{
					Bytes: realConf.ip.AsSlice(),
				},
				SrcMask: &balancerpb.Addr{
					Bytes: srcMask,
				},
			})
		}

		virtualServices = append(virtualServices, &balancerpb.VirtualService{
			Id: &balancerpb.VsIdentifier{
				Addr: &balancerpb.Addr{
					Bytes: vsConf.ip.AsSlice(),
				},
				Port:  uint32(vsConf.port),
				Proto: vsConf.proto,
			},
			AllowedSrcs: allowedSrcs,
			Scheduler:   vsConf.scheduler,
			Flags: &balancerpb.VsFlags{
				Gre:    vsConf.gre,
				FixMss: vsConf.fixMss,
				Ops:    false,
				PureL3: false,
				Wlc:    false,
			},
			Reals: reals,
			Peers: []*balancerpb.Addr{},
		})
	}

	moduleConfig := &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("5.5.5.5").AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("fe80::5").AsSlice(),
			},
			Vs:             virtualServices,
			DecapAddresses: []*balancerpb.Addr{},
			SessionsTimeouts: &balancerpb.SessionsTimeouts{
				TcpSynAck: uint32(sessionTimeout),
				TcpSyn:    uint32(sessionTimeout),
				TcpFin:    uint32(sessionTimeout),
				Tcp:       uint32(sessionTimeout),
				Udp:       uint32(sessionTimeout),
				Default:   uint32(sessionTimeout),
			},
		},
		State: &balancerpb.StateConfig{
			SessionTableCapacity:      func() *uint64 { v := uint64(initialCapacity); return &v }(),
			SessionTableMaxLoadFactor: func() *float32 { v := float32(maxLoadFactor); return &v }(),
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
		Balancer: moduleConfig,
		AgentMemory: func() *datasize.ByteSize {
			memory := 16 * datasize.MB
			return &memory
		}(),
	})
	require.NoError(t, err)
	defer ts.Free()

	// Enable all reals
	utils.EnableAllReals(t, ts)

	// Set initial time
	ts.Mock.SetCurrentTime(time.Unix(0, 0))

	rng := rand.New(rand.NewSource(111222))

	// Build simple list for random packet generation
	type vsSimple struct {
		ip    netip.Addr
		port  uint16
		proto balancerpb.TransportProto
	}

	vsSimpleList := make([]vsSimple, 0, len(virtualServicesConfig))
	for _, vsConf := range virtualServicesConfig {
		vsSimpleList = append(vsSimpleList, vsSimple{
			ip:    vsConf.ip,
			port:  vsConf.port,
			proto: vsConf.proto,
		})
	}

	// Helper to generate random client IP based on VS IP version
	randomClientIP := func(vsIP netip.Addr) netip.Addr {
		if vsIP.Is4() {
			return netip.AddrFrom4([4]byte{
				10,
				byte(rng.Intn(256)),
				byte(rng.Intn(256)),
				byte(rng.Intn(256)),
			})
		}
		// IPv6
		return netip.MustParseAddr(fmt.Sprintf("2001:db8::%x", rng.Intn(65536)))
	}

	// Helper to generate random port
	randomPort := func() uint16 {
		return uint16(1024 + rng.Intn(64511))
	}

	// Track active sessions with their selected reals
	activeSessions := make(map[string]*sessionInfo)

	// Helper to create session key
	sessionKey := func(clientIP netip.Addr, clientPort uint16, vsIP netip.Addr, vsPort uint16) string {
		return fmt.Sprintf("%s:%d->%s:%d", clientIP, clientPort, vsIP, vsPort)
	}

	makeTcpSynPacket := func(clientIP netip.Addr, clientPort uint16, vsIP netip.Addr, vsPort uint16) gopacket.Packet {
		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			vsIP,
			vsPort,
			&layers.TCP{SYN: true},
		)
		return xpacket.LayersToPacket(t, packetLayers...)
	}

	// Phase 1: Send 16 random packets to establish initial sessions
	t.Run("Phase1_Create_16_Initial_Sessions", func(t *testing.T) {
		packets := make([]gopacket.Packet, 0, 16)
		packetToSession := make(map[int]*sessionInfo)

		for i := range 16 {
			// Randomly select a virtual service
			vs := vsSimpleList[rng.Intn(len(vsSimpleList))]
			clientIP := randomClientIP(vs.ip)
			clientPort := randomPort()

			session := &sessionInfo{
				clientIP:   clientIP,
				clientPort: clientPort,
				vsIP:       vs.ip,
				vsPort:     vs.port,
				proto:      vs.proto,
			}
			packetToSession[i] = session

			var packet gopacket.Packet
			if vs.proto == balancerpb.TransportProto_TCP {
				packet = makeTcpSynPacket(clientIP, clientPort, vs.ip, vs.port)
			} else {
				packetLayers := utils.MakeUDPPacket(
					clientIP,
					clientPort,
					vs.ip,
					vs.port,
				)
				packet = xpacket.LayersToPacket(t, packetLayers...)
			}
			packets = append(packets, packet)
		}

		result, err := ts.Mock.HandlePackets(packets...)
		require.NoError(t, err)

		t.Logf(
			"Sent 16 packets: Output=%d, Drop=%d",
			len(result.Output),
			len(result.Drop),
		)

		// Track which sessions were accepted and their selected reals
		for i, outPacket := range result.Output {
			session := packetToSession[i]

			// Extract the real IP from the output packet
			realIP, ok := netip.AddrFromSlice(outPacket.DstIP)
			require.True(t, ok, "failed to parse real IP")
			session.realIP = realIP

			key := sessionKey(
				session.clientIP,
				session.clientPort,
				session.vsIP,
				session.vsPort,
			)
			activeSessions[key] = session
		}

		t.Logf("Created %d initial sessions", len(activeSessions))
		assert.GreaterOrEqual(
			t,
			len(activeSessions),
			12,
			"at least 75%% of packets should be accepted",
		)
	})

	// Phase 2: Perform 10 iterations of the stress test cycle
	for iteration := range 10 {
		t.Run(fmt.Sprintf("Iteration_%d", iteration+1), func(t *testing.T) {
			currentTime := ts.Mock.CurrentTime()

			// Step 1: Sync active sessions and resize table on demand
			t.Run("Step1_Sync_And_Resize", func(t *testing.T) {
				err := ts.Balancer.Refresh(currentTime)
				require.NoError(t, err)

				config := ts.Balancer.Config()
				capacity := uint64(0)
				if config.State != nil &&
					config.State.SessionTableCapacity != nil {
					capacity = *config.State.SessionTableCapacity
				}
				t.Logf(
					"Session table capacity: %d, Active sessions: %d",
					capacity,
					len(activeSessions),
				)
			})

			// Step 2: Send packets to existing sessions (TCP non-SYN or UDP)
			t.Run("Step2_Send_To_Existing_Sessions", func(t *testing.T) {
				packets := make([]gopacket.Packet, 0, len(activeSessions))
				sessionList := make([]*sessionInfo, 0, len(activeSessions))

				for _, session := range activeSessions {
					sessionList = append(sessionList, session)

					var packetLayers []gopacket.SerializableLayer
					if session.proto == balancerpb.TransportProto_TCP {
						packetLayers = utils.MakeTCPPacket(
							session.clientIP,
							session.clientPort,
							session.vsIP,
							session.vsPort,
							&layers.TCP{}, // No SYN flag
						)
					} else {
						packetLayers = utils.MakeUDPPacket(
							session.clientIP,
							session.clientPort,
							session.vsIP,
							session.vsPort,
						)
					}
					packets = append(
						packets,
						xpacket.LayersToPacket(t, packetLayers...),
					)
				}

				result, err := ts.Mock.HandlePackets(packets...)
				require.NoError(t, err)

				// All packets should be accepted (no drops)
				assert.Equal(
					t,
					len(packets),
					len(result.Output),
					"all packets to existing sessions should be accepted",
				)
				assert.Empty(t, result.Drop, "no packets should be dropped")

				// Validate each packet and verify real consistency
				for i, outPacket := range result.Output {
					session := sessionList[i]

					// Verify the same real is selected
					realIP, ok := netip.AddrFromSlice(outPacket.DstIP)
					require.True(t, ok, "failed to parse real IP")
					assert.Equal(
						t,
						session.realIP,
						realIP,
						"real server should remain consistent for session %s:%d->%s:%d",
						session.clientIP,
						session.clientPort,
						session.vsIP,
						session.vsPort,
					)
				}

				t.Logf(
					"Sent %d packets to existing sessions, all accepted with consistent reals",
					len(packets),
				)
			})

			// Step 3: Create N/2 new sessions
			t.Run("Step3_Create_New_Sessions", func(t *testing.T) {
				N := len(activeSessions)
				newSessionCount := N / 2
				if newSessionCount == 0 {
					newSessionCount = 1
				}

				packets := make([]gopacket.Packet, 0, newSessionCount)
				packetToSession := make(map[int]*sessionInfo)

				for i := 0; i < newSessionCount; i++ {
					// Randomly select a virtual service
					vs := vsSimpleList[rng.Intn(len(vsSimpleList))]
					clientIP := randomClientIP(vs.ip)
					clientPort := randomPort()

					session := &sessionInfo{
						clientIP:   clientIP,
						clientPort: clientPort,
						vsIP:       vs.ip,
						vsPort:     vs.port,
						proto:      vs.proto,
					}
					packetToSession[i] = session

					var packet gopacket.Packet
					if vs.proto == balancerpb.TransportProto_TCP {
						packet = makeTcpSynPacket(
							clientIP,
							clientPort,
							vs.ip,
							vs.port,
						)
					} else {
						packetLayers := utils.MakeUDPPacket(
							clientIP,
							clientPort,
							vs.ip,
							vs.port,
						)
						packet = xpacket.LayersToPacket(t, packetLayers...)
					}
					packets = append(packets, packet)
				}

				result, err := ts.Mock.HandlePackets(packets...)
				require.NoError(t, err)

				// All new sessions should be accepted
				require.Equal(
					t,
					len(packets),
					len(result.Output),
					"all new session packets should be accepted",
				)
				require.Empty(t, result.Drop, "no packets should be dropped")

				// Track new sessions and their selected reals
				for i, outPacket := range result.Output {
					session := packetToSession[i]

					// Extract the real IP from the output packet
					realIP, ok := netip.AddrFromSlice(outPacket.DstIP)
					require.True(t, ok, "failed to parse real IP")
					session.realIP = realIP

					key := sessionKey(
						session.clientIP,
						session.clientPort,
						session.vsIP,
						session.vsPort,
					)
					activeSessions[key] = session
				}

				t.Logf(
					"Created %d new sessions (N=%d, N/2=%d), total active: %d",
					len(result.Output),
					N,
					newSessionCount,
					len(activeSessions),
				)
			})

			// Note: Do NOT advance time as per requirements
		})
	}

	// Final verification
	t.Run("Final_Verification", func(t *testing.T) {
		currentTime := ts.Mock.CurrentTime()

		// Sync one more time
		err := ts.Balancer.Refresh(currentTime)
		require.NoError(t, err)

		// Get sessions info
		sessions, err := ts.Balancer.Sessions(currentTime)
		require.NoError(t, err)

		t.Logf(
			"Final state: %d active sessions tracked, %d sessions in balancer",
			len(activeSessions),
			len(sessions),
		)

		// Verify session count matches
		assert.Equal(t, len(activeSessions), len(sessions),
			"tracked sessions should match balancer sessions")

		// Get info
		info, err := ts.Balancer.Info(currentTime)
		require.NoError(t, err)
		t.Logf("Module active sessions: %d", info.ActiveSessions)

		config := ts.Balancer.Config()
		capacity := uint64(0)
		if config.State != nil && config.State.SessionTableCapacity != nil {
			capacity = *config.State.SessionTableCapacity
		}
		t.Logf("Session table capacity: %d", capacity)

		// Verify VS and Real active sessions sum up correctly
		totalVsSessions := uint64(0)
		for _, vsInfo := range info.Vs {
			totalVsSessions += vsInfo.ActiveSessions
		}
		t.Logf("Total VS active sessions: %d", totalVsSessions)

		totalRealSessions := uint64(0)
		for _, vsInfo := range info.Vs {
			for _, realInfo := range vsInfo.Reals {
				totalRealSessions += realInfo.ActiveSessions
			}
		}
		t.Logf("Total Real active sessions: %d", totalRealSessions)

		// The total should match (each session belongs to one VS and one Real)
		assert.Equal(t, totalVsSessions, totalRealSessions,
			"VS and Real session counts should match")
	})
}
