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
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/module"
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
	balancer *module.Balancer,
	currentTime time.Time,
	expectedSessions []sessionKey,
	vsIp netip.Addr,
	vsPort uint16,
	realAddr netip.Addr,
) {
	t.Helper()

	expectedCount := uint(len(expectedSessions))

	// Get sessions info
	sessionsInfo, err := balancer.GetSessionsInfo(currentTime)
	require.NoError(t, err)
	assert.Equal(
		t,
		expectedCount,
		sessionsInfo.SessionsCount,
		"should have %d active sessions",
		expectedCount,
	)
	assert.Equal(
		t,
		int(expectedCount),
		len(sessionsInfo.Sessions),
		"sessions list should have %d entries",
		expectedCount,
	)

	// Create a map of expected sessions for validation
	expectedSessionsMap := make(map[sessionKey]bool)
	for _, session := range expectedSessions {
		expectedSessionsMap[session] = true
	}

	// Verify each session has correct properties
	for i, session := range sessionsInfo.Sessions {
		// Verify VS identifier
		assert.Equal(
			t,
			vsIp,
			session.Real.Vs.Ip,
			"session %d: VS IP should match",
			i,
		)
		assert.Equal(
			t,
			vsPort,
			session.Real.Vs.Port,
			"session %d: VS port should match",
			i,
		)

		// Verify Real identifier
		assert.Equal(
			t,
			realAddr,
			session.Real.Ip,
			"session %d: Real IP should match",
			i,
		)

		// Verify client IP and port match one of our expected sessions
		clientKey := sessionKey{
			ip:   session.ClientAddr,
			port: session.ClientPort,
		}
		assert.True(
			t,
			expectedSessionsMap[clientKey],
			"session %d: client %v:%d should be in expected sessions",
			i,
			session.ClientAddr,
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

	// Get state info to verify VS and Real active sessions
	stateInfo := balancer.GetStateInfo(currentTime)

	// Verify module active sessions
	assert.Equal(
		t,
		expectedCount,
		stateInfo.ActiveSessions.Value,
		"module should have %d active sessions",
		expectedCount,
	)

	// Verify VS active sessions
	require.Equal(t, 1, len(stateInfo.VsInfo), "should have exactly one VS")
	assert.Equal(
		t,
		expectedCount,
		stateInfo.VsInfo[0].ActiveSessions.Value,
		"VS should have %d active sessions",
		expectedCount,
	)

	// Verify Real active sessions
	require.Equal(t, 1, len(stateInfo.RealInfo), "should have exactly one Real")
	assert.Equal(
		t,
		expectedCount,
		stateInfo.RealInfo[0].ActiveSessions.Value,
		"Real should have %d active sessions",
		expectedCount,
	)
}

// TestSessionTableManual tests session table behavior with timeouts and resizing.
// It creates sessions, verifies they stay active after resizing or when refreshed
// within timeout, and tests that new sessions can be created alongside existing ones.
func TestSessionTableManual(t *testing.T) {
	vsIp := IpAddr("1.1.1.1")
	vsPort := uint16(80)
	realAddr := IpAddr("2.2.2.2")

	sessionTimeout := 60 // in seconds
	initialCapacity := 16
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
						SrcAddr: IpAddr("2.2.2.2").AsSlice(),
						SrcMask: IpAddr("2.2.2.2").AsSlice(),
						Enabled: true,
					},
				},
			},
		},
		SessionsTimeouts: &balancerpb.SessionsTimeouts{
			TcpSynAck: uint32(sessionTimeout),
			TcpSyn:    uint32(sessionTimeout),
			TcpFin:    uint32(sessionTimeout),
			Tcp:       uint32(sessionTimeout),
			Udp:       uint32(sessionTimeout),
			Default:   uint32(sessionTimeout),
		},
		Wlc: &balancerpb.WlcConfig{
			WlcPower:      10,
			MaxRealWeight: 1000,
			UpdatePeriod:  durationpb.New(0), // do not update in background
		},
	}

	stateConfig := &balancerpb.ModuleStateConfig{
		SessionTableCapacity: uint64(initialCapacity),
		SessionTableScanPeriod: durationpb.New(
			0,
		), // do not update in background
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

			packetLayers := MakeTCPPacket(
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
			ValidatePacket(t, balancer.GetModuleConfig(), packets[i], outPacket)
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
			packetLayers := MakeTCPPacket(
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
			ValidatePacket(t, balancer.GetModuleConfig(), packets[i], outPacket)
		}

		t.Logf(
			"Sent non-SYN packets to 10 sessions, all accepted and properly encapsulated",
		)
	})

	// Resize session table and get active sessions
	t.Run("Resize_And_Verify_Active_Sessions", func(t *testing.T) {
		currentTime := mock.CurrentTime()

		// Sync active sessions and resize table on demand
		err := balancer.SyncActiveSessionsAndWlcAndResizeTableOnDemand(
			currentTime,
		)
		require.NoError(t, err)

		// Check active sessions using helper function
		checkActiveSessions(
			t,
			balancer,
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
			packetLayers := MakeTCPPacket(
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
			ValidatePacket(t, balancer.GetModuleConfig(), packets[i], outPacket)
		}

		// Verify sessions are still active
		currentTime := mock.CurrentTime()
		err = balancer.SyncActiveSessionsAndWlcAndResizeTableOnDemand(
			currentTime,
		)
		require.NoError(t, err)

		sessionsInfo, err := balancer.GetSessionsInfo(currentTime)
		require.NoError(t, err)
		assert.Equal(
			t,
			uint(10),
			sessionsInfo.SessionsCount,
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

			packetLayers := MakeTCPPacket(
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
			packetLayers := MakeTCPPacket(
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
			ValidatePacket(t, balancer.GetModuleConfig(), packets[i], outPacket)
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
		err := balancer.SyncActiveSessionsAndWlcAndResizeTableOnDemand(
			currentTime,
		)
		require.NoError(t, err)

		// Check active sessions using helper function
		checkActiveSessions(
			t,
			balancer,
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
		// Advance time by 30 seconds (old sessions at 30s age, not expire)
		newTime := mock.AdvanceTime(30 * time.Second)
		t.Logf(
			"Advanced time to %v (30s elapsed, old sessions at 60s age)",
			newTime,
		)

		// Send packets only to new sessions
		packets := make([]gopacket.Packet, 0, 10)
		for _, session := range newSessions {
			packetLayers := MakeTCPPacket(
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
			ValidatePacket(t, balancer.GetModuleConfig(), packets[i], outPacket)
		}

		// Check active sessions immediately after sending packets
		currentTime := mock.CurrentTime()
		checkActiveSessions(
			t,
			balancer,
			currentTime,
			sessions,
			vsIp,
			vsPort,
			realAddr,
		)

		t.Logf(
			"Sent packets to 10 new sessions only, verified only new sessions are active",
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
				balancer,
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
			err := balancer.SyncActiveSessionsAndWlcAndResizeTableOnDemand(
				currentTime,
			)
			require.NoError(t, err)

			// Check active sessions after resize
			checkActiveSessions(
				t,
				balancer,
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
				balancer,
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
			balancer,
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
		err := balancer.GetModuleConfigState().Update(256, 0, 0.5, now)
		require.NoError(t, err, "failed to update module config state")
		cap := balancer.GetModuleConfigState().SessionTableCapacity()
		assert.GreaterOrEqual(
			t,
			cap,
			uint(256),
			"result capacity should be greater or than requested",
		)
	})

	t.Run("Phase6_Send_256_Sessions_Check_80_Percent", func(t *testing.T) {
		// Generate 256 unique packets and store session keys
		packets := make([]gopacket.Packet, 0, 256)
		sessionToPacket := make(map[sessionKey]gopacket.Packet)

		for range 256 {
			srcIP := randomClientIP()
			srcPort := randomPort()
			session := sessionKey{ip: srcIP, port: srcPort}

			packetLayers := MakeTCPPacket(
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
			ValidatePacket(
				t,
				balancer.GetModuleConfig(),
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
		err := balancer.SyncActiveSessionsAndWlcAndResizeTableOnDemand(
			currentTime,
		)
		require.NoError(t, err)

		// Check only accepted sessions are active
		checkActiveSessions(
			t,
			balancer,
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

	t.Run("Phase6_Advance_59s_Resize_To_512", func(t *testing.T) {
		// Advance time by 59 seconds (old sessions at 59s age)
		newTime := mock.AdvanceTime(59 * time.Second)
		t.Logf(
			"Advanced time to %v (59s elapsed, old sessions at 59s age)",
			newTime,
		)

		// Resize table to 512
		now := mock.CurrentTime()

		err := balancer.GetModuleConfigState().Update(512, 0, 0.5, now)
		require.NoError(t, err, "failed to resize session table to 512")

		t.Logf("Resized session table to 512")
	})

	t.Run("Phase6_Send_256_More_Sessions", func(t *testing.T) {
		// Generate 256 new unique packets and store session keys
		packets := make([]gopacket.Packet, 0, 256)
		sessionToPacket := make(map[sessionKey]gopacket.Packet)

		for range 256 {
			srcIP := randomClientIP()
			srcPort := randomPort()
			session := sessionKey{ip: srcIP, port: srcPort}

			packetLayers := MakeTCPPacket(
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
			ValidatePacket(
				t,
				balancer.GetModuleConfig(),
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
		err := balancer.SyncActiveSessionsAndWlcAndResizeTableOnDemand(
			currentTime,
		)
		require.NoError(t, err)

		// Verify both old and new sessions are active
		allSessions := append(
			[]sessionKey{},
			phase6OldSessions...,
		)
		allSessions = append(allSessions, phase6NewSessions...)

		checkActiveSessions(
			t,
			balancer,
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
			balancer,
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
		err := balancer.GetModuleConfigState().Update(300, 0, 0.5, now)
		require.NoError(t, err, "failed to resize session table to 300")

		currentTime := mock.CurrentTime()

		// Verify new sessions still active after resize
		checkActiveSessions(
			t,
			balancer,
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
