package balancer

import (
	"fmt"
	"math/rand"
	"net/netip"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	mock "github.com/yanet-platform/yanet2/mock/go"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/module"
	"google.golang.org/protobuf/types/known/durationpb"
)

////////////////////////////////////////////////////////////////////////////////

type testCase struct {
	batchSize      int
	numBatches     int
	timeoutBatches int
}

type config struct {
	mock     *mock.YanetMock
	balancer *module.Balancer
	rng      *rand.Rand
	vsIp     netip.Addr
	vsPort   uint16
	timeouts int
}

func executeTestCase(t *testing.T, config *config, test *testCase) {
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

	t.Logf(
		"Test batchSize=%d, numBatches=%d, timeoutBatches=%d, advanceTimePerBatch=%.1fs, sessionsTimeouts=%.1fs, sessionTableCapacity=%d",
		test.batchSize,
		test.numBatches,
		test.timeoutBatches,
		advanceTimePerBatch.Seconds(),
		float32(config.timeouts),
		config.balancer.GetModuleConfigState().SessionTableCapacity(),
	)

	// Verify initial state is valid

	t.Run("Verify_Initial_State", func(t *testing.T) {
		// Verify sessions sessionsInfo
		sessionsInfo, err := config.balancer.GetSessionsInfo(
			config.mock.CurrentTime(),
		)
		assert.NoError(t, err, "failed to get sessions info")
		assert.Equal(t, uint(0), sessionsInfo.SessionsCount)
		assert.Equal(t, 0, len(sessionsInfo.Sessions))

		// Get state info
		stateInfo := config.balancer.GetStateInfo(config.mock.CurrentTime())

		// Verify module state
		assert.Equal(t, uint(0), stateInfo.ActiveSessions.Value)

		// Verify virtual services active sessions
		assert.Equal(t, uint(0), stateInfo.VsInfo[0].ActiveSessions.Value)

		// Verify real services active sessions
		assert.Equal(t, uint(0), stateInfo.RealInfo[0].ActiveSessions.Value)
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
				packetLayers := MakeTCPPacket(
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
					packetLayers := MakeTCPPacket(
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
			result, err := config.mock.HandlePackets(packets...)
			require.NoError(t, err)

			currentTime := config.mock.CurrentTime()

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
			err = config.balancer.SyncActiveSessionsAndWlcAndResizeTableOnDemand(
				currentTime,
			)
			require.NoError(t, err)

			// Check active sessions are correct
			activeSessionsCount := uint(len(activeSessions))

			if batch%logPeriod == 0 || batch+1 == test.numBatches {
				t.Logf(
					"Batch %d verified: Sent %d packet sessions (%d new + %d active), Output=%d, Drop=%d, ActiveSessions=%d, SessionTableCapacity=%d",
					batch,
					len(packets),
					len(packets)-len(prolongedSessions),
					len(prolongedSessions),
					len(result.Output),
					len(result.Drop),
					activeSessionsCount,
					config.balancer.GetModuleConfigState().
						SessionTableCapacity(),
				)
			}

			// Get state info
			state := config.balancer.GetStateInfo(config.mock.CurrentTime())

			// Verify active sessions for VS
			require.Equal(t, 1, len(state.VsInfo), "should have exactly one VS")
			vsActiveSessions := state.VsInfo[0].ActiveSessions.Value
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
				len(state.RealInfo),
				"should have exactly one Real",
			)
			realActiveSessions := state.RealInfo[0].ActiveSessions.Value
			assert.Equal(
				t,
				activeSessionsCount,
				realActiveSessions,
				"Real active sessions should match expected after batch %d",
				batch+1,
			)

			// Advance time
			newTime := config.mock.AdvanceTime(advanceTimePerBatch)

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

// TestSessionTable sends many random TCP SYN packets to a single
// real of a single VS, calling SyncActiveSessionsAndWlcAndResizeTableOnDemand
// after each batch and verifying that active sessions count matches expectations
// considering session expiration based on timeout.
func TestSessionTable(t *testing.T) {
	vsIp := IpAddr("1.1.1.1")
	vsPort := uint16(80)
	realAddr := IpAddr("2.2.2.2")

	sessionsTimeout := 64 // in seconds
	defaultCapacity := 16
	maxLoadFactor := 0.5

	// Configure balancer with single VS and single real
	moduleConfig := &balancerpb.ModuleConfig{
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
			TcpSynAck: uint32(sessionsTimeout),
			TcpSyn:    uint32(sessionsTimeout),
			TcpFin:    uint32(sessionsTimeout),
			Tcp:       uint32(sessionsTimeout),
			Udp:       uint32(sessionsTimeout),
			Default:   uint32(sessionsTimeout),
		},
		Wlc: &balancerpb.WlcConfig{
			WlcPower:      10,
			MaxRealWeight: 1000,
			UpdatePeriod:  durationpb.New(0),
		},
	}

	stateConfig := &balancerpb.ModuleStateConfig{
		SessionTableCapacity:      uint64(defaultCapacity),
		SessionTableScanPeriod:    durationpb.New(0),
		SessionTableMaxLoadFactor: float32(maxLoadFactor),
	}

	// Setup test
	setup, err := SetupTest(&TestConfig{
		moduleConfig: moduleConfig,
		stateConfig:  stateConfig,
	})
	require.NoError(t, err)
	defer setup.Free()

	mock := setup.mock
	balancer := setup.balancer

	// Set time to mock
	mock.SetCurrentTime(time.Unix(0, 0))

	rng := rand.New(rand.NewSource(123))

	config := config{
		mock:     mock,
		balancer: balancer,
		rng:      rng,
		vsIp:     vsIp,
		vsPort:   vsPort,
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
		// Initially resize session table to the 90% of first batch size.
		err = balancer.GetModuleConfigState().
			Update(9*uint(test.batchSize)/10, 0, float32(maxLoadFactor), mock.CurrentTime())
		require.NoError(t, err, "failed to shrink module config state")

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

		// Prepare the next one test case

		// Make all sessions expire
		mock.AdvanceTime(time.Duration(sessionsTimeout) * time.Second)

		// Sync active sessions
		err = balancer.SyncActiveSessionsAndWlcAndResizeTableOnDemand(
			mock.CurrentTime(),
		)
		require.NoError(t, err, "failed to sync active sessions before shrink")
	}
}
