package balancer

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	mock "github.com/yanet-platform/yanet2/mock/go"
	mbalancer "github.com/yanet-platform/yanet2/modules/balancer/controlplane"
)

////////////////////////////////////////////////////////////////////////////////

// test real selection respects weight and disabled reals
// test select after update works
// test select respects sessions
// test OPS
// test pure L3

////////////////////////////////////////////////////////////////////////////////

func smallConfig() (*mbalancer.ModuleInstanceConfig, *mbalancer.SessionsTimeouts) {
	config := mbalancer.ModuleInstanceConfig{
		Services: []mbalancer.VirtualServiceConfig{
			{
				Info: mbalancer.VirtualServiceInfo{
					Address: IpAddr("192.166.13.22"),
					Port:    1000,
					Flags: mbalancer.VsFlags{
						GRE:    false,
						OPS:    false,
						PureL3: false,
						FixMSS: false,
					},
					Scheduler: mbalancer.VsSchedulerPRR,
					Proto:     mbalancer.Tcp,
					AllowedSrc: []netip.Prefix{
						IpPrefix("10.12.0.0/8"),
					},
				},
				Reals: []mbalancer.RealConfig{
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
	timeouts := mbalancer.SessionsTimeouts{
		TcpSynAck: 60,
		TcpSyn:    60,
		TcpFin:    60,
		Tcp:       60,
		Udp:       60,
		Default:   60,
	}
	return &config, &timeouts
}

func smallSetup(t *testing.T) *TestSetup {
	balancer, timeouts := smallConfig()

	setup, err := SetupTest(&TestConfig{
		balancer:         balancer,
		timeouts:         timeouts,
		sessionTableSize: 1024,
	})
	require.NoError(t, err)

	return setup
}

////////////////////////////////////////////////////////////////////////////////

func allowedSrc(idx uint8) netip.Addr {
	return IpAddr(fmt.Sprintf("10.12.0.%d", idx))
}

////////////////////////////////////////////////////////////////////////////////

func sendRandomSYNs(
	t *testing.T,
	mock *mock.YanetMock,
	balancerInstance *mbalancer.ModuleInstance,
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
			vs.Info.Address,
			vs.Info.Port,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, layers...)
		packets = append(packets, packet)
	}

	result, err := mock.HandlePackets(packets...)
	assert.Nil(t, err)
	assert.Equal(t, packetCount, len(result.Output))
	assert.Empty(t, result.Drop)

	for packetIdx := range packetCount {
		resultPacket := result.Output[packetIdx]
		originalPacket := packets[packetIdx]
		ValidatePacket(
			t,
			balancerInstance.GetConfig(),
			originalPacket,
			resultPacket,
		)
	}
}

////////////////////////////////////////////////////////////////////////////////

func TestSelectAfterUpdate(t *testing.T) {
	setup := smallSetup(t)
	defer setup.Free()

	packetCountBeforeRealUpdate := 10

	mock := setup.mock
	balancer := setup.balancer

	// send some syn packets to the first virtual service from different sources
	t.Run("Send_Some_Packets_Before_Update", func(t *testing.T) {
		// send random SYNs from unique sources
		sendRandomSYNs(t, mock, balancer, 0, 0, packetCountBeforeRealUpdate)

		// check balancer state info

		info, err := balancer.StateInfo()
		assert.NotNil(t, info)
		assert.Nil(t, err)

		// check vs
		assert.Equal(t, 1, len(info.VsInfo))
		assert.Equal(
			t,
			packetCountBeforeRealUpdate,
			int(info.VsInfo[0].ActiveSessions),
		)
		assert.Equal(
			t,
			packetCountBeforeRealUpdate,
			int(info.VsInfo[0].Stats.IncomingPackets),
		)

		// check reals
		assert.Equal(t, 3, len(info.RealInfo))

		// check first real
		{
			info := &info.RealInfo[0]
			assert.Equal(
				t,
				packetCountBeforeRealUpdate,
				int(info.ActiveSessions),
			)
			assert.Equal(
				t,
				packetCountBeforeRealUpdate,
				int(info.Stats.SendPackets),
			)
			assert.Equal(
				t,
				int(info.ActiveSessions),
				int(info.Stats.SendPackets),
			)
		}

		// check disabled reals
		for _, disabledReal := range []uint64{1, 2} {
			assert.Equal(t, 0, int(info.RealInfo[disabledReal].ActiveSessions))
			assert.Equal(
				t,
				0,
				int(info.RealInfo[disabledReal].Stats.SendPackets),
			)
		}

		// validate state info
		ValidateStateInfo(t, info, balancer.VirtualServices())
	})

	// enabled disabled reals

	t.Run("Enable_Disabled_Reals", func(t *testing.T) {
		// update CP config gen
		vs := &balancer.GetConfig().Services[0]
		updates := make([]*mbalancer.RealUpdate, 0, 2)
		for _, realIdx := range []uint64{1, 2} {
			real := &vs.Reals[realIdx]
			updates = append(updates, &mbalancer.RealUpdate{
				VirtualIp: vs.Info.Address,
				Proto:     vs.Info.Proto,
				Port:      vs.Info.Port,
				RealIp:    real.DstAddr,
				Enable:    true,
			})
		}

		err := balancer.UpdateReals(updates, false)
		require.Nil(t, err, "failed to update reals")
	})

	// send packets again and check enabled reals accept them

	packetCountAfterRealUpdate := 10

	t.Run("Send_Some_Packets_After_Update", func(t *testing.T) {
		// send random SYNs from unique sources
		sendRandomSYNs(
			t,
			mock,
			balancer,
			0,
			packetCountBeforeRealUpdate,
			packetCountAfterRealUpdate,
		)

		// check balancer state info

		info, err := balancer.StateInfo()
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
			assert.Less(
				t,
				packetCountBeforeRealUpdate,
				int(info.ActiveSessions),
			)
			assert.Less(
				t,
				packetCountBeforeRealUpdate,
				int(info.Stats.SendPackets),
			)
			assert.Equal(
				t,
				int(info.ActiveSessions),
				int(info.Stats.SendPackets),
			)
			packetsSum += int(info.Stats.SendPackets)
		}

		// check other two reals
		for _, disabledReal := range []uint64{1, 2} {
			info := &info.RealInfo[disabledReal]
			assert.Less(t, 0, int(info.ActiveSessions))
			assert.Less(t, 0, int(info.Stats.SendPackets))
			assert.Equal(
				t,
				int(info.ActiveSessions),
				int(info.Stats.SendPackets),
			)
			packetsSum += int(info.Stats.SendPackets)
		}

		assert.Equal(
			t,
			packetsSum,
			packetCountBeforeRealUpdate+packetCountAfterRealUpdate,
		)

		// validate state info
		ValidateStateInfo(t, info, balancer.VirtualServices())
	})

	// disabled first and second reals

	t.Run("Disable_First_and_Second_Reals", func(t *testing.T) {
		// update CP config gen
		vs := &balancer.GetConfig().Services[0]
		updates := make([]*mbalancer.RealUpdate, 0, 2)
		for _, realIdx := range []uint64{0, 1} {
			real := &vs.Reals[realIdx]
			updates = append(updates, &mbalancer.RealUpdate{
				VirtualIp: vs.Info.Address,
				Proto:     vs.Info.Proto,
				Port:      vs.Info.Port,
				RealIp:    real.DstAddr,
				Enable:    false,
			})
		}

		err := balancer.UpdateReals(updates, false)
		require.Nil(t, err, "failed to update reals")
	})

	// send packets

	packetCountAfterSecondUpdate := 20

	t.Run("Send_Some_Packets_After_Second_Update", func(t *testing.T) {
		// set prev state info
		infoBefore, err := balancer.StateInfo()
		require.NotNil(t, infoBefore)
		require.Nil(t, err)

		// send random SYNs from unique sources
		sendRandomSYNs(
			t,
			mock,
			balancer,
			0,
			packetCountBeforeRealUpdate+packetCountAfterRealUpdate,
			packetCountAfterSecondUpdate,
		)

		// check balancer state info

		info, err := balancer.StateInfo()
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
			assert.Equal(
				t,
				realInfo.ActiveSessions,
				realInfoBefore.ActiveSessions,
			)
			assert.Equal(
				t,
				realInfo.Stats.SendPackets,
				realInfoBefore.Stats.SendPackets,
			)
		}

		// check enabled real

		enabled := 2
		realInfo := &info.RealInfo[enabled]
		realInfoBefore := &infoBefore.RealInfo[enabled]
		assert.Greater(
			t,
			realInfo.ActiveSessions,
			realInfoBefore.ActiveSessions,
		)
		assert.Greater(
			t,
			realInfo.Stats.SendPackets,
			realInfoBefore.Stats.SendPackets,
		)

		// validate state info
		ValidateStateInfo(t, info, balancer.VirtualServices())
	})
}

////////////////////////////////////////////////////////////////////////////////

func TestNewConfig(t *testing.T) {
	// first, balancer with some config is instantiated

	setup := smallSetup(t)
	defer setup.Free()

	mock := setup.mock
	balancer := setup.balancer

	// virtual service config

	vsIp := IpAddr("192.160.11.1")
	vsPort := uint16(1015)
	vsProto := mbalancer.Tcp
	vsAllowedSrc := []netip.Prefix{
		IpPrefix("10.0.1.0/24"),
	}

	// make new balancer config

	config := mbalancer.ModuleInstanceConfig{
		Services: []mbalancer.VirtualServiceConfig{
			{
				Info: mbalancer.VirtualServiceInfo{
					Address:    vsIp,
					Port:       vsPort,
					Proto:      vsProto,
					AllowedSrc: vsAllowedSrc,
					Scheduler:  mbalancer.VsSchedulerPRR,
				},
				Reals: []mbalancer.RealConfig{
					{
						DstAddr: IpAddr("10.1.1.1"),
						Weight:  1,
						Enabled: true,
						SrcAddr: IpAddr("1.1.1.1"),
						SrcMask: IpAddr("1.1.1.1"),
					},
				},
			},
		},
	}

	// update config
	err := balancer.UpdateConfig(&config)
	require.NoError(t, err)

	// send packet, it is scheduled on the first real

	clientIp := IpAddr("10.0.1.22")
	clientPort := uint16(1000)

	t.Run("Send_First_Packet", func(t *testing.T) {
		packetLayers := MakeTCPPacket(clientIp, clientPort, vsIp, vsPort, &layers.TCP{SYN: true})
		packet := xpacket.LayersToPacket(t, packetLayers...)

		result, err := mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output))
		ValidatePacket(t, balancer.GetConfig(), packet, result.Output[0])
	})

	// update config

	config = mbalancer.ModuleInstanceConfig{
		Services: []mbalancer.VirtualServiceConfig{
			{
				Info: mbalancer.VirtualServiceInfo{
					Address:    vsIp,
					Port:       vsPort,
					Proto:      vsProto,
					AllowedSrc: vsAllowedSrc,
					Scheduler:  mbalancer.VsSchedulerPRR,
				},
				Reals: []mbalancer.RealConfig{
					{
						DstAddr: IpAddr("10.12.2.2"),
						Weight:  1,
						Enabled: true,
						SrcAddr: IpAddr("133.12.13.11"),
						SrcMask: IpAddr("255.0.240.192"),
					},
				},
			},
		},
	}

	err = balancer.UpdateConfig(&config)
	require.NoError(t, err)

	// send packet to the same virtual service (not SYN),
	// ensure it is dropped because its real was removed

	t.Run("Send_Second_Packet_Without_Reschedule", func(t *testing.T) {
		packetLayers := MakeTCPPacket(clientIp, clientPort, vsIp, vsPort, &layers.TCP{})
		packet := xpacket.LayersToPacket(t, packetLayers...)

		// Check packet is dropped because its real was removed

		result, err := mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Drop))
		require.Empty(t, len(result.Output))
	})

	// send packet to real with reschedule

	t.Run("Send_Second_Packet_With_Reschedule", func(t *testing.T) {
		packetLayers := MakeTCPPacket(clientIp, clientPort, vsIp, vsPort, &layers.TCP{SYN: true})
		packet := xpacket.LayersToPacket(t, packetLayers...)

		// Check packet is dropped because its real was removed

		result, err := mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output))
		require.Empty(t, result.Drop)

		ValidatePacket(t, balancer.GetConfig(), packet, result.Output[0])
	})

	// remove virtual service from config

	config = mbalancer.ModuleInstanceConfig{}

	err = balancer.UpdateConfig(&config)
	require.NoError(t, err)

	//send packet when no virtual services are enabled

	t.Run("Send_Second_Packet_With_Reschedule_no_Vs", func(t *testing.T) {
		packetLayers := MakeTCPPacket(clientIp, clientPort, vsIp, vsPort, &layers.TCP{SYN: true})
		packet := xpacket.LayersToPacket(t, packetLayers...)

		// Check packet is dropped because its real was removed
		result, err := mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Drop))
		require.Empty(t, len(result.Output))
	})
}
