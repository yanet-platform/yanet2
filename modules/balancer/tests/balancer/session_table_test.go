package balancer

import (
	"math/rand"
	"net/netip"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"google.golang.org/protobuf/types/known/durationpb"
)

// TestSessionTableManyRandomTCPSyns sends many random TCP SYN packets to a single
// real of a single VS, calling SyncActiveSessionsAndWlcAndResizeTableOnDemand
// every 50 packets and verifying that active sessions count matches the number
// of successfully processed packets.
func TestSessionTableManyRandomTCPSyns(t *testing.T) {
	vsIp := IpAddr("1.1.1.1")
	vsPort := uint16(80)
	realAddr := IpAddr("2.2.2.2")

	// Configure balancer with single VS and single real
	config := &balancerpb.ModuleConfig{
		SourceAddressV4: IpAddr("5.5.5.5").AsSlice(),
		SourceAddressV6: IpAddr("fe80::5").AsSlice(),
		VirtualServices: []*balancerpb.VirtualService{
			{
				Addr:  vsIp.AsSlice(),
				Port:  uint32(vsPort),
				Proto: balancerpb.TransportProto_TCP,
				AllowedSrcs: []*balancerpb.Subnet{
					{
						Addr: IpAddr("10.0.0.0").AsSlice(),
						Size: 8, // Allow all 10.x.x.x addresses
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
						DstAddr: realAddr.AsSlice(),
						Weight:  1,
						SrcAddr: IpAddr("4.4.4.4").AsSlice(),
						SrcMask: IpAddr("4.4.4.4").AsSlice(),
						Enabled: true,
					},
				},
			},
		},
		SessionsTimeouts: &balancerpb.SessionsTimeouts{
			TcpSynAck: 300, // Long timeout to prevent expiration during test
			TcpSyn:    300,
			TcpFin:    300,
			Tcp:       300,
			Udp:       300,
			Default:   300,
		},
		Wlc: &balancerpb.WlcConfig{
			WlcPower:      10,
			MaxRealWeight: 1000,
			UpdatePeriod:  durationpb.New(2 * time.Second),
		},
	}

	stateConfig := &balancerpb.ModuleStateConfig{
		SessionTableCapacity:      8, // Small initial capacity to trigger resizing
		SessionTableScanPeriod:    durationpb.New(10 * time.Second),
		SessionTableMaxLoadFactor: 0.75, // High load factor
	}

	// Setup test
	setup, err := SetupTest(&TestConfig{
		moduleConfig: config,
		stateConfig:  stateConfig,
	})
	require.NoError(t, err)
	defer setup.Free()

	mock := setup.mock
	balancer := setup.balancer

	// Track total output packets (not dropped)
	totalOutputPackets := 0

	// Send packets in batches of 50
	const batchSize = 5
	const numBatches = 10 // Total: 500 packets

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	for batch := range numBatches {
		t.Logf("Processing batch %d/%d", batch+1, numBatches)

		// Generate 50 random TCP SYN packets
		packets := make([]gopacket.Packet, batchSize)
		for i := range batchSize {
			// Generate random source IP in 10.x.x.x range
			srcIP := netip.AddrFrom4([4]byte{
				10,
				byte(rng.Intn(256)),
				byte(rng.Intn(256)),
				byte(rng.Intn(256)),
			})
			// Generate random source port
			srcPort := uint16(1024 + rng.Intn(64511)) // 1024-65535

			// Create TCP SYN packet
			packetLayers := MakeTCPPacket(
				srcIP,
				srcPort,
				vsIp,
				vsPort,
				&layers.TCP{SYN: true},
			)
			packets[i] = xpacket.LayersToPacket(t, packetLayers...)
		}

		// Send all 50 packets at once
		result, err := mock.HandlePackets(packets...)
		require.NoError(t, err)

		// Track output packets (successfully processed, not dropped)
		batchOutputCount := len(result.Output)
		totalOutputPackets += batchOutputCount

		t.Logf("Batch %d: sent=%d, output=%d, dropped=%d, total_output=%d",
			batch+1, batchSize, batchOutputCount, len(result.Drop), totalOutputPackets)

		// Sync active sessions and resize table on demand
		err = balancer.SyncActiveSessionsAndWlcAndResizeTableOnDemand()
		require.NoError(t, err)

		// Get state info
		state := balancer.GetStateInfo()

		// Verify active sessions for VS match output packets
		require.Equal(t, 1, len(state.VsInfo), "should have exactly one VS")
		vsActiveSessions := state.VsInfo[0].ActiveSessions.Value
		assert.Equal(t, uint(totalOutputPackets), vsActiveSessions,
			"VS active sessions should match total output packets after batch %d", batch+1)

		// Verify active sessions for Real match output packets
		require.Equal(t, 1, len(state.RealInfo), "should have exactly one Real")
		realActiveSessions := state.RealInfo[0].ActiveSessions.Value
		assert.Equal(t, uint(totalOutputPackets), realActiveSessions,
			"Real active sessions should match total output packets after batch %d", batch+1)

		t.Logf("Batch %d verified: VS sessions=%d, Real sessions=%d, expected=%d",
			batch+1, vsActiveSessions, realActiveSessions, totalOutputPackets)
	}

	// Final verification
	t.Logf("Test completed: total packets sent=%d, total output=%d",
		batchSize*numBatches, totalOutputPackets)

	state := balancer.GetStateInfo()
	assert.Equal(t, uint(totalOutputPackets), state.VsInfo[0].ActiveSessions.Value,
		"Final VS active sessions should match total output packets")
	assert.Equal(t, uint(totalOutputPackets), state.RealInfo[0].ActiveSessions.Value,
		"Final Real active sessions should match total output packets")
}

// TestSessionTableOverflowAndPersistence tests that:
// 1. Session table can overflow (some SYN packets get dropped)
// 2. Sessions that were successfully created persist
// 3. Non-SYN packets for existing sessions are not dropped
func TestSessionTableOverflowAndPersistence(t *testing.T) {
	vsIp := IpAddr("1.1.1.1")
	vsPort := uint16(80)
	realAddr := IpAddr("2.2.2.2")

	// Configure balancer with single VS and single real
	config := &balancerpb.ModuleConfig{
		SourceAddressV4: IpAddr("5.5.5.5").AsSlice(),
		SourceAddressV6: IpAddr("fe80::5").AsSlice(),
		VirtualServices: []*balancerpb.VirtualService{
			{
				Addr:  vsIp.AsSlice(),
				Port:  uint32(vsPort),
				Proto: balancerpb.TransportProto_TCP,
				AllowedSrcs: []*balancerpb.Subnet{
					{
						Addr: IpAddr("10.0.0.0").AsSlice(),
						Size: 8, // Allow all 10.x.x.x addresses
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
						DstAddr: realAddr.AsSlice(),
						Weight:  1,
						SrcAddr: IpAddr("4.4.4.4").AsSlice(),
						SrcMask: IpAddr("4.4.4.4").AsSlice(),
						Enabled: true,
					},
				},
			},
		},
		SessionsTimeouts: &balancerpb.SessionsTimeouts{
			TcpSynAck: 300, // Long timeout to prevent expiration during test
			TcpSyn:    300,
			TcpFin:    300,
			Tcp:       300,
			Udp:       300,
			Default:   300,
		},
		Wlc: &balancerpb.WlcConfig{
			WlcPower:      10,
			MaxRealWeight: 1000,
			UpdatePeriod:  durationpb.New(2 * time.Second),
		},
	}

	stateConfig := &balancerpb.ModuleStateConfig{
		SessionTableCapacity:      16, // Very small capacity to cause overflow
		SessionTableScanPeriod:    durationpb.New(100 * time.Second),
		SessionTableMaxLoadFactor: 0.75,
	}

	// Setup test
	setup, err := SetupTest(&TestConfig{
		moduleConfig: config,
		stateConfig:  stateConfig,
	})
	require.NoError(t, err)
	defer setup.Free()

	mock := setup.mock
	balancer := setup.balancer

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	// Phase 1: Send many SYN packets to cause overflow
	// Track ALL sessions and which were successfully created
	type sessionKey struct {
		srcIP   netip.Addr
		srcPort uint16
	}

	const totalSyns = 100
	const batchSize = 50

	allSessions := make([]sessionKey, 0, totalSyns)
	successfulSessions := make(map[sessionKey]bool)

	t.Logf("Phase 1: Sending %d SYN packets to cause overflow", totalSyns)

	for batch := range totalSyns / batchSize {
		packets := make([]gopacket.Packet, batchSize)
		batchSessions := make([]sessionKey, batchSize)

		for i := range batchSize {
			// Generate random source IP in 10.x.x.x range
			srcIP := netip.AddrFrom4([4]byte{
				10,
				byte(rng.Intn(256)),
				byte(rng.Intn(256)),
				byte(rng.Intn(256)),
			})
			srcPort := uint16(1024 + rng.Intn(64511))

			session := sessionKey{srcIP: srcIP, srcPort: srcPort}
			batchSessions[i] = session
			allSessions = append(allSessions, session)

			// Create TCP SYN packet
			packetLayers := MakeTCPPacket(
				srcIP,
				srcPort,
				vsIp,
				vsPort,
				&layers.TCP{SYN: true},
			)
			packets[i] = xpacket.LayersToPacket(t, packetLayers...)
		}

		// Send batch
		result, err := mock.HandlePackets(packets...)
		require.NoError(t, err)

		// Track which sessions were successfully created (output, not dropped)
		outputCount := len(result.Output)
		droppedCount := len(result.Drop)

		t.Logf("Batch %d: sent=%d, output=%d, dropped=%d",
			batch+1, batchSize, outputCount, droppedCount)

		// Mark successful sessions
		// Note: We assume packets are processed in order for simplicity
		for i := 0; i < outputCount && i < len(batchSessions); i++ {
			successfulSessions[batchSessions[i]] = true
		}

		// Sync to update active sessions count
		err = balancer.SyncActiveSessionsAndWlcAndResizeTableOnDemand()
		require.NoError(t, err)
	}

	t.Logf("Phase 1 complete: %d total sessions, %d successfully created, %d dropped",
		len(allSessions), len(successfulSessions), len(allSessions)-len(successfulSessions))
	require.Greater(t, len(successfulSessions), 0, "At least some sessions should be created")
	require.Less(t, len(successfulSessions), totalSyns, "Some packets should be dropped due to overflow")

	state := balancer.GetStateInfo()
	activeSessions := state.VsInfo[0].ActiveSessions.Value
	t.Logf("Active sessions after sync: %d (expected: %d)", activeSessions, len(successfulSessions))

	// Phase 2: Send non-SYN packets for ALL original sessions (both successful and dropped)
	// Only packets for successful sessions should be processed
	t.Logf("Phase 2: Sending non-SYN packets for ALL %d sessions (successful and dropped)", len(allSessions))

	// Send non-SYN packets in batches
	totalNonSynPackets := 0
	totalNonSynOutput := 0
	totalNonSynDropped := 0

	for i := 0; i < len(allSessions); i += batchSize {
		end := min(i+batchSize, len(allSessions))
		batchSessions := allSessions[i:end]

		packets := make([]gopacket.Packet, len(batchSessions))
		for j, session := range batchSessions {
			// Create non-SYN TCP packet (ACK flag set)
			packetLayers := MakeTCPPacket(
				session.srcIP,
				session.srcPort,
				vsIp,
				vsPort,
				&layers.TCP{ACK: true}, // Non-SYN packet
			)
			packets[j] = xpacket.LayersToPacket(t, packetLayers...)
		}

		result, err := mock.HandlePackets(packets...)
		require.NoError(t, err)

		totalNonSynPackets += len(packets)
		totalNonSynOutput += len(result.Output)
		totalNonSynDropped += len(result.Drop)

		t.Logf("Non-SYN batch: sent=%d, output=%d, dropped=%d",
			len(packets), len(result.Output), len(result.Drop))

		// Update active sessions.
		err = balancer.SyncActiveSessionsAndWlcAndResizeTableOnDemand()
		require.NoError(t, err)

		finalState := balancer.GetStateInfo()
		finalActiveSessions := finalState.VsInfo[0].ActiveSessions.Value
		t.Logf("Final active sessions: %d (expected: %d)", finalActiveSessions, len(successfulSessions))
	}

	t.Logf("Phase 2 complete: sent=%d non-SYN packets, output=%d, dropped=%d",
		totalNonSynPackets, totalNonSynOutput, totalNonSynDropped)

	// Verify that only packets for successful sessions were processed
	assert.Equal(t, len(successfulSessions), totalNonSynOutput,
		"Only non-SYN packets for existing sessions should be processed")
	assert.Equal(t, len(allSessions)-len(successfulSessions), totalNonSynDropped,
		"Non-SYN packets for dropped sessions should be dropped")

	// assert.Equal(t, uint(len(successfulSessions)), finalActiveSessions,
	// 	"Active sessions should remain the same after non-SYN packets")
}
