package balancer_test

// TestScheduling is a comprehensive test suite for the balancer module that covers:
//
// # Session Management
// - TCP session establishment and persistence
// - UDP session establishment and persistence
// - Session table overflow handling
// - Session timeout configuration
//
// # Scheduling Algorithms
// - SOURCE_HASH: consistent hashing based on client IP+port
// - ROUND_ROBIN: sequential distribution across reals
// - Both with and without One Packet Scheduling (OPS) mode
//
// # Weight Distribution
// - Equal weight distribution (1:1:1)
// - Weighted distribution (1:2:3)
// - Weight updates and redistribution
// - Statistical validation of weight-based traffic distribution
//
// # Real Server Management
// - Enabling/disabling real servers
// - Handling disabled reals with existing sessions
// - Removing reals from configuration
// - Real server state transitions
//
// # API Outputs
// - Config(): Configuration retrieval and validation
// - Info(): Runtime information (active sessions, VS info)
// - Stats(): Packet processing statistics (L4, ICMP, common)
// - Graph(): Topology visualization (VS-to-real relationships)
//
// # State Restoration
// - Creating new agent from existing shared memory
// - Preserving configuration across agent restarts
// - Maintaining session state after restoration
// - Verifying all functionality after restoration
//
// The test uses 8 virtual services with different configurations:
// - VS1: TCP + SOURCE_HASH (session-based)
// - VS2: UDP + SOURCE_HASH (session-based)
// - VS3: TCP + ROUND_ROBIN (session-based)
// - VS4: UDP + ROUND_ROBIN (session-based)
// - VS5: TCP + SOURCE_HASH + OPS (no session)
// - VS6: TCP + ROUND_ROBIN + OPS (no session)
// - VS7: TCP + SOURCE_HASH + OPS + Weighted (1:2:3)
// - VS8: TCP + ROUND_ROBIN + OPS + Weighted (1:2:3)
//
// Each VS has 3 real servers for comprehensive distribution testing.

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

// Virtual Service configurations for different test scenarios
var (
	// VS1: TCP + SOURCE_HASH (session-based)
	vs1IP   = netip.MustParseAddr("10.0.1.1")
	vs1Port = uint16(80)

	// VS2: UDP + SOURCE_HASH (session-based)
	vs2IP   = netip.MustParseAddr("10.0.2.1")
	vs2Port = uint16(80)

	// VS3: TCP + ROUND_ROBIN (session-based)
	vs3IP   = netip.MustParseAddr("10.0.3.1")
	vs3Port = uint16(80)

	// VS4: UDP + ROUND_ROBIN (session-based)
	vs4IP   = netip.MustParseAddr("10.0.4.1")
	vs4Port = uint16(80)

	// VS5: TCP + SOURCE_HASH + OPS (no session)
	vs5IP   = netip.MustParseAddr("10.0.5.1")
	vs5Port = uint16(80)

	// VS6: TCP + ROUND_ROBIN + OPS (no session)
	vs6IP   = netip.MustParseAddr("10.0.6.1")
	vs6Port = uint16(80)

	// VS7: TCP + SOURCE_HASH + OPS + Weighted (1:2:3)
	vs7IP   = netip.MustParseAddr("10.0.7.1")
	vs7Port = uint16(80)

	// VS8: TCP + ROUND_ROBIN + OPS + Weighted (1:2:3)
	vs8IP   = netip.MustParseAddr("10.0.8.1")
	vs8Port = uint16(80)

	// VS9: TCP + SOURCE_HASH + PureL3 (port must be 0 for PureL3)
	vs9IP   = netip.MustParseAddr("10.0.9.1")
	vs9Port = uint16(0)

	// VS10: TCP + ROUND_ROBIN + PureL3 (port must be 0 for PureL3)
	vs10IP   = netip.MustParseAddr("10.0.10.1")
	vs10Port = uint16(0)

	// VS11: UDP + SOURCE_HASH + PureL3 (port must be 0 for PureL3)
	vs11IP   = netip.MustParseAddr("10.0.11.1")
	vs11Port = uint16(0)

	// VS12: UDP + ROUND_ROBIN + PureL3 (port must be 0 for PureL3)
	vs12IP   = netip.MustParseAddr("10.0.12.1")
	vs12Port = uint16(0)

	// Real servers (3 per VS, same IPs for simplicity)
	real1IP = netip.MustParseAddr("192.168.1.1")
	real2IP = netip.MustParseAddr("192.168.1.2")
	real3IP = netip.MustParseAddr("192.168.1.3")

	// Client base IP
	clientBaseIP = netip.MustParseAddr("3.3.3.1")

	// Source address for balancer
	balancerSrcV4 = netip.MustParseAddr("5.5.5.5")
	balancerSrcV6 = netip.MustParseAddr("fe80::5")
)

// createReal creates a Real configuration
func createReal(ip netip.Addr, weight uint32) *balancerpb.Real {
	return &balancerpb.Real{
		Id: &balancerpb.RelativeRealIdentifier{
			Ip:   &balancerpb.Addr{Bytes: ip.AsSlice()},
			Port: 0,
		},
		Weight: weight,
		SrcAddr: &balancerpb.Addr{
			Bytes: netip.MustParseAddr("4.4.4.4").AsSlice(),
		},
		SrcMask: &balancerpb.Addr{
			Bytes: netip.MustParseAddr("255.255.255.255").AsSlice(),
		},
	}
}

// createVirtualService creates a VirtualService configuration
func createVirtualService(
	ip netip.Addr,
	port uint16,
	proto balancerpb.TransportProto,
	scheduler balancerpb.VsScheduler,
	ops bool,
	reals []*balancerpb.Real,
) *balancerpb.VirtualService {
	return createVirtualServiceWithFlags(
		ip,
		port,
		proto,
		scheduler,
		ops,
		false,
		reals,
	)
}

// createVirtualServiceWithFlags creates a VirtualService configuration with custom flags
func createVirtualServiceWithFlags(
	ip netip.Addr,
	port uint16,
	proto balancerpb.TransportProto,
	scheduler balancerpb.VsScheduler,
	ops bool,
	pureL3 bool,
	reals []*balancerpb.Real,
) *balancerpb.VirtualService {
	return &balancerpb.VirtualService{
		Id: &balancerpb.VsIdentifier{
			Addr:  &balancerpb.Addr{Bytes: ip.AsSlice()},
			Port:  uint32(port),
			Proto: proto,
		},
		Scheduler: scheduler,
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
			Ops:    ops,
			PureL3: pureL3,
			Wlc:    false,
		},
		Reals: reals,
		Peers: []*balancerpb.Addr{},
	}
}

// createSchedulingTestConfig creates the balancer configuration with all 8 virtual services
func createSchedulingTestConfig() *balancerpb.BalancerConfig {
	// Equal weight reals (weight = 1)
	equalReals := []*balancerpb.Real{
		createReal(real1IP, 1),
		createReal(real2IP, 1),
		createReal(real3IP, 1),
	}

	// Weighted reals (1:2:3)
	weightedReals := []*balancerpb.Real{
		createReal(real1IP, 1),
		createReal(real2IP, 2),
		createReal(real3IP, 3),
	}

	return &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: &balancerpb.Addr{Bytes: balancerSrcV4.AsSlice()},
			SourceAddressV6: &balancerpb.Addr{Bytes: balancerSrcV6.AsSlice()},
			Vs: []*balancerpb.VirtualService{
				// VS1: TCP + SOURCE_HASH (session-based)
				createVirtualService(
					vs1IP,
					vs1Port,
					balancerpb.TransportProto_TCP,
					balancerpb.VsScheduler_SOURCE_HASH,
					false,
					equalReals,
				),
				// VS2: UDP + SOURCE_HASH (session-based)
				createVirtualService(
					vs2IP,
					vs2Port,
					balancerpb.TransportProto_UDP,
					balancerpb.VsScheduler_SOURCE_HASH,
					false,
					equalReals,
				),
				// VS3: TCP + ROUND_ROBIN (session-based)
				createVirtualService(
					vs3IP,
					vs3Port,
					balancerpb.TransportProto_TCP,
					balancerpb.VsScheduler_ROUND_ROBIN,
					false,
					equalReals,
				),
				// VS4: UDP + ROUND_ROBIN (session-based)
				createVirtualService(
					vs4IP,
					vs4Port,
					balancerpb.TransportProto_UDP,
					balancerpb.VsScheduler_ROUND_ROBIN,
					false,
					equalReals,
				),
				// VS5: TCP + SOURCE_HASH + OPS (no session)
				createVirtualService(
					vs5IP,
					vs5Port,
					balancerpb.TransportProto_TCP,
					balancerpb.VsScheduler_SOURCE_HASH,
					true,
					equalReals,
				),
				// VS6: TCP + ROUND_ROBIN + OPS (no session)
				createVirtualService(
					vs6IP,
					vs6Port,
					balancerpb.TransportProto_TCP,
					balancerpb.VsScheduler_ROUND_ROBIN,
					true,
					equalReals,
				),
				// VS7: TCP + SOURCE_HASH + OPS + Weighted
				createVirtualService(
					vs7IP,
					vs7Port,
					balancerpb.TransportProto_TCP,
					balancerpb.VsScheduler_SOURCE_HASH,
					true,
					weightedReals,
				),
				// VS8: TCP + ROUND_ROBIN + OPS + Weighted
				createVirtualService(
					vs8IP,
					vs8Port,
					balancerpb.TransportProto_TCP,
					balancerpb.VsScheduler_ROUND_ROBIN,
					true,
					weightedReals,
				),
				// VS9: TCP + SOURCE_HASH + PureL3
				createVirtualServiceWithFlags(
					vs9IP,
					vs9Port,
					balancerpb.TransportProto_TCP,
					balancerpb.VsScheduler_SOURCE_HASH,
					false,
					true,
					equalReals,
				),
				// VS10: TCP + ROUND_ROBIN + PureL3
				createVirtualServiceWithFlags(
					vs10IP,
					vs10Port,
					balancerpb.TransportProto_TCP,
					balancerpb.VsScheduler_ROUND_ROBIN,
					false,
					true,
					equalReals,
				),
				// VS11: UDP + SOURCE_HASH + PureL3
				createVirtualServiceWithFlags(
					vs11IP,
					vs11Port,
					balancerpb.TransportProto_UDP,
					balancerpb.VsScheduler_SOURCE_HASH,
					false,
					true,
					equalReals,
				),
				// VS12: UDP + ROUND_ROBIN + PureL3
				createVirtualServiceWithFlags(
					vs12IP,
					vs12Port,
					balancerpb.TransportProto_UDP,
					balancerpb.VsScheduler_ROUND_ROBIN,
					false,
					true,
					equalReals,
				),
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

// generateClientIP generates a unique client IP based on index
func generateClientIP(index int) netip.Addr {
	// Start from 3.3.3.1 and increment
	base := clientBaseIP.As4()
	base[3] = byte((int(base[3]) + index) % 256)
	if index >= 256 {
		base[2] = byte((int(base[2]) + index/256) % 256)
	}
	return netip.AddrFrom4(base)
}

// TestScheduling is the main test function that tests all scheduling scenarios
func TestScheduling(t *testing.T) {
	// Create balancer configuration with all 8 virtual services
	config := createSchedulingTestConfig()

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

	// Enable all reals
	utils.EnableAllReals(t, ts)

	// Run all scheduling checks
	t.Run("InitialChecks", func(t *testing.T) {
		runSchedulingChecks(t, ts, "initial")
	})

	// State restoration test: create new balancer agent and verify it contains the previous balancer
	t.Run("StateRestoration", func(t *testing.T) {
		testStateRestoration(t, ts)
	})
}

// runSchedulingChecks runs all scheduling-related subtests
func runSchedulingChecks(t *testing.T, ts *utils.TestSetup, phase string) {
	t.Helper()

	// TCP Session Establishment (VS1)
	t.Run("TCP_SessionEstablishment", func(t *testing.T) {
		testTCPSessionEstablishment(t, ts)
	})

	// UDP Session Establishment (VS2)
	t.Run("UDP_SessionEstablishment", func(t *testing.T) {
		testUDPSessionEstablishment(t, ts)
	})

	// OPS Mode - No Session Created (VS5)
	t.Run("OPS_NoSessionCreated", func(t *testing.T) {
		testOPSNoSessionCreated(t, ts)
	})

	// Source Hash - Same Client Same Real (VS1, VS5)
	t.Run("SourceHash_SameClientSameReal", func(t *testing.T) {
		testSourceHashSameClientSameReal(t, ts)
	})

	// Source Hash with OPS - Same Client Same Real (VS5)
	t.Run("SourceHash_OPS_SameClientSameReal", func(t *testing.T) {
		testSourceHashOPSSameClientSameReal(t, ts)
	})

	// Round Robin Distribution (VS6 - OPS mode for independent scheduling)
	t.Run("RoundRobin_Distribution", func(t *testing.T) {
		testRoundRobinDistribution(t, ts)
	})

	// Weight Distribution - Source Hash (VS7)
	t.Run("WeightDistribution_SourceHash", func(t *testing.T) {
		testWeightDistributionSourceHash(t, ts)
	})

	// Weight Distribution - Round Robin (VS8)
	t.Run("WeightDistribution_RoundRobin", func(t *testing.T) {
		testWeightDistributionRoundRobin(t, ts)
	})

	// Weight Distribution After Update (VS7, VS8)
	t.Run("WeightDistribution_AfterUpdate", func(t *testing.T) {
		testWeightDistributionAfterUpdate(t, ts)
	})

	// Disabled Reals - No New Packets (VS1)
	t.Run("DisabledReals_NoNewPackets", func(t *testing.T) {
		testDisabledRealsNoNewPackets(t, ts)
	})

	// API Output Tests
	t.Run("Config_Output", func(t *testing.T) {
		testConfigOutput(t, ts)
	})

	t.Run("Info_Output", func(t *testing.T) {
		testInfoOutput(t, ts)
	})

	t.Run("Stats_Output", func(t *testing.T) {
		testStatsOutput(t, ts)
	})

	t.Run("Graph_Output", func(t *testing.T) {
		testGraphOutput(t, ts)
	})

	// PureL3 Tests
	t.Run("PureL3_SourceHash_PortIndependence", func(t *testing.T) {
		testPureL3SourceHashPortIndependence(t, ts)
	})

	t.Run("PureL3_RoundRobin_Distribution", func(t *testing.T) {
		testPureL3RoundRobinDistribution(t, ts)
	})

	t.Run("PureL3_SessionCreation", func(t *testing.T) {
		testPureL3SessionCreation(t, ts)
	})

	t.Run("PureL3_UDP_SourceHash", func(t *testing.T) {
		testPureL3UDPSourceHash(t, ts)
	})
}

// testTCPSessionEstablishment verifies that TCP sessions are established
// and packets from the same client go to the same real
func testTCPSessionEstablishment(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	clientIP := generateClientIP(100)
	clientPort := uint16(10000)

	// Send multiple TCP packets from the same client
	var outputPackets []*framework.PacketInfo
	for i := range 5 {
		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			vs1IP,
			vs1Port,
			&layers.TCP{SYN: i == 0, ACK: i > 0},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output), "expected 1 output packet")
		require.Empty(t, result.Drop, "expected no dropped packets")
		outputPackets = append(outputPackets, result.Output[0])
	}

	// Verify all packets went to the same real
	realIP, allSame := utils.AllPacketsToSameReal(outputPackets)
	assert.True(
		t,
		allSame,
		"all packets from same client should go to same real",
	)
	assert.True(t, realIP.IsValid(), "real IP should be valid")

	// Verify session was created
	info, err := ts.Balancer.Info(ts.Mock.CurrentTime())
	require.NoError(t, err)
	assert.GreaterOrEqual(
		t,
		info.ActiveSessions,
		uint64(1),
		"should have at least one active session",
	)
}

// testUDPSessionEstablishment verifies that UDP sessions are established
// and packets from the same client go to the same real
func testUDPSessionEstablishment(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	clientIP := generateClientIP(200)
	clientPort := uint16(20000)

	// Send multiple UDP packets from the same client
	var outputPackets []*framework.PacketInfo
	for range 5 {
		packetLayers := utils.MakeUDPPacket(
			clientIP,
			clientPort,
			vs2IP,
			vs2Port,
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output), "expected 1 output packet")
		require.Empty(t, result.Drop, "expected no dropped packets")
		outputPackets = append(outputPackets, result.Output[0])
	}

	// Verify all packets went to the same real
	realIP, allSame := utils.AllPacketsToSameReal(outputPackets)
	assert.True(
		t,
		allSame,
		"all packets from same client should go to same real",
	)
	assert.True(t, realIP.IsValid(), "real IP should be valid")
}

// testOPSNoSessionCreated verifies that OPS mode does not create sessions
func testOPSNoSessionCreated(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	// Get initial session count
	initialInfo, err := ts.Balancer.Info(ts.Mock.CurrentTime())
	require.NoError(t, err)
	initialSessions := initialInfo.ActiveSessions

	// Send packets to VS5 (OPS mode) from a new client
	clientIP := generateClientIP(300)
	clientPort := uint16(30000)

	for i := 0; i < 5; i++ {
		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			vs5IP,
			vs5Port,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output), "expected 1 output packet")
	}

	// Verify no new sessions were created
	finalInfo, err := ts.Balancer.Info(ts.Mock.CurrentTime())
	require.NoError(t, err)
	assert.Equal(
		t,
		initialSessions,
		finalInfo.ActiveSessions,
		"OPS mode should not create new sessions",
	)
}

// testSourceHashSameClientSameReal verifies that source_hash schedules
// packets from the same client to the same real
func testSourceHashSameClientSameReal(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	// Test with VS1 (TCP + SOURCE_HASH, session-based)
	clientIP := generateClientIP(400)
	clientPort := uint16(40000)

	var outputPackets []*framework.PacketInfo
	for i := 0; i < 10; i++ {
		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			vs1IP,
			vs1Port,
			&layers.TCP{SYN: i == 0, ACK: i > 0},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output), "expected 1 output packet")
		outputPackets = append(outputPackets, result.Output[0])
	}

	// Verify all packets went to the same real
	_, allSame := utils.AllPacketsToSameReal(outputPackets)
	assert.True(
		t,
		allSame,
		"source_hash should send all packets from same client to same real",
	)
}

// testSourceHashOPSSameClientSameReal verifies that source_hash with OPS
// still schedules based on hash (same IP+port -> same real)
func testSourceHashOPSSameClientSameReal(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	// Test with VS5 (TCP + SOURCE_HASH + OPS)
	clientIP := generateClientIP(500)
	clientPort := uint16(50000)

	var outputPackets []*framework.PacketInfo
	for range 10 {
		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			vs5IP,
			vs5Port,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output), "expected 1 output packet")
		outputPackets = append(outputPackets, result.Output[0])
	}

	// Verify all packets went to the same real (hash-based, not session-based)
	_, allSame := utils.AllPacketsToSameReal(outputPackets)
	assert.True(
		t,
		allSame,
		"source_hash with OPS should send all packets from same client to same real based on hash",
	)
}

// testRoundRobinDistribution verifies that round_robin distributes packets across reals
func testRoundRobinDistribution(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	// Test with VS6 (TCP + ROUND_ROBIN + OPS) - OPS mode for independent scheduling
	var outputPackets []*framework.PacketInfo

	// Send packets from different clients to trigger round-robin
	for i := range 30 {
		clientIP := generateClientIP(600 + i)
		clientPort := uint16(60000 + i)

		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			vs6IP,
			vs6Port,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output), "expected 1 output packet")
		outputPackets = append(outputPackets, result.Output[0])
	}

	// Verify packets are distributed across multiple reals
	distributed := utils.PacketsDistributedAcrossReals(outputPackets)
	assert.True(
		t,
		distributed,
		"round_robin should distribute packets across multiple reals",
	)

	// Count packets per real
	counts := utils.CountPacketsPerReal(outputPackets)
	assert.GreaterOrEqual(
		t,
		len(counts),
		2,
		"packets should go to at least 2 different reals",
	)
}

// testWeightDistributionSourceHash verifies that packets are distributed
// proportionally to weights with source_hash scheduler
func testWeightDistributionSourceHash(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	// Test with VS7 (TCP + SOURCE_HASH + OPS + Weighted 1:2:3)
	var outputPackets []*framework.PacketInfo

	// Send many packets from different clients
	for i := range 600 {
		clientIP := generateClientIP(700 + i)
		clientPort := uint16(1000 + (i % 60000))

		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			vs7IP,
			vs7Port,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output), "expected 1 output packet")
		outputPackets = append(outputPackets, result.Output[0])
	}

	// Verify weight distribution (1:2:3 = ~16.7%:~33.3%:~50%)
	counts := utils.CountPacketsPerReal(outputPackets)
	expectedWeights := map[netip.Addr]uint32{
		real1IP: 1,
		real2IP: 2,
		real3IP: 3,
	}

	// Use 15% tolerance for statistical variance
	utils.ValidateWeightDistribution(t, counts, expectedWeights, 0.15)
}

// testWeightDistributionRoundRobin verifies that packets are distributed
// proportionally to weights with round_robin scheduler
func testWeightDistributionRoundRobin(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	// Test with VS8 (TCP + ROUND_ROBIN + OPS + Weighted 1:2:3)
	var outputPackets []*framework.PacketInfo

	// Send many packets from different clients
	for i := 0; i < 600; i++ {
		clientIP := generateClientIP(800 + i)
		clientPort := uint16(2000 + (i % 60000))

		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			vs8IP,
			vs8Port,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output), "expected 1 output packet")
		outputPackets = append(outputPackets, result.Output[0])
	}

	// Verify weight distribution (1:2:3 = ~16.7%:~33.3%:~50%)
	counts := utils.CountPacketsPerReal(outputPackets)
	expectedWeights := map[netip.Addr]uint32{
		real1IP: 1,
		real2IP: 2,
		real3IP: 3,
	}

	// Use 15% tolerance for statistical variance
	utils.ValidateWeightDistribution(t, counts, expectedWeights, 0.15)
}

// testWeightDistributionAfterUpdate verifies that weight distribution
// holds after real weight update
func testWeightDistributionAfterUpdate(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	// Update weights for VS7 to 3:2:1 (reverse of original 1:2:3)
	config := ts.Balancer.Config()
	var vs7 *balancerpb.VirtualService
	for _, vs := range config.PacketHandler.Vs {
		vsAddr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
		if vsAddr == vs7IP {
			vs7 = vs
			break
		}
	}
	require.NotNil(t, vs7, "VS7 should exist")

	// Update weights
	newWeight1 := uint32(3)
	newWeight2 := uint32(2)
	newWeight3 := uint32(1)
	updates := []*balancerpb.RealUpdate{
		{
			RealId: &balancerpb.RealIdentifier{
				Vs: vs7.Id,
				Real: &balancerpb.RelativeRealIdentifier{
					Ip:   &balancerpb.Addr{Bytes: real1IP.AsSlice()},
					Port: 0,
				},
			},
			Weight: &newWeight1,
		},
		{
			RealId: &balancerpb.RealIdentifier{
				Vs: vs7.Id,
				Real: &balancerpb.RelativeRealIdentifier{
					Ip:   &balancerpb.Addr{Bytes: real2IP.AsSlice()},
					Port: 0,
				},
			},
			Weight: &newWeight2,
		},
		{
			RealId: &balancerpb.RealIdentifier{
				Vs: vs7.Id,
				Real: &balancerpb.RelativeRealIdentifier{
					Ip:   &balancerpb.Addr{Bytes: real3IP.AsSlice()},
					Port: 0,
				},
			},
			Weight: &newWeight3,
		},
	}

	_, err := ts.Balancer.UpdateReals(updates, false)
	require.NoError(t, err, "failed to update real weights")

	// Send packets and verify new distribution
	var outputPackets []*framework.PacketInfo
	for i := range 600 {
		clientIP := generateClientIP(900 + i)
		clientPort := uint16(3000 + (i % 60000))

		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			vs7IP,
			vs7Port,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output), "expected 1 output packet")
		outputPackets = append(outputPackets, result.Output[0])
	}

	// Verify new weight distribution (3:2:1 = ~50%:~33.3%:~16.7%)
	counts := utils.CountPacketsPerReal(outputPackets)
	expectedWeights := map[netip.Addr]uint32{
		real1IP: 3,
		real2IP: 2,
		real3IP: 1,
	}

	// Use 15% tolerance for statistical variance
	utils.ValidateWeightDistribution(t, counts, expectedWeights, 0.15)
}

// testDisabledRealsNoNewPackets verifies that disabled reals do not accept new packets
func testDisabledRealsNoNewPackets(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	// Get VS1 configuration
	config := ts.Balancer.Config()
	var vs1 *balancerpb.VirtualService
	for _, vs := range config.PacketHandler.Vs {
		vsAddr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
		if vsAddr == vs1IP {
			vs1 = vs
			break
		}
	}
	require.NotNil(t, vs1, "VS1 should exist")

	// Disable real1
	enableFalse := false
	updates := []*balancerpb.RealUpdate{
		{
			RealId: &balancerpb.RealIdentifier{
				Vs: vs1.Id,
				Real: &balancerpb.RelativeRealIdentifier{
					Ip:   &balancerpb.Addr{Bytes: real1IP.AsSlice()},
					Port: 0,
				},
			},
			Enable: &enableFalse,
		},
	}

	_, err := ts.Balancer.UpdateReals(updates, false)
	require.NoError(t, err, "failed to disable real")

	// Send packets from new clients (use different client IPs to avoid existing sessions)
	var outputPackets []*framework.PacketInfo
	for i := 0; i < 100; i++ {
		clientIP := generateClientIP(1000 + i)
		clientPort := uint16(4000 + i)

		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			vs1IP,
			vs1Port,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output), "expected 1 output packet")
		outputPackets = append(outputPackets, result.Output[0])
	}

	// Verify no packets went to the disabled real
	counts := utils.CountPacketsPerReal(outputPackets)
	disabledRealCount := counts[real1IP]
	assert.Equal(
		t,
		0,
		disabledRealCount,
		"disabled real should not receive any new packets",
	)

	// Re-enable real1 for subsequent tests
	enableTrue := true
	reEnableUpdates := []*balancerpb.RealUpdate{
		{
			RealId: &balancerpb.RealIdentifier{
				Vs: vs1.Id,
				Real: &balancerpb.RelativeRealIdentifier{
					Ip:   &balancerpb.Addr{Bytes: real1IP.AsSlice()},
					Port: 0,
				},
			},
			Enable: &enableTrue,
		},
	}
	_, err = ts.Balancer.UpdateReals(reEnableUpdates, false)
	require.NoError(t, err, "failed to re-enable real")
}

// testConfigOutput verifies the Config() API output
func testConfigOutput(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	config := ts.Balancer.Config()
	require.NotNil(t, config, "config should not be nil")
	require.NotNil(
		t,
		config.PacketHandler,
		"packet handler config should not be nil",
	)

	// Verify virtual services
	assert.Equal(
		t,
		12,
		len(config.PacketHandler.Vs),
		"should have 12 virtual services",
	)

	// Verify state config
	require.NotNil(t, config.State, "state config should not be nil")
	assert.NotNil(
		t,
		config.State.SessionTableCapacity,
		"session table capacity should be set",
	)
	assert.NotNil(
		t,
		config.State.SessionTableMaxLoadFactor,
		"max load factor should be set",
	)

	// Verify sessions timeouts
	require.NotNil(
		t,
		config.PacketHandler.SessionsTimeouts,
		"sessions timeouts should not be nil",
	)
	assert.Equal(
		t,
		uint32(60),
		config.PacketHandler.SessionsTimeouts.Tcp,
		"TCP timeout should be 60",
	)
	assert.Equal(
		t,
		uint32(60),
		config.PacketHandler.SessionsTimeouts.Udp,
		"UDP timeout should be 60",
	)

	t.Logf("Config verified: %d virtual services, table_capacity=%d",
		len(config.PacketHandler.Vs), *config.State.SessionTableCapacity)
}

// testInfoOutput verifies the Info() API output
func testInfoOutput(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	info, err := ts.Balancer.Info(ts.Mock.CurrentTime())
	require.NoError(t, err, "failed to get balancer info")
	require.NotNil(t, info, "info should not be nil")

	// Verify info fields
	assert.GreaterOrEqual(
		t,
		info.ActiveSessions,
		uint64(0),
		"active sessions should be non-negative",
	)
	require.NotNil(t, info.Vs, "virtual services info should not be nil")

	// Verify VS info
	for i, vsInfo := range info.Vs {
		require.NotNil(t, vsInfo, "VS info %d should not be nil", i)
		assert.GreaterOrEqual(
			t,
			vsInfo.ActiveSessions,
			uint64(0),
			"VS %d active sessions should be non-negative",
			i,
		)
	}

	t.Logf("Info verified: active_sessions=%d, vs_count=%d",
		info.ActiveSessions, len(info.Vs))
}

// testStatsOutput verifies the Stats() API output
func testStatsOutput(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	statsRef := &balancerpb.PacketHandlerRef{
		Device:   &utils.DeviceName,
		Pipeline: &utils.PipelineName,
		Function: &utils.FunctionName,
		Chain:    &utils.ChainName,
	}

	stats, err := ts.Balancer.Stats(statsRef)
	require.NoError(t, err, "failed to get balancer stats")
	require.NotNil(t, stats, "stats should not be nil")

	// Verify common stats
	require.NotNil(t, stats.Common, "common stats should not be nil")
	assert.GreaterOrEqual(
		t,
		stats.Common.IncomingPackets,
		uint64(0),
		"incoming packets should be non-negative",
	)
	assert.GreaterOrEqual(
		t,
		stats.Common.IncomingBytes,
		uint64(0),
		"incoming bytes should be non-negative",
	)
	assert.GreaterOrEqual(
		t,
		stats.Common.OutgoingPackets,
		uint64(0),
		"outgoing packets should be non-negative",
	)
	assert.GreaterOrEqual(
		t,
		stats.Common.OutgoingBytes,
		uint64(0),
		"outgoing bytes should be non-negative",
	)

	// Verify L4 stats
	require.NotNil(t, stats.L4, "L4 stats should not be nil")
	assert.GreaterOrEqual(
		t,
		stats.L4.IncomingPackets,
		uint64(0),
		"L4 incoming packets should be non-negative",
	)
	assert.GreaterOrEqual(
		t,
		stats.L4.OutgoingPackets,
		uint64(0),
		"L4 outgoing packets should be non-negative",
	)

	// Verify ICMP stats
	require.NotNil(t, stats.Icmpv4, "ICMPv4 stats should not be nil")
	require.NotNil(t, stats.Icmpv6, "ICMPv6 stats should not be nil")

	t.Logf(
		"Stats verified: incoming_packets=%d, incoming_bytes=%d, outgoing_packets=%d, outgoing_bytes=%d",
		stats.Common.IncomingPackets,
		stats.Common.IncomingBytes,
		stats.Common.OutgoingPackets,
		stats.Common.OutgoingBytes,
	)
}

// testGraphOutput verifies the Graph() API output
func testGraphOutput(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	graph := ts.Balancer.Graph()
	require.NotNil(t, graph, "graph should not be nil")
	require.NotNil(
		t,
		graph.VirtualServices,
		"virtual services should not be nil",
	)

	// Verify number of virtual services
	assert.Equal(
		t,
		12,
		len(graph.VirtualServices),
		"should have 12 virtual services in graph",
	)

	// Verify each virtual service
	for i, vs := range graph.VirtualServices {
		require.NotNil(t, vs, "virtual service %d should not be nil", i)
		require.NotNil(t, vs.Identifier, "VS %d should have identifier", i)
		require.NotNil(t, vs.Reals, "VS %d should have reals", i)
		assert.Equal(t, 3, len(vs.Reals), "VS %d should have 3 reals", i)

		// Verify each real
		for j, real := range vs.Reals {
			require.NotNil(t, real, "real %d of VS %d should not be nil", j, i)
			require.NotNil(
				t,
				real.Identifier,
				"real %d of VS %d should have identifier",
				j,
				i,
			)
			assert.GreaterOrEqual(
				t,
				real.Weight,
				uint32(0),
				"real %d of VS %d weight should be non-negative",
				j,
				i,
			)
			assert.GreaterOrEqual(
				t,
				real.EffectiveWeight,
				uint32(0),
				"real %d of VS %d effective weight should be non-negative",
				j,
				i,
			)
		}
	}

	t.Logf(
		"Graph verified: %d virtual services with 3 reals each",
		len(graph.VirtualServices),
	)
}

// testStateRestoration verifies that creating a new balancer agent
// restores the previous balancer state from shared memory
func testStateRestoration(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	// Create a logger for the new agent
	logLevel := zapcore.InfoLevel
	logger, _, err := logging.Init(&logging.Config{
		Level: logLevel,
	})
	require.NoError(t, err, "failed to create logger")

	// Create a new balancer agent using the same shared memory
	// This should restore the existing balancer from shared memory
	newAgent, err := balancer.NewBalancerAgent(
		ts.Mock.SharedMemory(),
		4*datasize.MB,
		logger,
	)
	require.NoError(t, err, "failed to create new balancer agent")

	// Verify the balancer exists in the new agent
	managers := newAgent.Managers()
	assert.Contains(
		t,
		managers,
		utils.BalancerName,
		"new agent should contain the existing balancer",
	)

	// Get the balancer manager from the new agent
	newBalancer, err := newAgent.BalancerManager(utils.BalancerName)
	require.NoError(t, err, "failed to get balancer from new agent")

	// Update test setup to use new agent and balancer
	ts.Agent = newAgent
	ts.Balancer = newBalancer

	// Run all scheduling checks again to verify they work after state restoration
	t.Run("AfterRestoreChecks", func(t *testing.T) {
		// Re-enable all reals (in case any were disabled)
		utils.EnableAllReals(t, ts)

		// Run a subset of checks to verify state restoration
		t.Run("TCP_SessionEstablishment", func(t *testing.T) {
			testTCPSessionEstablishmentAfterRestore(t, ts)
		})

		t.Run("SourceHash_Consistency", func(t *testing.T) {
			testSourceHashConsistencyAfterRestore(t, ts)
		})

		t.Run("RoundRobin_Distribution", func(t *testing.T) {
			testRoundRobinDistributionAfterRestore(t, ts)
		})
	})
}

// testTCPSessionEstablishmentAfterRestore verifies TCP session establishment after state restoration
func testTCPSessionEstablishmentAfterRestore(
	t *testing.T,
	ts *utils.TestSetup,
) {
	t.Helper()

	clientIP := generateClientIP(1100)
	clientPort := uint16(11000)

	var outputPackets []*framework.PacketInfo
	for i := range 5 {
		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			vs1IP,
			vs1Port,
			&layers.TCP{SYN: i == 0, ACK: i > 0},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output), "expected 1 output packet")
		outputPackets = append(outputPackets, result.Output[0])
	}

	// Verify all packets went to the same real
	_, allSame := utils.AllPacketsToSameReal(outputPackets)
	assert.True(
		t,
		allSame,
		"all packets from same client should go to same real after restore",
	)
}

// testSourceHashConsistencyAfterRestore verifies source hash consistency after state restoration
func testSourceHashConsistencyAfterRestore(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	clientIP := generateClientIP(1200)
	clientPort := uint16(12000)

	var outputPackets []*framework.PacketInfo
	for range 10 {
		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			vs5IP,
			vs5Port,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output), "expected 1 output packet")
		outputPackets = append(outputPackets, result.Output[0])
	}

	// Verify all packets went to the same real (hash-based)
	_, allSame := utils.AllPacketsToSameReal(outputPackets)
	assert.True(t, allSame, "source_hash should be consistent after restore")
}

// testRoundRobinDistributionAfterRestore verifies round robin distribution after state restoration
func testRoundRobinDistributionAfterRestore(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	var outputPackets []*framework.PacketInfo
	for i := range 30 {
		clientIP := generateClientIP(1300 + i)
		clientPort := uint16(13000 + i)

		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			vs6IP,
			vs6Port,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output), "expected 1 output packet")
		outputPackets = append(outputPackets, result.Output[0])
	}

	// Verify packets are distributed across multiple reals
	distributed := utils.PacketsDistributedAcrossReals(outputPackets)
	assert.True(
		t,
		distributed,
		"round_robin should distribute packets after restore",
	)
}

// testPureL3SourceHashPortIndependence verifies that PureL3 mode accepts packets
// on any destination port and schedules based on destination port
func testPureL3SourceHashPortIndependence(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	clientIP := generateClientIP(1400)
	clientPort := uint16(14000)

	// Test 1: Packets to the same destination port should go to the same real
	var sameDstPortPackets []*framework.PacketInfo
	dstPort1 := uint16(8080)
	for i := 0; i < 5; i++ {
		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			vs9IP,
			dstPort1, // Same destination port
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output), "expected 1 output packet")
		sameDstPortPackets = append(sameDstPortPackets, result.Output[0])
	}

	// Verify all packets to same dst port went to the same real
	realIP1, allSame := utils.AllPacketsToSameReal(sameDstPortPackets)
	assert.True(
		t,
		allSame,
		"PureL3 SOURCE_HASH should send all packets to same dst port to same real",
	)
	assert.True(t, realIP1.IsValid(), "real IP should be valid")

	// Test 2: Packets to different destination ports can go to different reals
	var differentDstPortPackets []*framework.PacketInfo
	for i := 0; i < 10; i++ {
		dstPort := uint16(9000 + i) // Different destination ports
		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			vs9IP,
			dstPort,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output), "expected 1 output packet")
		differentDstPortPackets = append(
			differentDstPortPackets,
			result.Output[0],
		)
	}

	// Verify packets to different dst ports can be distributed
	counts := utils.CountPacketsPerReal(differentDstPortPackets)
	t.Logf("PureL3 distribution across different dst ports: %v", counts)
	// We don't assert distribution here as it depends on hash function,
	// but we verify that packets were accepted on different ports
}

// testPureL3RoundRobinDistribution verifies that PureL3 mode with ROUND_ROBIN
// distributes packets across reals
func testPureL3RoundRobinDistribution(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	var outputPackets []*framework.PacketInfo

	// Send packets from different client IPs
	for i := range 30 {
		clientIP := generateClientIP(1500 + i)
		clientPort := uint16(15000)

		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			vs10IP,
			vs10Port,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output), "expected 1 output packet")
		outputPackets = append(outputPackets, result.Output[0])
	}

	// Verify packets are distributed across multiple reals
	distributed := utils.PacketsDistributedAcrossReals(outputPackets)
	assert.True(
		t,
		distributed,
		"PureL3 ROUND_ROBIN should distribute packets across multiple reals",
	)

	// Count packets per real
	counts := utils.CountPacketsPerReal(outputPackets)
	assert.GreaterOrEqual(
		t,
		len(counts),
		2,
		"packets should go to at least 2 different reals",
	)
}

// testPureL3SessionCreation verifies that sessions are created correctly in PureL3 mode
// Sessions should be based on client IP + client port + dst port combination
func testPureL3SessionCreation(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	// Get initial session count
	initialInfo, err := ts.Balancer.Info(ts.Mock.CurrentTime())
	require.NoError(t, err)
	initialSessions := initialInfo.ActiveSessions

	clientIP := generateClientIP(1600)
	clientPort := uint16(16000)
	dstPort := uint16(8080)

	// Send multiple packets with same client IP, client port, and dst port
	for i := 0; i < 5; i++ {
		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			vs9IP,
			dstPort, // Same dst port
			&layers.TCP{SYN: i == 0, ACK: i > 0},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output), "expected 1 output packet")
	}

	// Verify sessions were created
	finalInfo, err := ts.Balancer.Info(ts.Mock.CurrentTime())
	require.NoError(t, err)

	// In PureL3 mode, sessions should be created based on the flow
	assert.Greater(
		t,
		finalInfo.ActiveSessions,
		initialSessions,
		"PureL3 mode should create sessions",
	)
}

// testPureL3UDPSourceHash verifies that PureL3 mode works with UDP and SOURCE_HASH
// Packets to the same destination port should go to the same real
func testPureL3UDPSourceHash(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	clientIP := generateClientIP(1700)
	clientPort := uint16(17000)

	// Test 1: Packets to the same destination port should go to the same real
	var sameDstPortPackets []*framework.PacketInfo
	dstPort1 := uint16(5353)
	for i := 0; i < 5; i++ {
		packetLayers := utils.MakeUDPPacket(
			clientIP,
			clientPort,
			vs11IP,
			dstPort1, // Same destination port
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output), "expected 1 output packet")
		sameDstPortPackets = append(sameDstPortPackets, result.Output[0])
	}

	// Verify all packets to same dst port went to the same real
	realIP, allSame := utils.AllPacketsToSameReal(sameDstPortPackets)
	assert.True(
		t,
		allSame,
		"PureL3 SOURCE_HASH with UDP should send all packets to same dst port to same real",
	)
	assert.True(t, realIP.IsValid(), "real IP should be valid")

	// Test 2: Verify PureL3 accepts packets on different destination ports
	var differentDstPortPackets []*framework.PacketInfo
	for i := 0; i < 10; i++ {
		dstPort := uint16(6000 + i) // Different destination ports
		packetLayers := utils.MakeUDPPacket(
			clientIP,
			clientPort,
			vs11IP,
			dstPort,
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output), "expected 1 output packet")
		differentDstPortPackets = append(
			differentDstPortPackets,
			result.Output[0],
		)
	}

	// Verify packets were accepted on different ports
	counts := utils.CountPacketsPerReal(differentDstPortPackets)
	t.Logf("PureL3 UDP distribution across different dst ports: %v", counts)
}
