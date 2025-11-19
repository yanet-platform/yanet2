package balancer

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	balancer "github.com/yanet-platform/yanet2/modules/balancer/controlplane"
	"github.com/yanet-platform/yanet2/tests/go/common"
)

////////////////////////////////////////////////////////////////////////////////

func makeConfig() (*balancer.ModuleInstanceConfig, *balancer.SessionsTimeouts) {
	config := balancer.ModuleInstanceConfig{
		Services: []balancer.VirtualService{
			{
				Address: IpAddr("192.166.13.22"),
				Port:    1000,
				Flags: balancer.VsFlags{
					GRE:    false,
					OPS:    false,
					PureL3: false,
					FixMSS: false,
				},
				Scheduler: balancer.VsSchedulerPRR,
				Proto:     balancer.TransportProtoTcp,
				AllowedSrc: []netip.Prefix{
					IpPrefix("10.12.0.0/8"),
				},
				Reals: []balancer.Real{
					{
						Weight:  1,
						DstAddr: IpAddr("1.1.1.1"),
						SrcAddr: IpAddr("3.3.3.3"),
						SrcMask: IpAddr("255.240.255.0"),
						Enabled: true,
					},
					{
						Weight:  2,
						DstAddr: IpAddr("2.2.2.2"),
						SrcAddr: IpAddr("3.3.3.3"),
						SrcMask: IpAddr("255.240.255.0"),
						Enabled: true,
					},
					{
						Weight:  2,
						DstAddr: IpAddr("3.3.3.3"),
						SrcAddr: IpAddr("3.3.3.3"),
						SrcMask: IpAddr("255.240.255.0"),
						Enabled: true,
					},
				},
			},
		},
	}
	timeouts := balancer.SessionsTimeouts{
		TcpSynAck: 60,
		TcpSyn:    60,
		TcpFin:    60,
		Tcp:       60,
		Udp:       60,
		Default:   60,
	}
	return &config, &timeouts
}

////////////////////////////////////////////////////////////////////////////////

func AllowedSrc(idx uint8) netip.Addr {
	return IpAddr(fmt.Sprintf("10.12.0.%d", idx))
}

////////////////////////////////////////////////////////////////////////////////

func TestUpdateReals(t *testing.T) {
	agent := AttachAgent(t)

	config, timeouts := makeConfig()

	PrepareForUpdate(t)

	balancerInstance, err := balancer.NewModuleInstance(agent, "balancer0", config, 2000, timeouts)
	require.NoError(t, err, "failed to make balancer")

	packetCountBeforeRealUpdate := 10

	// send some syn packets to the first virtual service from different sources
	t.Run("Send_Some_Packets_Before_Update", func(t *testing.T) {
		firstVsIp := config.Services[0].Address
		firstVsPort := config.Services[0].Port
		packets := make([]gopacket.Packet, 0, packetCountBeforeRealUpdate)
		for packetIdx := range packetCountBeforeRealUpdate {
			layers := MakeTCPPacket(AllowedSrc(uint8(packetIdx)), 42175, firstVsIp, firstVsPort, &layers.TCP{SYN: true})
			packet := common.LayersToPacket(t, layers...)
			packets = append(packets, packet)
		}

		result, err := HandlePackets(balancerInstance, packets...)
		assert.Nil(t, err)
		assert.Equal(t, packetCountBeforeRealUpdate, len(result.Output))
		assert.Empty(t, result.Drop)
		assert.Empty(t, result.Input)

		for packetIdx := range packetCountBeforeRealUpdate {
			resultPacket := result.Output[packetIdx]
			originalPacket := packets[packetIdx]
			ValidatePacket(t, balancerInstance.GetConfig(), originalPacket, resultPacket)
		}

		// check balancer state info

		info, err := balancerInstance.StateInfo()
		assert.NotNil(t, info)
		assert.Nil(t, err)

		// check vs
		assert.Equal(t, 1, len(info.VsInfo))
		assert.Equal(t, packetCountBeforeRealUpdate, int(info.VsInfo[0].ActiveSessions))
		assert.Equal(t, packetCountBeforeRealUpdate, int(info.VsInfo[0].Stats.IncomingPackets))

		// check reals
		assert.Equal(t, 3, len(info.RealInfo))
		assert.Equal(t, packetCountBeforeRealUpdate, int(info.RealInfo[0].ActiveSessions))
		assert.Equal(t, packetCountBeforeRealUpdate, int(info.RealInfo[0].Stats.SendPackets))
		for disabledReal := range []uint64{1, 2} {
			assert.Equal(t, 0, int(info.RealInfo[disabledReal].ActiveSessions))
			assert.Equal(t, 0, int(info.RealInfo[disabledReal].Stats.SendPackets))
		}
	})

	t.Run("Enable_Disabled_Reals", func(t *testing.T) {
		PrepareForUpdate(t)

		vs := &config.Services[0]
		updates := make([]*balancer.RealUpdate, 0, 2)
		for realIdx := range []uint64{1, 2} {
			real := &vs.Reals[realIdx]
			updates = append(updates, &balancer.RealUpdate{
				VirtualIp: vs.Address,
				Proto:     vs.Proto,
				Port:      vs.Port,
				RealIp:    real.DstAddr,
				Enable:    true,
				Weight:    2,
			})
		}

		err := balancerInstance.UpdateReals(updates, false)
		require.Nil(t, err, "failed to update reals")
	})

	// t.Logf("state info before real updates: %v", stateInfoBeforeRealsUpdate.JsonPretty())

	// enable first real
}
