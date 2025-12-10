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
	balancermod "github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancer"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"google.golang.org/protobuf/types/known/durationpb"
)

////////////////////////////////////////////////////////////////////////////////

// test real selection respects weight and disabled reals
// test select after update works
// test select respects sessions
// test OPS
// test pure L3

////////////////////////////////////////////////////////////////////////////////

func smallConfig() (*balancerpb.ModuleConfig, *balancerpb.SessionsTimeouts) {
	config := &balancerpb.ModuleConfig{
		SourceAddressV4: IpAddr("5.5.5.5").AsSlice(),
		SourceAddressV6: IpAddr("fe80::5").AsSlice(),
		VirtualServices: []*balancerpb.VirtualService{
			{
				Addr: IpAddr("192.166.13.22").AsSlice(),
				Port: 1000,
				Flags: &balancerpb.VsFlags{
					Gre:    false,
					Ops:    false,
					PureL3: false,
					FixMss: false,
				},
				Scheduler: balancerpb.VsScheduler_PRR,
				Proto:     balancerpb.TransportProto_TCP,
				AllowedSrcs: []*balancerpb.Subnet{
					{
						Addr: IpAddr("10.12.0.0").AsSlice(),
						Size: 8,
					},
				},
				Reals: []*balancerpb.Real{
					{
						Weight:  1,
						DstAddr: IpAddr("1.1.1.1").AsSlice(),
						SrcAddr: IpAddr("3.3.3.3").AsSlice(),
						SrcMask: IpAddr("255.240.255.0").AsSlice(),
						Enabled: true,
					},
					{
						Weight:  2,
						DstAddr: IpAddr("2.2.2.2").AsSlice(),
						SrcAddr: IpAddr("3.3.3.3").AsSlice(),
						SrcMask: IpAddr("255.240.255.0").AsSlice(),
						Enabled: false,
					},
					{
						Weight:  2,
						DstAddr: IpAddr("3.3.3.3").AsSlice(),
						SrcAddr: IpAddr("3.3.3.3").AsSlice(),
						SrcMask: IpAddr("255.240.255.0").AsSlice(),
						Enabled: false,
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
			UpdatePeriod:  durationpb.New(0),
		},
	}
	timeouts := &balancerpb.SessionsTimeouts{
		TcpSynAck: 60,
		TcpSyn:    60,
		TcpFin:    60,
		Tcp:       60,
		Udp:       60,
		Default:   60,
	}
	return config, timeouts
}

func smallSetup(t *testing.T) *TestSetup {
	balancer, _ := smallConfig()

	setup, err := SetupTest(&TestConfig{
		moduleConfig: balancer,
		stateConfig: &balancerpb.ModuleStateConfig{
			SessionTableCapacity:      100,
			SessionTableScanPeriod:    durationpb.New(0),
			SessionTableMaxLoadFactor: 0.5,
		},
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
	balancerInstance *balancermod.Balancer,
	vsIdx int,
	packetIdxOffset int,
	packetCount int,
) {
	vs := &balancerInstance.GetModuleConfig().VirtualServices[vsIdx]
	packets := make([]gopacket.Packet, 0, packetCount)
	for packetIdx := range packetCount {
		layers := MakeTCPPacket(
			allowedSrc(uint8(packetIdx+packetIdxOffset)),
			42175,
			vs.Identifier.Ip,
			vs.Identifier.Port,
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
			balancerInstance.GetModuleConfig(),
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

		info := balancer.GetStateInfo()
		assert.NotNil(t, info)

		// check vs
		assert.Equal(t, 1, len(info.VsInfo))
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
				int(info.Stats.Packets),
			)
		}

		// check disabled reals
		for _, disabledReal := range []uint64{1, 2} {
			assert.Equal(
				t,
				0,
				int(info.RealInfo[disabledReal].Stats.Packets),
			)
		}

		// validate state info
		ValidateStateInfo(t, info, balancer.GetModuleConfig().VirtualServices)
	})

	// enabled disabled reals

	t.Run("Enable_Disabled_Reals", func(t *testing.T) {
		// update CP config by enabling reals
		config, _ := balancer.GetConfig()
		config.VirtualServices[0].Reals[1].Enabled = true
		config.VirtualServices[0].Reals[2].Enabled = true

		err := balancer.Update(config, nil)
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

		info := balancer.GetStateInfo()
		assert.NotNil(t, info)

		// check vs
		assert.Equal(t, 1, len(info.VsInfo))
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
				int(info.Stats.Packets),
			)
			packetsSum += int(info.Stats.Packets)
		}

		// check other two reals
		for _, disabledReal := range []uint64{1, 2} {
			info := &info.RealInfo[disabledReal]
			assert.Less(t, 0, int(info.Stats.Packets))
			packetsSum += int(info.Stats.Packets)
		}

		assert.Equal(
			t,
			packetsSum,
			packetCountBeforeRealUpdate+packetCountAfterRealUpdate,
		)

		// validate state info
		ValidateStateInfo(t, info, balancer.GetModuleConfig().VirtualServices)
	})

	// disabled first and second reals

	t.Run("Disable_First_and_Second_Reals", func(t *testing.T) {
		// update CP config by disabling reals
		config, _ := balancer.GetConfig()
		config.VirtualServices[0].Reals[0].Enabled = false
		config.VirtualServices[0].Reals[1].Enabled = false

		err := balancer.Update(config, nil)
		require.Nil(t, err, "failed to update reals")
	})

	// send packets

	packetCountAfterSecondUpdate := 20

	t.Run("Send_Some_Packets_After_Second_Update", func(t *testing.T) {
		// set prev state info
		infoBefore := balancer.GetStateInfo()
		require.NotNil(t, infoBefore)

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

		info := balancer.GetStateInfo()
		require.NotNil(t, info)

		// check vs
		assert.Equal(t, 1, len(info.VsInfo))
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
				realInfo.Stats.Packets,
				realInfoBefore.Stats.Packets,
				"disabled real should not receive new packets",
			)
		}

		// check enabled real - it should have received all new packets

		enabled := 2
		realInfo := &info.RealInfo[enabled]
		realInfoBefore := &infoBefore.RealInfo[enabled]

		// The third real should have received all the new packets
		assert.Equal(
			t,
			realInfo.Stats.Packets,
			realInfoBefore.Stats.Packets+uint64(packetCountAfterSecondUpdate),
			"enabled real should receive all new packets",
		)

		// validate state info
		ValidateStateInfo(t, info, balancer.GetModuleConfig().VirtualServices)
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
	vsProto := balancerpb.TransportProto_TCP
	vsAllowedSrc := []*balancerpb.Subnet{
		{
			Addr: IpAddr("10.0.1.0").AsSlice(),
			Size: 24,
		},
	}

	// make new balancer config

	config := &balancerpb.ModuleConfig{
		SourceAddressV4: IpAddr("5.5.5.5").AsSlice(),
		SourceAddressV6: IpAddr("fe80::5").AsSlice(),
		VirtualServices: []*balancerpb.VirtualService{
			{
				Addr:        vsIp.AsSlice(),
				Port:        uint32(vsPort),
				Proto:       vsProto,
				AllowedSrcs: vsAllowedSrc,
				Scheduler:   balancerpb.VsScheduler_PRR,
				Flags: &balancerpb.VsFlags{
					Gre:    false,
					Ops:    false,
					PureL3: false,
					FixMss: false,
				},
				Reals: []*balancerpb.Real{
					{
						DstAddr: IpAddr("10.1.1.1").AsSlice(),
						Weight:  1,
						Enabled: true,
						SrcAddr: IpAddr("1.1.1.1").AsSlice(),
						SrcMask: IpAddr("1.1.1.1").AsSlice(),
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
			UpdatePeriod:  durationpb.New(0),
		},
	}

	// update config
	err := balancer.Update(config, nil)
	require.NoError(t, err)

	// send packet, it is scheduled on the first real

	clientIp := IpAddr("10.0.1.22")
	clientPort := uint16(1000)

	t.Run("Send_First_Packet", func(t *testing.T) {
		packetLayers := MakeTCPPacket(
			clientIp,
			clientPort,
			vsIp,
			vsPort,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)

		result, err := mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output))
		ValidatePacket(t, balancer.GetModuleConfig(), packet, result.Output[0])
	})

	// update config

	config = &balancerpb.ModuleConfig{
		SourceAddressV4: IpAddr("5.5.5.5").AsSlice(),
		SourceAddressV6: IpAddr("fe80::5").AsSlice(),
		VirtualServices: []*balancerpb.VirtualService{
			{
				Addr:        vsIp.AsSlice(),
				Port:        uint32(vsPort),
				Proto:       vsProto,
				AllowedSrcs: vsAllowedSrc,
				Scheduler:   balancerpb.VsScheduler_PRR,
				Flags: &balancerpb.VsFlags{
					Gre:    false,
					Ops:    false,
					PureL3: false,
					FixMss: false,
				},
				Reals: []*balancerpb.Real{
					{
						DstAddr: IpAddr("10.12.2.2").AsSlice(),
						Weight:  1,
						Enabled: true,
						SrcAddr: IpAddr("133.12.13.11").AsSlice(),
						SrcMask: IpAddr("255.0.240.192").AsSlice(),
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
			UpdatePeriod:  durationpb.New(0),
		},
	}

	err = balancer.Update(config, nil)
	require.NoError(t, err)

	// send packet to the same virtual service (not SYN),
	// ensure it is dropped because its real was removed

	t.Run("Send_Second_Packet_Without_Reschedule", func(t *testing.T) {
		packetLayers := MakeTCPPacket(
			clientIp,
			clientPort,
			vsIp,
			vsPort,
			&layers.TCP{},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)

		// Check packet is dropped because its real was removed

		result, err := mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Drop))
		require.Empty(t, len(result.Output))
	})

	// send packet to real with reschedule

	t.Run("Send_Second_Packet_With_Reschedule", func(t *testing.T) {
		packetLayers := MakeTCPPacket(
			clientIp,
			clientPort,
			vsIp,
			vsPort,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)

		// Check packet is dropped because its real was removed

		result, err := mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output))
		require.Empty(t, result.Drop)

		ValidatePacket(t, balancer.GetModuleConfig(), packet, result.Output[0])
	})

	// remove virtual service from config

	config = &balancerpb.ModuleConfig{
		SourceAddressV4: IpAddr("5.5.5.5").AsSlice(),
		SourceAddressV6: IpAddr("fe80::5").AsSlice(),
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
			UpdatePeriod:  durationpb.New(0),
		},
	}

	err = balancer.Update(config, nil)
	require.NoError(t, err)

	//send packet when no virtual services are enabled

	t.Run("Send_Second_Packet_With_Reschedule_no_Vs", func(t *testing.T) {
		packetLayers := MakeTCPPacket(
			clientIp,
			clientPort,
			vsIp,
			vsPort,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)

		// Check packet is dropped because its real was removed
		result, err := mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Drop))
		require.Empty(t, len(result.Output))
	})
}
