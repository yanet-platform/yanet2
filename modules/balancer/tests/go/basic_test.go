package balancer_test

import (
	"net/netip"
	"testing"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/tests/go/utils"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// Test addresses.
var (
	// Virtual services.
	vs1AddrV4 = netip.MustParseAddr("10.0.0.1")    // TCP IPv4
	vs2AddrV4 = netip.MustParseAddr("10.0.0.2")    // UDP IPv4
	vs3AddrV6 = netip.MustParseAddr("2001:db8::1") // TCP IPv6 GRE
	vs4AddrV4 = netip.MustParseAddr("10.0.0.4")    // TCP IPv4 OPS

	// Reals for VS1.
	real1a = netip.MustParseAddr("192.168.1.1")
	real1b = netip.MustParseAddr("192.168.1.2")
	real1c = netip.MustParseAddr("192.168.1.3")

	// Reals for VS2.
	real2a = netip.MustParseAddr("192.168.2.1")
	real2b = netip.MustParseAddr("192.168.2.2")

	// Reals for VS3 (IPv6).
	real3a = netip.MustParseAddr("fd00::1")
	real3b = netip.MustParseAddr("fd00::2")

	// Reals for VS4.
	real4a = netip.MustParseAddr("192.168.4.1")
	real4b = netip.MustParseAddr("192.168.4.2")

	// Client addresses.
	clientV4 = netip.MustParseAddr("3.3.3.1")
	clientV6 = netip.MustParseAddr("2001:db8::3")

	// New VS for UpdateVirtualServices test.
	vs5AddrV4 = netip.MustParseAddr("10.0.0.5")
	real5a    = netip.MustParseAddr("192.168.5.1")
	real5b    = netip.MustParseAddr("192.168.5.2")
)

func buildInitialConfig() *balancerpb.BalancerConfig {
	return utils.NewConfigBuilder().
		AddVS(
			// VS1: TCP IPv4, SOURCE_HASH, 3 reals
			utils.NewTCPVS(vs1AddrV4.String(), 80).
				AllowAll().
				AddReal(
					utils.R(real1a.String()),
					utils.R(real1b.String()),
					utils.R(real1c.String()),
				).Build(),

			// VS2: UDP IPv4, ROUND_ROBIN, 2 reals with different weights
			utils.NewUDPVS(vs2AddrV4.String(), 12345).
				WithScheduler(balancerpb.VsScheduler_ROUND_ROBIN).
				AllowAll().
				AddReal(
					utils.RW(real2a.String(), 2),
					utils.RW(real2b.String(), 1),
				).Build(),

			// VS3: TCP IPv6, GRE encapsulation, 2 reals
			utils.NewTCPVS(vs3AddrV6.String(), 443).
				GRE().
				AllowAll().
				AddReal(
					utils.R(real3a.String()),
					utils.R(real3b.String()),
				).Build(),

			// VS4: TCP IPv4, OPS mode (no sessions)
			utils.NewTCPVS(vs4AddrV4.String(), 8080).
				OPS().
				AllowAll().
				AddReal(
					utils.R(real4a.String()),
					utils.R(real4b.String()),
				).Build(),
		).
		Build()
}

func TestBasic(t *testing.T) {
	config := buildInitialConfig()

	ts, err := utils.Make(&utils.TestConfig{
		Mock:        utils.SingleWorkerMockConfig(128*datasize.MB, 4*utils.MB),
		Balancer:    config,
		AgentMemory: 64 * datasize.MB,
	})
	require.NoError(t, err)
	defer ts.Free()

	utils.EnableAllReals(t, ts)
	ts.Mock.SetCurrentTime(time.Unix(1000, 0))

	t.Run("InitialTraffic", func(t *testing.T) {
		// TCP IPv4 → VS1
		sendAndValidate(t, ts, clientV4, 10000, vs1AddrV4, 80, &layers.TCP{SYN: true})

		// UDP IPv4 → VS2
		sendAndValidateUDP(t, ts, clientV4, 10001, vs2AddrV4, 12345)

		// TCP IPv6 → VS3 (GRE)
		sendAndValidate(t, ts, clientV6, 10002, vs3AddrV6, 443, &layers.TCP{SYN: true})

		// TCP IPv4 → VS4 (OPS — no session)
		sendAndValidate(t, ts, clientV4, 10003, vs4AddrV4, 8080, &layers.TCP{SYN: true})
	})

	t.Run("SessionAffinity", func(t *testing.T) {
		// Same 5-tuple as above → must go to same real.
		results1 := sendAndCollect(t, ts, clientV4, 10000, vs1AddrV4, 80, &layers.TCP{}, 5)
		realIP, same := utils.AllPacketsToSameReal(results1)
		require.True(t, same, "session affinity violated for VS1")
		t.Logf("VS1 session pinned to real %s", realIP)

		// OPS → packets may go to different reals (not required to be same).
		sendAndCollect(t, ts, clientV4, 10003, vs4AddrV4, 8080, &layers.TCP{}, 5)
	})

	t.Run("ListSessions", func(t *testing.T) {
		now := ts.Mock.CurrentTime()

		// Collect all sessions without any filter.
		var allSessions []*balancerpb.Session
		err := ts.Balancer.ListSessions(nil, now, func(s *balancerpb.Session) error {
			allSessions = append(allSessions, s)
			return nil
		})
		require.NoError(t, err)
		require.NotEmpty(t, allSessions, "expected at least one session after sending traffic")

		// Group sessions by VS.
		type vsID struct {
			addr netip.Addr
			port uint32
		}
		sessionsByVS := make(map[vsID][]*balancerpb.Session)
		for _, s := range allSessions {
			addr, _ := netip.AddrFromSlice(s.VsId.Addr)
			key := vsID{addr: addr, port: s.VsId.Port}
			sessionsByVS[key] = append(sessionsByVS[key], s)
		}

		// VS1 (TCP IPv4 :80) — we sent traffic from clientV4:10000, expect a session.
		vs1Sessions := sessionsByVS[vsID{addr: vs1AddrV4, port: 80}]
		require.NotEmpty(t, vs1Sessions, "expected session(s) for VS1")

		// Verify session fields for VS1.
		found := false
		for _, s := range vs1Sessions {
			clientAddr, _ := netip.AddrFromSlice(s.ClientAddr)
			if clientAddr == clientV4 && s.ClientPort == 10000 {
				found = true
				assert.Equal(t, uint32(80), s.VsId.Port)
				assert.Equal(t, balancerpb.TransportProto_TCP, s.VsId.Proto)
				assert.NotNil(t, s.RealId)
				assert.NotNil(t, s.CreateTimestamp)
				assert.NotNil(t, s.LastPacketTimestamp)
				assert.NotNil(t, s.Timeout)
				break
			}
		}
		assert.True(t, found, "expected session for clientV4:10000 → VS1")

		// VS2 (UDP IPv4 :12345) — we sent traffic from clientV4:10001.
		vs2Sessions := sessionsByVS[vsID{addr: vs2AddrV4, port: 12345}]
		require.NotEmpty(t, vs2Sessions, "expected session(s) for VS2")

		// VS3 (TCP IPv6 :443) — we sent traffic from clientV6:10002.
		vs3Sessions := sessionsByVS[vsID{addr: vs3AddrV6, port: 443}]
		require.NotEmpty(t, vs3Sessions, "expected session(s) for VS3")

		// Filter by VS1 VIP — should return only VS1 sessions.
		var filteredSessions []*balancerpb.Session
		err = ts.Balancer.ListSessions(&balancerpb.Filter{
			Vip:    vs1AddrV4.AsSlice(),
			VsPort: ptrTo(uint32(80)),
			Proto:  ptrTo(balancerpb.TransportProto_TCP),
		}, now, func(s *balancerpb.Session) error {
			filteredSessions = append(filteredSessions, s)
			return nil
		})
		require.NoError(t, err)

		for _, s := range filteredSessions {
			addr, _ := netip.AddrFromSlice(s.VsId.Addr)
			assert.Equal(t, vs1AddrV4, addr, "filtered session should belong to VS1")
			assert.Equal(t, uint32(80), s.VsId.Port)
		}

		t.Logf("total sessions: %d, VS1=%d, VS2=%d, VS3=%d, filtered(VS1)=%d",
			len(allSessions), len(vs1Sessions), len(vs2Sessions), len(vs3Sessions),
			len(filteredSessions))
	})

	t.Run("Update", func(t *testing.T) {
		// Update: change VS2 real weights.
		updatedConfig := buildInitialConfig()
		updatedConfig.PacketHandler.Vs[1].Reals[0].Weight = 5 // real2a: 5
		updatedConfig.PacketHandler.Vs[1].Reals[1].Weight = 5 // real2b: 5

		now := ts.Mock.CurrentTime()
		_, err := ts.Balancer.Update(updatedConfig, &now)
		require.NoError(t, err)
		ts.Config = updatedConfig

		utils.EnableAllReals(t, ts)

		// Send traffic and check distribution.
		results := sendManyUDP(t, ts, vs2AddrV4, 12345, 200)
		counts := utils.CountPacketsPerReal(results)
		require.Len(t, counts, 2, "expected packets to 2 reals")
		t.Logf("VS2 distribution after Update: %v", counts)
	})

	t.Run("UpdateVirtualServices", func(t *testing.T) {
		// Add a new VS5.
		newVS := utils.NewTCPVS(vs5AddrV4.String(), 9090).
			AllowAll().
			AddReal(
				utils.R(real5a.String()),
				utils.R(real5b.String()),
			).Build()

		_, err := ts.Balancer.UpdateVS(
			[]*balancerpb.VirtualService{newVS},
		)
		require.NoError(t, err)

		// UpdateVirtualServices clones b.config, so ts.Config is now stale.
		ts.Config = ts.Balancer.Config()

		// Enable reals for the new VS.
		utils.EnableAllReals(t, ts)

		// Send traffic to VS5 and validate.
		sendAndValidate(t, ts, clientV4, 20000, vs5AddrV4, 9090, &layers.TCP{SYN: true})

		// Existing VS1 still works.
		sendAndValidate(t, ts, clientV4, 20001, vs1AddrV4, 80, &layers.TCP{SYN: true})
	})

	t.Run("DeleteVirtualServices", func(t *testing.T) {
		// Delete VS4 (OPS).
		vs4ToDelete := &balancerpb.VirtualService{
			Id: &balancerpb.VsIdentifier{
				Addr:  vs4AddrV4.AsSlice(),
				Port:  8080,
				Proto: balancerpb.TransportProto_TCP,
			},
		}

		_, err := ts.Balancer.DeleteVS(
			[]*balancerpb.VirtualService{vs4ToDelete},
		)
		require.NoError(t, err)

		// Remove from stored config.
		var remaining []*balancerpb.VirtualService
		for _, vs := range ts.Config.PacketHandler.Vs {
			vsAddr, _ := netip.AddrFromSlice(vs.Id.Addr)
			if vsAddr != vs4AddrV4 || vs.Id.Port != 8080 {
				remaining = append(remaining, vs)
			}
		}
		ts.Config.PacketHandler.Vs = remaining

		// Traffic to deleted VS4 should be dropped.
		pkt := xpacket.LayersToPacket(t,
			utils.MakeTCPPacket(clientV4, 30000, vs4AddrV4, 8080, &layers.TCP{SYN: true})...,
		)
		result, err := ts.Mock.HandlePackets(pkt)
		require.NoError(t, err)
		assert.Empty(t, result.Output, "expected no output for deleted VS")
		assert.NotEmpty(t, result.Drop, "expected drop for deleted VS")

		// VS1 still works.
		sendAndValidate(t, ts, clientV4, 30001, vs1AddrV4, 80, &layers.TCP{SYN: true})
	})

	t.Run("UpdateReals", func(t *testing.T) {
		// Disable real1b in VS1.
		_, err := ts.Balancer.UpdateReals([]*balancerpb.RealUpdate{
			utils.DisableReal(
				ts.Config.PacketHandler.Vs[0].Id,
				ts.Config.PacketHandler.Vs[0].Reals[1].Id,
			),
		}, false)
		require.NoError(t, err)

		// Send many packets with unique sources → should only go to real1a and real1c.
		results := sendMany(t, ts, vs1AddrV4, 80, 100)
		counts := utils.CountPacketsPerReal(results)
		_, hasDisabled := counts[real1b]
		assert.False(t, hasDisabled, "disabled real1b should receive no traffic, got %v", counts)
		t.Logf("VS1 distribution with real1b disabled: %v", counts)

		// Re-enable real1b.
		_, err = ts.Balancer.UpdateReals([]*balancerpb.RealUpdate{
			utils.EnableReal(
				ts.Config.PacketHandler.Vs[0].Id,
				ts.Config.PacketHandler.Vs[0].Reals[1].Id,
			),
		}, false)
		require.NoError(t, err)

		// Now traffic should go to all 3 reals.
		results = sendMany(t, ts, vs1AddrV4, 80, 200)
		counts = utils.CountPacketsPerReal(results)
		assert.Len(t, counts, 3, "expected 3 reals after re-enabling, got %v", counts)
	})

	t.Run("GetState", func(t *testing.T) {
		ref := utils.StateRef()
		states, err := ts.Balancer.GetState(ref, nil, true, ts.Mock.CurrentTime())
		require.NoError(t, err)
		require.NotEmpty(t, states)

		state := states[0]

		// L4 stats should show some processed packets.
		require.NotNil(t, state.L4Stats)
		assert.Greater(t, state.L4Stats.IncomingPackets, uint64(0),
			"expected non-zero incoming packets")
		assert.Greater(t, state.L4Stats.OutgoingPackets, uint64(0),
			"expected non-zero outgoing packets")

		// Should have VS states.
		assert.NotEmpty(t, state.VirtualServices, "expected virtual service states")

		t.Logf("L4 stats: incoming=%d outgoing=%d select_vs_failed=%d",
			state.L4Stats.IncomingPackets,
			state.L4Stats.OutgoingPackets,
			state.L4Stats.SelectVsFailed,
		)

		for _, vs := range state.VirtualServices {
			vsAddr, _ := netip.AddrFromSlice(vs.Id.Addr)
			t.Logf("VS %s:%d incoming=%d outgoing=%d sessions=%d",
				vsAddr, vs.Id.Port,
				vs.Stats.IncomingPackets, vs.Stats.OutgoingPackets,
				vs.Stats.CreatedSessions,
			)
			for _, real := range vs.Reals {
				realAddr, _ := netip.AddrFromSlice(real.Id.Ip)
				t.Logf("  Real %s: packets=%d enabled=%t weight=%d",
					realAddr, real.RealStats.Packets, real.Enabled, real.Weight,
				)
			}
		}
	})

	ts.Mock.AdvanceTime(time.Second * 200)

	t.Run("GetStateAfterTimeAdvance", func(t *testing.T) {
		states, err := ts.Balancer.GetState(nil, nil, false, ts.Mock.CurrentTime())
		require.NoError(t, err)
		require.NotEmpty(t, states)

		state := states[0]

		assert.Equal(t, state.ActiveSessions, uint64(0),
			"expected zero active sessions after time advance")

		for _, vs := range state.VirtualServices {
			for _, real := range vs.Reals {
				assert.Equal(t, real.ActiveSessions, uint64(0),
					"expected zero active sessions for real %s", real.Id)
			}
			assert.Equal(t, vs.ActiveSessions, uint64(0),
				"expected zero active sessions for VS %s", vs.Id)
		}
	})

	t.Run("ListSessionsAfterTimeAdvance", func(t *testing.T) {
		now := ts.Mock.CurrentTime()
		var allSessions []*balancerpb.Session
		err := ts.Balancer.ListSessions(nil, now, func(s *balancerpb.Session) error {
			allSessions = append(allSessions, s)
			return nil
		})
		require.NoError(t, err)
		require.Empty(t, allSessions, "expected no sessions after time advance")
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func sendAndValidate(
	t *testing.T,
	ts *utils.TestSetup,
	srcIP netip.Addr,
	srcPort uint16,
	dstIP netip.Addr,
	dstPort uint16,
	tcp *layers.TCP,
) {
	t.Helper()

	pktLayers := utils.MakeTCPPacket(srcIP, srcPort, dstIP, dstPort, tcp)
	pkt := xpacket.LayersToPacket(t, pktLayers...)

	result, err := ts.Mock.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "expected 1 output packet")
	require.Empty(t, result.Drop, "expected no drops")

	utils.ValidatePacket(t, ts.Config, pkt, result.Output[0])
}

func sendAndValidateUDP(
	t *testing.T,
	ts *utils.TestSetup,
	srcIP netip.Addr,
	srcPort uint16,
	dstIP netip.Addr,
	dstPort uint16,
) {
	t.Helper()

	pktLayers := utils.MakeUDPPacket(srcIP, srcPort, dstIP, dstPort)
	pkt := xpacket.LayersToPacket(t, pktLayers...)

	result, err := ts.Mock.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "expected 1 output packet")
	require.Empty(t, result.Drop, "expected no drops")

	utils.ValidatePacket(t, ts.Config, pkt, result.Output[0])
}

func sendAndCollect(
	t *testing.T,
	ts *utils.TestSetup,
	srcIP netip.Addr,
	srcPort uint16,
	dstIP netip.Addr,
	dstPort uint16,
	tcp *layers.TCP,
	count int,
) []*framework.PacketInfo {
	t.Helper()

	var results []*framework.PacketInfo
	for range count {
		pkt := xpacket.LayersToPacket(t,
			utils.MakeTCPPacket(srcIP, srcPort, dstIP, dstPort, tcp)...,
		)
		result, err := ts.Mock.HandlePackets(pkt)
		require.NoError(t, err)
		require.Len(t, result.Output, 1)
		utils.ValidatePacket(t, ts.Config, pkt, result.Output[0])
		results = append(results, result.Output[0])
	}
	return results
}

// sendMany sends TCP SYN packets with unique source IPs to the given VS.
func sendMany(
	t *testing.T,
	ts *utils.TestSetup,
	dstIP netip.Addr,
	dstPort uint16,
	count int,
) []*framework.PacketInfo {
	t.Helper()

	var results []*framework.PacketInfo
	baseIP := netip.MustParseAddr("5.5.0.1")

	for i := range count {
		b := baseIP.As4()
		b[2] = byte(i >> 8)
		b[3] = byte(i&0xff) + 1
		srcIP := netip.AddrFrom4(b)

		pkt := xpacket.LayersToPacket(t,
			utils.MakeTCPPacket(srcIP, uint16(40000+i), dstIP, dstPort, &layers.TCP{SYN: true})...,
		)
		result, err := ts.Mock.HandlePackets(pkt)
		require.NoError(t, err)
		require.Len(t, result.Output, 1, "packet %d: expected 1 output", i)
		utils.ValidatePacket(t, ts.Config, pkt, result.Output[0])
		results = append(results, result.Output[0])
	}
	return results
}

// sendManyUDP sends UDP packets with unique source IPs to the given VS.
func sendManyUDP(
	t *testing.T,
	ts *utils.TestSetup,
	dstIP netip.Addr,
	dstPort uint16,
	count int,
) []*framework.PacketInfo {
	t.Helper()

	var results []*framework.PacketInfo
	baseIP := netip.MustParseAddr("6.6.0.1")

	for i := range count {
		b := baseIP.As4()
		b[2] = byte(i >> 8)
		b[3] = byte(i&0xff) + 1
		srcIP := netip.AddrFrom4(b)

		pkt := xpacket.LayersToPacket(t,
			utils.MakeUDPPacket(srcIP, uint16(50000+i), dstIP, dstPort)...,
		)
		result, err := ts.Mock.HandlePackets(pkt)
		require.NoError(t, err)
		require.Len(t, result.Output, 1, "packet %d: expected 1 output", i)
		utils.ValidatePacket(t, ts.Config, pkt, result.Output[0])
		results = append(results, result.Output[0])
	}
	return results
}

func ptrTo[T any](v T) *T {
	return &v
}
