package balancer_test

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
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestWlc(t *testing.T) {
	vsIp := test_utils.IpAddr("1.1.1.1")
	vsPort := uint16(80)
	real1Ip := test_utils.IpAddr("2.2.2.2")
	real2Ip := test_utils.IpAddr("3.3.3.3")
	real3Ip := test_utils.IpAddr("4.4.4.4")

	client := func(id int) netip.Addr {
		return test_utils.IpAddr(
			fmt.Sprintf("10.%d.%d.%d", id/(256*256)%256, (id/256)%256, id%256),
		)
	}

	config := &balancerpb.ModuleConfig{
		SourceAddressV4: test_utils.IpAddr("5.5.5.5").AsSlice(),
		SourceAddressV6: test_utils.IpAddr("fe80::5").AsSlice(),
		VirtualServices: []*balancerpb.VirtualService{
			{
				Addr:  vsIp.AsSlice(),
				Port:  uint32(vsPort),
				Proto: balancerpb.TransportProto_TCP,
				AllowedSrcs: []*balancerpb.Subnet{
					{
						Addr: test_utils.IpAddr("10.0.0.0").AsSlice(),
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
						SrcAddr: real1Ip.AsSlice(),
						SrcMask: real1Ip.AsSlice(),
					},
					{
						Weight:  1,
						DstAddr: real2Ip.AsSlice(),
						SrcAddr: real2Ip.AsSlice(),
						SrcMask: real2Ip.AsSlice(),
					},
					{
						Weight:  2,
						DstAddr: real3Ip.AsSlice(),
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

	setup, err := test_utils.SetupTest(&test_utils.TestConfig{
		ModuleConfig: config,
		StateConfig: &balancerpb.ModuleStateConfig{
			SessionTableCapacity:      8000,
			SessionTableMaxLoadFactor: 0.5,
			SessionTableScanPeriod:    durationpb.New(0),
		},
	})
	require.NoError(t, err, "failed to setup test")
	defer setup.Free()

	mock := setup.Mock
	bal := setup.Balancer

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
			packetLayers := test_utils.MakeTCPPacket(
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

		configStats := bal.GetConfigStats(
			test_utils.DefaultDeviceName,
			test_utils.DefaultPipelineName,
			test_utils.DefaultFunctionName,
			test_utils.DefaultChainName,
		)

		assert.Equal(
			t,
			uint64(packets/2),
			configStats.RealInfo[0].Stats.CreatedSessions,
		)
		assert.Equal(
			t,
			uint64(packets/2),
			configStats.RealInfo[1].Stats.CreatedSessions,
		)
		assert.Equal(t, uint64(0), configStats.RealInfo[2].Stats.CreatedSessions)

		// Scan active sessions, update them,
		// and recalculate effective weights
		err = bal.SyncActiveSessionsAndWlcAndResizeTableOnDemand(now)
		require.NoError(t, err)

		stateInfo := bal.GetStateInfo(now)
		reals := stateInfo.RealInfo

		assert.Equal(t, uint(packets/2), reals[0].ActiveSessions.Value)
		assert.Equal(t, uint(packets/2), reals[1].ActiveSessions.Value)
		assert.Equal(t, uint(0), reals[2].ActiveSessions.Value)
	})

	// enable third real

	t.Run("Enable_Third_Real", func(t *testing.T) {
		config, stateConfig := bal.GetConfig()
		config.VirtualServices[0].Reals[2].Enabled = true
		err := bal.Update(config, stateConfig)
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
				if err := test_utils.SyncActiveSessionsAndWlcAndResizeTableOnDemand(now); err != nil {
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
			packetLayers := test_utils.MakeTCPPacket(
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

		configStats := bal.GetConfigStats(
			test_utils.DefaultDeviceName,
			test_utils.DefaultPipelineName,
			test_utils.DefaultFunctionName,
			test_utils.DefaultChainName,
		)

		firstTwoPackets := configStats.Reals[0].Stats.CreatedSessions + configStats.Reals[1].Stats.CreatedSessions
		thirdPackets := configStats.Reals[2].Stats.CreatedSessions

		rel := float64(thirdPackets) / float64(firstTwoPackets)
		assert.Less(t, math.Abs(rel-1.0), 0.3)
	})
}
