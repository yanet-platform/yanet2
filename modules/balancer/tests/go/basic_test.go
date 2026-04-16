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
)

// Test addresses.
var (
	// Virtual services.
	vs1Addr = netip.MustParseAddr("10.0.0.1")    // TCP IPv4
	vs2Addr = netip.MustParseAddr("10.0.0.2")    // UDP IPv4
	vs3Addr = netip.MustParseAddr("2001:db8::1") // TCP IPv6 GRE
	vs4Addr = netip.MustParseAddr("10.0.0.4")    // TCP IPv4 OPS
	vs5Addr = netip.MustParseAddr("10.0.0.5")

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

	// Reals for VS5.
	real5a = netip.MustParseAddr("192.168.5.1")
	real5b = netip.MustParseAddr("192.168.5.2")

	// Client addresses.
	clientV4 = netip.MustParseAddr("3.3.3.1")
	clientV6 = netip.MustParseAddr("2001:db8::3")
)

func buildInitialConfig() *balancerpb.BalancerConfig {
	return utils.NewConfigBuilder().
		AddVS(
			// VS1: TCP IPv4, source hash, 3 reals
			utils.NewTCPVS(vs1Addr.String(), 80).
				AllowAll().
				AddReal(
					utils.R(real1a.String()),
					utils.R(real1b.String()),
					utils.R(real1c.String()),
				).Build(),

			// VS2: UDP IPv4, round robin, 2 reals with different weights
			utils.NewUDPVS(vs2Addr.String(), 12345).
				WithScheduler(balancerpb.VsScheduler_WRR).
				AllowAll().
				AddReal(
					utils.RW(real2a.String(), 2),
					utils.RW(real2b.String(), 1),
				).Build(),

			// VS3: TCP IPv6, GRE encapsulation, 2 reals
			utils.NewTCPVS(vs3Addr.String(), 443).
				GRE().
				AllowAll().
				AddReal(
					utils.R(real3a.String()),
					utils.R(real3b.String()),
				).Build(),

			// VS4: TCP IPv4, OPS mode (no sessions)
			utils.NewTCPVS(vs4Addr.String(), 8080).
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
		Mock:        utils.SingleWorkerMockConfig(128*datasize.MB, 4*datasize.MB),
		Balancer:    config,
		AgentMemory: 64 * datasize.MB,
	})
	require.NoError(t, err)
	defer ts.Free()

	utils.EnableAllReals(t, ts)
	ts.Mock.SetCurrentTime(time.Unix(1000, 0))

	vs1 := utils.VsIDFromPb(ts.Balancer.Config().PacketHandler.Vs[0].Id)
	vs2 := utils.VsIDFromPb(ts.Balancer.Config().PacketHandler.Vs[1].Id)
	vs3 := utils.VsIDFromPb(ts.Balancer.Config().PacketHandler.Vs[2].Id)

	t.Run("InitialTraffic", func(t *testing.T) {
		// TCP IPv4 => VS1
		utils.SendAndValidateTCP(t, ts, clientV4, 10000, vs1Addr, 80, &layers.TCP{SYN: true})

		// UDP IPv4 => VS2
		utils.SendAndValidateUDP(t, ts, clientV4, 10001, vs2Addr, 12345)

		// TCP IPv6 => VS3 (GRE)
		utils.SendAndValidateTCP(t, ts, clientV6, 10002, vs3Addr, 443, &layers.TCP{SYN: true})

		// TCP IPv4 => VS4
		// OPS mode => no session created
		utils.SendAndValidateTCP(t, ts, clientV4, 10003, vs4Addr, 8080, &layers.TCP{SYN: true})
	})

	t.Run("SessionAffinity", func(t *testing.T) {
		packetsCount := 10
		var rl *utils.RealID
		for range packetsCount {
			pkt := utils.SendAndValidateTCP(t, ts, clientV4, 10000, vs1Addr, 80, &layers.TCP{})
			if rl == nil {
				rl = &pkt.RealID
			} else if pkt.RealID.Compare(rl) != 0 {
				t.Fatalf("expected all packets to go to the same real, got %s and %s", rl, &pkt.RealID)
			}
		}
	})

	t.Run("ListSessions", func(t *testing.T) {
		now := ts.Mock.CurrentTime()

		count := 0
		meetVs1 := false
		meetVs2 := false
		meetVs3 := false
		err := ts.Balancer.ListSessions(nil, now, func(s *balancerpb.Session) error {
			pkt, err := utils.PacketInfoFromSessionPb(s)
			require.NoError(t, err)
			count++

			switch {
			case pkt.VsID.Compare(&vs1) == 0:
				meetVs1 = true
			case pkt.VsID.Compare(&vs2) == 0:
				meetVs2 = true
			case pkt.VsID.Compare(&vs3) == 0:
				meetVs3 = true
			}

			return nil
		})
		require.NoError(t, err)

		assert.Equal(t, count, 3, "expected to meet 3 sessions (one for each no-ops-VS)")
		assert.True(t, meetVs1, "expected to meet VS1")
		assert.True(t, meetVs2, "expected to meet VS2")
		assert.True(t, meetVs3, "expected to meet VS3")
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

		for range 10 {
			utils.SendAndValidateUDP(t, ts, clientV4, 20000, vs2Addr, 12345)
		}
	})

	t.Run("UpdateVS", func(t *testing.T) {
		// Add a new VS5.
		newVS := utils.NewTCPVS(vs5Addr.String(), 9090).
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
		for range 10 {
			utils.SendAndValidateTCP(t, ts, clientV4, 20000, vs5Addr, 9090, &layers.TCP{SYN: true})
		}

		// Existing VS1 still works.
		for range 10 {
			utils.SendAndValidateTCP(t, ts, clientV4, 20001, vs1Addr, 80, &layers.TCP{SYN: true})
		}
	})

	t.Run("DeleteVS", func(t *testing.T) {
		// Delete VS4 (OPS).
		vs4ToDelete := &balancerpb.VirtualService{
			Id: &balancerpb.VsIdentifier{
				Addr:  vs4Addr.AsSlice(),
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
			if vsAddr != vs4Addr || vs.Id.Port != 8080 {
				remaining = append(remaining, vs)
			}
		}
		ts.Config.PacketHandler.Vs = remaining

		// Traffic to deleted VS4 should be dropped.
		pkt := xpacket.LayersToPacket(t,
			utils.MakeTCPPacketLayers(clientV4, 30000, vs4Addr, 8080, &layers.TCP{SYN: true})...,
		)
		result, err := ts.Mock.HandlePackets(pkt)
		require.NoError(t, err)
		assert.Empty(t, result.Output, "expected no output for deleted VS")
		assert.NotEmpty(t, result.Drop, "expected drop for deleted VS")

		// VS1 still works.
		for range 10 {
			utils.SendAndValidateTCP(t, ts, clientV4, 30001, vs1Addr, 80, &layers.TCP{SYN: true})
		}
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

		// Send many packets with unique sources -> should only go to real1a and real1c.
		results := utils.SendAndValidateRandomSrcPorts(
			t,
			ts,
			clientV4,
			vs1Addr,
			80,
			&layers.TCP{SYN: true},
			1000,
		)
		counts, err := utils.CountPacketsPerReal(results)
		require.NoError(t, err)

		_, hasDisabled := counts[real1b]
		assert.False(t, hasDisabled, "disabled real1b should receive no traffic, got %v", counts)
		assert.Equal(t, 2, len(counts))

		// Re-enable real1b.
		_, err = ts.Balancer.UpdateReals([]*balancerpb.RealUpdate{
			utils.EnableReal(
				ts.Config.PacketHandler.Vs[0].Id,
				ts.Config.PacketHandler.Vs[0].Reals[1].Id,
			),
		}, false)
		require.NoError(t, err)

		results = utils.SendAndValidateRandomSrcPorts(
			t,
			ts,
			clientV4,
			vs1Addr,
			80,
			&layers.TCP{SYN: true},
			1000,
		)
		counts, err = utils.CountPacketsPerReal(results)
		require.NoError(t, err)

		_, hasEnabled := counts[real1b]
		assert.True(t, hasEnabled, "enabled real1b should receive traffic, got %v", counts)
		assert.Equal(t, 3, len(counts))
	})

	t.Run("GetState", func(t *testing.T) {
		ref := utils.PacketHandlerRef()
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
		assert.Equal(t, 4, len(state.VirtualServices), "expected 4 virtual services")
		assert.Greater(t, state.ActiveSessions, uint64(0),
			"expected non-zero active sessions")
		assert.NotNil(t, state.LastPacketTimestamp, "expected last packet timestamp")
	})

	ts.Mock.AdvanceTime(time.Second * 200)

	t.Run("GetStateAfterTimeAdvance", func(t *testing.T) {
		states, err := ts.Balancer.GetState(nil, nil, false, ts.Mock.CurrentTime())
		require.NoError(t, err)
		require.NotEmpty(t, states)

		state := states[0]

		assert.Equal(t, state.ActiveSessions, uint64(0),
			"expected zero active sessions after time advance")
	})

	t.Run("ListSessionsAfterTimeAdvance", func(t *testing.T) {
		now := ts.Mock.CurrentTime()
		found := false
		err := ts.Balancer.ListSessions(nil, now, func(_ *balancerpb.Session) error {
			found = true
			return nil
		})
		require.NoError(t, err)
		require.False(t, found, "expected no sessions after time advance")
	})
}
