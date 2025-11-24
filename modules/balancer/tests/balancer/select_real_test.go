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

// test real selection respects weight and disabled reals
// test select after update works
// test select respects sessions
// test OPS
// test pure L3

////////////////////////////////////////////////////////////////////////////////

func smallConfig() (*balancer.ModuleInstanceConfig, *balancer.SessionsTimeouts) {
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
						Enabled: false,
					},
					{
						Weight:  2,
						DstAddr: IpAddr("3.3.3.3"),
						SrcAddr: IpAddr("3.3.3.3"),
						SrcMask: IpAddr("255.240.255.0"),
						Enabled: false,
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

func allowedSrc(idx uint8) netip.Addr {
	return IpAddr(fmt.Sprintf("10.12.0.%d", idx))
}

////////////////////////////////////////////////////////////////////////////////

func sendRandomSYNs(
	t *testing.T,
	balancerInstance *balancer.ModuleInstance,
	vsIdx int,
	packetIdxOffset int,
	packetCount int,
) {
	vs := &balancerInstance.GetConfig().Services[vsIdx]
	packets := make([]gopacket.Packet, 0, packetCount)
	for packetIdx := range packetCount {
		layers := MakeTCPPacket(
			allowedSrc(uint8(packetIdx+packetIdxOffset)),
			42175,
			vs.Address,
			vs.Port,
			&layers.TCP{SYN: true},
		)
		packet := common.LayersToPacket(t, layers...)
		packets = append(packets, packet)
	}

	result, err := HandlePackets(balancerInstance, packets...)
	assert.Nil(t, err)
	assert.Equal(t, packetCount, len(result.Output))
	assert.Empty(t, result.Drop)
	assert.Empty(t, result.Input)

	for packetIdx := range packetCount {
		resultPacket := result.Output[packetIdx]
		originalPacket := packets[packetIdx]
		ValidatePacket(t, balancerInstance.GetConfig(), originalPacket, resultPacket)
	}
}

////////////////////////////////////////////////////////////////////////////////

func TestSelectAfterUpdate(t *testing.T) {
	config, timeouts := smallConfig()

	PrepareForUpdate(t)

	balancerInstance, err := balancer.NewModuleInstance(agent, "balancer0", config, 2000, timeouts)
	require.NoError(t, err, "failed to make balancer")
	defer balancerInstance.Free()

	packetCountBeforeRealUpdate := 10

	// send some syn packets to the first virtual service from different sources
	t.Run("Send_Some_Packets_Before_Update", func(t *testing.T) {
		// send random SYNs from unique sources
		sendRandomSYNs(t, balancerInstance, 0, 0, packetCountBeforeRealUpdate)

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

		// check first real
		{
			info := &info.RealInfo[0]
			assert.Equal(t, packetCountBeforeRealUpdate, int(info.ActiveSessions))
			assert.Equal(t, packetCountBeforeRealUpdate, int(info.Stats.SendPackets))
			assert.Equal(t, int(info.ActiveSessions), int(info.Stats.SendPackets))
		}

		// check disabled reals
		for _, disabledReal := range []uint64{1, 2} {
			assert.Equal(t, 0, int(info.RealInfo[disabledReal].ActiveSessions))
			assert.Equal(t, 0, int(info.RealInfo[disabledReal].Stats.SendPackets))
		}

		// validate state info
		ValidateStateInfo(t, info, balancerInstance.GetConfig())
	})

	// enabled disabled reals

	t.Run("Enable_Disabled_Reals", func(t *testing.T) {
		// update CP config gen
		PrepareForUpdate(t)

		vs := &config.Services[0]
		updates := make([]*balancer.RealUpdate, 0, 2)
		for _, realIdx := range []uint64{1, 2} {
			real := &vs.Reals[realIdx]
			updates = append(updates, &balancer.RealUpdate{
				VirtualIp: vs.Address,
				Proto:     vs.Proto,
				Port:      vs.Port,
				RealIp:    real.DstAddr,
				Enable:    true,
			})
		}

		err := balancerInstance.UpdateReals(updates, false)
		require.Nil(t, err, "failed to update reals")
	})

	// send packets again and check enabled reals accept them

	packetCountAfterRealUpdate := 10

	t.Run("Send_Some_Packets_After_Update", func(t *testing.T) {
		// send random SYNs from unique sources
		sendRandomSYNs(
			t,
			balancerInstance,
			0,
			packetCountBeforeRealUpdate,
			packetCountAfterRealUpdate,
		)

		// check balancer state info

		info, err := balancerInstance.StateInfo()
		assert.NotNil(t, info)
		assert.Nil(t, err)

		// check vs
		assert.Equal(t, 1, len(info.VsInfo))
		assert.Equal(
			t,
			packetCountBeforeRealUpdate+packetCountAfterRealUpdate,
			int(info.VsInfo[0].ActiveSessions),
		)
		assert.Equal(
			t,
			packetCountBeforeRealUpdate+packetCountAfterRealUpdate,
			int(info.VsInfo[0].Stats.IncomingPackets),
		)

		// check reals
		assert.Equal(t, 3, len(info.RealInfo))

		// check first real
		packetsSum := 0
		{
			info := &info.RealInfo[0]
			assert.Less(t, packetCountBeforeRealUpdate, int(info.ActiveSessions))
			assert.Less(t, packetCountBeforeRealUpdate, int(info.Stats.SendPackets))
			assert.Equal(t, int(info.ActiveSessions), int(info.Stats.SendPackets))
			packetsSum += int(info.Stats.SendPackets)
		}

		// check other two reals
		for _, disabledReal := range []uint64{1, 2} {
			info := &info.RealInfo[disabledReal]
			assert.Less(t, 0, int(info.ActiveSessions))
			assert.Less(t, 0, int(info.Stats.SendPackets))
			assert.Equal(t, int(info.ActiveSessions), int(info.Stats.SendPackets))
			packetsSum += int(info.Stats.SendPackets)
		}

		assert.Equal(t, packetsSum, packetCountBeforeRealUpdate+packetCountAfterRealUpdate)

		// validate state info
		ValidateStateInfo(t, info, balancerInstance.GetConfig())
	})

	// disabled first and second reals

	t.Run("Disable_First_and_Second_Reals", func(t *testing.T) {
		// update CP config gen
		PrepareForUpdate(t)

		vs := &config.Services[0]
		updates := make([]*balancer.RealUpdate, 0, 2)
		for _, realIdx := range []uint64{0, 1} {
			real := &vs.Reals[realIdx]
			updates = append(updates, &balancer.RealUpdate{
				VirtualIp: vs.Address,
				Proto:     vs.Proto,
				Port:      vs.Port,
				RealIp:    real.DstAddr,
				Enable:    false,
			})
		}

		err := balancerInstance.UpdateReals(updates, false)
		require.Nil(t, err, "failed to update reals")
	})

	// send packets

	packetCountAfterSecondUpdate := 20

	t.Run("Send_Some_Packets_After_Second_Update", func(t *testing.T) {
		// set prev state info
		infoBefore, err := balancerInstance.StateInfo()
		require.NotNil(t, infoBefore)
		require.Nil(t, err)

		// send random SYNs from unique sources
		sendRandomSYNs(
			t,
			balancerInstance,
			0,
			packetCountBeforeRealUpdate+packetCountAfterRealUpdate,
			packetCountAfterSecondUpdate,
		)

		// check balancer state info

		info, err := balancerInstance.StateInfo()
		require.NotNil(t, info)
		require.Nil(t, err)

		// check vs
		assert.Equal(t, 1, len(info.VsInfo))
		assert.Equal(
			t,
			packetCountBeforeRealUpdate+packetCountAfterRealUpdate+packetCountAfterSecondUpdate,
			int(info.VsInfo[0].ActiveSessions),
		)
		assert.Equal(
			t,
			packetCountBeforeRealUpdate+packetCountAfterRealUpdate+packetCountAfterSecondUpdate,
			int(info.VsInfo[0].Stats.IncomingPackets),
		)

		// check reals
		assert.Equal(t, 3, len(info.RealInfo))

		for disabled := range []uint64{0, 1} {
			realInfo := &info.RealInfo[disabled]
			realInfoBefore := &infoBefore.RealInfo[disabled]
			assert.Equal(t, realInfo.ActiveSessions, realInfoBefore.ActiveSessions)
			assert.Equal(t, realInfo.Stats.SendPackets, realInfoBefore.Stats.SendPackets)
		}

		// check enabled real

		enabled := 2
		realInfo := &info.RealInfo[enabled]
		realInfoBefore := &infoBefore.RealInfo[enabled]
		assert.Greater(t, realInfo.ActiveSessions, realInfoBefore.ActiveSessions)
		assert.Greater(t, realInfo.Stats.SendPackets, realInfoBefore.Stats.SendPackets)

		// validate state info
		ValidateStateInfo(t, info, balancerInstance.GetConfig())
	})
}
