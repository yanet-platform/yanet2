package balancer

import (
	"fmt"
	"math"
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

// PRR, WLC, WRR
// one packet scheduler
// sessions are created
// pure l3
// test timeouts with mock
// session table

////////////////////////////////////////////////////////////////////////////////

func TestWlc(t *testing.T) {
	vsIp := IpAddr("1.1.1.1")
	vsPort := uint16(80)
	real1Ip := IpAddr("2.2.2.2")
	real2Ip := IpAddr("3.3.3.3")
	real3Ip := IpAddr("4.4.4.4")

	client := func(id int) netip.Addr {
		return IpAddr(
			fmt.Sprintf("10.%d.%d.%d", id/(256*256)%256, (id/256)%256, id%256),
		)
	}

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
						Size: 8,
					},
				},
				Scheduler: balancerpb.VsScheduler_WLC,
				Flags: &balancerpb.VsFlags{
					Gre:    false,
					FixMss: false,
					Ops:    false,
					PureL3: false,
				},
				Reals: []*balancerpb.Real{
					{
						Weight:  1,
						DstAddr: real1Ip.AsSlice(),
						Enabled: true,
						SrcAddr: real1Ip.AsSlice(),
						SrcMask: real1Ip.AsSlice(),
					},
					{
						Weight:  1,
						DstAddr: real2Ip.AsSlice(),
						Enabled: true,
						SrcAddr: real2Ip.AsSlice(),
						SrcMask: real2Ip.AsSlice(),
					},
					{
						Weight:  2,
						DstAddr: real3Ip.AsSlice(),
						Enabled: false,
						SrcAddr: real3Ip.AsSlice(),
						SrcMask: real3Ip.AsSlice(),
					},
				},
			},
		},
		SessionsTimeouts: &balancerpb.SessionsTimeouts{
			TcpSynAck: 60,
			TcpSyn:    60,
			TcpFin:    60,
			Tcp:       60,
			Udp:       60,
			Default:   60,
		},
		Wlc: &balancerpb.WlcConfig{
			WlcPower:      10,
			MaxRealWeight: 1000,
			UpdatePeriod:  &durationpb.Duration{},
		},
	}

	setup, err := SetupTest(&TestConfig{
		moduleConfig: config,
		stateConfig: &balancerpb.ModuleStateConfig{
			SessionTableCapacity:      8000,
			SessionTableMaxLoadFactor: 0.5,
			SessionTableScanPeriod:    durationpb.New(0),
		},
	})
	require.NoError(t, err, "failed to setup test")
	defer setup.Free()

	mock := setup.mock
	balancer := setup.balancer

	// send random packets

	packets := 500

	// send random SYNs to the first two reals
	// expect uniform distribution

	mock.SetCurrentTime(time.Unix(0, 0))
	now := mock.CurrentTime()

	t.Run("Send_Random_SYNs", func(t *testing.T) {
		packetsToSend := make([]gopacket.Packet, 0, packets)
		for packetIdx := range packets {
			client := client(packetIdx)
			packetLayers := MakeTCPPacket(
				client,
				1000,
				vsIp,
				vsPort,
				&layers.TCP{SYN: true},
			)
			packet := xpacket.LayersToPacket(t, packetLayers...)
			packetsToSend = append(packetsToSend, packet)
		}
		result, err := mock.HandlePackets(packetsToSend...)
		require.NoError(t, err)

		require.Equal(t, packets, len(result.Output))
		require.Empty(t, result.Drop)

		configStats := balancer.GetConfigStats(
			defaultDeviceName,
			defaultPipelineName,
			defaultFunctionName,
			defaultChainName,
		)

		assert.Equal(
			t,
			uint64(packets/2),
			configStats.Reals[0].Stats.CreatedSessions,
		)
		assert.Equal(
			t,
			uint64(packets/2),
			configStats.Reals[1].Stats.CreatedSessions,
		)
		assert.Equal(t, uint64(0), configStats.Reals[2].Stats.CreatedSessions)

		// Scan active sessions, update them,
		// and recalculate effective weights
		err = balancer.SyncActiveSessionsAndWlcAndResizeTableOnDemand(now)
		require.NoError(t, err)

		stateInfo := balancer.GetStateInfo(now)
		reals := stateInfo.RealInfo

		assert.Equal(t, uint(packets/2), reals[0].ActiveSessions.Value)
		assert.Equal(t, uint(packets/2), reals[1].ActiveSessions.Value)
		assert.Equal(t, uint(0), reals[2].ActiveSessions.Value)
	})

	// enable third real

	t.Run("Enable_Third_Real", func(t *testing.T) {
		config, stateConfig := balancer.GetConfig()
		config.VirtualServices[0].Reals[2].Enabled = true
		err := balancer.Update(config, stateConfig)
		require.NoError(t, err)
	})

	// send more random packets
	// ensure, we have good distribution after

	t.Run("Send_Random_SYNs_Again", func(t *testing.T) {
		packetsToSend := make([]gopacket.Packet, 0)
		firstClient := packets
		packets = 5 * packets
		for packetIdx := range packets {
			if packetIdx%50 == 0 {
				if err := balancer.SyncActiveSessionsAndWlcAndResizeTableOnDemand(now); err != nil {
					t.Errorf(
						"failed to update active sessions: packetIdx=%d",
						packetIdx,
					)
				}
				result, err := mock.HandlePackets(packetsToSend...)
				assert.NoError(t, err)
				assert.Equal(t, len(packetsToSend), len(result.Output))
				assert.Empty(t, result.Drop)
				packetsToSend = make([]gopacket.Packet, 0)
			}
			client := client(firstClient + packetIdx)
			packetLayers := MakeTCPPacket(
				client,
				1000,
				vsIp,
				vsPort,
				&layers.TCP{SYN: true},
			)
			packet := xpacket.LayersToPacket(t, packetLayers...)
			packetsToSend = append(packetsToSend, packet)
		}
		result, err := mock.HandlePackets(packetsToSend...)
		require.NoError(t, err)

		require.Equal(t, len(packetsToSend), len(result.Output))
		require.Empty(t, result.Drop)

		configStats := balancer.GetConfigStats(
			defaultDeviceName,
			defaultPipelineName,
			defaultFunctionName,
			defaultChainName,
		)

		firstTwoPackets := configStats.Reals[0].Stats.CreatedSessions + configStats.Reals[1].Stats.CreatedSessions
		thirdPackets := configStats.Reals[2].Stats.CreatedSessions

		rel := float64(thirdPackets) / float64(firstTwoPackets)
		assert.Less(t, math.Abs(rel-1.0), 0.3)
	})
}

// TestHashConsistency verifies that packets from the same client
// consistently go to the same real server based on hash calculation,
// even without active sessions (PureL3 mode or expired sessions).
func TestHashConsistency(t *testing.T) {
	// Common test data
	vs1Ip := IpAddr("1.1.1.1")
	vs1Port := uint16(80)
	vs2Ip := IpAddr("1.1.1.2")
	vs2Port := uint16(8080)
	real1Ip := IpAddr("2.2.2.2")
	real2Ip := IpAddr("3.3.3.3")
	clientIp := IpAddr("10.0.0.100")
	clientPort := uint16(12345)

	// Common setup with two virtual services:
	// - VS1: PureL3 mode (no sessions)
	// - VS2: Sessions enabled with 1 second timeout
	config := &balancerpb.ModuleConfig{
		SourceAddressV4: IpAddr("5.5.5.5").AsSlice(),
		SourceAddressV6: IpAddr("fe80::5").AsSlice(),
		VirtualServices: []*balancerpb.VirtualService{
			{
				Addr:  vs1Ip.AsSlice(),
				Port:  uint32(vs1Port),
				Proto: balancerpb.TransportProto_TCP,
				AllowedSrcs: []*balancerpb.Subnet{
					{
						Addr: IpAddr("10.0.0.0").AsSlice(),
						Size: 8,
					},
				},
				Scheduler: balancerpb.VsScheduler_WRR,
				Flags: &balancerpb.VsFlags{
					Gre:    false,
					FixMss: false,
					Ops:    false,
					PureL3: true, // Enable PureL3 - no sessions
				},
				Reals: []*balancerpb.Real{
					{
						Weight:  1,
						DstAddr: real1Ip.AsSlice(),
						Enabled: true,
						SrcAddr: real1Ip.AsSlice(),
						SrcMask: real1Ip.AsSlice(),
					},
					{
						Weight:  1,
						DstAddr: real2Ip.AsSlice(),
						Enabled: true,
						SrcAddr: real2Ip.AsSlice(),
						SrcMask: real2Ip.AsSlice(),
					},
				},
			},
			{
				Addr:  vs2Ip.AsSlice(),
				Port:  uint32(vs2Port),
				Proto: balancerpb.TransportProto_TCP,
				AllowedSrcs: []*balancerpb.Subnet{
					{
						Addr: IpAddr("10.0.0.0").AsSlice(),
						Size: 8,
					},
				},
				Scheduler: balancerpb.VsScheduler_WRR,
				Flags: &balancerpb.VsFlags{
					Gre:    false,
					FixMss: false,
					Ops:    false,
					PureL3: false, // Sessions enabled
				},
				Reals: []*balancerpb.Real{
					{
						Weight:  1,
						DstAddr: real1Ip.AsSlice(),
						Enabled: true,
						SrcAddr: real1Ip.AsSlice(),
						SrcMask: real1Ip.AsSlice(),
					},
					{
						Weight:  1,
						DstAddr: real2Ip.AsSlice(),
						Enabled: true,
						SrcAddr: real2Ip.AsSlice(),
						SrcMask: real2Ip.AsSlice(),
					},
				},
			},
		},
		SessionsTimeouts: &balancerpb.SessionsTimeouts{
			TcpSynAck: 1, // 1 second timeout
			TcpSyn:    1,
			TcpFin:    1,
			Tcp:       1,
			Udp:       1,
			Default:   1,
		},
		Wlc: &balancerpb.WlcConfig{
			WlcPower:      10,
			MaxRealWeight: 1000,
			UpdatePeriod:  &durationpb.Duration{},
		},
	}

	setup, err := SetupTest(&TestConfig{
		moduleConfig: config,
		stateConfig: &balancerpb.ModuleStateConfig{
			SessionTableCapacity:      8000,
			SessionTableMaxLoadFactor: 0.5,
			SessionTableScanPeriod:    durationpb.New(time.Second),
		},
	})
	require.NoError(t, err, "failed to setup test")
	defer setup.Free()

	mock := setup.mock
	balancer := setup.balancer

	t.Run("PureL3_Hash_Consistency", func(t *testing.T) {
		// Test with PureL3 virtual service (no sessions)
		mock.SetCurrentTime(time.Unix(0, 0))

		// Send multiple packets from the same client (same 5-tuple)
		var firstRealIp netip.Addr
		numPackets := 10

		for i := range numPackets {
			packetLayers := MakeTCPPacket(
				clientIp,
				clientPort,
				vs1Ip,
				vs1Port,
				&layers.TCP{SYN: true},
			)
			packet := xpacket.LayersToPacket(t, packetLayers...)

			result, err := mock.HandlePackets(packet)
			require.NoError(t, err, "packet %d failed", i)
			require.Len(
				t,
				result.Output,
				1,
				"packet %d: expected 1 output packet",
				i,
			)
			require.Empty(
				t,
				result.Drop,
				"packet %d: no packets should be dropped",
				i,
			)

			// Extract the real server IP from the tunneled packet (outer header)
			outputPacket := result.Output[0]

			// Parse the raw packet data to get the outer IP header
			parsed := gopacket.NewPacket(
				outputPacket.RawData,
				layers.LayerTypeEthernet,
				gopacket.Default,
			)
			ipLayer := parsed.Layer(layers.LayerTypeIPv4)
			require.NotNil(
				t,
				ipLayer,
				"packet %d: output should have IPv4 layer",
				i,
			)

			ipv4, ok := ipLayer.(*layers.IPv4)
			require.True(t, ok, "packet %d: failed to cast to IPv4", i)

			realIp, ok := netip.AddrFromSlice(ipv4.DstIP)
			require.True(t, ok, "packet %d: failed to parse real IP", i)

			if i == 0 {
				firstRealIp = realIp
				t.Logf("First packet routed to real: %s", firstRealIp)
			} else {
				// All packets from same client should go to same real
				assert.Equal(t, firstRealIp, realIp,
					"Packet %d went to different real (%s) than first packet (%s). "+
						"Hash-based consistency failed in PureL3 mode!", i, realIp, firstRealIp)
			}
		}

		t.Logf(
			"SUCCESS: All %d packets from same client consistently routed to %s (PureL3 mode)",
			numPackets,
			firstRealIp,
		)
	})

	t.Run("Expired_Sessions_Hash_Consistency", func(t *testing.T) {
		// Test with session-based virtual service (sessions expire after 1 second)
		mock.SetCurrentTime(time.Unix(0, 0))

		var firstRealIp netip.Addr
		numRounds := 5

		for round := range numRounds {
			// Send packet to VS2 (session-based)
			packetLayers := MakeTCPPacket(
				clientIp,
				clientPort,
				vs2Ip,
				vs2Port,
				&layers.TCP{SYN: true},
			)
			packet := xpacket.LayersToPacket(t, packetLayers...)

			result, err := mock.HandlePackets(packet)
			require.NoError(t, err, "round %d: packet failed", round)
			require.Len(
				t,
				result.Output,
				1,
				"round %d: expected 1 output packet",
				round,
			)

			// Extract real server IP from the tunneled packet (outer header)
			outputPacket := result.Output[0]

			// Parse the raw packet data to get the outer IP header
			parsed := gopacket.NewPacket(
				outputPacket.RawData,
				layers.LayerTypeEthernet,
				gopacket.Default,
			)
			ipLayer := parsed.Layer(layers.LayerTypeIPv4)
			require.NotNil(
				t,
				ipLayer,
				"round %d: output should have IPv4 layer",
				round,
			)

			ipv4, ok := ipLayer.(*layers.IPv4)
			require.True(t, ok, "round %d: failed to cast to IPv4", round)

			realIp, ok := netip.AddrFromSlice(ipv4.DstIP)
			require.True(t, ok, "round %d: failed to parse real IP", round)

			if round == 0 {
				firstRealIp = realIp
				t.Logf(
					"Round %d: First packet routed to real: %s",
					round,
					firstRealIp,
				)
			} else {
				assert.Equal(t, firstRealIp, realIp,
					"Round %d: Packet went to different real (%s) than first packet (%s). "+
						"Hash-based consistency failed after session expiry!", round, realIp, firstRealIp)
			}

			// Advance time by 2 seconds to expire the session
			mock.AdvanceTime(2 * time.Second)

			// Clean up expired sessions
			err = balancer.SyncActiveSessionsAndWlcAndResizeTableOnDemand(
				mock.CurrentTime(),
			)
			require.NoError(t, err, "round %d: failed to sync sessions", round)
		}

		t.Logf(
			"SUCCESS: All %d packets from same client consistently routed to %s (with session expiry)",
			numRounds,
			firstRealIp,
		)
	})
}
