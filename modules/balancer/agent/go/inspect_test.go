package balancer

import (
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	mock "github.com/yanet-platform/yanet2/mock/go"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestBalancerAgentInspect(t *testing.T) {
	// Create mock Yanet instance
	m, err := mock.NewYanetMock(&mock.YanetMockConfig{
		AgentsMemory: 1 << 27,
		DpMemory:     1 << 24,
		Workers:      1,
		Devices: []mock.YanetMockDeviceConfig{
			{
				ID:   0,
				Name: "eth0",
			},
		},
	})
	require.NoError(t, err, "failed to initialize mock")
	require.NotNil(t, m, "mock is nil")
	defer m.Free()

	// Create logger for tests
	log := zap.NewNop().Sugar()

	// Create balancer agent
	agent, err := NewBalancerAgent(m.SharedMemory(), 32*datasize.MB, log)
	require.NoError(t, err, "failed to create balancer agent")
	require.NotNil(t, agent, "balancer agent is nil")

	// Define first balancer configuration
	firstBalancerConfig := &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SessionsTimeouts: &balancerpb.SessionsTimeouts{
				TcpSynAck: 10,
				TcpSyn:    20,
				TcpFin:    15,
				Tcp:       100,
				Udp:       11,
				Default:   19,
			},
			Vs: []*balancerpb.VirtualService{
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
						},
						Port:  80,
						Proto: balancerpb.TransportProto_TCP,
					},
					Flags: &balancerpb.VsFlags{
						FixMss: true,
					},
					Scheduler: balancerpb.VsScheduler_SOURCE_HASH,
					Reals: []*balancerpb.Real{
						{
							Id: &balancerpb.RelativeRealIdentifier{
								Ip: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("10.0.1.1").
										AsSlice(),
								},
								Port: 8080,
							},
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("172.16.0.0").
									AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.0").
									AsSlice(),
							},
							Weight: 100,
						},
					},
					AllowedSrcs: []*balancerpb.AllowedSources{
						{
							Nets: []*balancerpb.Net{{
								Addr: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("192.168.1.0").
										AsSlice(),
								},
								Mask: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("255.255.255.0").
										AsSlice(),
								},
							}},
						},
					},
				},
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("10.0.0.2").AsSlice(),
						},
						Port:  443,
						Proto: balancerpb.TransportProto_TCP,
					},
					Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
					Reals: []*balancerpb.Real{
						{
							Id: &balancerpb.RelativeRealIdentifier{
								Ip: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("10.0.1.2").
										AsSlice(),
								},
								Port: 8443,
							},
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("172.16.0.0").
									AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.0").
									AsSlice(),
							},
							Weight: 50,
						},
					},
				},
			},
			SourceAddressV4: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("2001:db8::1").AsSlice(),
			},
			DecapAddresses: []*balancerpb.Addr{},
		},
		State: &balancerpb.StateConfig{
			SessionTableCapacity:      func() *uint64 { v := uint64(1000); return &v }(),
			SessionTableMaxLoadFactor: nil,
			RefreshPeriod:             durationpb.New(0),
			Wlc:                       nil,
		},
	}

	// Define second balancer configuration
	secondBalancerConfig := &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SessionsTimeouts: &balancerpb.SessionsTimeouts{
				TcpSynAck: 15,
				TcpSyn:    25,
				TcpFin:    20,
				Tcp:       69,
				Udp:       15,
				Default:   25,
			},
			Vs: []*balancerpb.VirtualService{
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("20.0.0.1").AsSlice(),
						},
						Port:  8080,
						Proto: balancerpb.TransportProto_UDP,
					},
					Scheduler: balancerpb.VsScheduler_SOURCE_HASH,
					Reals: []*balancerpb.Real{
						{
							Id: &balancerpb.RelativeRealIdentifier{
								Ip: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("20.0.1.1").
										AsSlice(),
								},
								Port: 9090,
							},
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("172.17.0.0").
									AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.0").
									AsSlice(),
							},
							Weight: 200,
						},
					},
				},
			},
			SourceAddressV4: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("20.0.0.1").AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("2001:db8::a").AsSlice(),
			},
			DecapAddresses: []*balancerpb.Addr{},
		},
		State: &balancerpb.StateConfig{
			SessionTableCapacity:      func() *uint64 { v := uint64(2000); return &v }(),
			SessionTableMaxLoadFactor: nil,
			RefreshPeriod:             durationpb.New(0),
			Wlc:                       nil,
		},
	}

	// Create first balancer
	err = agent.NewBalancerManager("balancer0", firstBalancerConfig)
	require.NoError(t, err, "failed to create first balancer")

	// Create second balancer
	err = agent.NewBalancerManager("balancer1", secondBalancerConfig)
	require.NoError(t, err, "failed to create second balancer")

	t.Run("Inspect_AgentLevel", func(t *testing.T) {
		// Get inspect data
		inspect := agent.Inspect()
		require.NotNil(t, inspect, "inspect data is nil")

		// Verify agent-level fields
		assert.Greater(
			t,
			inspect.MemoryLimit,
			uint64(0),
			"memory limit should be greater than 0",
		)
		assert.GreaterOrEqual(
			t,
			inspect.MemoryUsage,
			uint64(0),
			"memory usage should be non-negative",
		)
		assert.LessOrEqual(
			t,
			inspect.MemoryUsage,
			inspect.MemoryLimit,
			"memory usage should not exceed limit",
		)

		// Verify we have two balancers
		require.Len(
			t,
			inspect.Balancers,
			2,
			"expected two balancers in inspect",
		)
	})

	t.Run("Inspect_BalancerNames", func(t *testing.T) {
		inspect := agent.Inspect()
		require.NotNil(t, inspect, "inspect data is nil")

		// Collect balancer names
		balancerNames := make(map[string]bool)
		for _, balancer := range inspect.Balancers {
			balancerNames[balancer.Name] = true
		}

		// Verify both balancer names are present
		assert.True(
			t,
			balancerNames["balancer0"],
			"balancer0 should be in inspect",
		)
		assert.True(
			t,
			balancerNames["balancer1"],
			"balancer1 should be in inspect",
		)
	})

	t.Run("Inspect_Balancer0_VirtualServices", func(t *testing.T) {
		inspect := agent.Inspect()
		require.NotNil(t, inspect, "inspect data is nil")

		// Find balancer0
		var balancer0 *balancerpb.BalancerInspect
		for _, b := range inspect.Balancers {
			if b.Name == "balancer0" {
				balancer0 = b
				break
			}
		}
		require.NotNil(t, balancer0, "balancer0 not found in inspect")

		// Verify packet handler inspect exists
		require.NotNil(
			t,
			balancer0.PacketHandlerInspect,
			"packet handler inspect is nil",
		)

		// Verify memory usage fields are present
		assert.GreaterOrEqual(
			t,
			balancer0.TotalUsage,
			uint64(0),
			"total usage should be non-negative",
		)

		// Check IPv4 VS inspect
		require.NotNil(
			t,
			balancer0.PacketHandlerInspect.VsIpv4Inspect,
			"IPv4 VS inspect is nil",
		)
		vsIpv4Inspects := balancer0.PacketHandlerInspect.VsIpv4Inspect.VsInspects
		require.Len(
			t,
			vsIpv4Inspects,
			2,
			"expected 2 IPv4 virtual services in balancer0",
		)

		// Verify VS identifiers
		vsAddrs := make(map[string]bool)
		for _, vsInspect := range vsIpv4Inspects {
			require.NotNil(t, vsInspect.Identifier, "VS identifier is nil")
			addr := netip.AddrFrom4([4]byte(vsInspect.Identifier.Addr.Bytes))
			vsAddrs[addr.String()] = true
		}

		assert.True(
			t,
			vsAddrs["10.0.0.1"],
			"VS 10.0.0.1:80 should be in balancer0",
		)
		assert.True(
			t,
			vsAddrs["10.0.0.2"],
			"VS 10.0.0.2:443 should be in balancer0",
		)
	})

	t.Run("Inspect_Balancer1_VirtualServices", func(t *testing.T) {
		inspect := agent.Inspect()
		require.NotNil(t, inspect, "inspect data is nil")

		// Find balancer1
		var balancer1 *balancerpb.BalancerInspect
		for _, b := range inspect.Balancers {
			if b.Name == "balancer1" {
				balancer1 = b
				break
			}
		}
		require.NotNil(t, balancer1, "balancer1 not found in inspect")

		// Verify packet handler inspect exists
		require.NotNil(
			t,
			balancer1.PacketHandlerInspect,
			"packet handler inspect is nil",
		)

		// Check IPv4 VS inspect
		require.NotNil(
			t,
			balancer1.PacketHandlerInspect.VsIpv4Inspect,
			"IPv4 VS inspect is nil",
		)
		vsIpv4Inspects := balancer1.PacketHandlerInspect.VsIpv4Inspect.VsInspects
		require.Len(
			t,
			vsIpv4Inspects,
			1,
			"expected 1 IPv4 virtual service in balancer1",
		)

		// Verify VS identifier
		vsInspect := vsIpv4Inspects[0]
		require.NotNil(t, vsInspect.Identifier, "VS identifier is nil")
		addr := netip.AddrFrom4([4]byte(vsInspect.Identifier.Addr.Bytes))
		assert.Equal(
			t,
			"20.0.0.1",
			addr.String(),
			"VS address should be 20.0.0.1",
		)
		assert.Equal(
			t,
			uint32(8080),
			vsInspect.Identifier.Port,
			"VS port should be 8080",
		)
		assert.Equal(
			t,
			balancerpb.TransportProto_UDP,
			vsInspect.Identifier.Proto,
			"VS proto should be UDP",
		)
	})

	t.Run("Inspect_StateMemory", func(t *testing.T) {
		inspect := agent.Inspect()
		require.NotNil(t, inspect, "inspect data is nil")

		// Verify state inspect for both balancers
		for _, balancer := range inspect.Balancers {
			require.NotNil(
				t,
				balancer.StateInspect,
				"state inspect is nil for %s",
				balancer.Name,
			)
			assert.GreaterOrEqual(
				t,
				balancer.StateInspect.TotalUsage,
				uint64(0),
				"state total usage should be non-negative",
			)
		}
	})

	t.Run("Inspect_MemoryBreakdown", func(t *testing.T) {
		inspect := agent.Inspect()
		require.NotNil(t, inspect, "inspect data is nil")

		// Verify memory breakdown for each balancer
		for _, balancer := range inspect.Balancers {
			// Packet handler memory
			ph := balancer.PacketHandlerInspect
			require.NotNil(t, ph, "packet handler inspect is nil")
			assert.GreaterOrEqual(
				t,
				ph.TotalUsage,
				uint64(0),
				"packet handler total usage should be non-negative",
			)

			// State memory
			state := balancer.StateInspect
			require.NotNil(t, state, "state inspect is nil")
			assert.GreaterOrEqual(
				t,
				state.TotalUsage,
				uint64(0),
				"state total usage should be non-negative",
			)

			// Total balancer memory should be sum of components
			expectedTotal := ph.TotalUsage + state.TotalUsage + balancer.OtherUsage
			assert.Equal(
				t,
				expectedTotal,
				balancer.TotalUsage,
				"balancer total usage should equal sum of components",
			)
		}
	})
}
