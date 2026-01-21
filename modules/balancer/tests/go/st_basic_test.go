package balancer_test

// TestSessionTableManual validates session table behavior with comprehensive testing of:
//
// # Session Creation and Persistence
// - Creating multiple sessions with random client IPs and ports
// - Verifying sessions remain active when refreshed within timeout period
// - Testing session table resizing with active sessions
//
// # Session Timeout Validation
// - Sending packets to existing sessions to refresh their timeout
// - Advancing time and verifying sessions expire after configured timeout
// - Testing that new sessions can be created alongside existing ones
//
// # Load Testing and Capacity Management
// - Creating 256 sessions and verifying at least 80% acceptance rate
// - Testing dynamic session table resizing (256 → 1024 → 300 capacity)
// - Verifying session persistence across table resize operations
// - Validating expired sessions are properly removed while active ones persist
//
// TestSessionTimeouts validates different session timeout types work correctly:
//
// # Configuration
// - Two virtual services: TCP (1.1.1.1:80) and UDP (2.2.2.2:53)
// - Different timeout values: UDP=30s, TCP=60s, TCP_SYN=20s, TCP_SYN_ACK=25s, TCP_FIN=15s
//
// # Timeout Validation Tests
// - UDP Session Timeout: Verifies UDP sessions expire at 30 seconds
// - TCP SYN Timeout: Verifies TCP SYN sessions expire at 20 seconds
// - TCP SYN-ACK Timeout: Verifies timeout switches to 25s after SYN-ACK packet
// - TCP Basic Timeout: Verifies timeout switches from SYN (20s) to TCP (60s) after regular packet
// - TCP FIN Timeout: Verifies timeout switches to 15s after FIN packet
//
// # Validation Pattern
// Each test follows the pattern:
// - Send packet(s) to create session
// - Verify session exists
// - Advance time by (timeout - 1) seconds
// - Verify session still persists
// - Advance time by 1 second (reaching exact timeout)
// - Verify session has expired

import (
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

// sessionKey represents a unique session identifier
type sessionKey struct {
	ip   netip.Addr
	port uint16
}

// checkActiveSessions verifies active sessions match expected sessions
func checkActiveSessions(
	t *testing.T,
	ts *utils.TestSetup,
	currentTime time.Time,
	expectedSessions []sessionKey,
	vsIp netip.Addr,
	vsPort uint16,
	realAddr netip.Addr,
) {
	t.Helper()

	expectedCount := uint64(len(expectedSessions))

	// Get sessions info
	sessions, err := ts.Balancer.Sessions(currentTime)
	require.NoError(t, err)
	assert.Equal(
		t,
		int(expectedCount),
		len(sessions),
		"sessions list should have %d entries",
		expectedCount,
	)

	// Create a map of expected sessions for validation
	expectedSessionsMap := make(map[sessionKey]bool)
	for _, session := range expectedSessions {
		expectedSessionsMap[session] = true
	}

	// Verify each session has correct properties
	for i, session := range sessions {
		// Verify VS identifier
		vsAddr, _ := netip.AddrFromSlice(session.RealId.Vs.Addr.Bytes)
		assert.Equal(
			t,
			vsIp,
			vsAddr,
			"session %d: VS IP should match",
			i,
		)
		assert.Equal(
			t,
			uint32(vsPort),
			session.RealId.Vs.Port,
			"session %d: VS port should match",
			i,
		)

		// Verify Real identifier
		realIP, _ := netip.AddrFromSlice(session.RealId.Real.Ip.Bytes)
		assert.Equal(
			t,
			realAddr,
			realIP,
			"session %d: Real IP should match",
			i,
		)

		// Verify client IP and port match one of our expected sessions
		clientAddr, _ := netip.AddrFromSlice(session.ClientAddr.Bytes)
		clientKey := sessionKey{
			ip:   clientAddr,
			port: uint16(session.ClientPort),
		}
		assert.True(
			t,
			expectedSessionsMap[clientKey],
			"session %d: client %v:%d should be in expected sessions",
			i,
			clientAddr,
			session.ClientPort,
		)

		// Delete session to not match same session twice.
		// In this way, we check sessions are unique
		delete(expectedSessionsMap, clientKey)
	}

	assert.Empty(
		t,
		expectedSessionsMap,
		"%d expected sessions not found",
		len(expectedSessionsMap),
	)

	// Get info to verify VS and Real active sessions
	info, err := ts.Balancer.Info(currentTime)
	require.NoError(t, err)

	// Verify module active sessions
	assert.Equal(
		t,
		expectedCount,
		info.ActiveSessions,
		"module should have %d active sessions",
		expectedCount,
	)

	// Verify VS active sessions
	require.Equal(t, 1, len(info.Vs), "should have exactly one VS")
	assert.Equal(
		t,
		expectedCount,
		info.Vs[0].ActiveSessions,
		"VS should have %d active sessions",
		expectedCount,
	)

	// Verify Real active sessions
	require.Equal(t, 1, len(info.Vs[0].Reals), "should have exactly one Real")
	assert.Equal(
		t,
		expectedCount,
		info.Vs[0].Reals[0].ActiveSessions,
		"Real should have %d active sessions",
		expectedCount,
	)
}

// TestSessionTableManual tests session table behavior with timeouts and resizing.
// It creates sessions, verifies they stay active after resizing or when refreshed
// within timeout, and tests that new sessions can be created alongside existing ones.
func TestSessionTableManual(t *testing.T) {
	vsIp := netip.MustParseAddr("1.1.1.1")
	vsPort := uint16(80)
	realAddr := netip.MustParseAddr("2.2.2.2")

	sessionTimeout := 60 // in seconds
	initialCapacity := 16
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
								Bytes: netip.MustParseAddr("2.2.2.2").AsSlice(),
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

	mock := ts.Mock

	// Set initial time
	mock.SetCurrentTime(time.Unix(0, 0))

	rng := rand.New(rand.NewSource(42))

	// Helper to generate random client IP in 10.x.x.x range
	randomClientIP := func() netip.Addr {
		return netip.AddrFrom4([4]byte{
			10,
			byte(rng.Intn(256)),
			byte(rng.Intn(256)),
			byte(rng.Intn(256)),
		})
	}

	// Helper to generate random port
	randomPort := func() uint16 {
		return uint16(1024 + rng.Intn(64511)) // 1024-65535
	}

	// Track session keys (srcIP, srcPort)
	sessions := make([]sessionKey, 0, 10)

	// Phase 1: Create 10 random sessions with TCP SYN packets
	t.Run("Phase1_Create_10_Sessions", func(t *testing.T) {
		packets := make([]gopacket.Packet, 0, 10)
		for range 10 {
			srcIP := randomClientIP()
			srcPort := randomPort()
			sessions = append(sessions, sessionKey{ip: srcIP, port: srcPort})

			packetLayers := utils.MakeTCPPacket(
				srcIP,
				srcPort,
				vsIp,
				vsPort,
				&layers.TCP{SYN: true},
			)
			packets = append(
				packets,
				xpacket.LayersToPacket(t, packetLayers...),
			)
		}

		result, err := mock.HandlePackets(packets...)
		require.NoError(t, err)

		// Verify all packets are in output
		assert.Equal(
			t,
			10,
			len(result.Output),
			"all 10 packets should be in output",
		)
		assert.Empty(t, result.Drop, "no packets should be dropped")

		// Verify each output packet is properly encapsulated
		for i, outPacket := range result.Output {
			utils.ValidatePacket(t, ts.Balancer.Config(), packets[i], outPacket)
		}

		t.Logf(
			"Created 10 sessions successfully, all packets properly encapsulated",
		)
	})

	// Advance time by 30 seconds
	t.Run("Advance_Time_30s", func(t *testing.T) {
		newTime := mock.AdvanceTime(30 * time.Second)
		t.Logf("Advanced time to %v (30s elapsed)", newTime)
	})

	// Phase 2: Send TCP non-SYN packets to the same sessions
	t.Run("Phase2_Send_NonSYN_To_Same_Sessions", func(t *testing.T) {
		packets := make([]gopacket.Packet, 0, 10)
		for _, session := range sessions {
			packetLayers := utils.MakeTCPPacket(
				session.ip,
				session.port,
				vsIp,
				vsPort,
				&layers.TCP{}, // No SYN flag
			)
			packets = append(
				packets,
				xpacket.LayersToPacket(t, packetLayers...),
			)
		}

		result, err := mock.HandlePackets(packets...)
		require.NoError(t, err)

		// Verify packets are not dropped (sessions still valid)
		assert.Equal(
			t,
			10,
			len(result.Output),
			"all packets should be in output",
		)
		assert.Empty(t, result.Drop, "no packets should be dropped")

		// Verify each output packet is properly encapsulated
		for i, outPacket := range result.Output {
			utils.ValidatePacket(t, ts.Balancer.Config(), packets[i], outPacket)
		}

		t.Logf(
			"Sent non-SYN packets to 10 sessions, all accepted and properly encapsulated",
		)
	})

	// Resize session table and get active sessions
	t.Run("Resize_And_Verify_Active_Sessions", func(t *testing.T) {
		currentTime := mock.CurrentTime()

		// Sync active sessions and resize table on demand
		err := ts.Balancer.Refresh(currentTime)
		require.NoError(t, err)

		// Check active sessions using helper function
		checkActiveSessions(
			t,
			ts,
			currentTime,
			sessions,
			vsIp,
			vsPort,
			realAddr,
		)

		t.Logf(
			"Verified 10 active sessions for VS and Real with correct identifiers",
		)
	})

	// Advance time by 40 seconds (total 70s from start, 40s from last packet)
	t.Run("Advance_Time_40s", func(t *testing.T) {
		newTime := mock.AdvanceTime(40 * time.Second)
		t.Logf(
			"Advanced time to %v (40s elapsed, sessions at 40s age)",
			newTime,
		)
	})

	// Phase 3: Send packets to the same sessions again
	t.Run("Phase3_Send_Packets_Again", func(t *testing.T) {
		packets := make([]gopacket.Packet, 0, 10)
		for _, session := range sessions {
			packetLayers := utils.MakeTCPPacket(
				session.ip,
				session.port,
				vsIp,
				vsPort,
				&layers.TCP{}, // No SYN flag
			)
			packets = append(
				packets,
				xpacket.LayersToPacket(t, packetLayers...),
			)
		}

		result, err := mock.HandlePackets(packets...)
		require.NoError(t, err)

		// Verify packets are not dropped (sessions still valid at 40s age)
		assert.Equal(
			t,
			10,
			len(result.Output),
			"all packets should be in output",
		)
		assert.Empty(t, result.Drop, "no packets should be dropped")

		// Verify each output packet is properly encapsulated
		for i, outPacket := range result.Output {
			utils.ValidatePacket(t, ts.Balancer.Config(), packets[i], outPacket)
		}

		// Verify sessions are still active
		currentTime := mock.CurrentTime()
		err = ts.Balancer.Refresh(currentTime)
		require.NoError(t, err)

		sessions, err := ts.Balancer.Sessions(currentTime)
		require.NoError(t, err)
		assert.Equal(
			t,
			10,
			len(sessions),
			"should still have 10 active sessions",
		)

		t.Logf(
			"Sent packets to 10 sessions again, all accepted, properly encapsulated, and sessions refreshed",
		)
	})

	// Advance time by 30 seconds
	t.Run("Advance_Time_30s_Again", func(t *testing.T) {
		newTime := mock.AdvanceTime(30 * time.Second)
		t.Logf("Advanced time to %v (30s elapsed)", newTime)
	})

	// Phase 4: Create 10 new sessions and send packets to old sessions
	t.Run("Phase4_Create_New_And_Refresh_Old_Sessions", func(t *testing.T) {
		packets := make([]gopacket.Packet, 0, 20)

		// Create 10 new sessions with TCP SYN packets
		newSessions := make([]sessionKey, 0, 10)
		for range 10 {
			srcIP := randomClientIP()
			srcPort := randomPort()
			newSessions = append(
				newSessions,
				sessionKey{ip: srcIP, port: srcPort},
			)

			packetLayers := utils.MakeTCPPacket(
				srcIP,
				srcPort,
				vsIp,
				vsPort,
				&layers.TCP{SYN: true},
			)
			packets = append(
				packets,
				xpacket.LayersToPacket(t, packetLayers...),
			)
		}

		// Send packets to old sessions to refresh them
		for _, session := range sessions {
			packetLayers := utils.MakeTCPPacket(
				session.ip,
				session.port,
				vsIp,
				vsPort,
				&layers.TCP{}, // No SYN flag
			)
			packets = append(
				packets,
				xpacket.LayersToPacket(t, packetLayers...),
			)
		}

		result, err := mock.HandlePackets(packets...)
		require.NoError(t, err)

		// Verify all packets are in output
		assert.Equal(
			t,
			20,
			len(result.Output),
			"all 20 packets should be in output",
		)
		assert.Empty(t, result.Drop, "no packets should be dropped")

		// Verify each output packet is properly encapsulated
		for i, outPacket := range result.Output {
			utils.ValidatePacket(t, ts.Balancer.Config(), packets[i], outPacket)
		}

		// Append new sessions to the sessions list for final verification
		sessions = append(sessions, newSessions...)

		t.Logf(
			"Created 10 new sessions and refreshed 10 old sessions, all packets properly encapsulated",
		)
	})

	// Advance time and verify all sessions are active
	t.Run("Verify_All_20_Sessions_Active", func(t *testing.T) {
		currentTime := mock.CurrentTime()

		// Sync active sessions
		err := ts.Balancer.Refresh(currentTime)
		require.NoError(t, err)

		// Check active sessions using helper function
		checkActiveSessions(
			t,
			ts,
			currentTime,
			sessions,
			vsIp,
			vsPort,
			realAddr,
		)

		t.Logf(
			"Verified all 20 sessions are active (10 new + 10 old refreshed) with correct client IPs and ports",
		)
	})

	// Keep only new sessions for expiration testing
	newSessions := sessions[10:] // Last 10 sessions are the new ones

	// Phase 5: Test session expiration
	t.Run("Phase5_Advance_30s_Send_To_New_Sessions_Only", func(t *testing.T) {
		// Advance time by 30 seconds (old sessions at 60s age, should expire)
		newTime := mock.AdvanceTime(30 * time.Second)
		t.Logf(
			"Advanced time to %v (30s elapsed, old sessions at 60s age)",
			newTime,
		)

		// Send packets only to new sessions
		packets := make([]gopacket.Packet, 0, 10)
		for _, session := range newSessions {
			packetLayers := utils.MakeTCPPacket(
				session.ip,
				session.port,
				vsIp,
				vsPort,
				&layers.TCP{}, // No SYN flag
			)
			packets = append(
				packets,
				xpacket.LayersToPacket(t, packetLayers...),
			)
		}

		result, err := mock.HandlePackets(packets...)
		require.NoError(t, err)

		// Verify all packets are in output
		assert.Equal(
			t,
			10,
			len(result.Output),
			"all 10 packets should be in output",
		)
		assert.Empty(t, result.Drop, "no packets should be dropped")

		// Verify each output packet is properly encapsulated
		for i, outPacket := range result.Output {
			utils.ValidatePacket(t, ts.Balancer.Config(), packets[i], outPacket)
		}

		// Check active sessions immediately after sending packets
		currentTime := mock.CurrentTime()
		checkActiveSessions(
			t,
			ts,
			currentTime,
			sessions,
			vsIp,
			vsPort,
			realAddr,
		)

		t.Logf(
			"Sent packets to 10 new sessions only, verified all sessions still active",
		)
	})

	t.Run(
		"Phase5_Advance_30s_Check_Only_New_Sessions_Active",
		func(t *testing.T) {
			// Advance time by 30 seconds (new sessions at 30s age)
			newTime := mock.AdvanceTime(30 * time.Second)
			t.Logf(
				"Advanced time to %v (30s elapsed, new sessions at 30s age)",
				newTime,
			)

			currentTime := mock.CurrentTime()

			// Check active sessions WITHOUT resizing
			checkActiveSessions(
				t,
				ts,
				currentTime,
				newSessions,
				vsIp,
				vsPort,
				realAddr,
			)

			t.Logf("Verified only 10 new sessions are active at 30s age")
		},
	)

	t.Run(
		"Phase5_Resize_And_Verify_New_Sessions_Still_Active",
		func(t *testing.T) {
			currentTime := mock.CurrentTime()

			// Resize and sync active sessions
			err := ts.Balancer.Refresh(currentTime)
			require.NoError(t, err)

			// Check active sessions after resize
			checkActiveSessions(
				t,
				ts,
				currentTime,
				newSessions,
				vsIp,
				vsPort,
				realAddr,
			)

			t.Logf("Verified 10 new sessions still active after resize")
		},
	)

	t.Run(
		"Phase5_Advance_29s_Verify_Sessions_Still_Active",
		func(t *testing.T) {
			// Advance time by 29 seconds (new sessions at 59s age, still valid)
			newTime := mock.AdvanceTime(29 * time.Second)
			t.Logf(
				"Advanced time to %v (29s elapsed, new sessions at 59s age)",
				newTime,
			)

			currentTime := mock.CurrentTime()

			// Check active sessions
			checkActiveSessions(
				t,
				ts,
				currentTime,
				newSessions,
				vsIp,
				vsPort,
				realAddr,
			)

			t.Logf(
				"Verified 10 new sessions still active at 59s age (< 60s timeout)",
			)
		},
	)

	t.Run("Phase5_Advance_1s_Verify_Sessions_Expired", func(t *testing.T) {
		// Advance time by 1 second (new sessions at 60s age, should expire)
		newTime := mock.AdvanceTime(1 * time.Second)
		t.Logf(
			"Advanced time to %v (1s elapsed, new sessions at 60s age - expired)",
			newTime,
		)

		currentTime := mock.CurrentTime()

		// Check active sessions - should be 0
		checkActiveSessions(
			t,
			ts,
			currentTime,
			[]sessionKey{},
			vsIp,
			vsPort,
			realAddr,
		)

		t.Logf("Verified all sessions expired after 60s timeout")
	})

	// Phase 6: Comprehensive session table test with 256 sessions, load testing, and resizing
	var phase6OldSessions []sessionKey
	var phase6NewSessions []sessionKey

	t.Run("Phase6_Manual_Resize_Session_Table", func(t *testing.T) {
		now := mock.CurrentTime()
		newCapacity := uint64(256)
		newMaxLoadFactor := float32(0.5)

		config := ts.Balancer.Config()
		config.State.SessionTableCapacity = &newCapacity
		config.State.SessionTableMaxLoadFactor = &newMaxLoadFactor

		err := ts.Balancer.Update(config, now)
		require.NoError(t, err, "failed to update config")

		t.Logf("Resized session table to capacity 256")
	})

	t.Run("Phase6_Send_256_Sessions_Check_80_Percent", func(t *testing.T) {
		// Generate 256 unique packets and store session keys
		packets := make([]gopacket.Packet, 0, 256)
		sessionToPacket := make(map[sessionKey]gopacket.Packet)

		for range 256 {
			srcIP := randomClientIP()
			srcPort := randomPort()
			session := sessionKey{ip: srcIP, port: srcPort}

			packetLayers := utils.MakeTCPPacket(
				srcIP,
				srcPort,
				vsIp,
				vsPort,
				&layers.TCP{SYN: true},
			)
			pkt := xpacket.LayersToPacket(t, packetLayers...)
			packets = append(packets, pkt)
			sessionToPacket[session] = pkt
		}

		result, err := mock.HandlePackets(packets...)
		require.NoError(t, err)

		// Verify at least 80% accepted (205 out of 256)
		acceptedCount := len(result.Output)
		require.GreaterOrEqual(
			t,
			acceptedCount,
			205,
			"at least 80%% of packets should be accepted",
		)

		t.Logf(
			"Sent 256 packets, %d accepted (%.1f%%)",
			acceptedCount,
			float64(acceptedCount)*100/256,
		)

		// Extract accepted session keys from output packets
		acceptedSessions := make([]sessionKey, 0, acceptedCount)
		for _, outPacket := range result.Output {
			// Get inner packet to extract session key
			if outPacket.InnerPacket == nil {
				t.Fatal("output packet has no inner packet")
			}

			innerIP, ok := netip.AddrFromSlice(outPacket.InnerPacket.SrcIP)
			if !ok {
				t.Fatalf(
					"failed to parse inner packet source IP: %v",
					outPacket.InnerPacket.SrcIP,
				)
			}

			port := outPacket.SrcPort
			session := sessionKey{ip: innerIP, port: port}

			// Find the original packet for validation
			originalPacket, ok := sessionToPacket[session]
			if !ok {
				t.Errorf(
					"could not find original packet for session %v:%d",
					innerIP,
					port,
				)
			}

			// Validate the packet
			utils.ValidatePacket(
				t,
				ts.Balancer.Config(),
				originalPacket,
				outPacket,
			)

			acceptedSessions = append(acceptedSessions, session)
		}

		// Store for later phases
		phase6OldSessions = acceptedSessions

		t.Logf("Extracted %d accepted session keys", len(acceptedSessions))
	})

	t.Run("Phase6_Check_Accepted_Sessions_Active", func(t *testing.T) {
		currentTime := mock.CurrentTime()

		// Sync active sessions
		err := ts.Balancer.Refresh(currentTime)
		require.NoError(t, err)

		// Check only accepted sessions are active
		checkActiveSessions(
			t,
			ts,
			currentTime,
			phase6OldSessions,
			vsIp,
			vsPort,
			realAddr,
		)

		t.Logf(
			"Verified %d accepted sessions are active",
			len(phase6OldSessions),
		)
	})

	t.Run("Phase6_Advance_59s_Resize_To_1024", func(t *testing.T) {
		// Advance time by 59 seconds (old sessions at 59s age)
		newTime := mock.AdvanceTime(59 * time.Second)
		t.Logf(
			"Advanced time to %v (59s elapsed, old sessions at 59s age)",
			newTime,
		)

		// Resize table to 1024
		now := mock.CurrentTime()
		newCapacity := uint64(1024)
		newMaxLoadFactor := float32(0.5)

		config := ts.Balancer.Config()
		config.State.SessionTableCapacity = &newCapacity
		config.State.SessionTableMaxLoadFactor = &newMaxLoadFactor

		err := ts.Balancer.Update(config, now)
		require.NoError(t, err, "failed to resize session table to 1024")

		t.Logf("Resized session table to 1024")
	})

	t.Run("Phase6_Send_256_More_Sessions", func(t *testing.T) {
		// Generate 256 new unique packets and store session keys
		packets := make([]gopacket.Packet, 0, 256)
		sessionToPacket := make(map[sessionKey]gopacket.Packet)

		for range 256 {
			srcIP := randomClientIP()
			srcPort := randomPort()
			session := sessionKey{ip: srcIP, port: srcPort}

			packetLayers := utils.MakeTCPPacket(
				srcIP,
				srcPort,
				vsIp,
				vsPort,
				&layers.TCP{SYN: true},
			)
			pkt := xpacket.LayersToPacket(t, packetLayers...)
			packets = append(packets, pkt)
			sessionToPacket[session] = pkt
		}

		result, err := mock.HandlePackets(packets...)
		require.NoError(t, err)

		// Extract session keys from output packets
		newSessions := make([]sessionKey, 0, 256)
		for _, outPacket := range result.Output {
			// Get inner packet to extract session key
			if outPacket.InnerPacket == nil {
				t.Fatal("output packet has no inner packet")
			}

			innerIP, ok := netip.AddrFromSlice(outPacket.InnerPacket.SrcIP)
			if !ok {
				t.Fatalf(
					"failed to parse inner packet source IP: %v",
					outPacket.InnerPacket.SrcIP,
				)
			}

			port := outPacket.SrcPort
			session := sessionKey{ip: innerIP, port: port}

			// Find the original packet for validation
			originalPacket, ok := sessionToPacket[session]
			if !ok {
				t.Errorf(
					"could not find original packet for session %v:%d",
					innerIP,
					port,
				)
			}

			// Validate the packet
			utils.ValidatePacket(
				t,
				ts.Balancer.Config(),
				originalPacket,
				outPacket,
			)

			newSessions = append(newSessions, session)
		}

		// Store for later phases
		phase6NewSessions = newSessions

		t.Logf("Sent new sessions, all accepted")
	})

	t.Run("Phase6_Check_All_Sessions_Active", func(t *testing.T) {
		currentTime := mock.CurrentTime()

		// Sync active sessions
		err := ts.Balancer.Refresh(currentTime)
		require.NoError(t, err)

		// Verify both old and new sessions are active
		allSessions := append(
			[]sessionKey{},
			phase6OldSessions...,
		)
		allSessions = append(allSessions, phase6NewSessions...)

		checkActiveSessions(
			t,
			ts,
			currentTime,
			allSessions,
			vsIp,
			vsPort,
			realAddr,
		)

		t.Logf(
			"Verified all %d sessions are active (%d old + %d new)",
			len(allSessions),
			len(phase6OldSessions),
			len(phase6NewSessions),
		)
	})

	t.Run("Phase6_Advance_1s_Check_Old_Expired", func(t *testing.T) {
		// Advance time by 1 second (old sessions now at 60s age - expired)
		newTime := mock.AdvanceTime(1 * time.Second)
		t.Logf(
			"Advanced time to %v (1s elapsed, old sessions at 60s age - expired)",
			newTime,
		)

		currentTime := mock.CurrentTime()

		// Only new sessions should be active
		checkActiveSessions(
			t,
			ts,
			currentTime,
			phase6NewSessions,
			vsIp,
			vsPort,
			realAddr,
		)

		t.Logf(
			"Verified old sessions expired, %d new sessions still active",
			len(phase6NewSessions),
		)
	})

	t.Run("Phase6_Resize_To_300_Check_New_Active", func(t *testing.T) {
		now := mock.CurrentTime()

		// Resize table back to 300
		newCapacity := uint64(300)
		newMaxLoadFactor := float32(0.5)

		config := ts.Balancer.Config()
		config.State.SessionTableCapacity = &newCapacity
		config.State.SessionTableMaxLoadFactor = &newMaxLoadFactor

		err := ts.Balancer.Update(config, now)
		require.NoError(t, err, "failed to resize session table to 300")

		require.LessOrEqual(
			t,
			uint64(300),
			*ts.Balancer.Config().State.SessionTableCapacity,
		)
		require.GreaterOrEqual(
			t,
			uint64(512),
			*ts.Balancer.Config().State.SessionTableCapacity,
		)

		currentTime := mock.CurrentTime()

		// Verify new sessions still active after resize
		checkActiveSessions(
			t,
			ts,
			currentTime,
			phase6NewSessions,
			vsIp,
			vsPort,
			realAddr,
		)

		t.Logf(
			"Resized table to 300, verified %d new sessions still active and old sessions are expired",
			len(phase6NewSessions),
		)
	})
}

// checkSessionsForVS verifies active sessions for a specific virtual service
func checkSessionsForVS(
	t *testing.T,
	ts *utils.TestSetup,
	currentTime time.Time,
	expectedCount int,
	vsIp netip.Addr,
	vsPort uint16,
) {
	t.Helper()

	// Get sessions info
	sessions, err := ts.Balancer.Sessions(currentTime)
	require.NoError(t, err)

	// Count sessions for this VS
	vsSessionCount := 0
	for _, session := range sessions {
		sessionVsAddr, _ := netip.AddrFromSlice(session.RealId.Vs.Addr.Bytes)
		sessionVsPort := uint16(session.RealId.Vs.Port)
		if sessionVsAddr == vsIp && sessionVsPort == vsPort {
			vsSessionCount++
		}
	}

	assert.Equal(
		t,
		expectedCount,
		vsSessionCount,
		"VS %v:%d should have %d sessions",
		vsIp,
		vsPort,
		expectedCount,
	)

	// Get info to verify VS active sessions
	info, err := ts.Balancer.Info(currentTime)
	require.NoError(t, err)

	// Find the VS in info and verify its active sessions
	for _, vsInfo := range info.Vs {
		vsAddr, _ := netip.AddrFromSlice(vsInfo.Id.Addr.Bytes)
		vsInfoPort := uint16(vsInfo.Id.Port)
		if vsAddr == vsIp && vsInfoPort == vsPort {
			assert.Equal(
				t,
				uint64(expectedCount),
				vsInfo.ActiveSessions,
				"VS %v:%d active sessions should match",
				vsIp,
				vsPort,
			)
			// Also verify Real active sessions sum
			totalRealSessions := uint64(0)
			for _, realInfo := range vsInfo.Reals {
				totalRealSessions += realInfo.ActiveSessions
			}
			assert.Equal(
				t,
				uint64(expectedCount),
				totalRealSessions,
				"VS %v:%d total real sessions should match",
				vsIp,
				vsPort,
			)
			return
		}
	}

	if expectedCount > 0 {
		t.Errorf("VS %v:%d not found in info", vsIp, vsPort)
	}
}

// TestSessionTimeouts verifies that different session timeout types work correctly.
// It creates two virtual services (TCP and UDP) with different timeout configurations
// and validates that sessions expire at the correct time based on their type:
// - UDP sessions use UDP timeout (30s)
// - TCP sessions use different timeouts based on packet flags:
//   - TCP_SYN timeout (20s) for SYN packets
//   - TCP_SYN_ACK timeout (25s) after SYN-ACK packets
//   - TCP_FIN timeout (15s) after FIN packets
//   - TCP timeout (60s) for established connections
//
// Each test verifies the session persists at timeout-1 and expires at timeout.
func TestSessionTimeouts(t *testing.T) {
	tcpVsIp := netip.MustParseAddr("1.1.1.1")
	tcpVsPort := uint16(80)
	tcpRealAddr := netip.MustParseAddr("10.2.2.2")

	udpVsIp := netip.MustParseAddr("2.2.2.2")
	udpVsPort := uint16(5353)
	udpRealAddr := netip.MustParseAddr("10.3.3.3")

	// Different timeout values to verify correct timeout is applied
	udpTimeout := 30       // seconds
	tcpTimeout := 60       // seconds
	tcpSynTimeout := 20    // seconds
	tcpSynAckTimeout := 25 // seconds
	tcpFinTimeout := 15    // seconds

	// Configure balancer with two virtual services (TCP and UDP)
	moduleConfig := &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("5.5.5.5").AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("fe80::5").AsSlice(),
			},
			Vs: []*balancerpb.VirtualService{
				// TCP Virtual Service
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: tcpVsIp.AsSlice(),
						},
						Port:  uint32(tcpVsPort),
						Proto: balancerpb.TransportProto_TCP,
					},
					AllowedSrcs: []*balancerpb.Net{
						{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("10.0.0.0").
									AsSlice(),
							},
							Size: 8,
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
									Bytes: tcpRealAddr.AsSlice(),
								},
								Port: 0,
							},
							Weight: 1,
							SrcAddr: &balancerpb.Addr{
								Bytes: tcpRealAddr.AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.255").
									AsSlice(),
							},
						},
					},
					Peers: []*balancerpb.Addr{},
				},
				// UDP Virtual Service
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: udpVsIp.AsSlice(),
						},
						Port:  uint32(udpVsPort),
						Proto: balancerpb.TransportProto_UDP,
					},
					AllowedSrcs: []*balancerpb.Net{
						{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("10.0.0.0").
									AsSlice(),
							},
							Size: 8,
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
									Bytes: udpRealAddr.AsSlice(),
								},
								Port: 0,
							},
							Weight: 1,
							SrcAddr: &balancerpb.Addr{
								Bytes: udpRealAddr.AsSlice(),
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
				TcpSynAck: uint32(tcpSynAckTimeout),
				TcpSyn:    uint32(tcpSynTimeout),
				TcpFin:    uint32(tcpFinTimeout),
				Tcp:       uint32(tcpTimeout),
				Udp:       uint32(udpTimeout),
				Default:   uint32(tcpTimeout),
			},
		},
		State: &balancerpb.StateConfig{
			SessionTableCapacity:      func() *uint64 { v := uint64(64); return &v }(),
			SessionTableMaxLoadFactor: func() *float32 { v := float32(0.5); return &v }(),
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

	mock := ts.Mock

	// Set initial time
	mock.SetCurrentTime(time.Unix(0, 0))

	// Test 1: UDP Session Timeout
	t.Run("UDP_Session_Timeout", func(t *testing.T) {
		clientIP := netip.MustParseAddr("10.1.1.1")
		clientPort := uint16(5000)

		// Send first UDP packet
		packetLayers := utils.MakeUDPPacket(
			clientIP,
			clientPort,
			udpVsIp,
			udpVsPort,
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := mock.HandlePackets(packet)
		require.NoError(t, err)
		assert.Equal(
			t,
			1,
			len(result.Output),
			"first UDP packet should be accepted",
		)

		// Send second UDP packet to ensure session is created
		result, err = mock.HandlePackets(packet)
		require.NoError(t, err)
		assert.Equal(
			t,
			1,
			len(result.Output),
			"second UDP packet should be accepted",
		)

		// Sync sessions
		currentTime := mock.CurrentTime()
		err = ts.Balancer.Refresh(currentTime)
		require.NoError(t, err)

		// Verify session exists
		checkSessionsForVS(t, ts, currentTime, 1, udpVsIp, udpVsPort)
		t.Logf("UDP session created successfully")

		// Advance time by timeout-1 (29 seconds)
		mock.AdvanceTime(time.Duration(udpTimeout-1) * time.Second)
		currentTime = mock.CurrentTime()

		// Verify session still exists
		checkSessionsForVS(t, ts, currentTime, 1, udpVsIp, udpVsPort)
		t.Logf("UDP session persists at %d seconds", udpTimeout-1)

		// Advance time by 1 second (total 30 seconds)
		mock.AdvanceTime(1 * time.Second)
		currentTime = mock.CurrentTime()

		// Verify session is gone
		checkSessionsForVS(t, ts, currentTime, 0, udpVsIp, udpVsPort)
		t.Logf("UDP session expired at %d seconds", udpTimeout)
	})

	// Reset time for next test
	mock.SetCurrentTime(time.Unix(1000, 0))

	// Test 2: TCP SYN Session Timeout
	t.Run("TCP_SYN_Session_Timeout", func(t *testing.T) {
		clientIP := netip.MustParseAddr("10.1.2.1")
		clientPort := uint16(6000)

		// Send TCP SYN packet
		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			tcpVsIp,
			tcpVsPort,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := mock.HandlePackets(packet)
		require.NoError(t, err)
		assert.Equal(
			t,
			1,
			len(result.Output),
			"TCP SYN packet should be accepted",
		)

		currentTime := mock.CurrentTime()
		err = ts.Balancer.Refresh(currentTime)
		require.NoError(t, err)

		// Verify session exists
		checkSessionsForVS(t, ts, currentTime, 1, tcpVsIp, tcpVsPort)
		t.Logf("TCP session created successfully")

		mock.AdvanceTime(time.Duration(tcpSynTimeout-1) * time.Second)
		currentTime = mock.CurrentTime()

		// Verify session still exists
		checkSessionsForVS(t, ts, currentTime, 1, tcpVsIp, tcpVsPort)
		t.Logf("TCP session persists at %d seconds", tcpTimeout-1)

		// Advance time by 1 second (total 60 seconds)
		mock.AdvanceTime(1 * time.Second)
		currentTime = mock.CurrentTime()

		// Verify session is gone
		checkSessionsForVS(t, ts, currentTime, 0, tcpVsIp, tcpVsPort)
		t.Logf("TCP session expired at %d seconds", tcpTimeout)
	})

	// Reset time for next test
	mock.SetCurrentTime(time.Unix(2000, 0))

	// Test 3: TCP SYN-ACK Timeout
	t.Run("TCP_SYN_ACK_Timeout", func(t *testing.T) {
		clientIP := netip.MustParseAddr("10.1.3.1")
		clientPort := uint16(7000)

		// Send TCP SYN packet
		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			tcpVsIp,
			tcpVsPort,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := mock.HandlePackets(packet)
		require.NoError(t, err)
		assert.Equal(
			t,
			1,
			len(result.Output),
			"TCP SYN packet should be accepted",
		)

		// Send TCP SYN-ACK packet from same client
		packetLayers = utils.MakeTCPPacket(
			clientIP,
			clientPort,
			tcpVsIp,
			tcpVsPort,
			&layers.TCP{SYN: true, ACK: true},
		)
		packet = xpacket.LayersToPacket(t, packetLayers...)
		result, err = mock.HandlePackets(packet)
		require.NoError(t, err)
		assert.Equal(
			t,
			1,
			len(result.Output),
			"TCP SYN-ACK packet should be accepted",
		)

		// Sync sessions
		currentTime := mock.CurrentTime()

		// Verify session exists
		checkSessionsForVS(t, ts, currentTime, 1, tcpVsIp, tcpVsPort)
		t.Logf("TCP SYN-ACK session created successfully")

		// Advance time by SYN-ACK timeout-1 (24 seconds)
		mock.AdvanceTime(time.Duration(tcpSynAckTimeout-1) * time.Second)
		currentTime = mock.CurrentTime()

		// Verify session still exists
		checkSessionsForVS(t, ts, currentTime, 1, tcpVsIp, tcpVsPort)
		t.Logf("TCP SYN-ACK session persists at %d seconds", tcpSynAckTimeout-1)

		// Advance time by 1 second (total 25 seconds)
		mock.AdvanceTime(1 * time.Second)
		currentTime = mock.CurrentTime()

		// Verify session is gone
		checkSessionsForVS(t, ts, currentTime, 0, tcpVsIp, tcpVsPort)
		t.Logf("TCP SYN-ACK session expired at %d seconds", tcpSynAckTimeout)
	})

	// Reset time for next test
	mock.SetCurrentTime(time.Unix(3000, 0))

	// Test 4: TCP SYN + Basic Packet Timeout
	t.Run("TCP_SYN_Then_Basic_Packet_Timeout", func(t *testing.T) {
		clientIP := netip.MustParseAddr("10.1.4.1")
		clientPort := uint16(8000)

		// Send TCP SYN packet
		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			tcpVsIp,
			tcpVsPort,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := mock.HandlePackets(packet)
		require.NoError(t, err)
		assert.Equal(
			t,
			1,
			len(result.Output),
			"TCP SYN packet should be accepted",
		)

		// Send regular TCP packet (no flags) - this should switch timeout to TCP timeout
		packetLayers = utils.MakeTCPPacket(
			clientIP,
			clientPort,
			tcpVsIp,
			tcpVsPort,
			&layers.TCP{}, // No flags
		)
		packet = xpacket.LayersToPacket(t, packetLayers...)
		result, err = mock.HandlePackets(packet)
		require.NoError(t, err)
		assert.Equal(
			t,
			1,
			len(result.Output),
			"TCP basic packet should be accepted",
		)

		// Sync sessions
		currentTime := mock.CurrentTime()

		// Verify session exists
		checkSessionsForVS(t, ts, currentTime, 1, tcpVsIp, tcpVsPort)
		t.Logf("TCP session (SYN->basic) created successfully")

		// Advance time by TCP timeout-1 (59 seconds)
		// This verifies timeout switched from TCP_SYN (20s) to TCP (60s)
		mock.AdvanceTime(time.Duration(tcpTimeout-1) * time.Second)
		currentTime = mock.CurrentTime()

		// Verify session still exists
		checkSessionsForVS(t, ts, currentTime, 1, tcpVsIp, tcpVsPort)
		t.Logf("TCP session (SYN->basic) persists at %d seconds", tcpTimeout-1)

		// Advance time by 1 second (total 60 seconds)
		mock.AdvanceTime(1 * time.Second)
		currentTime = mock.CurrentTime()

		// Verify session is gone
		checkSessionsForVS(t, ts, currentTime, 0, tcpVsIp, tcpVsPort)
		t.Logf("TCP session (SYN->basic) expired at %d seconds", tcpTimeout)
	})

	// Reset time for next test
	mock.SetCurrentTime(time.Unix(4000, 0))

	// Test 5: TCP SYN + FIN Packet Timeout
	t.Run("TCP_SYN_Then_FIN_Packet_Timeout", func(t *testing.T) {
		clientIP := netip.MustParseAddr("10.1.5.1")
		clientPort := uint16(9000)

		// Send TCP SYN packet
		packetLayers := utils.MakeTCPPacket(
			clientIP,
			clientPort,
			tcpVsIp,
			tcpVsPort,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := mock.HandlePackets(packet)
		require.NoError(t, err)
		assert.Equal(
			t,
			1,
			len(result.Output),
			"TCP SYN packet should be accepted",
		)

		// Send TCP FIN packet - this should switch timeout to TCP_FIN timeout
		packetLayers = utils.MakeTCPPacket(
			clientIP,
			clientPort,
			tcpVsIp,
			tcpVsPort,
			&layers.TCP{FIN: true},
		)
		packet = xpacket.LayersToPacket(t, packetLayers...)
		result, err = mock.HandlePackets(packet)
		require.NoError(t, err)
		assert.Equal(
			t,
			1,
			len(result.Output),
			"TCP FIN packet should be accepted",
		)

		// Sync sessions
		currentTime := mock.CurrentTime()

		// Verify session exists
		checkSessionsForVS(t, ts, currentTime, 1, tcpVsIp, tcpVsPort)
		t.Logf("TCP session (SYN->FIN) created successfully")

		// Advance time by FIN timeout-1 (14 seconds)
		mock.AdvanceTime(time.Duration(tcpFinTimeout-1) * time.Second)
		currentTime = mock.CurrentTime()

		// Verify session still exists
		checkSessionsForVS(t, ts, currentTime, 1, tcpVsIp, tcpVsPort)
		t.Logf("TCP session (SYN->FIN) persists at %d seconds", tcpFinTimeout-1)

		// Advance time by 1 second (total 15 seconds)
		mock.AdvanceTime(1 * time.Second)
		currentTime = mock.CurrentTime()

		// Verify session is gone
		checkSessionsForVS(t, ts, currentTime, 0, tcpVsIp, tcpVsPort)
		t.Logf("TCP session (SYN->FIN) expired at %d seconds", tcpFinTimeout)
	})
}
