package balancer

import (
	"net/netip"
	"testing"

	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	mbalancer "github.com/yanet-platform/yanet2/modules/balancer/controlplane"
)

////////////////////////////////////////////////////////////////////////////////

func TestBasic(t *testing.T) {
	vsIp := IpAddr("1.1.1.1")
	vsPort := uint16(80)
	realAddr := IpAddr("2.2.2.2")
	clientIp := IpAddr("3.3.3.3")

	// make balancer config

	config := mbalancer.ModuleInstanceConfig{
		Services: []mbalancer.VirtualService{
			{
				Address:    vsIp,
				Port:       vsPort,
				Proto:      mbalancer.Tcp,
				AllowedSrc: []netip.Prefix{IpPrefix("3.3.3.0/24")},
				Scheduler:  mbalancer.VsSchedulerPRR,
				Reals: []mbalancer.Real{
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
	ValidatePacket(t, balancer.GetConfig(), packet, result.Output[0])

	// checkout info and counters
}
