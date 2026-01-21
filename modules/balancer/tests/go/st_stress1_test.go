package balancer_test

// TestSessionTableStress1 performs comprehensive stress testing of the session table:
//
// # Test Configuration
// - Single virtual service with one real server
// - Session timeout: 64 seconds
// - Initial capacity: 16 (dynamically resized during test)
// - Max load factor: 0.5
//
// # Test Scenarios
// Multiple test cases with varying parameters:
// - Small batches with long intervals: 10 packets × 500 batches
// - Small batches with short intervals: 10 packets × 4 batches
// - Large batches with long intervals: 500 packets × 10 batches
// - Large batches with short intervals: 500 packets × 10 batches
//
// # Validation Per Batch
// - Sends random TCP SYN packets from unique client IPs
// - Randomly refreshes 1/3 of existing sessions with non-SYN packets
// - Verifies packet acceptance rate (drop rate < 10%)
// - Validates active session counts match expectations
// - Confirms session table resizes dynamically as needed
// - Advances time and removes expired sessions from tracking
//
// # Session Expiration Testing
// - Tracks session creation and last packet times
// - Removes sessions from tracking after timeout period
// - Verifies balancer's active session counts match tracked sessions
// - Validates VS and Real active session counts are consistent
//
// # Performance Metrics
// - Monitors drop rates across all batches
// - Tracks failed active session insertions
// - Ensures acceptable performance under load (< 10% failure rate)

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

////////////////////////////////////////////////////////////////////////////////

type testCase struct {
	batchSize      int
	numBatches     int
	timeoutBatches int
}

type stressConfig struct {
	ts       *utils.TestSetup
	rng      *rand.Rand
	vsIp     netip.Addr
	vsPort   uint16
	realAddr netip.Addr
	timeouts int
}

func executeTestCase(t *testing.T, config *stressConfig, test *testCase) {
	// Track sessions by their creation time to calculate expected active sessions
	type sessionKey struct {
		ip   netip.Addr
		port uint16
	}
	activeSessions := make(
		map[sessionKey]time.Time,
	) // maps session to its last packet time

	totalDropped := 0
	totalOutput := 0
	totalPackets := 0

	advanceTimePerBatch := time.Duration(
		config.timeouts/test.timeoutBatches+1,
	) * time.Second

	// Get initial capacity
	initialConfig := config.ts.Balancer.Config()
	require.NotNil(t, initialConfig.State)
	require.NotNil(t, initialConfig.State.SessionTableCapacity)
	initialCapacity := *initialConfig.State.SessionTableCapacity

	t.Logf(
		"Test batchSize=%d, numBatches=%d, timeoutBatches=%d, advanceTimePerBatch=%.1fs, sessionsTimeouts=%.1fs, sessionTableCapacity=%d",
		test.batchSize,
		test.numBatches,
		test.timeoutBatches,
		advanceTimePerBatch.Seconds(),
		float32(config.timeouts),
		initialCapacity,
	)

	// Verify initial state is valid

	t.Run("Verify_Initial_State", func(t *testing.T) {
		currentTime := config.ts.Mock.CurrentTime()

		// Verify sessions
		sessions, err := config.ts.Balancer.Sessions(currentTime)
		require.NoError(t, err, "failed to get sessions")
		assert.Equal(t, 0, len(sessions))

		// Get info
		info, err := config.ts.Balancer.Info(currentTime)
		require.NoError(t, err)

		// Verify module state
		assert.Equal(t, uint64(0), info.ActiveSessions)

		// Verify virtual services active sessions
		require.Equal(t, 1, len(info.Vs), "should have exactly one VS")
		assert.Equal(t, uint64(0), info.Vs[0].ActiveSessions)

		// Verify real services active sessions
		require.Equal(
			t,
			1,
			len(info.Vs[0].Reals),
			"should have exactly one Real",
		)
		assert.Equal(t, uint64(0), info.Vs[0].Reals[0].ActiveSessions)
	})

	// Generate and send packets

	t.Run("Send_Packets", func(t *testing.T) {
		logPeriod := (test.numBatches + 9) / 10

		// It is possible only if
		// there is no enough time to
		// extend table, which is very rare
		// in production.
		failedToFindActiveSession := 0
		findActiveSessions := 0

		for batch := range test.numBatches {
			// Generate `batchSize` random TCP SYN packets
			// (with possible repetitions)
			packets := make(
				[]gopacket.Packet,
				0,
				test.batchSize+len(activeSessions)/2,
			)
			for range test.batchSize {
				// Generate random source IP in 10.x.x.x range
				srcIP := netip.AddrFrom4([4]byte{
					10,
					byte(config.rng.Intn(256)),
					byte(config.rng.Intn(256)),
					byte(config.rng.Intn(256)),
				})

				// Generate random source port
				srcPort := uint16(1024 + config.rng.Intn(64511)) // 1024-65535

				// Create TCP SYN packet
				packetLayers := utils.MakeTCPPacket(
					srcIP,
					srcPort,
					config.vsIp,
					config.vsPort,
					&layers.TCP{SYN: true},
				)
				packets = append(
					packets,
					xpacket.LayersToPacket(t, packetLayers...),
				)
			}

			prolongedSessions := make([]sessionKey, 0, len(activeSessions)/2)
			for activeSession := range activeSessions {
				if config.rng.Intn(3) == 0 { // prolong with 1/3 probability
					packetLayers := utils.MakeTCPPacket(
						activeSession.ip,
						activeSession.port,
						config.vsIp,
						config.vsPort,
						&layers.TCP{},
					)
					packets = append(
						packets,
						xpacket.LayersToPacket(t, packetLayers...),
					)
					prolongedSessions = append(prolongedSessions, activeSession)
				}
			}

			totalPackets += len(packets)

			if batch%logPeriod == 0 || batch+1 == test.numBatches {
				t.Logf(
					"Batch %d sending packets to sessions: %d (%d new + %d active)",
					batch,
					len(packets),
					test.batchSize,
					len(prolongedSessions),
				)
			}

			// Send all packets at once
			result, err := config.ts.Mock.HandlePackets(packets...)
			require.NoError(t, err)

			currentTime := config.ts.Mock.CurrentTime()

			totalDropped += len(result.Drop)
			totalOutput += len(result.Output)

			assert.Equal(t, len(packets), len(result.Drop)+len(result.Output))

			for _, outPkt := range result.Output {
				// Extract source IP and port from output packet
				ip, ok := netip.AddrFromSlice(outPkt.InnerPacket.SrcIP)
				require.True(t, ok, "incorrect src ip")
				key := sessionKey{
					ip:   ip,
					port: outPkt.SrcPort,
				}
				activeSessions[key] = currentTime
			}

			// Trace active sessions counters
			findActiveSessions += len(prolongedSessions)
			for _, session := range prolongedSessions {
				if currentTime != activeSessions[session] {
					failedToFindActiveSession += 1
				}
			}

			// Sync active sessions and resize table on demand
			err = config.ts.Balancer.Refresh(currentTime)
			require.NoError(t, err)

			// Check active sessions are correct
			activeSessionsCount := uint64(len(activeSessions))

			if batch%logPeriod == 0 || batch+1 == test.numBatches {
				currentConfig := config.ts.Balancer.Config()
				currentCapacity := uint64(0)
				if currentConfig.State != nil &&
					currentConfig.State.SessionTableCapacity != nil {
					currentCapacity = *currentConfig.State.SessionTableCapacity
				}

				t.Logf(
					"Batch %d verified: Sent %d packet sessions (%d new + %d active), Output=%d, Drop=%d, ActiveSessions=%d, SessionTableCapacity=%d",
					batch,
					len(packets),
					len(packets)-len(prolongedSessions),
					len(prolongedSessions),
					len(result.Output),
					len(result.Drop),
					activeSessionsCount,
					currentCapacity,
				)
			}

			// Get info
			info, err := config.ts.Balancer.Info(currentTime)
			require.NoError(t, err)

			// Verify active sessions for VS
			require.Equal(t, 1, len(info.Vs), "should have exactly one VS")
			vsActiveSessions := info.Vs[0].ActiveSessions
			assert.Equal(
				t,
				activeSessionsCount,
				vsActiveSessions,
				"VS active sessions should match expected after batch %d",
				batch+1,
			)

			// Verify active sessions for Real
			require.Equal(
				t,
				1,
				len(info.Vs[0].Reals),
				"should have exactly one Real",
			)
			realActiveSessions := info.Vs[0].Reals[0].ActiveSessions
			assert.Equal(
				t,
				activeSessionsCount,
				realActiveSessions,
				"Real active sessions should match expected after batch %d",
				batch+1,
			)

			// Advance time
			newTime := config.ts.Mock.AdvanceTime(advanceTimePerBatch)

			// Remove expired sessions from our tracking
			for key, lastPacketTime := range activeSessions {
				if newTime.Sub(
					lastPacketTime,
				) > time.Duration(
					config.timeouts,
				)*time.Second {
					delete(activeSessions, key)
				}
			}
		}

		assert.Equal(t, totalPackets, totalDropped+totalOutput)

		dropRate := float64(totalDropped) / float64(totalPackets) * 100
		t.Logf(
			"Drop rate: %.2f%% (dropped=%d, total=%d)",
			dropRate,
			totalDropped,
			totalPackets,
		)

		// Just ensure some packets not dropped
		assert.Less(t, dropRate, 10.0)

		failedToFindRate := float64(
			failedToFindActiveSession,
		) / float64(
			findActiveSessions,
		) * 100
		t.Logf(
			"Failed to insert active sessions rate: %.2f%% (failed=%d, total=%d)",
			failedToFindRate,
			failedToFindActiveSession,
			findActiveSessions,
		)

		// Just ensure not all active session packets
		// were dropped
		assert.Less(t, failedToFindRate, 10.0)
	})
}

//////////////////////////////////////////////////////////////////////////////

// TestSessionTableStress1 sends many random TCP SYN packets to a single
// real of a single VS, calling Refresh() after each batch and verifying
// that active sessions count matches expectations considering session
// expiration based on timeout.
func TestSessionTableStress1(t *testing.T) {
	vsIp := netip.MustParseAddr("1.1.1.1")
	vsPort := uint16(80)
	realAddr := netip.MustParseAddr("2.2.2.2")

	sessionsTimeout := 64 // in seconds
	defaultCapacity := 16
	maxLoadFactor := 0.5

	// Configure balancer with single VS and single real
	moduleConfig := &balancerpb.BalancerConfig{
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
					AllowedSrcs: []*balancerpb.Net{
						{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("10.0.0.0").
									AsSlice(),
							},
							Size: 8, // Allow all 10.x.x.x addresses
						},
					},
					Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
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
									Bytes: realAddr.AsSlice(),
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
				TcpSynAck: uint32(sessionsTimeout),
				TcpSyn:    uint32(sessionsTimeout),
				TcpFin:    uint32(sessionsTimeout),
				Tcp:       uint32(sessionsTimeout),
				Udp:       uint32(sessionsTimeout),
				Default:   uint32(sessionsTimeout),
			},
		},
		State: &balancerpb.StateConfig{
			SessionTableCapacity:      func() *uint64 { v := uint64(defaultCapacity); return &v }(),
			SessionTableMaxLoadFactor: func() *float32 { v := float32(maxLoadFactor); return &v }(),
			RefreshPeriod: durationpb.New(
				0,
			), // do not update in background
			Wlc: &balancerpb.WlcConfig{
				Power:     func() *uint64 { v := uint64(10); return &v }(),
				MaxWeight: func() *uint32 { v := uint32(1000); return &v }(),
			},
		},
	}

	// Setup test
	ts, err := utils.Make(&utils.TestConfig{
		Mock:     utils.SingleWorkerMockConfig(128*datasize.MB, 4*datasize.MB),
		Balancer: moduleConfig,
		AgentMemory: func() *datasize.ByteSize {
			memory := 32 * datasize.MB
			return &memory
		}(),
	})
	require.NoError(t, err)
	defer ts.Free()

	// Enable all reals
	utils.EnableAllReals(t, ts)

	// Set time to mock
	ts.Mock.SetCurrentTime(time.Unix(0, 0))

	rng := rand.New(rand.NewSource(123))

	config := stressConfig{
		ts:       ts,
		rng:      rng,
		vsIp:     vsIp,
		vsPort:   vsPort,
		realAddr: realAddr,
		timeouts: sessionsTimeout,
	}

	tests := []testCase{
		{10, 5, 2},   // just a small test
		{10, 500, 2}, // emit a few packets with big time intervals
		{10, 4, 5},   // emit a few packets with small time intervals
		{500, 10, 2}, // emit many packets with small time interval
		{500, 10, 5}, // emit many packets with big time interval
	}

	// Run test cases
	for _, test := range tests {
		// Initially resize session table to the 90% of first batch size
		newCapacity := uint64(9 * test.batchSize / 10)
		if newCapacity < 1 {
			newCapacity = 1
		}

		currentConfig := ts.Balancer.Config()
		currentConfig.State.SessionTableCapacity = &newCapacity
		err = ts.Balancer.Update(currentConfig, ts.Mock.CurrentTime())
		require.NoError(t, err, "failed to shrink session table capacity")

		// Run current test case
		testName := fmt.Sprintf(
			"BatchSize_%d_NumBatches_%d_TimeoutBatches_%d",
			test.batchSize,
			test.numBatches,
			test.timeoutBatches,
		)
		t.Run(testName, func(t *testing.T) {
			executeTestCase(t, &config, &test)
		})

		// Prepare the next test case

		// Make all sessions expire
		ts.Mock.AdvanceTime(time.Duration(sessionsTimeout) * time.Second)

		// Sync active sessions
		err = ts.Balancer.Refresh(ts.Mock.CurrentTime())
		require.NoError(t, err, "failed to sync active sessions before shrink")
	}
}
