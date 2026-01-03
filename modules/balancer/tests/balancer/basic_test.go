package balancer_test

import (
	balancer "github.com/yanet-platform/yanet2/modules/balancer/tests/balancer"
	"testing"
	"time"

	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	balancerffi "github.com/yanet-platform/yanet2/modules/balancer/controlplane/agent/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"google.golang.org/protobuf/types/known/durationpb"
)

////////////////////////////////////////////////////////////////////////////////

func TestBalancerBasics(t *testing.T) {
	vsIp := balancer.IpAddr("1.1.1.1")
	vsPort := uint16(80)
	realAddr := balancer.IpAddr("2.2.2.2")
	clientIp := balancer.IpAddr("3.3.3.3")

	// make balancer config using protobuf

	config := &balancerpb.ModuleConfig{
		SourceAddressV4: balancer.IpAddr("5.5.5.5").AsSlice(),
		SourceAddressV6: balancer.IpAddr("fe80::5").AsSlice(),
		VirtualServices: []*balancerpb.VirtualService{
			{
				Addr:  vsIp.AsSlice(),
				Port:  uint32(vsPort),
				Proto: balancerpb.TransportProto_TCP,
				AllowedSrcs: []*balancerpb.Subnet{
					{
						Addr: balancer.IpAddr("3.3.3.0").AsSlice(),
						Size: 24,
					},
				},
				Scheduler: balancerpb.VsScheduler_PRR,
				Flags: &balancerpb.VsFlags{
					Gre:    false,
					FixMss: false,
					Ops:    false,
					PureL3: false,
				},
				Reals: []*balancerpb.Real{
					{
						DstAddr: realAddr.AsSlice(),
						Weight:  1,
						SrcAddr: balancer.IpAddr("4.4.4.4").AsSlice(),
						SrcMask: balancer.IpAddr("4.4.4.4").AsSlice(),
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

	stateConfig := &balancerpb.ModuleStateConfig{
		SessionTableCapacity:      100,
		SessionTableScanPeriod:    durationpb.New(0),
		SessionTableMaxLoadFactor: 0.8,
	}

	// setup test

	setup, err := balancer.SetupTest(&balancer.TestConfig{
		ModuleConfig: config,
		StateConfig:  stateConfig,
	})
	require.NoError(t, err)
	defer setup.Free()

	mock := setup.Mock
	bal := setup.Balancer

	// send packet and expect response

	packetLayers := balancer.MakeTCPPacket(
		clientIp,
		1000,
		vsIp,
		vsPort,
		&layers.TCP{SYN: true},
	)
	packet := xpacket.LayersToPacket(t, packetLayers...)
	result, err := mock.HandlePackets(packet)
	require.NoError(t, err)
	require.Equal(t, 1, len(result.Output))
	require.Empty(t, result.Drop)

	// validate response packet
	response := result.Output[0]
	moduleConfig, _ := bal.GetConfig()
	balancer.ValidatePacket(t, moduleConfig, packet, response)

	// check info and counters

	expectedVsStats := balancerffi.VsStats{
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

	expectedRealStats := balancerffi.RealStats{
		PacketsRealDisabled:   0,
		PacketsRealNotPresent: 0,
		OpsPackets:            0,
		ErrorIcmpPackets:      0,
		CreatedSessions:       1,
		Packets:               1,
		Bytes:                 uint64(len(packet.Data())),
	}

	t.Run("Read_State_Info", func(t *testing.T) {
		state := bal.GetStateInfo(time.Now())

		require.Equal(t, 1, len(state.RealInfo))
		realInfo := &state.RealInfo[0]
		assert.Equal(t, expectedRealStats, realInfo.Stats)

		require.Equal(t, 1, len(state.VsInfo))
		vsInfo := &state.VsInfo[0]
		assert.Equal(t, expectedVsStats, vsInfo.Stats)
	})

	t.Run("Read_Config_Stats", func(t *testing.T) {
		configStats := bal.GetConfigStats(
			balancer.DefaultDeviceName,
			balancer.DefaultPipelineName,
			balancer.DefaultFunctionName,
			balancer.DefaultChainName,
		)
		require.Equal(t, 1, len(configStats.VsInfo))
		vsInfo := &configStats.VsInfo[0]

		require.Equal(t, 1, len(configStats.RealInfo))
		realInfo := &configStats.RealInfo[0]

		assert.Equal(t, expectedVsStats, vsInfo.Stats)
		assert.Equal(t, expectedRealStats, realInfo.Stats)
	})
}
