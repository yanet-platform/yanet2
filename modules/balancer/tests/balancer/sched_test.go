package balancer

import (
	"fmt"
	"math"
	"net/netip"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	mbalancer "github.com/yanet-platform/yanet2/modules/balancer/controlplane"
)

// PRR, WLC, WRR
// one packet scheduler
// sessions are created
// pure l3
// test timeouts (how?)
// session table

////////////////////////////////////////////////////////////////////////////////

func TestWlc(t *testing.T) {
	vsIp := IpAddr("1.1.1.1")
	vsPort := uint16(80)
	real1Ip := IpAddr("2.2.2.2")
	real2Ip := IpAddr("3.3.3.3")
	real3Ip := IpAddr("4.4.4.4")

	client := func(id int) netip.Addr {
		return IpAddr(fmt.Sprintf("10.%d.%d.%d", id/(256*256)%256, (id/256)%256, id%256))
	}

	config := mbalancer.ModuleInstanceConfig{
		Services: []mbalancer.VirtualServiceConfig{
			{
				Info: mbalancer.VirtualServiceInfo{
					Address: vsIp,
					Port:    vsPort,
					Proto:   mbalancer.Tcp,
					AllowedSrc: []netip.Prefix{
						IpPrefix("10.0.0.0/8"),
					},
					Scheduler: mbalancer.VsSchedulerWLC,
				},
				Reals: []mbalancer.RealConfig{
					{
						Weight:  1,
						DstAddr: real1Ip,
						Enabled: true,
						SrcAddr: real1Ip,
						SrcMask: real1Ip,
					},
					{
						Weight:  1,
						DstAddr: real2Ip,
						Enabled: true,
						SrcAddr: real2Ip,
						SrcMask: real2Ip,
					},
					{
						Weight:  2,
						DstAddr: real3Ip,
						Enabled: false,
						SrcAddr: real3Ip,
						SrcMask: real3Ip,
					},
				},
			},
		},
	}

	timeouts := mbalancer.SessionsTimeouts{
		TcpSynAck: 60,
		TcpSyn:    60,
		TcpFin:    60,
		Tcp:       60,
		Udp:       60,
	}

	setup, err := SetupTest(&TestConfig{
		balancer:         &config,
		timeouts:         &timeouts,
		sessionTableSize: 10000,
	})
	defer setup.Free()

	require.NoError(t, err, "failed to setup test")

	mock := setup.mock
	balancer := setup.balancer

	// send random packets

	packets := 500

	// send random SYNs to the first two reals
	// expect uniform distribution

	t.Run("Send_Random_SYNs", func(t *testing.T) {
		packetsToSend := make([]gopacket.Packet, 0, packets)
		for packetIdx := range packets {
			client := client(packetIdx)
			packetLayers := MakeTCPPacket(client, 1000, vsIp, vsPort, &layers.TCP{SYN: true})
			packet := xpacket.LayersToPacket(t, packetLayers...)
			packetsToSend = append(packetsToSend, packet)
		}
		result, err := mock.HandlePackets(packetsToSend...)
		require.NoError(t, err)

		require.Equal(t, packets, len(result.Output))
		require.Empty(t, result.Drop)

		configInfo, err := balancer.ConfigInfo(defaultDeviceName, defaultPipelineName, defaultFunctionName, defaultChainName)
		require.NoError(t, err)

		assert.Equal(t, uint64(packets/2), configInfo.Vs[0].Reals[0].Stats.CreatedSessions)
		assert.Equal(t, uint64(packets/2), configInfo.Vs[0].Reals[1].Stats.CreatedSessions)
		assert.Equal(t, uint64(0), configInfo.Vs[0].Reals[2].Stats.CreatedSessions)

		stateInfo, err := balancer.StateInfo()
		require.NoError(t, err)
		reals := stateInfo.RealInfo
		assert.Equal(t, uint64(packets/2), reals[0].ActiveSessions)
		assert.Equal(t, uint64(packets/2), reals[1].ActiveSessions)
		assert.Equal(t, uint64(0), reals[2].ActiveSessions)
	})

	// enable third real

	t.Run("Enable_Third_Real", func(t *testing.T) {
		err := balancer.UpdateReals([]*mbalancer.RealUpdate{
			{
				VirtualIp: vsIp,
				Proto:     mbalancer.Tcp,
				Port:      vsPort,
				RealIp:    real3Ip,
				Enable:    true,
			},
		}, false)
		require.NoError(t, err)
	})

	// send more random packets
	// ensure, we have good distribution after

	t.Run("Send_Random_SYNs_Again", func(t *testing.T) {
		packetsToSend := make([]gopacket.Packet, 0)
		firstClient := packets
		packets = 5 * packets
		for packetIdx := range packets {
			if packetIdx%10 == 0 {
				if err := balancer.UpdateWlc(); err != nil {
					t.Errorf("failed to update wlc: packetIdx=%d", packetIdx)
				}
				result, err := mock.HandlePackets(packetsToSend...)
				assert.NoError(t, err)
				assert.Equal(t, len(packetsToSend), len(result.Output))
				assert.Empty(t, result.Drop)
				packetsToSend = make([]gopacket.Packet, 0)
			}
			client := client(firstClient + packetIdx)
			packetLayers := MakeTCPPacket(client, 1000, vsIp, vsPort, &layers.TCP{SYN: true})
			packet := xpacket.LayersToPacket(t, packetLayers...)
			packetsToSend = append(packetsToSend, packet)
		}
		result, err := mock.HandlePackets(packetsToSend...)
		require.NoError(t, err)

		require.Equal(t, len(packetsToSend), len(result.Output))
		require.Empty(t, result.Drop)

		info, err := balancer.ConfigInfo(defaultDeviceName, defaultPipelineName, defaultFunctionName, defaultChainName)
		require.NoError(t, err)

		vsInfo := &info.Vs[0]

		firstTwoPackets := vsInfo.Reals[0].Stats.CreatedSessions + vsInfo.Reals[1].Stats.CreatedSessions
		thirdPackets := vsInfo.Reals[2].Stats.CreatedSessions

		rel := float64(thirdPackets) / float64(firstTwoPackets)
		assert.Less(t, math.Abs(rel-1.0), 0.2)
	})
}
