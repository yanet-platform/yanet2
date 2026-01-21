package balancer_test

import (
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/tests/go/utils"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestBasicOperations(t *testing.T) {
	// Define test addresses
	vsIp := netip.MustParseAddr("1.1.1.1")
	vsPort := uint16(80)
	realAddr := netip.MustParseAddr("2.2.2.2")
	clientIp := netip.MustParseAddr("3.3.3.3")

	// Create balancer configuration
	config := &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("5.5.5.5").AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("fe80::5").AsSlice(),
			},
			Vs: []*balancerpb.VirtualService{
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: vsIp.AsSlice(),
						},
						Port:  uint32(vsPort),
						Proto: balancerpb.TransportProto_TCP,
					},
					Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
					AllowedSrcs: []*balancerpb.Net{
						{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("3.3.3.0").AsSlice(),
							},
							Size: 24,
						},
					},
					Flags: &balancerpb.VsFlags{
						Gre:    false,
						FixMss: false,
						Ops:    false,
						PureL3: false,
						Wlc:    false,
					},
					Reals: []*balancerpb.Real{
						{
							Id: &balancerpb.RelativeRealIdentifier{
								Ip: &balancerpb.Addr{
									Bytes: realAddr.AsSlice(),
								},
								Port: 0,
							},
							Weight: 1,
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("4.4.4.4").AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.255").
									AsSlice(),
							},
						},
					},
					Peers: []*balancerpb.Addr{},
				},
			},
			DecapAddresses: []*balancerpb.Addr{},
			SessionsTimeouts: &balancerpb.SessionsTimeouts{
				TcpSynAck: 60,
				TcpSyn:    60,
				TcpFin:    60,
				Tcp:       60,
				Udp:       60,
				Default:   60,
			},
		},
		State: &balancerpb.StateConfig{
			SessionTableCapacity:      func() *uint64 { v := uint64(100); return &v }(),
			SessionTableMaxLoadFactor: func() *float32 { v := float32(0.8); return &v }(),
			RefreshPeriod:             durationpb.New(0),
			Wlc: &balancerpb.WlcConfig{
				Power:     func() *uint64 { v := uint64(10); return &v }(),
				MaxWeight: func() *uint32 { v := uint32(1000); return &v }(),
			},
		},
	}

	// Setup test
	ts, err := utils.Make(&utils.TestConfig{
		Mock:     utils.SingleWorkerMockConfig(64*datasize.MB, 4*datasize.MB),
		Balancer: config,
		AgentMemory: func() *datasize.ByteSize {
			memory := 16 * datasize.MB
			return &memory
		}(),
	})
	require.NoError(t, err)
	defer ts.Free()

	mock := ts.Mock
	balancer := ts.Balancer

	// Enable all reals before sending packets
	enableTrue := true
	realUpdates := []*balancerpb.RealUpdate{
		{
			RealId: &balancerpb.RealIdentifier{
				Vs: &balancerpb.VsIdentifier{
					Addr:  &balancerpb.Addr{Bytes: vsIp.AsSlice()},
					Port:  uint32(vsPort),
					Proto: balancerpb.TransportProto_TCP,
				},
				Real: &balancerpb.RelativeRealIdentifier{
					Ip:   &balancerpb.Addr{Bytes: realAddr.AsSlice()},
					Port: 0,
				},
			},
			Enable: &enableTrue,
		},
	}
	_, err = balancer.UpdateReals(realUpdates, false)
	require.NoError(t, err, "failed to enable reals")

	// Create and send TCP SYN packet
	packetLayers := utils.MakeTCPPacket(
		clientIp,
		1000,
		vsIp,
		vsPort,
		&layers.TCP{SYN: true},
	)
	packet := xpacket.LayersToPacket(t, packetLayers...)
	result, err := mock.HandlePackets(packet)
	require.NoError(t, err)
	require.Equal(t, 1, len(result.Output), "expected 1 output packet")
	require.Empty(t, result.Drop, "expected no dropped packets")

	// Validate response packet
	response := result.Output[0]
	utils.ValidatePacket(t, balancer.Config(), packet, response)

	// Check balancer info and stats
	t.Run("Read_Balancer_Info", func(t *testing.T) {
		info, err := balancer.Info(mock.CurrentTime())
		require.NoError(t, err)

		// Basic validation that info is populated
		assert.NotNil(t, info, "balancer info should not be nil")

		// Check that we have session information
		assert.Equal(
			t,
			uint64(1),
			info.ActiveSessions,
			"should have exactly one active session",
		)
	})

	t.Run("Read_Balancer_Stats", func(t *testing.T) {
		// Get stats for the specific packet handler
		ref := &balancerpb.PacketHandlerRef{
			Device:   &utils.DeviceName,
			Pipeline: &utils.PipelineName,
			Function: &utils.FunctionName,
			Chain:    &utils.ChainName,
		}

		stats, err := balancer.Stats(ref)
		require.NoError(t, err)
		require.NotNil(t, stats, "stats should not be nil")

		// Validate that we have VS stats
		require.NotEmpty(t, stats.Vs, "should have VS stats")

		// Check VS stats
		vsStats := stats.Vs[0]
		assert.Equal(
			t,
			uint64(1),
			vsStats.Stats.IncomingPackets,
			"should have 1 incoming packet",
		)
		assert.Equal(
			t,
			uint64(1),
			vsStats.Stats.OutgoingPackets,
			"should have 1 outgoing packet",
		)
		assert.Equal(
			t,
			uint64(1),
			vsStats.Stats.CreatedSessions,
			"should have 1 created session",
		)
		assert.Equal(
			t,
			uint64(len(packet.Data())),
			vsStats.Stats.IncomingBytes,
			"incoming bytes should match packet size",
		)
		assert.Equal(
			t,
			uint64(len(packet.Data())),
			vsStats.Stats.OutgoingBytes,
			"outgoing bytes should match packet size",
		)

		// Check Real stats
		require.NotEmpty(t, vsStats.Reals, "should have Real stats")
		realStats := vsStats.Reals[0]
		assert.Equal(
			t,
			uint64(1),
			realStats.Stats.CreatedSessions,
			"real should have 1 created session",
		)
		assert.Equal(
			t,
			uint64(1),
			realStats.Stats.Packets,
			"real should have 1 packet",
		)
		assert.Equal(
			t,
			uint64(len(packet.Data())),
			realStats.Stats.Bytes,
			"real bytes should match packet size",
		)
	})
}
