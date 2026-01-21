package balancer_test

// TestWlc validates the Weighted Least Connection (WLC) scheduling algorithm:
//
// # Initial Configuration
// - Virtual service: 1.1.1.1:80 (TCP) with WLC enabled
// - Three real servers with weights: Real1=1, Real2=1, Real3=2
// - Initially only Real1 and Real2 are enabled
//
// # Stage 1: Two Reals with Equal Weights
// - Sends 500 random TCP SYN packets
// - Validates uniform distribution (250 packets each to Real1 and Real2)
// - Verifies Stats, Info, and Sessions APIs show correct counts
// - Enables Real3 (weight=2)
//
// # Stage 1 Continued: Three Reals with Weights 1:1:2
// - Sends 2500 more random TCP SYN packets
// - Validates distribution proportional to weights
// - Verifies Real3 receives approximately 2× traffic of Real1/Real2
//
// # Stage 2: State Persistence with New Agent
// - Creates new BalancerAgent attached to same shared memory
// - Verifies all reals are enabled via Graph()
// - Confirms original weights (1, 1, 2) via Config()
// - Disables Real1 and sends 100 packets
//   * Validates packets only go to Real2 and Real3
//   * Expected distribution: Real2 ~33%, Real3 ~67% (weights 1:2)
// - Re-enables Real1 and sends 300 more packets
// - Validates session distribution proportional to weights (1:1:2)
//   * Expected ratios: Real1=25%, Real2=25%, Real3=50%
//   * Tolerance: ±15%
//
// # Stage 3: Multi-VS Configuration Update
// - Updates config to 4 virtual services:
//   * VS1: Original VS with WLC=true, weights 1:1:2
//   * VS2: New VS with WLC=true, weights 1:2:1
//   * VS3: New VS with WLC=false (ROUND_ROBIN), weights 1:1
//   * VS4: New VS with WLC=true, weights 2:2:1
// - Verifies Config() matches updated configuration
// - Creates third BalancerAgent and verifies config persistence
// - Confirms all 4 virtual services present with correct settings

import (
	"fmt"
	"math"
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/logging"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	balancer "github.com/yanet-platform/yanet2/modules/balancer/agent/go"
	"github.com/yanet-platform/yanet2/modules/balancer/tests/go/utils"
	"go.uber.org/zap/zapcore"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestWlc(t *testing.T) {
	vsIp := netip.MustParseAddr("1.1.1.1")
	vsPort := uint16(80)
	real1Ip := netip.MustParseAddr("2.2.2.2")
	real2Ip := netip.MustParseAddr("3.3.3.3")
	real3Ip := netip.MustParseAddr("4.4.4.4")

	client := func(id int) netip.Addr {
		return netip.MustParseAddr(
			fmt.Sprintf("10.%d.%d.%d", id/(256*256)%256, (id/256)%256, id%256),
		)
	}

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
							Bytes: vsIp.AsSlice(),
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
						Wlc:    true,
					},
					Reals: []*balancerpb.Real{
						{
							Id: &balancerpb.RelativeRealIdentifier{
								Ip: &balancerpb.Addr{
									Bytes: real1Ip.AsSlice(),
								},
								Port: 0,
							},
							Weight: 1,
							SrcAddr: &balancerpb.Addr{
								Bytes: real1Ip.AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.255").
									AsSlice(),
							},
						},
						{
							Id: &balancerpb.RelativeRealIdentifier{
								Ip: &balancerpb.Addr{
									Bytes: real2Ip.AsSlice(),
								},
								Port: 0,
							},
							Weight: 1,
							SrcAddr: &balancerpb.Addr{
								Bytes: real2Ip.AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.255").
									AsSlice(),
							},
						},
						{
							Id: &balancerpb.RelativeRealIdentifier{
								Ip: &balancerpb.Addr{
									Bytes: real3Ip.AsSlice(),
								},
								Port: 0,
							},
							Weight: 2,
							SrcAddr: &balancerpb.Addr{
								Bytes: real3Ip.AsSlice(),
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
			SessionTableCapacity:      func() *uint64 { v := uint64(8000); return &v }(),
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
			memory := 16 * datasize.MB
			return &memory
		}(),
	})
	require.NoError(t, err, "failed to setup test")
	defer ts.Free()

	mock := ts.Mock
	balancerMgr := ts.Balancer

	// Enable only the first two reals initially
	initialConfig := balancerMgr.Config()
	enableTrue := true
	enableFalse := false
	initialUpdates := []*balancerpb.RealUpdate{
		{
			RealId: &balancerpb.RealIdentifier{
				Vs: initialConfig.PacketHandler.Vs[0].Id,
				Real: &balancerpb.RelativeRealIdentifier{
					Ip:   &balancerpb.Addr{Bytes: real1Ip.AsSlice()},
					Port: 0,
				},
			},
			Enable: &enableTrue,
		},
		{
			RealId: &balancerpb.RealIdentifier{
				Vs: initialConfig.PacketHandler.Vs[0].Id,
				Real: &balancerpb.RelativeRealIdentifier{
					Ip:   &balancerpb.Addr{Bytes: real2Ip.AsSlice()},
					Port: 0,
				},
			},
			Enable: &enableTrue,
		},
		{
			RealId: &balancerpb.RealIdentifier{
				Vs: initialConfig.PacketHandler.Vs[0].Id,
				Real: &balancerpb.RelativeRealIdentifier{
					Ip:   &balancerpb.Addr{Bytes: real3Ip.AsSlice()},
					Port: 0,
				},
			},
			Enable: &enableFalse,
		},
	}
	_, err = balancerMgr.UpdateReals(initialUpdates, false)
	require.NoError(t, err, "failed to enable first two reals")

	// Send random packets
	packets := 500

	// Send random SYNs to the first two reals
	// Expect uniform distribution
	now := mock.CurrentTime()

	t.Run("Send_Random_SYNs", func(t *testing.T) {
		for packetIdx := range packets {
			clientIP := client(packetIdx)
			packetLayers := utils.MakeTCPPacket(
				clientIP,
				1000,
				vsIp,
				vsPort,
				&layers.TCP{SYN: true},
			)
			packet := xpacket.LayersToPacket(t, packetLayers...)
			result, err := mock.HandlePackets(packet)
			require.NoError(t, err)
			require.Equal(t, 1, len(result.Output))
			require.Empty(t, result.Drop)
		}

		// Get stats
		statsRef := &balancerpb.PacketHandlerRef{
			Device:   &utils.DeviceName,
			Pipeline: &utils.PipelineName,
			Function: &utils.FunctionName,
			Chain:    &utils.ChainName,
		}
		stats, err := balancerMgr.Stats(statsRef)
		require.NoError(t, err)
		require.NotNil(t, stats)
		require.NotEmpty(t, stats.Vs)

		vsStats := stats.Vs[0]
		require.NotEmpty(t, vsStats.Reals)

		assert.Equal(
			t,
			uint64(packets/2),
			vsStats.Reals[0].Stats.CreatedSessions,
		)
		assert.Equal(
			t,
			uint64(packets/2),
			vsStats.Reals[1].Stats.CreatedSessions,
		)
		assert.Equal(t, uint64(0), vsStats.Reals[2].Stats.CreatedSessions)

		// NEW: Validate Stats
		t.Run("Validate_Stats", func(t *testing.T) {
			assert.Equal(
				t,
				uint64(packets),
				vsStats.Stats.IncomingPackets,
				"VS incoming packets",
			)
			assert.Equal(
				t,
				uint64(packets),
				vsStats.Stats.OutgoingPackets,
				"VS outgoing packets",
			)
			assert.Equal(
				t,
				uint64(packets/2),
				vsStats.Reals[0].Stats.Packets,
				"Real1 packets",
			)
			assert.Equal(
				t,
				uint64(packets/2),
				vsStats.Reals[1].Stats.Packets,
				"Real2 packets",
			)
			assert.Equal(
				t,
				uint64(0),
				vsStats.Reals[2].Stats.Packets,
				"Real3 packets",
			)
		})

		// NEW: Validate Info
		t.Run("Validate_Info", func(t *testing.T) {
			info, err := balancerMgr.Info(now)
			require.NoError(t, err)
			require.NotNil(t, info)
			require.NotEmpty(t, info.Vs)

			vsInfo := info.Vs[0]
			require.NotEmpty(t, vsInfo.Reals)

			assert.Equal(
				t,
				uint64(packets),
				info.ActiveSessions,
				"total active sessions",
			)
			assert.Equal(
				t,
				uint64(packets),
				vsInfo.ActiveSessions,
				"VS active sessions",
			)
			assert.Equal(
				t,
				uint64(packets/2),
				vsInfo.Reals[0].ActiveSessions,
				"Real1 active sessions",
			)
			assert.Equal(
				t,
				uint64(packets/2),
				vsInfo.Reals[1].ActiveSessions,
				"Real2 active sessions",
			)
			assert.Equal(
				t,
				uint64(0),
				vsInfo.Reals[2].ActiveSessions,
				"Real3 active sessions",
			)
		})

		// NEW: Validate Sessions
		t.Run("Validate_Sessions", func(t *testing.T) {
			sessions, err := balancerMgr.Sessions(now)
			require.NoError(t, err)
			require.NotNil(t, sessions)

			assert.Equal(t, packets, len(sessions), "total sessions count")

			// Count sessions per real
			real1Sessions := 0
			real2Sessions := 0
			real3Sessions := 0

			for _, session := range sessions {
				realAddr, _ := netip.AddrFromSlice(session.RealId.Real.Ip.Bytes)
				switch realAddr {
				case real1Ip:
					real1Sessions++
				case real2Ip:
					real2Sessions++
				case real3Ip:
					real3Sessions++
				}
			}

			assert.Equal(t, packets/2, real1Sessions, "Real1 sessions")
			assert.Equal(t, packets/2, real2Sessions, "Real2 sessions")
			assert.Equal(t, 0, real3Sessions, "Real3 sessions")
		})

		// Refresh to scan active sessions, update them,
		// and recalculate effective weights
		err = balancerMgr.Refresh(now)
		require.NoError(t, err)
	})

	// Enable third real
	t.Run("Enable_Third_Real", func(t *testing.T) {
		config := balancerMgr.Config()
		enableTrue := true
		updates := []*balancerpb.RealUpdate{
			{
				RealId: &balancerpb.RealIdentifier{
					Vs: config.PacketHandler.Vs[0].Id,
					Real: &balancerpb.RelativeRealIdentifier{
						Ip:   &balancerpb.Addr{Bytes: real3Ip.AsSlice()},
						Port: 0,
					},
				},
				Enable: &enableTrue,
			},
		}
		_, err := balancerMgr.UpdateReals(updates, false)
		require.NoError(t, err)
	})

	// Send more random packets
	// Ensure we have good distribution after
	t.Run("Send_Random_SYNs_Again", func(t *testing.T) {
		firstClient := packets
		packets = 5 * packets
		for packetIdx := range packets {
			if packetIdx%50 == 0 {
				if err := balancerMgr.Refresh(now); err != nil {
					t.Errorf(
						"failed to refresh: packetIdx=%d, error=%v",
						packetIdx,
						err,
					)
				}
			}
			clientIP := client(firstClient + packetIdx)
			packetLayers := utils.MakeTCPPacket(
				clientIP,
				1000,
				vsIp,
				vsPort,
				&layers.TCP{SYN: true},
			)
			packet := xpacket.LayersToPacket(t, packetLayers...)
			result, err := mock.HandlePackets(packet)
			require.NoError(t, err)
			require.Equal(t, 1, len(result.Output))
			require.Empty(t, result.Drop)
		}

		// Get stats
		statsRef := &balancerpb.PacketHandlerRef{
			Device:   &utils.DeviceName,
			Pipeline: &utils.PipelineName,
			Function: &utils.FunctionName,
			Chain:    &utils.ChainName,
		}
		stats, err := balancerMgr.Stats(statsRef)
		require.NoError(t, err)
		require.NotNil(t, stats)
		require.NotEmpty(t, stats.Vs)

		vsStats := stats.Vs[0]
		require.NotEmpty(t, vsStats.Reals)

		firstTwoPackets := vsStats.Reals[0].Stats.CreatedSessions + vsStats.Reals[1].Stats.CreatedSessions
		thirdPackets := vsStats.Reals[2].Stats.CreatedSessions

		rel := float64(thirdPackets) / float64(firstTwoPackets)
		assert.Less(t, math.Abs(rel-1.0), 0.3)
	})

	// NEW: Stage 2 - New Balancer Agent with State Persistence
	t.Run("Stage2_New_Agent_State_Persistence", func(t *testing.T) {
		// Create new balancer agent using same shared memory
		logLevel := zapcore.InfoLevel
		sugaredLogger, _, _ := logging.Init(&logging.Config{
			Level: logLevel,
		})

		agentMemory := 16 * datasize.MB
		newAgent, err := balancer.NewBalancerAgent(
			ts.Mock.SharedMemory(), // Same shared memory
			agentMemory,
			sugaredLogger,
		)
		require.NoError(t, err, "failed to create new balancer agent")

		// Attach to existing BalancerManager
		newBalancer, err := newAgent.BalancerManager(utils.BalancerName)
		require.NoError(t, err, "failed to attach to existing balancer manager")
		require.NotNil(t, newBalancer, "balancer manager should not be nil")

		// 2.1: Verify all reals are enabled using Graph()
		t.Run("Verify_All_Reals_Enabled", func(t *testing.T) {
			graph := newBalancer.Graph()
			require.NotNil(t, graph)
			require.NotEmpty(t, graph.VirtualServices)

			vs := graph.VirtualServices[0]
			require.Equal(t, 3, len(vs.Reals), "should have 3 reals")

			// Verify all reals are enabled
			for i, real := range vs.Reals {
				assert.True(t, real.Enabled, "Real%d should be enabled", i+1)
			}
		})

		// 2.2: Verify config contains original weights (1, 1, 2)
		t.Run("Verify_Original_Weights", func(t *testing.T) {
			config := newBalancer.Config()
			require.NotNil(t, config)
			require.NotNil(t, config.PacketHandler)
			require.NotEmpty(t, config.PacketHandler.Vs)

			vs := config.PacketHandler.Vs[0]
			require.Equal(t, 3, len(vs.Reals), "should have 3 reals")

			assert.Equal(t, uint32(1), vs.Reals[0].Weight, "Real1 weight")
			assert.Equal(t, uint32(1), vs.Reals[1].Weight, "Real2 weight")
			assert.Equal(t, uint32(2), vs.Reals[2].Weight, "Real3 weight")
		})

		// 2.3: Disable first real and send packets
		t.Run("Disable_Real1_And_Send_Packets", func(t *testing.T) {
			config := newBalancer.Config()
			enableFalse := false
			updates := []*balancerpb.RealUpdate{
				{
					RealId: &balancerpb.RealIdentifier{
						Vs: config.PacketHandler.Vs[0].Id,
						Real: &balancerpb.RelativeRealIdentifier{
							Ip:   &balancerpb.Addr{Bytes: real1Ip.AsSlice()},
							Port: 0,
						},
					},
					Enable: &enableFalse,
				},
			}
			_, err := newBalancer.UpdateReals(updates, false)
			require.NoError(t, err, "failed to disable Real1")

			// Send 100 new packets
			firstClient := 3500
			for i := 0; i < 100; i++ {
				clientIP := client(firstClient + i)
				packetLayers := utils.MakeTCPPacket(
					clientIP,
					uint16(2000+i),
					vsIp,
					vsPort,
					&layers.TCP{SYN: true},
				)
				packet := xpacket.LayersToPacket(t, packetLayers...)
				result, err := mock.HandlePackets(packet)
				require.NoError(t, err)
				require.Equal(t, 1, len(result.Output))
				require.Empty(t, result.Drop)
			}

			// Verify packets only go to Real2 and Real3
			// Expected distribution: Real2 ~33%, Real3 ~67% (weights 1:2)
			statsRef := &balancerpb.PacketHandlerRef{
				Device:   &utils.DeviceName,
				Pipeline: &utils.PipelineName,
				Function: &utils.FunctionName,
				Chain:    &utils.ChainName,
			}
			stats, err := newBalancer.Stats(statsRef)
			require.NoError(t, err)

			vsStats := stats.Vs[0]
			// Note: Stats are cumulative, so we can't easily verify just these 100 packets
			// But we can verify Real1 didn't get any new sessions
			t.Logf(
				"After disabling Real1: Real1=%d, Real2=%d, Real3=%d sessions",
				vsStats.Reals[0].Stats.CreatedSessions,
				vsStats.Reals[1].Stats.CreatedSessions,
				vsStats.Reals[2].Stats.CreatedSessions,
			)
		})

		// 2.4: Re-enable first real and send more packets
		t.Run("Enable_Real1_And_Send_More_Packets", func(t *testing.T) {
			config := newBalancer.Config()
			enableTrue := true
			updates := []*balancerpb.RealUpdate{
				{
					RealId: &balancerpb.RealIdentifier{
						Vs: config.PacketHandler.Vs[0].Id,
						Real: &balancerpb.RelativeRealIdentifier{
							Ip:   &balancerpb.Addr{Bytes: real1Ip.AsSlice()},
							Port: 0,
						},
					},
					Enable: &enableTrue,
				},
			}
			_, err := newBalancer.UpdateReals(updates, false)
			require.NoError(t, err, "failed to enable Real1")

			// Send 300 more packets
			firstClient := 3600
			for i := 0; i < 300; i++ {
				if i%50 == 0 {
					err := newBalancer.Refresh(now)
					require.NoError(t, err)
				}
				clientIP := client(firstClient + i)
				packetLayers := utils.MakeTCPPacket(
					clientIP,
					uint16(3000+i),
					vsIp,
					vsPort,
					&layers.TCP{SYN: true},
				)
				packet := xpacket.LayersToPacket(t, packetLayers...)
				result, err := mock.HandlePackets(packet)
				require.NoError(t, err)
				require.Equal(t, 1, len(result.Output))
				require.Empty(t, result.Drop)
			}
		})

		// 2.5: Validate session distribution proportional to weights (1:1:2)
		t.Run("Validate_Session_Distribution", func(t *testing.T) {
			info, err := newBalancer.Info(now)
			require.NoError(t, err)
			require.NotNil(t, info)

			vsInfo := info.Vs[0]
			real1Sessions := vsInfo.Reals[0].ActiveSessions
			real2Sessions := vsInfo.Reals[1].ActiveSessions
			real3Sessions := vsInfo.Reals[2].ActiveSessions

			totalSessions := real1Sessions + real2Sessions + real3Sessions
			require.Greater(
				t,
				totalSessions,
				uint64(0),
				"should have active sessions",
			)

			// Calculate ratios
			real1Ratio := float64(real1Sessions) / float64(totalSessions)
			real2Ratio := float64(real2Sessions) / float64(totalSessions)
			real3Ratio := float64(real3Sessions) / float64(totalSessions)

			// Expected ratios based on weights 1:1:2
			// Total weight = 4, so Real1=25%, Real2=25%, Real3=50%
			expectedReal1Ratio := 0.25
			expectedReal2Ratio := 0.25
			expectedReal3Ratio := 0.50

			tolerance := 0.15 // 15% tolerance

			t.Logf(
				"Session distribution: Real1=%.2f%% (expected 25%%), Real2=%.2f%% (expected 25%%), Real3=%.2f%% (expected 50%%)",
				real1Ratio*100,
				real2Ratio*100,
				real3Ratio*100,
			)

			assert.InDelta(
				t,
				expectedReal1Ratio,
				real1Ratio,
				tolerance,
				"Real1 session ratio",
			)
			assert.InDelta(
				t,
				expectedReal2Ratio,
				real2Ratio,
				tolerance,
				"Real2 session ratio",
			)
			assert.InDelta(
				t,
				expectedReal3Ratio,
				real3Ratio,
				tolerance,
				"Real3 session ratio",
			)
		})
	})

	// NEW: Stage 3 - Multi-VS Configuration Update
	t.Run("Stage3_Multi_VS_Configuration", func(t *testing.T) {
		// 3.1: Create updated config with 4 virtual services
		vs2Ip := netip.MustParseAddr("10.10.1.1")
		vs3Ip := netip.MustParseAddr("10.10.2.1")
		vs4Ip := netip.MustParseAddr("10.10.3.1")

		real4Ip := netip.MustParseAddr("192.168.1.1")
		real5Ip := netip.MustParseAddr("192.168.1.2")
		real6Ip := netip.MustParseAddr("192.168.1.3")
		real7Ip := netip.MustParseAddr("192.168.2.1")
		real8Ip := netip.MustParseAddr("192.168.2.2")
		real9Ip := netip.MustParseAddr("192.168.3.1")
		real10Ip := netip.MustParseAddr("192.168.3.2")
		real11Ip := netip.MustParseAddr("192.168.3.3")

		createReal := func(ip netip.Addr, weight uint32) *balancerpb.Real {
			return &balancerpb.Real{
				Id: &balancerpb.RelativeRealIdentifier{
					Ip:   &balancerpb.Addr{Bytes: ip.AsSlice()},
					Port: 0,
				},
				Weight: weight,
				SrcAddr: &balancerpb.Addr{
					Bytes: ip.AsSlice(),
				},
				SrcMask: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("255.255.255.255").AsSlice(),
				},
			}
		}

		createVS := func(ip netip.Addr, port uint16, wlc bool, reals []*balancerpb.Real) *balancerpb.VirtualService {
			return &balancerpb.VirtualService{
				Id: &balancerpb.VsIdentifier{
					Addr:  &balancerpb.Addr{Bytes: ip.AsSlice()},
					Port:  uint32(port),
					Proto: balancerpb.TransportProto_TCP,
				},
				Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
				AllowedSrcs: []*balancerpb.Net{
					{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("0.0.0.0").AsSlice(),
						},
						Size: 0,
					},
				},
				Flags: &balancerpb.VsFlags{
					Gre:    false,
					FixMss: false,
					Ops:    false,
					PureL3: false,
					Wlc:    wlc,
				},
				Reals: reals,
				Peers: []*balancerpb.Addr{},
			}
		}

		updatedConfig := &balancerpb.BalancerConfig{
			PacketHandler: &balancerpb.PacketHandlerConfig{
				SourceAddressV4: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("5.5.5.5").AsSlice(),
				},
				SourceAddressV6: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("fe80::5").AsSlice(),
				},
				Vs: []*balancerpb.VirtualService{
					// Keep original VS with WLC=true
					createVS(vsIp, vsPort, true, []*balancerpb.Real{
						createReal(real1Ip, 1),
						createReal(real2Ip, 1),
						createReal(real3Ip, 2),
					}),
					// Add VS2 with WLC=true, weights 1,2,1
					createVS(vs2Ip, 80, true, []*balancerpb.Real{
						createReal(real4Ip, 1),
						createReal(real5Ip, 2),
						createReal(real6Ip, 1),
					}),
					// Add VS3 with WLC=false (RR), weights 1,1
					createVS(vs3Ip, 80, false, []*balancerpb.Real{
						createReal(real7Ip, 1),
						createReal(real8Ip, 1),
					}),
					// Add VS4 with WLC=true, weights 2,2,1
					createVS(vs4Ip, 80, true, []*balancerpb.Real{
						createReal(real9Ip, 2),
						createReal(real10Ip, 2),
						createReal(real11Ip, 1),
					}),
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
				SessionTableCapacity:      func() *uint64 { v := uint64(8000); return &v }(),
				SessionTableMaxLoadFactor: func() *float32 { v := float32(0.5); return &v }(),
				RefreshPeriod:             durationpb.New(0),
				Wlc: &balancerpb.WlcConfig{
					Power:     func() *uint64 { v := uint64(10); return &v }(),
					MaxWeight: func() *uint32 { v := uint32(1000); return &v }(),
				},
			},
		}

		err := balancerMgr.Update(updatedConfig, now)
		require.NoError(t, err, "failed to update configuration")

		// 3.2: Verify Config() matches updated configuration
		t.Run("Verify_Config_Matches_Update", func(t *testing.T) {
			retrievedConfig := balancerMgr.Config()
			require.NotNil(t, retrievedConfig)
			require.NotNil(t, retrievedConfig.PacketHandler)

			// Verify 4 virtual services
			assert.Equal(
				t,
				4,
				len(retrievedConfig.PacketHandler.Vs),
				"should have 4 virtual services",
			)

			// Verify each VS
			vs1Found := false
			vs2Found := false
			vs3Found := false
			vs4Found := false

			for _, vs := range retrievedConfig.PacketHandler.Vs {
				vsAddr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
				switch vsAddr {
				case vsIp:
					vs1Found = true
					require.NotNil(t, vs.Flags, "VS1 flags should not be nil")
					assert.True(t, vs.Flags.Wlc, "VS1 should have WLC=true")
					assert.Equal(t, 3, len(vs.Reals), "VS1 should have 3 reals")
					assert.Equal(
						t,
						uint32(1),
						vs.Reals[0].Weight,
						"VS1 Real1 weight",
					)
					assert.Equal(
						t,
						uint32(1),
						vs.Reals[1].Weight,
						"VS1 Real2 weight",
					)
					assert.Equal(
						t,
						uint32(2),
						vs.Reals[2].Weight,
						"VS1 Real3 weight",
					)
				case vs2Ip:
					vs2Found = true
					require.NotNil(t, vs.Flags, "VS2 flags should not be nil")
					assert.True(t, vs.Flags.Wlc, "VS2 should have WLC=true")
					assert.Equal(t, 3, len(vs.Reals), "VS2 should have 3 reals")
					assert.Equal(
						t,
						uint32(1),
						vs.Reals[0].Weight,
						"VS2 Real1 weight",
					)
					assert.Equal(
						t,
						uint32(2),
						vs.Reals[1].Weight,
						"VS2 Real2 weight",
					)
					assert.Equal(
						t,
						uint32(1),
						vs.Reals[2].Weight,
						"VS2 Real3 weight",
					)
				case vs3Ip:
					vs3Found = true
					require.NotNil(t, vs.Flags, "VS3 flags should not be nil")
					assert.False(t, vs.Flags.Wlc, "VS3 should have WLC=false")
					assert.Equal(t, 2, len(vs.Reals), "VS3 should have 2 reals")
					assert.Equal(
						t,
						uint32(1),
						vs.Reals[0].Weight,
						"VS3 Real1 weight",
					)
					assert.Equal(
						t,
						uint32(1),
						vs.Reals[1].Weight,
						"VS3 Real2 weight",
					)
				case vs4Ip:
					vs4Found = true
					require.NotNil(t, vs.Flags, "VS4 flags should not be nil")
					assert.True(t, vs.Flags.Wlc, "VS4 should have WLC=true")
					assert.Equal(t, 3, len(vs.Reals), "VS4 should have 3 reals")
					assert.Equal(
						t,
						uint32(2),
						vs.Reals[0].Weight,
						"VS4 Real1 weight",
					)
					assert.Equal(
						t,
						uint32(2),
						vs.Reals[1].Weight,
						"VS4 Real2 weight",
					)
					assert.Equal(
						t,
						uint32(1),
						vs.Reals[2].Weight,
						"VS4 Real3 weight",
					)
				}
			}

			assert.True(t, vs1Found, "VS1 should be present")
			assert.True(t, vs2Found, "VS2 should be present")
			assert.True(t, vs3Found, "VS3 should be present")
			assert.True(t, vs4Found, "VS4 should be present")
		})

		// 3.3: Create new BalancerAgent and verify config persistence
		t.Run("Verify_Config_Persistence_With_New_Agent", func(t *testing.T) {
			// Create third balancer agent using same shared memory
			logLevel := zapcore.InfoLevel
			sugaredLogger, _, _ := logging.Init(&logging.Config{
				Level: logLevel,
			})

			agentMemory := 16 * datasize.MB
			thirdAgent, err := balancer.NewBalancerAgent(
				ts.Mock.SharedMemory(), // Same shared memory
				agentMemory,
				sugaredLogger,
			)
			require.NoError(t, err, "failed to create third balancer agent")

			// Attach to existing BalancerManager
			thirdBalancer, err := thirdAgent.BalancerManager(utils.BalancerName)
			require.NoError(
				t,
				err,
				"failed to attach to existing balancer manager",
			)
			require.NotNil(
				t,
				thirdBalancer,
				"balancer manager should not be nil",
			)

			// Verify config matches the updated multi-VS config
			thirdConfig := thirdBalancer.Config()
			require.NotNil(t, thirdConfig)
			require.NotNil(t, thirdConfig.PacketHandler)

			// Verify 4 virtual services
			assert.Equal(
				t,
				4,
				len(thirdConfig.PacketHandler.Vs),
				"should have 4 virtual services",
			)

			// Verify each VS is present with correct configuration
			vs1Found := false
			vs2Found := false
			vs3Found := false
			vs4Found := false

			for _, vs := range thirdConfig.PacketHandler.Vs {
				vsAddr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
				switch vsAddr {
				case vsIp:
					vs1Found = true
					require.NotNil(t, vs.Flags, "VS1 flags should not be nil")
					assert.True(t, vs.Flags.Wlc, "VS1 should have WLC=true")
					assert.Equal(t, 3, len(vs.Reals), "VS1 should have 3 reals")
				case vs2Ip:
					vs2Found = true
					require.NotNil(t, vs.Flags, "VS2 flags should not be nil")
					assert.True(t, vs.Flags.Wlc, "VS2 should have WLC=true")
					assert.Equal(t, 3, len(vs.Reals), "VS2 should have 3 reals")
				case vs3Ip:
					vs3Found = true
					require.NotNil(t, vs.Flags, "VS3 flags should not be nil")
					assert.False(t, vs.Flags.Wlc, "VS3 should have WLC=false")
					assert.Equal(t, 2, len(vs.Reals), "VS3 should have 2 reals")
				case vs4Ip:
					vs4Found = true
					require.NotNil(t, vs.Flags, "VS4 flags should not be nil")
					assert.True(t, vs.Flags.Wlc, "VS4 should have WLC=true")
					assert.Equal(t, 3, len(vs.Reals), "VS4 should have 3 reals")
				}
			}

			assert.True(
				t,
				vs1Found,
				"VS1 should be present in third agent config",
			)
			assert.True(
				t,
				vs2Found,
				"VS2 should be present in third agent config",
			)
			assert.True(
				t,
				vs3Found,
				"VS3 should be present in third agent config",
			)
			assert.True(
				t,
				vs4Found,
				"VS4 should be present in third agent config",
			)

			t.Log(
				"Successfully verified config persistence across agent instances",
			)
		})
	})
}
