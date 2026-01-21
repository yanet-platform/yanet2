package ffi

import (
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xnetip"
	mock "github.com/yanet-platform/yanet2/mock/go"
)

func TestAgent(t *testing.T) {
	// Create mock
	m, err := mock.NewYanetMock(&mock.YanetMockConfig{
		AgentsMemory: 1 << 28,
		DpMemory:     1 << 24,
		Workers:      1,
		Devices: []mock.YanetMockDeviceConfig{
			{
				Id:   0,
				Name: "eth0",
			},
		},
	})
	require.NoError(t, err, "failed to initialize mock")
	require.NotNil(t, m, "mock is nil")
	defer m.Free()

	// Create balancer agent

	agent, err := NewBalancerAgent(m.SharedMemory(), 1<<27)
	require.NoError(t, err, "failed to create balancer agent")
	require.NotNil(t, agent, "balancer agent is nil")

	managers := agent.Managers()
	assert.Empty(t, managers)

	firstManagerConfig := BalancerManagerConfig{
		Balancer: BalancerConfig{
			Handler: PacketHandlerConfig{
				SessionsTimeouts: SessionsTimeouts{
					TcpSynAck: 10,
					TcpSyn:    20,
					TcpFin:    15,
					Tcp:       100,
					Udp:       11,
					Default:   19,
				},
				VirtualServices: []VsConfig{
					{
						Identifier: VsIdentifier{
							Addr:           netip.MustParseAddr("10.12.13.213"),
							Port:           80,
							TransportProto: VsTransportProtoTcp,
						},
						Flags:     VsFlags{FixMSS: true},
						Scheduler: VsSchedulerSourceHash,
						Reals: []RealConfig{
							{
								Identifier: RelativeRealIdentifier{
									Addr: netip.MustParseAddr("10.12.13.213"),
									Port: 8080,
								},
								Src: xnetip.FromPrefix(
									netip.MustParsePrefix("172.16.0.0/24"),
								),
								Weight: 100,
							},
						},
						AllowedSrc: []netip.Prefix{
							netip.MustParsePrefix("192.1.1.1/24"),
							netip.MustParsePrefix("192.12.0.0/16"),
						},
						PeersV4: []netip.Addr{
							netip.MustParseAddr("12.1.1.3"),
							netip.MustParseAddr("12.1.1.4"),
						},
						PeersV6: []netip.Addr{
							netip.MustParseAddr("2001:db8::2"),
							netip.MustParseAddr("2001:db8::3"),
						},
					},
				},
				SourceV4: netip.MustParseAddr("10.12.13.213"),
				SourceV6: netip.MustParseAddr("2001:db8::1"),
				DecapV4: []netip.Addr{
					netip.MustParseAddr("10.13.11.215"),
					netip.MustParseAddr("10.14.11.214"),
				},
				DecapV6: []netip.Addr{
					netip.MustParseAddr("2001:db8::3"),
					netip.MustParseAddr("2001:db8::2"),
				},
			},
			State: StateConfig{
				TableCapacity: 1000,
			},
		},
		Wlc: BalancerManagerWlcConfig{
			Power:         10,
			MaxRealWeight: 1024,
			Vs:            []uint32{0, 1},
		},
		RefreshPeriod: time.Millisecond * 10,
		MaxLoadFactor: 0.75,
	}

	t.Run("First_Manager", func(t *testing.T) {
		_, err := agent.NewManager("balancer0", &firstManagerConfig)
		require.NoError(t, err, "failed to create manager")
	})

	t.Run("Managers", func(t *testing.T) {
		managers := agent.Managers()
		assert.Len(t, managers, 1)
		assert.Equal(t, "balancer0", managers[0].Name())
		assert.Equal(t, &firstManagerConfig, managers[0].Config())
	})

	secondManagerConfig := BalancerManagerConfig{
		Balancer: BalancerConfig{
			Handler: PacketHandlerConfig{
				SessionsTimeouts: SessionsTimeouts{
					TcpSynAck: 15,
					TcpSyn:    25,
					TcpFin:    20,
					Tcp:       120,
					Udp:       15,
					Default:   25,
				},
				VirtualServices: []VsConfig{
					{
						Identifier: VsIdentifier{
							Addr:           netip.MustParseAddr("10.20.30.40"),
							Port:           443,
							TransportProto: VsTransportProtoTcp,
						},
						Flags:     VsFlags{OPS: true, GRE: true},
						Scheduler: VsSchedulerRoundRobin,
						Reals: []RealConfig{
							{
								Identifier: RelativeRealIdentifier{
									Addr: netip.MustParseAddr("10.20.30.40"),
									Port: 8443,
								},
								Src: xnetip.FromPrefix(
									netip.MustParsePrefix("172.17.0.0/24"),
								),
								Weight: 150,
							},
							{
								Identifier: RelativeRealIdentifier{
									Addr: netip.MustParseAddr("10.20.30.41"),
									Port: 8443,
								},
								Src: xnetip.FromPrefix(
									netip.MustParsePrefix("172.17.1.0/24"),
								),
								Weight: 200,
							},
						},
						AllowedSrc: []netip.Prefix{
							netip.MustParsePrefix("192.2.2.0/24"),
							netip.MustParsePrefix("192.13.0.0/16"),
						},
						PeersV4: []netip.Addr{
							netip.MustParseAddr("12.2.2.3"),
							netip.MustParseAddr("12.2.2.4"),
						},
						PeersV6: []netip.Addr{
							netip.MustParseAddr("2001:db8::4"),
							netip.MustParseAddr("2001:db8::5"),
						},
					},
				},
				SourceV4: netip.MustParseAddr("10.20.30.40"),
				SourceV6: netip.MustParseAddr("2001:db8::10"),
				DecapV4: []netip.Addr{
					netip.MustParseAddr("10.15.12.216"),
					netip.MustParseAddr("10.16.12.215"),
				},
				DecapV6: []netip.Addr{
					netip.MustParseAddr("2001:db8::5"),
					netip.MustParseAddr("2001:db8::4"),
				},
			},
			State: StateConfig{
				TableCapacity: 2000,
			},
		},
		Wlc: BalancerManagerWlcConfig{
			Power:         15,
			MaxRealWeight: 512,
			Vs:            []uint32{},
		},
		RefreshPeriod: time.Millisecond * 20,
		MaxLoadFactor: 0.85,
	}

	t.Run("Second_Manager", func(t *testing.T) {
		_, err := agent.NewManager("balancer1", &secondManagerConfig)
		require.NoError(t, err, "failed to create manager")
	})

	t.Run("Managers2", func(t *testing.T) {
		managers := agent.Managers()
		assert.Len(t, managers, 2)

		assert.Equal(t, managers[0].Name(), "balancer0")
		assert.Equal(t, managers[0].Config(), &firstManagerConfig)

		assert.Equal(t, managers[1].Name(), "balancer1")
		assert.Equal(t, managers[1].Config(), &secondManagerConfig)
	})

	t.Run("Create_Existing_Manager", func(t *testing.T) {
		_, err := agent.NewManager("balancer0", &firstManagerConfig)
		require.Error(t, err, "created existent manager")
	})

	t.Run("Reattach", func(t *testing.T) {
		agent1, err := NewBalancerAgent(m.SharedMemory(), 1<<22)
		require.NoError(t, err, "failed to create agent")

		managers := agent1.Managers()
		assert.Len(t, managers, 2)

		assert.Equal(t, managers[0].Name(), "balancer0")
		assert.Equal(t, managers[0].Config(), &firstManagerConfig)

		assert.Equal(t, managers[1].Name(), "balancer1")
		assert.Equal(t, managers[1].Config(), &secondManagerConfig)
	})
}
