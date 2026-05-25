package test

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/filterpb"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	mock "github.com/yanet-platform/yanet2/mock/go"
	balancer2 "github.com/yanet-platform/yanet2/modules/balancer2/controlplane"
	"github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb"
)

type testEnv struct {
	mock      *mock.YanetMock
	packetGen *PacketGenerator
	setup     *TestSetup
}

func setupTestEnv(t *testing.T, balancer *balancer2.ConfigParams) *testEnv {
	setup, err := SetupTest(&TestConfig{
		balancer:         balancer,
		sessionsCapacity: 1024,
	})
	require.NoError(t, err)

	return &testEnv{
		mock:      setup.mock,
		packetGen: NewPacketGenerator(),
		setup:     setup,
	}
}

func TestBasic(t *testing.T) {
	vs1 := &balancerpb.VsIdentifier{
		Addr:  net.ParseIP("2a02:6b8:0:3400:0:853a:0:3"),
		Port:  80,
		Proto: balancerpb.TransportProto_TCP,
	}
	vs2 := &balancerpb.VsIdentifier{
		Addr:  net.ParseIP("2a02:6b8:0:3400:0:853a:0:3"),
		Port:  80,
		Proto: balancerpb.TransportProto_UDP,
	}
	rs := &balancerpb.RelativeRealIdentifier{
		Ip:   net.ParseIP("2a02:6b8:c0e:1003:0:675:a15a:3314"),
		Port: 0,
	}

	config := &balancer2.ConfigParams{
		Timeouts: &balancerpb.SessionsTimeouts{
			TcpSynAck: 60,
			TcpSyn:    30,
			TcpFin:    30,
			Tcp:       10,
			Udp:       20,
		},
		Addr: &balancerpb.AddrConfig{
			SourceIp4: net.ParseIP("1.1.1.1"),
			SourceIp6: net.ParseIP("1::"),
		},
		Wlc: &balancerpb.WlcConfig{
			Power:     10,
			MaxWeight: 10,
		},
		Vs: &balancerpb.VsConfigList{
			Vs: []*balancerpb.VsConfig{
				{
					Id:        vs1,
					Scheduler: balancerpb.VsScheduler_WLC,
					Flags: &balancerpb.VsFlags{
						FixMss: true,
					},
					Reals: []*balancerpb.RealConfig{
						{
							Weight: func() *uint32 {
								x := uint32(1)
								return &x
							}(),
							Id: rs,
							Src: &filterpb.IPNet{
								Addr: net.ParseIP("2a02:6b8:6666::"),
								Mask: net.CIDRMask(64, 128),
							},
						},
					},
					AllowedSources: []*balancerpb.AllowedSources{
						{
							Nets: []*filterpb.IPNet{
								// [::/0]
								{
									Addr: net.ParseIP("::"),
									Mask: net.CIDRMask(0, 128),
								},
							},
							Tag: func() *string {
								s := "123"
								return &s
							}(),
						},
					},
				},
				{
					Id:        vs2,
					Scheduler: balancerpb.VsScheduler_WLC,
					Flags: &balancerpb.VsFlags{
						FixMss: true,
					},
					Reals: []*balancerpb.RealConfig{
						{
							Weight: func() *uint32 {
								x := uint32(1)
								return &x
							}(),
							Id: rs,
							Src: &filterpb.IPNet{
								Addr: net.ParseIP("2a02:6b8:6666::"),
								Mask: net.CIDRMask(64, 128),
							},
						},
					},
					AllowedSources: []*balancerpb.AllowedSources{
						{
							Nets: []*filterpb.IPNet{
								// [::/0]
								{
									Addr: net.ParseIP("::"),
									Mask: net.CIDRMask(0, 128),
								},
							},
							Tag: func() *string {
								s := "123"
								return &s
							}(),
						},
					},
				},
			},
		},
	}
	te := setupTestEnv(t, config)
	balancer := te.setup.balancer
	sessions := te.setup.sessions
	layers := te.packetGen.MakeTCPPacket(
		"1::",
		"2a02:6b8:0:3400:0:853a:0:3",
		100,
		80,
		true,
		false,
		false,
		false,
		nil,
	)
	packet := xpacket.LayersToPacket(t, layers...)
	result, err := te.mock.HandlePackets(packet)
	assert.NoError(t, err, "failed to handle packets")
	assert.Equal(t, 1, len(result.Drop), "not dropped packet but there is no reals")
	err = balancer.UpdateReals([]*balancerpb.RealUpdate{
		{
			RealId: &balancerpb.RealIdentifier{
				Vs:   vs1,
				Real: rs,
			},
			Enable: func() *bool {
				en := true
				return &en
			}(),
		},
		{
			RealId: &balancerpb.RealIdentifier{
				Vs:   vs2,
				Real: rs,
			},
			Enable: func() *bool {
				en := true
				return &en
			}(),
		},
	})
	assert.NoError(t, err, "failed to update reals")
	result, err = te.mock.HandlePackets(packet)
	assert.NoError(t, err, "failed to handle packets")
	assert.Equal(t, 1, len(result.Output), err, "no output packets")
	assert.True(t, result.Output[0].IsTunneled, "result packet is not tunneled")

	states := balancer.GetState(nil, nil, te.mock.CurrentTime())
	assert.Equal(t, 1, len(states))
	state := states[0]
	assert.Equal(t, uint64(1), state.Vs[0].Reals[0].Stats.Packets)
	assert.Equal(t, uint64(1), state.Vs[0].Stats.CreatedSessions)
	assert.Equal(t, uint64(2), state.Vs[0].AllowedSourcesStats[0].Passes)
	assert.Equal(t, "123", state.Vs[0].AllowedSourcesStats[0].Tag)

	count := 0
	for range sessions.IterSessions(te.mock.CurrentTime()) {
		count += 1
	}
	assert.Equal(t, 1, count)
}
