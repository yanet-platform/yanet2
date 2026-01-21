package balancer_test

// TestConfigUpdateAndStats is a comprehensive test that verifies:
//
// # Initial Configuration
// - 2 virtual services (VS1, VS2) with 3 reals each
// - Packet distribution across all reals
// - Session creation and persistence
//
// # Real Server State Management
// - Disabling reals and verifying traffic routing
// - Enabled/disabled state tracking in Graph
//
// # API Outputs (Initial State)
// - Config(): Configuration retrieval
// - Graph(): Topology with enabled/disabled states
// - Stats(): Exact packet counts per VS and real
// - Info(): Exact session counts per VS
// - Sessions(): Session details and distribution
//
// # Configuration Update
// - Removing VS1 completely
// - Modifying VS2 (keeping Real6, adding Real7, Real8)
// - Adding new VS3 with 3 new reals
// - Verifying Real6 remains enabled while new reals are disabled
//
// # API Outputs (After Update)
// - Config(): Only VS2 and VS3 present
// - Graph(): Correct topology, Real6 enabled, new reals initially disabled
// - Stats(): VS2 cumulative (not reset), VS3 new, NO VS1
// - Info(): Only VS2 and VS3, NO VS1
// - Sessions(): Only VS2 and VS3 sessions, NO VS1 or deleted reals
//
// # State Persistence with New Agent
// - Creating new BalancerAgent attached to same shared memory
// - Verifying existing BalancerManager is discovered and accessible
// - Config(), Graph(), Stats(), Info(), Sessions() match previous outputs
// - Sending new packets through new agent (10 to VS2, 10 to VS3)
// - Verifying Stats, Info, Sessions update correctly with new traffic

import (
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
	"github.com/yanet-platform/yanet2/tests/functional/framework"
	"go.uber.org/zap/zapcore"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Test addresses for config update test
var (
	// Virtual Services
	cfgVs1IP   = netip.MustParseAddr("10.10.1.1")
	cfgVs1Port = uint16(80)
	cfgVs2IP   = netip.MustParseAddr("10.10.2.1")
	cfgVs2Port = uint16(80)
	cfgVs3IP   = netip.MustParseAddr("10.10.3.1")
	cfgVs3Port = uint16(80)

	// Real servers for VS1
	cfgReal1IP = netip.MustParseAddr("192.168.11.1")
	cfgReal2IP = netip.MustParseAddr("192.168.11.2")
	cfgReal3IP = netip.MustParseAddr("192.168.11.3")

	// Real servers for VS2
	cfgReal4IP = netip.MustParseAddr("192.168.12.1")
	cfgReal5IP = netip.MustParseAddr("192.168.12.2")
	cfgReal6IP = netip.MustParseAddr("192.168.12.3")

	// New real servers for VS2 (after update)
	cfgReal7IP = netip.MustParseAddr("192.168.12.4")
	cfgReal8IP = netip.MustParseAddr("192.168.12.5")

	// Real servers for VS3 (new)
	cfgReal9IP  = netip.MustParseAddr("192.168.13.1")
	cfgReal10IP = netip.MustParseAddr("192.168.13.2")
	cfgReal11IP = netip.MustParseAddr("192.168.13.3")

	// Client base IP
	cfgClientBaseIP = netip.MustParseAddr("3.3.13.1")

	// Balancer source addresses
	cfgBalancerSrcV4 = netip.MustParseAddr("5.5.15.5")
	cfgBalancerSrcV6 = netip.MustParseAddr("fe80::15")
)

// createCfgReal creates a Real configuration
func createCfgReal(ip netip.Addr, weight uint32) *balancerpb.Real {
	return &balancerpb.Real{
		Id: &balancerpb.RelativeRealIdentifier{
			Ip:   &balancerpb.Addr{Bytes: ip.AsSlice()},
			Port: 0,
		},
		Weight: weight,
		SrcAddr: &balancerpb.Addr{
			Bytes: netip.MustParseAddr("4.4.14.4").AsSlice(),
		},
		SrcMask: &balancerpb.Addr{
			Bytes: netip.MustParseAddr("255.255.255.255").AsSlice(),
		},
	}
}

// createCfgVirtualService creates a VirtualService configuration
func createCfgVirtualService(
	ip netip.Addr,
	port uint16,
	reals []*balancerpb.Real,
) *balancerpb.VirtualService {
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
			Wlc:    false,
		},
		Reals: reals,
		Peers: []*balancerpb.Addr{},
	}
}

// createCfgInitialConfig creates the initial balancer configuration with VS1 and VS2
func createCfgInitialConfig() *balancerpb.BalancerConfig {
	vs1Reals := []*balancerpb.Real{
		createCfgReal(cfgReal1IP, 1),
		createCfgReal(cfgReal2IP, 1),
		createCfgReal(cfgReal3IP, 1),
	}

	vs2Reals := []*balancerpb.Real{
		createCfgReal(cfgReal4IP, 1),
		createCfgReal(cfgReal5IP, 1),
		createCfgReal(cfgReal6IP, 1),
	}

	return &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: &balancerpb.Addr{
				Bytes: cfgBalancerSrcV4.AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: cfgBalancerSrcV6.AsSlice(),
			},
			Vs: []*balancerpb.VirtualService{
				createCfgVirtualService(cfgVs1IP, cfgVs1Port, vs1Reals),
				createCfgVirtualService(cfgVs2IP, cfgVs2Port, vs2Reals),
			},
			DecapAddresses: []*balancerpb.Addr{},
			SessionsTimeouts: &balancerpb.SessionsTimeouts{
				TcpSynAck: 600,
				TcpSyn:    600,
				TcpFin:    600,
				Tcp:       600,
				Udp:       600,
				Default:   600,
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

// createCfgUpdatedConfig creates the updated configuration with VS2 (modified) and VS3 (new)
func createCfgUpdatedConfig() *balancerpb.BalancerConfig {
	vs2Reals := []*balancerpb.Real{
		createCfgReal(cfgReal6IP, 1), // OLD - persists
		createCfgReal(cfgReal7IP, 1), // NEW
		createCfgReal(cfgReal8IP, 1), // NEW
	}

	vs3Reals := []*balancerpb.Real{
		createCfgReal(cfgReal9IP, 1),
		createCfgReal(cfgReal10IP, 1),
		createCfgReal(cfgReal11IP, 1),
	}

	return &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: &balancerpb.Addr{
				Bytes: cfgBalancerSrcV4.AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: cfgBalancerSrcV6.AsSlice(),
			},
			Vs: []*balancerpb.VirtualService{
				createCfgVirtualService(cfgVs2IP, cfgVs2Port, vs2Reals),
				createCfgVirtualService(cfgVs3IP, cfgVs3Port, vs3Reals),
			},
			DecapAddresses: []*balancerpb.Addr{},
			SessionsTimeouts: &balancerpb.SessionsTimeouts{
				TcpSynAck: 600,
				TcpSyn:    600,
				TcpFin:    600,
				Tcp:       600,
				Udp:       600,
				Default:   600,
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

// generateCfgClientIP generates a unique client IP based on index
func generateCfgClientIP(index int) netip.Addr {
	base := cfgClientBaseIP.As4()
	// Ensure we don't wrap around and create duplicates
	base[3] = byte(index % 256)
	base[2] = byte((int(base[2]) + index/256) % 256)
	return netip.AddrFrom4(base)
}

// sendCfgPacketsToVS sends count packets to a virtual service from different client IPs
func sendCfgPacketsToVS(
	t *testing.T,
	ts *utils.TestSetup,
	vsIP netip.Addr,
	vsPort uint16,
	count int,
	clientIPStart int,
) []*framework.PacketInfo {
	t.Helper()

	var outputPackets []*framework.PacketInfo

	for i := range count {
		clientIP := generateCfgClientIP(clientIPStart + i)
		// Use clientIPStart + i to ensure unique ports across all phases
		clientPort := uint16(10000 + clientIPStart + i)

		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			vsIP,
			vsPort,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output), "expected 1 output packet")
		require.Empty(t, result.Drop, "expected no dropped packets")
		outputPackets = append(outputPackets, result.Output[0])
	}

	return outputPackets
}

// verifyCfgPacketDistribution verifies that packets are distributed as expected
func verifyCfgPacketDistribution(
	t *testing.T,
	packets []*framework.PacketInfo,
	expectedCounts map[netip.Addr]int,
) {
	t.Helper()

	actualCounts := utils.CountPacketsPerReal(packets)

	for realIP, expectedCount := range expectedCounts {
		actualCount := actualCounts[realIP]
		assert.Equal(
			t,
			expectedCount,
			actualCount,
			"packet count mismatch for real %s",
			realIP,
		)
	}
}

// findCfgVsInGraph finds a virtual service in the graph by IP
func findCfgVsInGraph(
	graph *balancerpb.Graph,
	vsIP netip.Addr,
) *balancerpb.GraphVs {
	for _, vs := range graph.VirtualServices {
		addr, _ := netip.AddrFromSlice(vs.Identifier.Addr.Bytes)
		if addr == vsIP {
			return vs
		}
	}
	return nil
}

// findCfgRealInVs finds a real in a virtual service by IP
func findCfgRealInVs(
	vs *balancerpb.GraphVs,
	realIP netip.Addr,
) *balancerpb.GraphReal {
	for _, real := range vs.Reals {
		addr, _ := netip.AddrFromSlice(real.Identifier.Ip.Bytes)
		if addr == realIP {
			return real
		}
	}
	return nil
}

// TestConfigUpdateAndStats is the main test function
func TestConfigUpdateAndStats(t *testing.T) {
	// Create initial configuration
	config := createCfgInitialConfig()

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

	// Phase 1: Initial State
	t.Run("Phase1_InitialState", func(t *testing.T) {
		testCfgPhase1InitialState(t, ts)
	})

	// Phase 2: Disable Reals
	t.Run("Phase2_DisableReals", func(t *testing.T) {
		testCfgPhase2DisableReals(t, ts)
	})

	// Phase 3: Verify APIs (Initial State)
	t.Run("Phase3_VerifyAPIs", func(t *testing.T) {
		testCfgPhase3VerifyAPIs(t, ts)
	})

	// Phase 4: Update Configuration
	t.Run("Phase4_UpdateConfig", func(t *testing.T) {
		testCfgPhase4UpdateConfig(t, ts)
	})

	// Phase 5: Verify APIs (After Update)
	t.Run("Phase5_VerifyUpdatedAPIs", func(t *testing.T) {
		testCfgPhase5VerifyUpdatedAPIs(t, ts)
	})

	// Phase 6: Verify State with New Agent
	t.Run("Phase6_StateWithNewAgent", func(t *testing.T) {
		testCfgPhase6StateWithNewAgent(t, ts)
	})
}

// testCfgPhase1InitialState tests the initial state with all reals enabled
func testCfgPhase1InitialState(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	// Enable all reals
	utils.EnableAllReals(t, ts)

	// Send 15 packets to VS1
	t.Log("Sending 15 packets to VS1")
	vs1Packets := sendCfgPacketsToVS(t, ts, cfgVs1IP, cfgVs1Port, 15, 0)

	// Send 15 packets to VS2
	t.Log("Sending 15 packets to VS2")
	vs2Packets := sendCfgPacketsToVS(t, ts, cfgVs2IP, cfgVs2Port, 15, 100)

	// Verify distribution (ROUND_ROBIN with equal weights: 5 packets per real)
	t.Log("Verifying packet distribution for VS1")
	verifyCfgPacketDistribution(t, vs1Packets, map[netip.Addr]int{
		cfgReal1IP: 5,
		cfgReal2IP: 5,
		cfgReal3IP: 5,
	})

	t.Log("Verifying packet distribution for VS2")
	verifyCfgPacketDistribution(t, vs2Packets, map[netip.Addr]int{
		cfgReal4IP: 5,
		cfgReal5IP: 5,
		cfgReal6IP: 5,
	})
}

// testCfgPhase2DisableReals tests disabling reals and verifying traffic routing
func testCfgPhase2DisableReals(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	config := ts.Balancer.Config()

	// Find VS1 and VS2
	var vs1, vs2 *balancerpb.VirtualService
	for _, vs := range config.PacketHandler.Vs {
		addr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
		switch addr {
		case cfgVs1IP:
			vs1 = vs
		case cfgVs2IP:
			vs2 = vs
		}
	}
	require.NotNil(t, vs1, "VS1 not found")
	require.NotNil(t, vs2, "VS2 not found")

	// Disable Real1 and Real2 in VS1, Real4 and Real5 in VS2
	enableFalse := false
	updates := []*balancerpb.RealUpdate{
		{
			RealId: &balancerpb.RealIdentifier{
				Vs: vs1.Id,
				Real: &balancerpb.RelativeRealIdentifier{
					Ip:   &balancerpb.Addr{Bytes: cfgReal1IP.AsSlice()},
					Port: 0,
				},
			},
			Enable: &enableFalse,
		},
		{
			RealId: &balancerpb.RealIdentifier{
				Vs: vs1.Id,
				Real: &balancerpb.RelativeRealIdentifier{
					Ip:   &balancerpb.Addr{Bytes: cfgReal2IP.AsSlice()},
					Port: 0,
				},
			},
			Enable: &enableFalse,
		},
		{
			RealId: &balancerpb.RealIdentifier{
				Vs: vs2.Id,
				Real: &balancerpb.RelativeRealIdentifier{
					Ip:   &balancerpb.Addr{Bytes: cfgReal4IP.AsSlice()},
					Port: 0,
				},
			},
			Enable: &enableFalse,
		},
		{
			RealId: &balancerpb.RealIdentifier{
				Vs: vs2.Id,
				Real: &balancerpb.RelativeRealIdentifier{
					Ip:   &balancerpb.Addr{Bytes: cfgReal5IP.AsSlice()},
					Port: 0,
				},
			},
			Enable: &enableFalse,
		},
	}

	_, err := ts.Balancer.UpdateReals(updates, false)
	require.NoError(t, err, "failed to disable reals")

	// Send 5 new packets to VS1 (new client IPs)
	t.Log("Sending 5 packets to VS1 after disabling reals")
	vs1Packets := sendCfgPacketsToVS(t, ts, cfgVs1IP, cfgVs1Port, 5, 200)

	// Send 5 new packets to VS2 (new client IPs)
	t.Log("Sending 5 packets to VS2 after disabling reals")
	vs2Packets := sendCfgPacketsToVS(t, ts, cfgVs2IP, cfgVs2Port, 5, 300)

	// Verify all packets go to enabled reals only
	t.Log("Verifying packets only go to Real3")
	verifyCfgPacketDistribution(t, vs1Packets, map[netip.Addr]int{
		cfgReal3IP: 5,
	})

	t.Log("Verifying packets only go to Real6")
	verifyCfgPacketDistribution(t, vs2Packets, map[netip.Addr]int{
		cfgReal6IP: 5,
	})
}

// testCfgPhase3VerifyAPIs verifies all API outputs in the initial state
func testCfgPhase3VerifyAPIs(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	t.Run("VerifyConfig", func(t *testing.T) {
		config := ts.Balancer.Config()
		require.NotNil(t, config)
		require.NotNil(t, config.PacketHandler)

		// Verify 2 virtual services
		assert.Equal(
			t,
			2,
			len(config.PacketHandler.Vs),
			"should have 2 virtual services",
		)

		// Verify each VS has 3 reals
		for _, vs := range config.PacketHandler.Vs {
			assert.Equal(t, 3, len(vs.Reals), "each VS should have 3 reals")
		}
	})

	t.Run("VerifyGraph", func(t *testing.T) {
		graph := ts.Balancer.Graph()
		require.NotNil(t, graph)
		require.NotNil(t, graph.VirtualServices)

		// Verify 2 virtual services
		assert.Equal(
			t,
			2,
			len(graph.VirtualServices),
			"should have 2 virtual services",
		)

		// Find VS1 and verify real states
		vs1 := findCfgVsInGraph(graph, cfgVs1IP)
		require.NotNil(t, vs1, "VS1 not found in graph")
		assert.Equal(t, 3, len(vs1.Reals), "VS1 should have 3 reals")

		// Verify Real1: DISABLED
		real1 := findCfgRealInVs(vs1, cfgReal1IP)
		require.NotNil(t, real1, "Real1 not found")
		assert.False(t, real1.Enabled, "Real1 should be disabled")

		// Verify Real2: DISABLED
		real2 := findCfgRealInVs(vs1, cfgReal2IP)
		require.NotNil(t, real2, "Real2 not found")
		assert.False(t, real2.Enabled, "Real2 should be disabled")

		// Verify Real3: ENABLED
		real3 := findCfgRealInVs(vs1, cfgReal3IP)
		require.NotNil(t, real3, "Real3 not found")
		assert.True(t, real3.Enabled, "Real3 should be enabled")

		// Find VS2 and verify real states
		vs2 := findCfgVsInGraph(graph, cfgVs2IP)
		require.NotNil(t, vs2, "VS2 not found in graph")
		assert.Equal(t, 3, len(vs2.Reals), "VS2 should have 3 reals")

		// Verify Real4: DISABLED
		real4 := findCfgRealInVs(vs2, cfgReal4IP)
		require.NotNil(t, real4, "Real4 not found")
		assert.False(t, real4.Enabled, "Real4 should be disabled")

		// Verify Real5: DISABLED
		real5 := findCfgRealInVs(vs2, cfgReal5IP)
		require.NotNil(t, real5, "Real5 not found")
		assert.False(t, real5.Enabled, "Real5 should be disabled")

		// Verify Real6: ENABLED
		real6 := findCfgRealInVs(vs2, cfgReal6IP)
		require.NotNil(t, real6, "Real6 not found")
		assert.True(t, real6.Enabled, "Real6 should be enabled")

		// Verify weights
		for _, vs := range graph.VirtualServices {
			for _, real := range vs.Reals {
				assert.Equal(
					t,
					uint32(1),
					real.Weight,
					"all reals should have weight 1",
				)
			}
		}
	})

	t.Run("VerifyStats", func(t *testing.T) {
		statsRef := &balancerpb.PacketHandlerRef{
			Device:   &utils.DeviceName,
			Pipeline: &utils.PipelineName,
			Function: &utils.FunctionName,
			Chain:    &utils.ChainName,
		}

		stats, err := ts.Balancer.Stats(statsRef)
		require.NoError(t, err)
		require.NotNil(t, stats)

		// Find VS1 stats
		var vs1Stats *balancerpb.NamedVsStats
		for _, vs := range stats.Vs {
			addr, _ := netip.AddrFromSlice(vs.Vs.Addr.Bytes)
			if addr == cfgVs1IP {
				vs1Stats = vs
				break
			}
		}
		require.NotNil(t, vs1Stats, "VS1 stats not found")

		// Verify VS1 stats: 20 packets total (15 initial + 5 after disable)
		assert.Equal(
			t,
			uint64(20),
			vs1Stats.Stats.IncomingPackets,
			"VS1 incoming packets",
		)
		assert.Equal(
			t,
			uint64(20),
			vs1Stats.Stats.OutgoingPackets,
			"VS1 outgoing packets",
		)

		// Verify Real1: 5 packets
		var real1Stats *balancerpb.NamedRealStats
		for _, real := range vs1Stats.Reals {
			addr, _ := netip.AddrFromSlice(real.Real.Real.Ip.Bytes)
			if addr == cfgReal1IP {
				real1Stats = real
				break
			}
		}
		require.NotNil(t, real1Stats, "Real1 stats not found")
		assert.Equal(t, uint64(5), real1Stats.Stats.Packets, "Real1 packets")

		// Verify Real2: 5 packets
		var real2Stats *balancerpb.NamedRealStats
		for _, real := range vs1Stats.Reals {
			addr, _ := netip.AddrFromSlice(real.Real.Real.Ip.Bytes)
			if addr == cfgReal2IP {
				real2Stats = real
				break
			}
		}
		require.NotNil(t, real2Stats, "Real2 stats not found")
		assert.Equal(t, uint64(5), real2Stats.Stats.Packets, "Real2 packets")

		// Verify Real3: 10 packets (5 initial + 5 after disable)
		var real3Stats *balancerpb.NamedRealStats
		for _, real := range vs1Stats.Reals {
			addr, _ := netip.AddrFromSlice(real.Real.Real.Ip.Bytes)
			if addr == cfgReal3IP {
				real3Stats = real
				break
			}
		}
		require.NotNil(t, real3Stats, "Real3 stats not found")
		assert.Equal(t, uint64(10), real3Stats.Stats.Packets, "Real3 packets")

		// Find VS2 stats
		var vs2Stats *balancerpb.NamedVsStats
		for _, vs := range stats.Vs {
			addr, _ := netip.AddrFromSlice(vs.Vs.Addr.Bytes)
			if addr == cfgVs2IP {
				vs2Stats = vs
				break
			}
		}
		require.NotNil(t, vs2Stats, "VS2 stats not found")

		// Verify VS2 stats: 20 packets total
		assert.Equal(
			t,
			uint64(20),
			vs2Stats.Stats.IncomingPackets,
			"VS2 incoming packets",
		)
		assert.Equal(
			t,
			uint64(20),
			vs2Stats.Stats.OutgoingPackets,
			"VS2 outgoing packets",
		)

		// Verify Real4: 5 packets
		var real4Stats *balancerpb.NamedRealStats
		for _, real := range vs2Stats.Reals {
			addr, _ := netip.AddrFromSlice(real.Real.Real.Ip.Bytes)
			if addr == cfgReal4IP {
				real4Stats = real
				break
			}
		}
		require.NotNil(t, real4Stats, "Real4 stats not found")
		assert.Equal(t, uint64(5), real4Stats.Stats.Packets, "Real4 packets")

		// Verify Real5: 5 packets
		var real5Stats *balancerpb.NamedRealStats
		for _, real := range vs2Stats.Reals {
			addr, _ := netip.AddrFromSlice(real.Real.Real.Ip.Bytes)
			if addr == cfgReal5IP {
				real5Stats = real
				break
			}
		}
		require.NotNil(t, real5Stats, "Real5 stats not found")
		assert.Equal(t, uint64(5), real5Stats.Stats.Packets, "Real5 packets")

		// Verify Real6: 10 packets
		var real6Stats *balancerpb.NamedRealStats
		for _, real := range vs2Stats.Reals {
			addr, _ := netip.AddrFromSlice(real.Real.Real.Ip.Bytes)
			if addr == cfgReal6IP {
				real6Stats = real
				break
			}
		}
		require.NotNil(t, real6Stats, "Real6 stats not found")
		assert.Equal(t, uint64(10), real6Stats.Stats.Packets, "Real6 packets")
	})

	t.Run("VerifyInfo", func(t *testing.T) {
		info, err := ts.Balancer.Info(ts.Mock.CurrentTime())
		require.NoError(t, err)
		require.NotNil(t, info)

		// Verify total active sessions: 40 (20 from VS1 + 20 from VS2)
		// Each VS: 15 initial packets + 5 after disabling = 20 sessions
		assert.Equal(
			t,
			uint64(40),
			info.ActiveSessions,
			"total active sessions",
		)

		// Find VS1 info
		var vs1Info *balancerpb.VsInfo
		for _, vs := range info.Vs {
			addr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
			if addr == cfgVs1IP {
				vs1Info = vs
				break
			}
		}
		require.NotNil(t, vs1Info, "VS1 info not found")
		assert.Equal(
			t,
			uint64(20),
			vs1Info.ActiveSessions,
			"VS1 active sessions",
		)

		// Find VS2 info
		var vs2Info *balancerpb.VsInfo
		for _, vs := range info.Vs {
			addr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
			if addr == cfgVs2IP {
				vs2Info = vs
				break
			}
		}
		require.NotNil(t, vs2Info, "VS2 info not found")
		assert.Equal(
			t,
			uint64(20),
			vs2Info.ActiveSessions,
			"VS2 active sessions",
		)
	})

	t.Run("VerifySessions", func(t *testing.T) {
		sessions, err := ts.Balancer.Sessions(ts.Mock.CurrentTime())
		require.NoError(t, err, "failed to get sessions")
		require.NotNil(t, sessions)

		// Count sessions
		sessionCount := 0
		vs1Sessions := 0
		vs2Sessions := 0

		for _, session := range sessions {
			sessionCount++

			vsAddr, _ := netip.AddrFromSlice(session.VsId.Addr.Bytes)
			switch vsAddr {
			case cfgVs1IP:
				vs1Sessions++
			case cfgVs2IP:
				vs2Sessions++
			}
		}

		// Verify total sessions: 40 (20 per VS)
		assert.Equal(t, 40, sessionCount, "total sessions")
		assert.Equal(t, 20, vs1Sessions, "VS1 sessions")
		assert.Equal(t, 20, vs2Sessions, "VS2 sessions")
	})
}

// testCfgPhase4UpdateConfig tests updating the configuration
func testCfgPhase4UpdateConfig(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	// Update configuration
	newConfig := createCfgUpdatedConfig()
	err := ts.Balancer.Update(newConfig, ts.Mock.CurrentTime())
	require.NoError(t, err, "failed to update configuration")

	// Verify graph immediately after update (before enabling new reals)
	t.Run("VerifyGraphAfterUpdate_BeforeEnabling", func(t *testing.T) {
		graph := ts.Balancer.Graph()
		require.NotNil(t, graph)

		// Verify 2 virtual services (VS2, VS3)
		assert.Equal(
			t,
			2,
			len(graph.VirtualServices),
			"should have 2 virtual services",
		)

		// Find VS2
		vs2 := findCfgVsInGraph(graph, cfgVs2IP)
		require.NotNil(t, vs2, "VS2 not found in graph")
		assert.Equal(t, 3, len(vs2.Reals), "VS2 should have 3 reals")

		// Verify Real6: ENABLED (persisted from old config)
		real6 := findCfgRealInVs(vs2, cfgReal6IP)
		require.NotNil(t, real6, "Real6 not found")
		assert.True(t, real6.Enabled, "Real6 should be enabled (persisted)")

		// Verify Real7: DISABLED (new real)
		real7 := findCfgRealInVs(vs2, cfgReal7IP)
		require.NotNil(t, real7, "Real7 not found")
		assert.False(t, real7.Enabled, "Real7 should be disabled (new)")

		// Verify Real8: DISABLED (new real)
		real8 := findCfgRealInVs(vs2, cfgReal8IP)
		require.NotNil(t, real8, "Real8 not found")
		assert.False(t, real8.Enabled, "Real8 should be disabled (new)")

		// Find VS3
		vs3 := findCfgVsInGraph(graph, cfgVs3IP)
		require.NotNil(t, vs3, "VS3 not found in graph")
		assert.Equal(t, 3, len(vs3.Reals), "VS3 should have 3 reals")

		// Verify all VS3 reals are disabled
		real9 := findCfgRealInVs(vs3, cfgReal9IP)
		require.NotNil(t, real9, "Real9 not found")
		assert.False(t, real9.Enabled, "Real9 should be disabled (new)")

		real10 := findCfgRealInVs(vs3, cfgReal10IP)
		require.NotNil(t, real10, "Real10 not found")
		assert.False(t, real10.Enabled, "Real10 should be disabled (new)")

		real11 := findCfgRealInVs(vs3, cfgReal11IP)
		require.NotNil(t, real11, "Real11 not found")
		assert.False(t, real11.Enabled, "Real11 should be disabled (new)")

		// Verify weights
		for _, vs := range graph.VirtualServices {
			for _, real := range vs.Reals {
				assert.Equal(
					t,
					uint32(1),
					real.Weight,
					"all reals should have weight 1",
				)
			}
		}
	})

	// Enable new reals
	config := ts.Balancer.Config()
	var vs2, vs3 *balancerpb.VirtualService
	for _, vs := range config.PacketHandler.Vs {
		addr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
		switch addr {
		case cfgVs2IP:
			vs2 = vs
		case cfgVs3IP:
			vs3 = vs
		}
	}
	require.NotNil(t, vs2, "VS2 not found")
	require.NotNil(t, vs3, "VS3 not found")

	enableTrue := true
	updates := []*balancerpb.RealUpdate{
		{
			RealId: &balancerpb.RealIdentifier{
				Vs: vs2.Id,
				Real: &balancerpb.RelativeRealIdentifier{
					Ip:   &balancerpb.Addr{Bytes: cfgReal7IP.AsSlice()},
					Port: 0,
				},
			},
			Enable: &enableTrue,
		},
		{
			RealId: &balancerpb.RealIdentifier{
				Vs: vs2.Id,
				Real: &balancerpb.RelativeRealIdentifier{
					Ip:   &balancerpb.Addr{Bytes: cfgReal8IP.AsSlice()},
					Port: 0,
				},
			},
			Enable: &enableTrue,
		},
		{
			RealId: &balancerpb.RealIdentifier{
				Vs: vs3.Id,
				Real: &balancerpb.RelativeRealIdentifier{
					Ip:   &balancerpb.Addr{Bytes: cfgReal9IP.AsSlice()},
					Port: 0,
				},
			},
			Enable: &enableTrue,
		},
		{
			RealId: &balancerpb.RealIdentifier{
				Vs: vs3.Id,
				Real: &balancerpb.RelativeRealIdentifier{
					Ip:   &balancerpb.Addr{Bytes: cfgReal10IP.AsSlice()},
					Port: 0,
				},
			},
			Enable: &enableTrue,
		},
		{
			RealId: &balancerpb.RealIdentifier{
				Vs: vs3.Id,
				Real: &balancerpb.RelativeRealIdentifier{
					Ip:   &balancerpb.Addr{Bytes: cfgReal11IP.AsSlice()},
					Port: 0,
				},
			},
			Enable: &enableTrue,
		},
	}

	_, err = ts.Balancer.UpdateReals(updates, false)
	require.NoError(t, err, "failed to enable new reals")

	// Send 15 packets to VS2
	t.Log("Sending 15 packets to VS2 after update")
	vs2Packets := sendCfgPacketsToVS(t, ts, cfgVs2IP, cfgVs2Port, 15, 400)

	// Send 15 packets to VS3
	t.Log("Sending 15 packets to VS3 after update")
	vs3Packets := sendCfgPacketsToVS(t, ts, cfgVs3IP, cfgVs3Port, 15, 500)

	// Verify distribution
	t.Log("Verifying packet distribution for VS2")
	verifyCfgPacketDistribution(t, vs2Packets, map[netip.Addr]int{
		cfgReal6IP: 5,
		cfgReal7IP: 5,
		cfgReal8IP: 5,
	})

	t.Log("Verifying packet distribution for VS3")
	verifyCfgPacketDistribution(t, vs3Packets, map[netip.Addr]int{
		cfgReal9IP:  5,
		cfgReal10IP: 5,
		cfgReal11IP: 5,
	})
}

// testCfgPhase5VerifyUpdatedAPIs verifies all API outputs after configuration update
func testCfgPhase5VerifyUpdatedAPIs(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	t.Run("VerifyConfig", func(t *testing.T) {
		config := ts.Balancer.Config()
		require.NotNil(t, config)
		require.NotNil(t, config.PacketHandler)

		// Verify only 2 virtual services (VS2, VS3)
		assert.Equal(
			t,
			2,
			len(config.PacketHandler.Vs),
			"should have 2 virtual services",
		)

		// Verify VS1 is NOT present
		for _, vs := range config.PacketHandler.Vs {
			addr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
			assert.NotEqual(t, cfgVs1IP, addr, "VS1 should not be present")
		}

		// Verify VS2 and VS3 are present
		foundVs2 := false
		foundVs3 := false
		for _, vs := range config.PacketHandler.Vs {
			addr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
			switch addr {
			case cfgVs2IP:
				foundVs2 = true
				assert.Equal(t, 3, len(vs.Reals), "VS2 should have 3 reals")
			case cfgVs3IP:
				foundVs3 = true
				assert.Equal(t, 3, len(vs.Reals), "VS3 should have 3 reals")
			}
		}
		assert.True(t, foundVs2, "VS2 should be present")
		assert.True(t, foundVs3, "VS3 should be present")
	})

	t.Run("VerifyGraph", func(t *testing.T) {
		graph := ts.Balancer.Graph()
		require.NotNil(t, graph)

		// Verify 2 virtual services
		assert.Equal(
			t,
			2,
			len(graph.VirtualServices),
			"should have 2 virtual services",
		)

		// Verify all reals are enabled
		for _, vs := range graph.VirtualServices {
			for _, real := range vs.Reals {
				assert.True(t, real.Enabled, "all reals should be enabled")
				assert.Equal(
					t,
					uint32(1),
					real.Weight,
					"all reals should have weight 1",
				)
			}
		}
	})

	t.Run("VerifyStats", func(t *testing.T) {
		statsRef := &balancerpb.PacketHandlerRef{
			Device:   &utils.DeviceName,
			Pipeline: &utils.PipelineName,
			Function: &utils.FunctionName,
			Chain:    &utils.ChainName,
		}

		stats, err := ts.Balancer.Stats(statsRef)
		require.NoError(t, err)
		require.NotNil(t, stats)

		// Verify VS1 is NOT present
		for _, vs := range stats.Vs {
			addr, _ := netip.AddrFromSlice(vs.Vs.Addr.Bytes)
			assert.NotEqual(
				t,
				cfgVs1IP,
				addr,
				"VS1 stats should not be present",
			)
		}

		// Find VS2 stats
		var vs2Stats *balancerpb.NamedVsStats
		for _, vs := range stats.Vs {
			addr, _ := netip.AddrFromSlice(vs.Vs.Addr.Bytes)
			if addr == cfgVs2IP {
				vs2Stats = vs
				break
			}
		}
		require.NotNil(t, vs2Stats, "VS2 stats not found")

		// Verify VS2 stats are CUMULATIVE: 35 packets (20 from old + 15 from new)
		assert.Equal(
			t,
			uint64(35),
			vs2Stats.Stats.IncomingPackets,
			"VS2 incoming packets (cumulative)",
		)
		assert.Equal(
			t,
			uint64(35),
			vs2Stats.Stats.OutgoingPackets,
			"VS2 outgoing packets (cumulative)",
		)

		// Verify Real6: 15 packets (10 from old + 5 from new)
		var real6Stats *balancerpb.NamedRealStats
		for _, real := range vs2Stats.Reals {
			addr, _ := netip.AddrFromSlice(real.Real.Real.Ip.Bytes)
			if addr == cfgReal6IP {
				real6Stats = real
				break
			}
		}
		require.NotNil(t, real6Stats, "Real6 stats not found")
		assert.Equal(
			t,
			uint64(15),
			real6Stats.Stats.Packets,
			"Real6 packets (cumulative)",
		)

		// Verify Real7: 5 packets (new)
		var real7Stats *balancerpb.NamedRealStats
		for _, real := range vs2Stats.Reals {
			addr, _ := netip.AddrFromSlice(real.Real.Real.Ip.Bytes)
			if addr == cfgReal7IP {
				real7Stats = real
				break
			}
		}
		require.NotNil(t, real7Stats, "Real7 stats not found")
		assert.Equal(t, uint64(5), real7Stats.Stats.Packets, "Real7 packets")

		// Verify Real8: 5 packets (new)
		var real8Stats *balancerpb.NamedRealStats
		for _, real := range vs2Stats.Reals {
			addr, _ := netip.AddrFromSlice(real.Real.Real.Ip.Bytes)
			if addr == cfgReal8IP {
				real8Stats = real
				break
			}
		}
		require.NotNil(t, real8Stats, "Real8 stats not found")
		assert.Equal(t, uint64(5), real8Stats.Stats.Packets, "Real8 packets")

		// Verify NO stats for Real4, Real5 (deleted)
		for _, real := range vs2Stats.Reals {
			addr, _ := netip.AddrFromSlice(real.Real.Real.Ip.Bytes)
			assert.NotEqual(
				t,
				cfgReal4IP,
				addr,
				"Real4 stats should not be present",
			)
			assert.NotEqual(
				t,
				cfgReal5IP,
				addr,
				"Real5 stats should not be present",
			)
		}

		// Find VS3 stats
		var vs3Stats *balancerpb.NamedVsStats
		for _, vs := range stats.Vs {
			addr, _ := netip.AddrFromSlice(vs.Vs.Addr.Bytes)
			if addr == cfgVs3IP {
				vs3Stats = vs
				break
			}
		}
		require.NotNil(t, vs3Stats, "VS3 stats not found")

		// Verify VS3 stats: 15 packets (new)
		assert.Equal(
			t,
			uint64(15),
			vs3Stats.Stats.IncomingPackets,
			"VS3 incoming packets",
		)
		assert.Equal(
			t,
			uint64(15),
			vs3Stats.Stats.OutgoingPackets,
			"VS3 outgoing packets",
		)

		// Verify each real has 5 packets
		assert.Equal(t, 3, len(vs3Stats.Reals), "VS3 should have 3 reals")
		for _, real := range vs3Stats.Reals {
			assert.Equal(
				t,
				uint64(5),
				real.Stats.Packets,
				"each VS3 real should have 5 packets",
			)
		}
	})

	t.Run("VerifyInfo", func(t *testing.T) {
		info, err := ts.Balancer.Info(ts.Mock.CurrentTime())
		require.NoError(t, err)
		require.NotNil(t, info)

		// Verify total active sessions: 40 (25 from VS2 + 15 from VS3)
		// VS1 deleted: its 20 sessions are removed
		// VS2: 10 sessions to Real6 (persisted) + 15 new = 25
		// VS3: 15 new sessions
		assert.Equal(
			t,
			uint64(40),
			info.ActiveSessions,
			"total active sessions",
		)

		// Verify VS1 is NOT present
		for _, vs := range info.Vs {
			addr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
			assert.NotEqual(t, cfgVs1IP, addr, "VS1 info should not be present")
		}

		// Find VS2 info
		var vs2Info *balancerpb.VsInfo
		for _, vs := range info.Vs {
			addr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
			if addr == cfgVs2IP {
				vs2Info = vs
				break
			}
		}
		require.NotNil(t, vs2Info, "VS2 info not found")
		// VS2: 10 old sessions (to Real6) + 15 new = 25
		assert.Equal(
			t,
			uint64(25),
			vs2Info.ActiveSessions,
			"VS2 active sessions",
		)

		// Find VS3 info
		var vs3Info *balancerpb.VsInfo
		for _, vs := range info.Vs {
			addr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
			if addr == cfgVs3IP {
				vs3Info = vs
				break
			}
		}
		require.NotNil(t, vs3Info, "VS3 info not found")
		assert.Equal(
			t,
			uint64(15),
			vs3Info.ActiveSessions,
			"VS3 active sessions",
		)
	})

	t.Run("VerifySessions", func(t *testing.T) {
		sessions, err := ts.Balancer.Sessions(ts.Mock.CurrentTime())
		require.NoError(t, err, "failed to get sessions")
		require.NotNil(t, sessions)

		// Count sessions
		sessionCount := 0
		vs1Sessions := 0
		vs2Sessions := 0
		vs3Sessions := 0

		for _, session := range sessions {
			sessionCount++

			vsAddr, _ := netip.AddrFromSlice(session.VsId.Addr.Bytes)
			switch vsAddr {
			case cfgVs1IP:
				vs1Sessions++
			case cfgVs2IP:
				vs2Sessions++
			case cfgVs3IP:
				vs3Sessions++
			}
		}

		// Verify total sessions: 40 (25 VS2 + 15 VS3)
		assert.Equal(t, 40, sessionCount, "total sessions")

		// Verify NO sessions for VS1 (deleted)
		assert.Equal(t, 0, vs1Sessions, "VS1 should have no sessions")

		// Verify sessions for VS2 and VS3
		// VS2: 10 old (to Real6) + 15 new = 25
		assert.Equal(t, 25, vs2Sessions, "VS2 sessions")
		assert.Equal(t, 15, vs3Sessions, "VS3 sessions")
	})
}

// testCfgPhase6StateWithNewAgent tests state persistence by creating a new balancer agent
// that attaches to the same shared memory and verifies all API outputs match Phase 5
func testCfgPhase6StateWithNewAgent(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	// Store Phase 5 outputs for comparison
	t.Run("StorePhase5Outputs", func(t *testing.T) {
		// These will be captured in the parent scope for comparison
		t.Log("Phase 5 outputs will be compared with new agent outputs")
	})

	// Get Phase 5 API outputs before creating new agent
	statsRef := &balancerpb.PacketHandlerRef{
		Device:   &utils.DeviceName,
		Pipeline: &utils.PipelineName,
		Function: &utils.FunctionName,
		Chain:    &utils.ChainName,
	}

	phase5Config := ts.Balancer.Config()
	phase5Graph := ts.Balancer.Graph()
	phase5Stats, err := ts.Balancer.Stats(statsRef)
	require.NoError(t, err, "failed to get Phase 5 stats")
	phase5Info, err := ts.Balancer.Info(ts.Mock.CurrentTime())
	require.NoError(t, err, "failed to get Phase 5 info")
	phase5Sessions, err := ts.Balancer.Sessions(ts.Mock.CurrentTime())
	require.NoError(t, err, "failed to get Phase 5 sessions")

	// Create new balancer agent and attach to existing manager
	t.Run("CreateNewAgentAndAttach", func(t *testing.T) {
		// Create new BalancerAgent using same shared memory
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

		// Verify Config matches Phase 5
		t.Run("VerifyConfigMatches", func(t *testing.T) {
			newConfig := newBalancer.Config()
			require.NotNil(t, newConfig)
			require.NotNil(t, newConfig.PacketHandler)

			// Verify same number of virtual services
			assert.Equal(
				t,
				len(phase5Config.PacketHandler.Vs),
				len(newConfig.PacketHandler.Vs),
				"should have same number of virtual services",
			)

			// Verify VS2 and VS3 are present
			assert.Equal(
				t,
				2,
				len(newConfig.PacketHandler.Vs),
				"should have 2 virtual services",
			)

			// Verify each VS has 3 reals
			for _, vs := range newConfig.PacketHandler.Vs {
				assert.Equal(t, 3, len(vs.Reals), "each VS should have 3 reals")
			}
		})

		// Verify Graph matches Phase 5
		t.Run("VerifyGraphMatches", func(t *testing.T) {
			newGraph := newBalancer.Graph()
			require.NotNil(t, newGraph)

			// Verify same number of virtual services
			assert.Equal(
				t,
				len(phase5Graph.VirtualServices),
				len(newGraph.VirtualServices),
				"should have same number of virtual services",
			)

			// Verify all reals are enabled (as in Phase 5)
			for _, vs := range newGraph.VirtualServices {
				for _, real := range vs.Reals {
					assert.True(t, real.Enabled, "all reals should be enabled")
					assert.Equal(
						t,
						uint32(1),
						real.Weight,
						"all reals should have weight 1",
					)
				}
			}
		})

		// Verify Stats match Phase 5
		t.Run("VerifyStatsMatch", func(t *testing.T) {
			newStats, err := newBalancer.Stats(statsRef)
			require.NoError(t, err)
			require.NotNil(t, newStats)

			// Verify same number of VS stats
			assert.Equal(
				t,
				len(phase5Stats.Vs),
				len(newStats.Vs),
				"should have same number of VS stats",
			)

			// Find VS2 stats
			var vs2Stats *balancerpb.NamedVsStats
			for _, vs := range newStats.Vs {
				addr, _ := netip.AddrFromSlice(vs.Vs.Addr.Bytes)
				if addr == cfgVs2IP {
					vs2Stats = vs
					break
				}
			}
			require.NotNil(t, vs2Stats, "VS2 stats not found")

			// Verify VS2 stats: 35 packets (cumulative from Phase 5)
			assert.Equal(
				t,
				uint64(35),
				vs2Stats.Stats.IncomingPackets,
				"VS2 incoming packets should match Phase 5",
			)
			assert.Equal(
				t,
				uint64(35),
				vs2Stats.Stats.OutgoingPackets,
				"VS2 outgoing packets should match Phase 5",
			)

			// Find VS3 stats
			var vs3Stats *balancerpb.NamedVsStats
			for _, vs := range newStats.Vs {
				addr, _ := netip.AddrFromSlice(vs.Vs.Addr.Bytes)
				if addr == cfgVs3IP {
					vs3Stats = vs
					break
				}
			}
			require.NotNil(t, vs3Stats, "VS3 stats not found")

			// Verify VS3 stats: 15 packets
			assert.Equal(
				t,
				uint64(15),
				vs3Stats.Stats.IncomingPackets,
				"VS3 incoming packets should match Phase 5",
			)
			assert.Equal(
				t,
				uint64(15),
				vs3Stats.Stats.OutgoingPackets,
				"VS3 outgoing packets should match Phase 5",
			)
		})

		// Verify Info matches Phase 5
		t.Run("VerifyInfoMatches", func(t *testing.T) {
			newInfo, err := newBalancer.Info(ts.Mock.CurrentTime())
			require.NoError(t, err)
			require.NotNil(t, newInfo)

			// Verify total active sessions: 40 (from Phase 5)
			assert.Equal(
				t,
				phase5Info.ActiveSessions,
				newInfo.ActiveSessions,
				"total active sessions should match Phase 5",
			)
			assert.Equal(
				t,
				uint64(40),
				newInfo.ActiveSessions,
				"total active sessions should be 40",
			)

			// Find VS2 info
			var vs2Info *balancerpb.VsInfo
			for _, vs := range newInfo.Vs {
				addr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
				if addr == cfgVs2IP {
					vs2Info = vs
					break
				}
			}
			require.NotNil(t, vs2Info, "VS2 info not found")
			assert.Equal(
				t,
				uint64(25),
				vs2Info.ActiveSessions,
				"VS2 active sessions should match Phase 5",
			)

			// Find VS3 info
			var vs3Info *balancerpb.VsInfo
			for _, vs := range newInfo.Vs {
				addr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
				if addr == cfgVs3IP {
					vs3Info = vs
					break
				}
			}
			require.NotNil(t, vs3Info, "VS3 info not found")
			assert.Equal(
				t,
				uint64(15),
				vs3Info.ActiveSessions,
				"VS3 active sessions should match Phase 5",
			)
		})

		// Verify Sessions match Phase 5
		t.Run("VerifySessionsMatch", func(t *testing.T) {
			newSessions, err := newBalancer.Sessions(ts.Mock.CurrentTime())
			require.NoError(t, err, "failed to get sessions")
			require.NotNil(t, newSessions)

			// Verify same number of sessions
			assert.Equal(
				t,
				len(phase5Sessions),
				len(newSessions),
				"should have same number of sessions as Phase 5",
			)
			assert.Equal(t, 40, len(newSessions), "should have 40 sessions")

			// Count sessions per VS
			vs2Sessions := 0
			vs3Sessions := 0
			for _, session := range newSessions {
				vsAddr, _ := netip.AddrFromSlice(session.VsId.Addr.Bytes)
				switch vsAddr {
				case cfgVs2IP:
					vs2Sessions++
				case cfgVs3IP:
					vs3Sessions++
				}
			}

			assert.Equal(t, 25, vs2Sessions, "VS2 should have 25 sessions")
			assert.Equal(t, 15, vs3Sessions, "VS3 should have 15 sessions")
		})

		// Send new packets through the new balancer
		t.Run("SendNewPackets", func(t *testing.T) {
			// Send 10 packets to VS2 (new client IPs starting at 600)
			t.Log("Sending 10 packets to VS2 through new balancer")
			vs2Packets := sendCfgPacketsToVS(
				t,
				ts,
				cfgVs2IP,
				cfgVs2Port,
				10,
				600,
			)

			// Send 10 packets to VS3 (new client IPs starting at 700)
			t.Log("Sending 10 packets to VS3 through new balancer")
			vs3Packets := sendCfgPacketsToVS(
				t,
				ts,
				cfgVs3IP,
				cfgVs3Port,
				10,
				700,
			)

			// Verify distribution (ROUND_ROBIN: ~3-4 packets per real)
			t.Log("Verifying packet distribution for VS2")
			verifyCfgPacketDistribution(t, vs2Packets, map[netip.Addr]int{
				cfgReal6IP: 4,
				cfgReal7IP: 3,
				cfgReal8IP: 3,
			})

			t.Log("Verifying packet distribution for VS3")
			verifyCfgPacketDistribution(t, vs3Packets, map[netip.Addr]int{
				cfgReal9IP:  4,
				cfgReal10IP: 3,
				cfgReal11IP: 3,
			})
		})

		// Verify Stats updated correctly
		t.Run("VerifyStatsUpdated", func(t *testing.T) {
			newStats, err := newBalancer.Stats(statsRef)
			require.NoError(t, err)
			require.NotNil(t, newStats)

			// Find VS2 stats
			var vs2Stats *balancerpb.NamedVsStats
			for _, vs := range newStats.Vs {
				addr, _ := netip.AddrFromSlice(vs.Vs.Addr.Bytes)
				if addr == cfgVs2IP {
					vs2Stats = vs
					break
				}
			}
			require.NotNil(t, vs2Stats, "VS2 stats not found")

			// Verify VS2 stats: 45 packets (35 from Phase 5 + 10 new)
			assert.Equal(
				t,
				uint64(45),
				vs2Stats.Stats.IncomingPackets,
				"VS2 incoming packets should be 45 (35+10)",
			)
			assert.Equal(
				t,
				uint64(45),
				vs2Stats.Stats.OutgoingPackets,
				"VS2 outgoing packets should be 45 (35+10)",
			)

			// Find VS3 stats
			var vs3Stats *balancerpb.NamedVsStats
			for _, vs := range newStats.Vs {
				addr, _ := netip.AddrFromSlice(vs.Vs.Addr.Bytes)
				if addr == cfgVs3IP {
					vs3Stats = vs
					break
				}
			}
			require.NotNil(t, vs3Stats, "VS3 stats not found")

			// Verify VS3 stats: 25 packets (15 from Phase 5 + 10 new)
			assert.Equal(
				t,
				uint64(25),
				vs3Stats.Stats.IncomingPackets,
				"VS3 incoming packets should be 25 (15+10)",
			)
			assert.Equal(
				t,
				uint64(25),
				vs3Stats.Stats.OutgoingPackets,
				"VS3 outgoing packets should be 25 (15+10)",
			)
		})

		// Verify Info updated correctly
		t.Run("VerifyInfoUpdated", func(t *testing.T) {
			newInfo, err := newBalancer.Info(ts.Mock.CurrentTime())
			require.NoError(t, err)
			require.NotNil(t, newInfo)

			// Verify total active sessions: 60 (40 from Phase 5 + 20 new)
			assert.Equal(
				t,
				uint64(60),
				newInfo.ActiveSessions,
				"total active sessions should be 60 (40+20)",
			)

			// Find VS2 info
			var vs2Info *balancerpb.VsInfo
			for _, vs := range newInfo.Vs {
				addr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
				if addr == cfgVs2IP {
					vs2Info = vs
					break
				}
			}
			require.NotNil(t, vs2Info, "VS2 info not found")
			assert.Equal(
				t,
				uint64(35),
				vs2Info.ActiveSessions,
				"VS2 active sessions should be 35 (25+10)",
			)

			// Find VS3 info
			var vs3Info *balancerpb.VsInfo
			for _, vs := range newInfo.Vs {
				addr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
				if addr == cfgVs3IP {
					vs3Info = vs
					break
				}
			}
			require.NotNil(t, vs3Info, "VS3 info not found")
			assert.Equal(
				t,
				uint64(25),
				vs3Info.ActiveSessions,
				"VS3 active sessions should be 25 (15+10)",
			)
		})

		// Verify Sessions updated correctly
		t.Run("VerifySessionsUpdated", func(t *testing.T) {
			newSessions, err := newBalancer.Sessions(ts.Mock.CurrentTime())
			require.NoError(t, err, "failed to get sessions")
			require.NotNil(t, newSessions)

			// Verify total sessions: 60 (40 from Phase 5 + 20 new)
			assert.Equal(
				t,
				60,
				len(newSessions),
				"should have 60 total sessions (40+20)",
			)

			// Count sessions per VS
			vs2Sessions := 0
			vs3Sessions := 0
			for _, session := range newSessions {
				vsAddr, _ := netip.AddrFromSlice(session.VsId.Addr.Bytes)
				switch vsAddr {
				case cfgVs2IP:
					vs2Sessions++
				case cfgVs3IP:
					vs3Sessions++
				}
			}

			assert.Equal(
				t,
				35,
				vs2Sessions,
				"VS2 should have 35 sessions (25+10)",
			)
			assert.Equal(
				t,
				25,
				vs3Sessions,
				"VS3 should have 25 sessions (15+10)",
			)
		})
	})
}
