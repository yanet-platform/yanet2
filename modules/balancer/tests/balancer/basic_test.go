package balancer

import (
	"net/netip"
	"testing"

	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	mbalancer "github.com/yanet-platform/yanet2/modules/balancer/controlplane"
)

////////////////////////////////////////////////////////////////////////////////

func TestBalancerBasics(t *testing.T) {
	vsIp := IpAddr("1.1.1.1")
	vsPort := uint16(80)
	realAddr := IpAddr("2.2.2.2")
	clientIp := IpAddr("3.3.3.3")

	// make balancer config

	config := mbalancer.ModuleInstanceConfig{
		Services: []mbalancer.VirtualServiceConfig{
			{
				Info: mbalancer.VirtualServiceInfo{
					Address:    vsIp,
					Port:       vsPort,
					Proto:      mbalancer.Tcp,
					AllowedSrc: []netip.Prefix{IpPrefix("3.3.3.0/24")},
					Scheduler:  mbalancer.VsSchedulerPRR,
				},
				Reals: []mbalancer.RealConfig{
					{
						DstAddr: realAddr,
						Weight:  1,
						SrcAddr: IpAddr("4.4.4.4"),
						SrcMask: IpAddr("4.4.4.4"),
						Enabled: true,
					},
				},
			},
		},
	}

	// setup timeouts

	timeouts := mbalancer.SessionsTimeouts{
		TcpSynAck: 60,
		TcpSyn:    60,
		TcpFin:    60,
		Tcp:       60,
		Udp:       60,
		Default:   60,
	}

	// setup test

	setup, err := SetupTest(&TestConfig{
		balancer:         &config,
		timeouts:         &timeouts,
		sessionTableSize: 10,
	})
	require.NoError(t, err)

	mock := setup.mock
	balancer := setup.balancer

	// send packet and expect response

	packetLayers := MakeTCPPacket(clientIp, 1000, vsIp, vsPort, &layers.TCP{SYN: true})
	packet := xpacket.LayersToPacket(t, packetLayers...)
	result, err := mock.HandlePackets(packet)
	require.NoError(t, err)
	require.Equal(t, 1, len(result.Output))
	require.Empty(t, result.Drop)

	// validate response packet
	response := result.Output[0]
	ValidatePacket(t, balancer.GetConfig(), packet, response)

	// check info and counters

	expectedVsStats := mbalancer.VsStats{
		IncomingPackets: 1,
		OutgoingPackets: 1,

		PacketSrcNotAllowed:  0,
		NoReals:              0,
		OpsPackets:           0,
		SessionTableOverflow: 0,
		RealIsDisabled:       0,
		CreatedSessions:      1,

		IncomingBytes: uint64(len(packet.Data())),
		OutgoingBytes: uint64(len(packet.Data())),
	}

	expectedRealStats := mbalancer.RealStats{
		RealDisabledPackets: 0,
		OpsPackets:          0,
		CreatedSessions:     1,
		SendPackets:         1,
		SendBytes:           uint64(len(packet.Data())),
	}

	t.Run("Read_State_Info", func(t *testing.T) {
		state, err := balancer.StateInfo()
		require.NoError(t, err)

		require.Equal(t, 1, len(state.RealInfo))
		realInfo := &state.RealInfo[0]
		assert.Equal(t, realInfo.ActiveSessions, uint64(1))
		assert.Equal(t, realInfo.Stats, expectedRealStats)

		require.Equal(t, 1, len(state.VsInfo))
		vsInfo := &state.VsInfo[0]
		assert.Equal(t, vsInfo.ActiveSessions, uint64(1))
		assert.Equal(t, vsInfo.Stats, expectedVsStats)
	})

	t.Run("Read_Config_Info", func(t *testing.T) {
		configInfo, err := balancer.ConfigInfo(
			defaultDeviceName,
			defaultPipelineName,
			defaultFunctionName,
			defaultChainName,
		)
		require.NoError(t, err)
		require.Equal(t, 1, len(configInfo.Vs))
		vsInfo := configInfo.Vs[0]

		require.Equal(t, 1, len(vsInfo.Reals))
		realInfo := &vsInfo.Reals[0]

		assert.Equal(t, vsInfo.Stats, expectedVsStats)
		assert.Equal(t, realInfo.Stats, expectedRealStats)
	})
}
