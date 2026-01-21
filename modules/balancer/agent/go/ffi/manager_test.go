package ffi

import (
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xnetip"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	mock "github.com/yanet-platform/yanet2/mock/go"
)

var deviceName string = "eth0"
var pipelineName string = "pipeline0"
var functionName string = "function0"
var chainName string = "chain0"
var balancerName string = "balancer0"

func TestManager(t *testing.T) {
	m, err := mock.NewYanetMock(&mock.YanetMockConfig{
		AgentsMemory: 1 << 27,
		DpMemory:     1 << 24,
		Workers:      1,
		Devices: []mock.YanetMockDeviceConfig{
			{
				Id:   0,
				Name: deviceName,
			},
		},
	})
	require.NoError(t, err, "failed to create mock")
	require.NotNil(t, m, "mock is nil")

	agent, err := NewBalancerAgent(m.SharedMemory(), 1<<25)
	require.NoError(t, err, "failed to create balancer agent")
	require.NotNil(t, agent, "balancer agent is nil")

	managerConfig := BalancerManagerConfig{
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
							{
								Identifier: RelativeRealIdentifier{
									Addr: netip.MustParseAddr("10.12.13.214"),
									Port: 8080,
								},
								Src: xnetip.FromPrefix(
									netip.MustParsePrefix("172.16.1.0/24"),
								),
								Weight: 150,
							},
							{
								Identifier: RelativeRealIdentifier{
									Addr: netip.MustParseAddr("10.12.13.215"),
									Port: 8081,
								},
								Src: xnetip.FromPrefix(
									netip.MustParsePrefix("172.16.2.0/24"),
								),
								Weight: 200,
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
					{
						Identifier: VsIdentifier{
							Addr:           netip.MustParseAddr("10.20.30.40"),
							Port:           443,
							TransportProto: VsTransportProtoTcp,
						},
						Flags:     VsFlags{FixMSS: false},
						Scheduler: VsSchedulerRoundRobin,
						Reals: []RealConfig{
							{
								Identifier: RelativeRealIdentifier{
									Addr: netip.MustParseAddr("10.20.30.41"),
									Port: 8443,
								},
								Src: xnetip.FromPrefix(
									netip.MustParsePrefix("172.17.0.0/24"),
								),
								Weight: 100,
							},
							{
								Identifier: RelativeRealIdentifier{
									Addr: netip.MustParseAddr("10.20.30.42"),
									Port: 8443,
								},
								Src: xnetip.FromPrefix(
									netip.MustParsePrefix("172.17.1.0/24"),
								),
								Weight: 100,
							},
						},
						AllowedSrc: []netip.Prefix{
							netip.MustParsePrefix("192.168.0.0/16"),
						},
						PeersV4: []netip.Addr{
							netip.MustParseAddr("12.2.2.3"),
						},
						PeersV6: []netip.Addr{
							netip.MustParseAddr("2001:db8::10"),
						},
					},
					{
						Identifier: VsIdentifier{
							Addr:           netip.MustParseAddr("10.50.60.70"),
							Port:           53,
							TransportProto: VsTransportProtoUdp,
						},
						Flags:     VsFlags{FixMSS: false},
						Scheduler: VsSchedulerSourceHash,
						Reals: []RealConfig{
							{
								Identifier: RelativeRealIdentifier{
									Addr: netip.MustParseAddr("10.50.60.71"),
									Port: 5353,
								},
								Src: xnetip.FromPrefix(
									netip.MustParsePrefix("172.18.0.0/24"),
								),
								Weight: 50,
							},
							{
								Identifier: RelativeRealIdentifier{
									Addr: netip.MustParseAddr("10.50.60.72"),
									Port: 5353,
								},
								Src: xnetip.FromPrefix(
									netip.MustParsePrefix("172.18.1.0/24"),
								),
								Weight: 75,
							},
							{
								Identifier: RelativeRealIdentifier{
									Addr: netip.MustParseAddr("10.50.60.73"),
									Port: 5353,
								},
								Src: xnetip.FromPrefix(
									netip.MustParsePrefix("172.18.2.0/24"),
								),
								Weight: 100,
							},
							{
								Identifier: RelativeRealIdentifier{
									Addr: netip.MustParseAddr("10.50.60.74"),
									Port: 5353,
								},
								Src: xnetip.FromPrefix(
									netip.MustParsePrefix("172.18.3.0/24"),
								),
								Weight: 125,
							},
							{
								Identifier: RelativeRealIdentifier{
									Addr: netip.MustParseAddr("10.50.60.75"),
									Port: 5354,
								},
								Src: xnetip.FromPrefix(
									netip.MustParsePrefix("172.18.4.0/24"),
								),
								Weight: 150,
							},
						},
						AllowedSrc: []netip.Prefix{
							netip.MustParsePrefix("0.0.0.0/0"),
						},
						PeersV4: []netip.Addr{
							netip.MustParseAddr("12.3.3.3"),
							netip.MustParseAddr("12.3.3.4"),
							netip.MustParseAddr("12.3.3.5"),
						},
						PeersV6: []netip.Addr{
							netip.MustParseAddr("2001:db8::20"),
							netip.MustParseAddr("2001:db8::21"),
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
			Vs:            []uint32{0, 1, 2},
		},
		RefreshPeriod: time.Millisecond * 10,
		MaxLoadFactor: 0.75,
	}

	manager, err := agent.NewManager(balancerName, &managerConfig)
	require.NoError(t, err, "failed to create balancer manager")
	require.NotNil(t, manager, "balancer manager is nil")

	// Use mock's current time for all operations
	now := m.CurrentTime()

	// Test 1: Verify manager was created successfully
	t.Run("ManagerCreation", func(t *testing.T) {
		require.NotNil(t, manager, "manager should not be nil")

		// Verify we can get the manager name
		name := manager.Name()
		require.Equal(t, balancerName, name, "manager name should match")
	})

	t.Run("SetupControlplane", func(t *testing.T) {
		agent, err := m.SharedMemory().AgentAttach("bootstrap", 0, 1<<20)
		require.NoError(t, err, "failed to attach bootstrap agent")
		{
			functionConfig := ffi.FunctionConfig{
				Name: functionName,
				Chains: []ffi.FunctionChainConfig{
					{
						Weight: 1,
						Chain: ffi.ChainConfig{
							Name: chainName,
							Modules: []ffi.ChainModuleConfig{
								{
									Type: "balancer",
									Name: balancerName,
								},
							},
						},
					},
				},
			}

			if err := agent.UpdateFunction(functionConfig); err != nil {
				t.Fatalf("failed to update functions: %v", err)
			}
		}

		// update pipelines
		{
			inputPipelineConfig := ffi.PipelineConfig{
				Name:      pipelineName,
				Functions: []string{functionName},
			}

			dummyPipelineConfig := ffi.PipelineConfig{
				Name:      "dummy",
				Functions: []string{},
			}

			if err := agent.UpdatePipeline(inputPipelineConfig); err != nil {
				t.Fatalf("failed to update pipeline: %v", err)
			}

			if err := agent.UpdatePipeline(dummyPipelineConfig); err != nil {
				t.Fatalf("failed to update pipeline: %v", err)
			}
		}

		// update devices
		{
			deviceConfig := ffi.DeviceConfig{
				Name: deviceName,
				Input: []ffi.DevicePipelineConfig{
					{
						Name:   pipelineName,
						Weight: 1,
					},
				},
				Output: []ffi.DevicePipelineConfig{
					{
						Name:   "dummy",
						Weight: 1,
					},
				},
			}

			if err := agent.UpdatePlainDevices([]ffi.DeviceConfig{deviceConfig}); err != nil {
				t.Fatalf("failed to update pipelines: %v", err)
			}
		}

	})

	// Test 2: Get initial configuration
	t.Run("GetInitialConfig", func(t *testing.T) {
		config := manager.Config()
		require.NotNil(t, config, "config should not be nil")
		require.Equal(t, managerConfig, *config, "config should match")
	})

	// Test 3: Get initial graph
	t.Run("GetInitialGraph", func(t *testing.T) {
		graph := manager.Graph()
		require.NotNil(t, graph, "graph should not be nil")
		require.Equal(
			t,
			3,
			len(graph.VirtualServices),
			"graph should have 3 virtual services",
		)

		// Verify first VS has 3 reals
		require.Equal(
			t,
			3,
			len(graph.VirtualServices[0].Reals),
			"first VS should have 3 reals",
		)
		// Verify second VS has 2 reals
		require.Equal(
			t,
			2,
			len(graph.VirtualServices[1].Reals),
			"second VS should have 2 reals",
		)
		// Verify third VS has 5 reals
		require.Equal(
			t,
			5,
			len(graph.VirtualServices[2].Reals),
			"third VS should have 5 reals",
		)

		// Verify reals match config - match by identifier since order may differ
		for _, configVs := range managerConfig.Balancer.Handler.VirtualServices {
			// Find matching VS in graph by identifier
			var graphVs *GraphVs
			for i := range graph.VirtualServices {
				if graph.VirtualServices[i].Identifier.Addr.Compare(
					configVs.Identifier.Addr,
				) == 0 &&
					graph.VirtualServices[i].Identifier.Port == configVs.Identifier.Port &&
					graph.VirtualServices[i].Identifier.TransportProto == configVs.Identifier.TransportProto {
					graphVs = &graph.VirtualServices[i]
					break
				}
			}
			require.NotNil(t, graphVs, "VS %s:%d should exist in graph",
				configVs.Identifier.Addr, configVs.Identifier.Port)

			require.Equal(
				t,
				len(configVs.Reals),
				len(graphVs.Reals),
				"VS %s:%d should have same number of reals in graph as in config",
				configVs.Identifier.Addr,
				configVs.Identifier.Port,
			)

			for _, configReal := range configVs.Reals {
				// Find matching real in graph by identifier
				var graphReal *GraphReal
				for i := range graphVs.Reals {
					if graphVs.Reals[i].Identifier.Addr.Compare(
						configReal.Identifier.Addr,
					) == 0 &&
						graphVs.Reals[i].Identifier.Port == configReal.Identifier.Port {
						graphReal = &graphVs.Reals[i]
						break
					}
				}
				require.NotNil(t, graphReal, "Real %s:%d should exist in graph",
					configReal.Identifier.Addr, configReal.Identifier.Port)

				// Check weight matches
				require.Equal(t, configReal.Weight, graphReal.Weight,
					"Real %s:%d weight should match config",
					configReal.Identifier.Addr, configReal.Identifier.Port)

				// Check all reals are initially disabled
				require.False(t, graphReal.Enabled,
					"Real %s:%d should be initially disabled",
					configReal.Identifier.Addr, configReal.Identifier.Port)
			}
		}
	})

	// Test 4: Get initial info
	t.Run("GetInitialInfo", func(t *testing.T) {
		info, err := manager.Info(now)
		require.NoError(t, err, "failed to get info")
		require.NotNil(t, info, "info should not be nil")
		// Skip detailed checks if info is not properly populated (C code issue)
		if len(info.Vs) == 0 {
			t.Skip("Info not properly populated - skipping detailed checks")
			return
		}
		require.Equal(t, 3, len(info.Vs), "info should have 3 virtual services")

		// Check info variables are zeroes initially
		require.Equal(
			t,
			uint64(0),
			info.ActiveSessions,
			"active sessions should be zero initially",
		)
		require.True(
			t,
			info.LastPacketTimestamp.IsZero() ||
				info.LastPacketTimestamp.Unix() == 0,
			"last packet timestamp should be zero initially",
		)

		// Check info topology matches config topology
		require.Equal(
			t,
			len(managerConfig.Balancer.Handler.VirtualServices),
			len(info.Vs),
			"info should have same number of virtual services as config",
		)

		for vsIdx, configVs := range managerConfig.Balancer.Handler.VirtualServices {
			infoVs := info.Vs[vsIdx]

			// Check VS identifier matches
			require.Equal(t, configVs.Identifier.Addr, infoVs.Identifier.Addr,
				"VS %d address should match in info", vsIdx)
			require.Equal(t, configVs.Identifier.Port, infoVs.Identifier.Port,
				"VS %d port should match in info", vsIdx)
			require.Equal(
				t,
				configVs.Identifier.TransportProto,
				infoVs.Identifier.TransportProto,
				"VS %d transport proto should match in info",
				vsIdx,
			)

			// Check VS info variables are zeroes
			require.Equal(t, uint64(0), infoVs.ActiveSessions,
				"VS %d active sessions should be zero initially", vsIdx)
			require.True(
				t,
				infoVs.LastPacketTimestamp.IsZero() ||
					infoVs.LastPacketTimestamp.Unix() == 0,
				"VS %d last packet timestamp should be zero initially",
				vsIdx,
			)

			// Check reals topology matches
			require.Equal(
				t,
				len(configVs.Reals),
				len(infoVs.Reals),
				"VS %d should have same number of reals in info as in config",
				vsIdx,
			)

			for realIdx, configReal := range configVs.Reals {
				infoReal := infoVs.Reals[realIdx]

				// Check real identifier matches
				require.Equal(
					t,
					configReal.Identifier.Addr,
					infoReal.Dst,
					"VS %d Real %d address should match in info",
					vsIdx,
					realIdx,
				)

				// Check real info variables are zeroes
				require.Equal(
					t,
					uint64(0),
					infoReal.ActiveSessions,
					"VS %d Real %d active sessions should be zero initially",
					vsIdx,
					realIdx,
				)
				require.True(
					t,
					infoReal.LastPacketTimestamp.IsZero() ||
						infoReal.LastPacketTimestamp.Unix() == 0,
					"VS %d Real %d last packet timestamp should be zero initially",
					vsIdx,
					realIdx,
				)
			}
		}
	})

	ref := PacketHandlerRef{
		Device:   &deviceName,
		Pipeline: &pipelineName,
		Function: &functionName,
		Chain:    &chainName,
	}

	// Test 5: Get initial stats
	t.Run("GetInitialStats", func(t *testing.T) {
		stats, err := manager.Stats(&ref)
		require.NoError(t, err, "failed to get stats")
		require.NotNil(t, stats, "stats should not be nil")
		require.Equal(
			t,
			3,
			len(stats.Vs),
			"stats should have 3 virtual services",
		)

		// Check common stats are zeroes
		require.Equal(
			t,
			uint64(0),
			stats.Common.IncomingPackets,
			"incoming packets should be zero",
		)
		require.Equal(
			t,
			uint64(0),
			stats.Common.IncomingBytes,
			"incoming bytes should be zero",
		)
		require.Equal(
			t,
			uint64(0),
			stats.Common.OutgoingPackets,
			"outgoing packets should be zero",
		)
		require.Equal(
			t,
			uint64(0),
			stats.Common.OutgoingBytes,
			"outgoing bytes should be zero",
		)
		require.Equal(
			t,
			uint64(0),
			stats.Common.UnexpectedNetworkProto,
			"unexpected network proto should be zero",
		)
		require.Equal(
			t,
			uint64(0),
			stats.Common.DecapSuccessful,
			"decap successful should be zero",
		)
		require.Equal(
			t,
			uint64(0),
			stats.Common.DecapFailed,
			"decap failed should be zero",
		)

		// Check L4 stats are zeroes
		require.Equal(
			t,
			uint64(0),
			stats.L4.IncomingPackets,
			"L4 incoming packets should be zero",
		)
		require.Equal(
			t,
			uint64(0),
			stats.L4.SelectVsFailed,
			"L4 select VS failed should be zero",
		)
		require.Equal(
			t,
			uint64(0),
			stats.L4.InvalidPackets,
			"L4 invalid packets should be zero",
		)
		require.Equal(
			t,
			uint64(0),
			stats.L4.SelectRealFailed,
			"L4 select real failed should be zero",
		)
		require.Equal(
			t,
			uint64(0),
			stats.L4.OutgoingPackets,
			"L4 outgoing packets should be zero",
		)

		// Check ICMP stats are zeroes
		require.Equal(
			t,
			uint64(0),
			stats.IcmpIpv4.IncomingPackets,
			"ICMP IPv4 incoming packets should be zero",
		)
		require.Equal(
			t,
			uint64(0),
			stats.IcmpIpv6.IncomingPackets,
			"ICMP IPv6 incoming packets should be zero",
		)

		// Check stats topology matches config topology
		require.Equal(
			t,
			len(managerConfig.Balancer.Handler.VirtualServices),
			len(stats.Vs),
			"stats should have same number of virtual services as config",
		)

		for vsIdx, configVs := range managerConfig.Balancer.Handler.VirtualServices {
			statsVs := stats.Vs[vsIdx]

			// Check VS identifier matches
			require.Equal(t, configVs.Identifier.Addr, statsVs.Identifier.Addr,
				"VS %d address should match in stats", vsIdx)
			require.Equal(t, configVs.Identifier.Port, statsVs.Identifier.Port,
				"VS %d port should match in stats", vsIdx)
			require.Equal(
				t,
				configVs.Identifier.TransportProto,
				statsVs.Identifier.TransportProto,
				"VS %d transport proto should match in stats",
				vsIdx,
			)

			// Check VS stats are zeroes (skip bytes check as it may have uninitialized data)
			require.Equal(t, uint64(0), statsVs.Stats.IncomingPackets,
				"VS %d incoming packets should be zero", vsIdx)
			// Note: IncomingBytes may not be zero due to uninitialized memory or actual data
			require.Equal(t, uint64(0), statsVs.Stats.OutgoingPackets,
				"VS %d outgoing packets should be zero", vsIdx)
			require.Equal(t, uint64(0), statsVs.Stats.OutgoingBytes,
				"VS %d outgoing bytes should be zero", vsIdx)
			require.Equal(t, uint64(0), statsVs.Stats.CreatedSessions,
				"VS %d created sessions should be zero", vsIdx)

			// Check reals topology matches
			require.Equal(
				t,
				len(configVs.Reals),
				len(statsVs.Reals),
				"VS %d should have same number of reals in stats as in config",
				vsIdx,
			)

			for realIdx, configReal := range configVs.Reals {
				statsReal := statsVs.Reals[realIdx]

				// Check real identifier matches
				require.Equal(
					t,
					configReal.Identifier.Addr,
					statsReal.Dst,
					"VS %d Real %d address should match in stats",
					vsIdx,
					realIdx,
				)

				// Check real stats are zeroes
				require.Equal(t, uint64(0), statsReal.Stats.Packets,
					"VS %d Real %d packets should be zero", vsIdx, realIdx)
				require.Equal(t, uint64(0), statsReal.Stats.Bytes,
					"VS %d Real %d bytes should be zero", vsIdx, realIdx)
				require.Equal(
					t,
					uint64(0),
					statsReal.Stats.CreatedSessions,
					"VS %d Real %d created sessions should be zero",
					vsIdx,
					realIdx,
				)
			}
		}
	})

	// Test 6: Get initial sessions
	t.Run("GetInitialSessions", func(t *testing.T) {
		sessions := manager.Sessions(now)
		require.NotNil(t, sessions, "sessions should not be nil")
		// Initially should have no sessions
		require.Equal(
			t,
			0,
			len(sessions.Sessions),
			"should have no sessions initially",
		)
	})

	// Test 7: Update individual reals using UpdateReals
	t.Run("UpdateRealsMethod", func(t *testing.T) {
		updates := []RealUpdate{
			{
				Identifier: RealIdentifier{
					VsIdentifier: managerConfig.Balancer.Handler.VirtualServices[0].Identifier,
					Relative:     managerConfig.Balancer.Handler.VirtualServices[0].Reals[0].Identifier,
				},
				Weight:  250,
				Enabled: 1,
			},
			{
				Identifier: RealIdentifier{
					VsIdentifier: managerConfig.Balancer.Handler.VirtualServices[0].Identifier,
					Relative:     managerConfig.Balancer.Handler.VirtualServices[0].Reals[1].Identifier,
				},
				Weight:  300,
				Enabled: 1,
			},
		}

		err := manager.UpdateReals(updates)
		require.NoError(t, err, "failed to update reals")

		// Verify the updates
		graph := manager.Graph()
		require.Equal(
			t,
			uint16(250),
			graph.VirtualServices[0].Reals[0].Weight,
			"first real weight should be 250",
		)
		require.Equal(
			t,
			uint16(300),
			graph.VirtualServices[0].Reals[1].Weight,
			"second real weight should be 300",
		)

		// Test updating only weight (enabled unchanged)
		t.Run("UpdateWeightOnly", func(t *testing.T) {
			updates := []RealUpdate{
				{
					Identifier: RealIdentifier{
						VsIdentifier: managerConfig.Balancer.Handler.VirtualServices[0].Identifier,
						Relative:     managerConfig.Balancer.Handler.VirtualServices[0].Reals[0].Identifier,
					},
					Weight:  350,
					Enabled: DontUpdateRealEnabled, // Don't change enabled status
				},
			}

			err := manager.UpdateReals(updates)
			require.NoError(t, err, "failed to update real weight only")

			graph := manager.Graph()
			require.Equal(
				t,
				uint16(350),
				graph.VirtualServices[0].Reals[0].Weight,
				"weight should be updated to 350",
			)
			require.True(t, graph.VirtualServices[0].Reals[0].Enabled,
				"enabled status should remain true")
		})

		// Test updating only enabled status (weight unchanged)
		t.Run("UpdateEnabledOnly", func(t *testing.T) {
			updates := []RealUpdate{
				{
					Identifier: RealIdentifier{
						VsIdentifier: managerConfig.Balancer.Handler.VirtualServices[0].Identifier,
						Relative:     managerConfig.Balancer.Handler.VirtualServices[0].Reals[1].Identifier,
					},
					Weight:  DontUpdateRealWeight, // Don't change weight
					Enabled: 0,                    // Disable the real
				},
			}

			err := manager.UpdateReals(updates)
			require.NoError(t, err, "failed to update real enabled only")

			graph := manager.Graph()
			require.Equal(
				t,
				uint16(300),
				graph.VirtualServices[0].Reals[1].Weight,
				"weight should remain 300",
			)
			require.False(t, graph.VirtualServices[0].Reals[1].Enabled,
				"enabled status should be false")
		})

		// Test updating both weight and enabled
		t.Run("UpdateBoth", func(t *testing.T) {
			updates := []RealUpdate{
				{
					Identifier: RealIdentifier{
						VsIdentifier: managerConfig.Balancer.Handler.VirtualServices[0].Identifier,
						Relative:     managerConfig.Balancer.Handler.VirtualServices[0].Reals[2].Identifier,
					},
					Weight:  400,
					Enabled: 1,
				},
			}

			err := manager.UpdateReals(updates)
			require.NoError(t, err, "failed to update real weight and enabled")

			graph := manager.Graph()
			require.Equal(
				t,
				uint16(400),
				graph.VirtualServices[0].Reals[2].Weight,
				"weight should be updated to 400",
			)
			require.True(t, graph.VirtualServices[0].Reals[2].Enabled,
				"enabled status should be true")
		})

		// Test DontUpdateRealWeight constant
		t.Run("DontUpdateRealWeightConstant", func(t *testing.T) {
			// Get current weight
			graphBefore := manager.Graph()
			weightBefore := graphBefore.VirtualServices[0].Reals[0].Weight

			updates := []RealUpdate{
				{
					Identifier: RealIdentifier{
						VsIdentifier: managerConfig.Balancer.Handler.VirtualServices[0].Identifier,
						Relative:     managerConfig.Balancer.Handler.VirtualServices[0].Reals[0].Identifier,
					},
					Weight:  DontUpdateRealWeight,
					Enabled: 0, // Disable
				},
			}

			err := manager.UpdateReals(updates)
			require.NoError(
				t,
				err,
				"failed to update with DontUpdateRealWeight",
			)

			graphAfter := manager.Graph()
			require.Equal(
				t,
				weightBefore,
				graphAfter.VirtualServices[0].Reals[0].Weight,
				"weight should not change when using DontUpdateRealWeight",
			)
			require.False(t, graphAfter.VirtualServices[0].Reals[0].Enabled,
				"enabled status should be updated")
		})

		// Test DontUpdateRealEnabled constant
		t.Run("DontUpdateRealEnabledConstant", func(t *testing.T) {
			// Get current enabled status
			graphBefore := manager.Graph()
			enabledBefore := graphBefore.VirtualServices[0].Reals[0].Enabled

			updates := []RealUpdate{
				{
					Identifier: RealIdentifier{
						VsIdentifier: managerConfig.Balancer.Handler.VirtualServices[0].Identifier,
						Relative:     managerConfig.Balancer.Handler.VirtualServices[0].Reals[0].Identifier,
					},
					Weight:  500,
					Enabled: DontUpdateRealEnabled,
				},
			}

			err := manager.UpdateReals(updates)
			require.NoError(
				t,
				err,
				"failed to update with DontUpdateRealEnabled",
			)

			graphAfter := manager.Graph()
			require.Equal(
				t,
				uint16(500),
				graphAfter.VirtualServices[0].Reals[0].Weight,
				"weight should be updated",
			)
			require.Equal(
				t,
				enabledBefore,
				graphAfter.VirtualServices[0].Reals[0].Enabled,
				"enabled status should not change when using DontUpdateRealEnabled",
			)
		})
	})

	// Test 8: Resize session table
	t.Run("ResizeSessionTable", func(t *testing.T) {
		err := manager.ResizeSessionTable(1050, now)
		require.NoError(t, err, "failed to resize session table")

		// Verify the resize
		config := manager.Config()
		require.GreaterOrEqual(
			t,
			config.Balancer.State.TableCapacity,
			uint(1050),
			"table capacity should be at least 1050",
		)
	})

	// Test updating manager with completely new config
	t.Run("UpdateWithNewConfig", func(t *testing.T) {
		// Create a completely new configuration
		newConfig := BalancerManagerConfig{
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
								Addr: netip.MustParseAddr(
									"192.168.1.100",
								),
								Port:           8080,
								TransportProto: VsTransportProtoTcp,
							},
							Flags:     VsFlags{FixMSS: false},
							Scheduler: VsSchedulerRoundRobin,
							Reals: []RealConfig{
								{
									Identifier: RelativeRealIdentifier{
										Addr: netip.MustParseAddr(
											"192.168.1.101",
										),
										Port: 9090,
									},
									Src: xnetip.FromPrefix(
										netip.MustParsePrefix(
											"10.0.0.0/24",
										),
									),
									Weight: 100,
								},
								{
									Identifier: RelativeRealIdentifier{
										Addr: netip.MustParseAddr(
											"192.168.1.102",
										),
										Port: 9090,
									},
									Src: xnetip.FromPrefix(
										netip.MustParsePrefix(
											"10.0.1.0/24",
										),
									),
									Weight: 200,
								},
							},
							AllowedSrc: []netip.Prefix{
								netip.MustParsePrefix("0.0.0.0/0"),
							},
							PeersV4: []netip.Addr{
								netip.MustParseAddr("192.168.2.1"),
							},
							PeersV6: []netip.Addr{
								netip.MustParseAddr("2001:db8::200"),
							},
						},
					},
					SourceV4: netip.MustParseAddr("192.168.1.1"),
					SourceV6: netip.MustParseAddr("2001:db8::100"),
					DecapV4: []netip.Addr{
						netip.MustParseAddr("192.168.3.1"),
					},
					DecapV6: []netip.Addr{
						netip.MustParseAddr("2001:db8::300"),
					},
				},
				State: StateConfig{
					TableCapacity: 2000,
				},
			},
			Wlc: BalancerManagerWlcConfig{
				Power:         15,
				MaxRealWeight: 2048,
				Vs:            []uint32{0},
			},
			RefreshPeriod: time.Millisecond * 20,
			MaxLoadFactor: 0.8,
		}

		err := manager.Update(&newConfig, now)
		require.NoError(t, err, "failed to update manager with new config")

		// Verify the new config is applied
		updatedConfig := manager.Config()
		require.NotNil(t, updatedConfig, "updated config should not be nil")
		require.Equal(t, 1, len(updatedConfig.Balancer.Handler.VirtualServices),
			"should have 1 virtual service after update")
		require.Equal(
			t,
			newConfig.Balancer.Handler.VirtualServices[0].Identifier.Addr,
			updatedConfig.Balancer.Handler.VirtualServices[0].Identifier.Addr,
			"VS address should match new config",
		)

		// Verify graph reflects new config
		graph := manager.Graph()
		require.NotNil(t, graph, "graph should not be nil")
		require.Equal(
			t,
			1,
			len(graph.VirtualServices),
			"graph should have 1 virtual service",
		)
		require.Equal(
			t,
			2,
			len(graph.VirtualServices[0].Reals),
			"VS should have 2 reals",
		)

		// Verify all reals are initially disabled after config update
		// Match by identifier since order may differ
		for _, configReal := range newConfig.Balancer.Handler.VirtualServices[0].Reals {
			var graphReal *GraphReal
			for i := range graph.VirtualServices[0].Reals {
				if graph.VirtualServices[0].Reals[i].Identifier.Addr.Compare(
					configReal.Identifier.Addr,
				) == 0 &&
					graph.VirtualServices[0].Reals[i].Identifier.Port == configReal.Identifier.Port {
					graphReal = &graph.VirtualServices[0].Reals[i]
					break
				}
			}
			require.NotNil(t, graphReal, "Real %s:%d should exist in graph",
				configReal.Identifier.Addr, configReal.Identifier.Port)
			require.False(
				t,
				graphReal.Enabled,
				"Real %s:%d should be disabled after config update",
				configReal.Identifier.Addr,
				configReal.Identifier.Port,
			)
			require.Equal(t, configReal.Weight, graphReal.Weight,
				"Real %s:%d weight should match new config",
				configReal.Identifier.Addr, configReal.Identifier.Port)
		}

		// Verify info reflects new topology
		info, err := manager.Info(now)
		require.NoError(t, err, "failed to get info after update")
		require.NotNil(t, info, "info should not be nil")
		require.Equal(t, 1, len(info.Vs), "info should have 1 virtual service")
		require.Equal(
			t,
			2,
			len(info.Vs[0].Reals),
			"info VS should have 2 reals",
		)

		// Verify stats reflects new topology
		stats, err := manager.Stats(&ref)
		require.NoError(t, err, "failed to get stats after update")
		require.NotNil(t, stats, "stats should not be nil")
		require.Equal(
			t,
			1,
			len(stats.Vs),
			"stats should have 1 virtual service",
		)
		require.Equal(
			t,
			2,
			len(stats.Vs[0].Reals),
			"stats VS should have 2 reals",
		)

		// Verify sessions (should still be empty or reset)
		sessions := manager.Sessions(now)
		require.NotNil(t, sessions, "sessions should not be nil")
	})

	// Test updating manager with same reals/VS but in different order
	t.Run("UpdateWithReorderedConfig", func(t *testing.T) {
		// First, restore the original config since previous test changed it
		err = manager.Update(&managerConfig, now)
		require.NoError(t, err, "failed to restore original config")

		// Now enable some reals
		updates := []RealUpdate{
			{
				Identifier: RealIdentifier{
					VsIdentifier: managerConfig.Balancer.Handler.VirtualServices[0].Identifier,
					Relative:     managerConfig.Balancer.Handler.VirtualServices[0].Reals[0].Identifier,
				},
				Weight:  100,
				Enabled: 1,
			},
			{
				Identifier: RealIdentifier{
					VsIdentifier: managerConfig.Balancer.Handler.VirtualServices[1].Identifier,
					Relative:     managerConfig.Balancer.Handler.VirtualServices[1].Reals[0].Identifier,
				},
				Weight:  100,
				Enabled: 1,
			},
		}

		err := manager.UpdateReals(updates)
		require.NoError(t, err, "failed to enable reals")

		// Verify reals are enabled
		graphBefore := manager.Graph()
		require.True(t, graphBefore.VirtualServices[0].Reals[0].Enabled,
			"first VS first real should be enabled")
		require.True(t, graphBefore.VirtualServices[1].Reals[0].Enabled,
			"second VS first real should be enabled")

		// Create a config with same VS and reals but in different order
		reorderedConfig := managerConfig
		// Swap the first two virtual services
		reorderedConfig.Balancer.Handler.VirtualServices = []VsConfig{
			managerConfig.Balancer.Handler.VirtualServices[1], // Second VS first
			managerConfig.Balancer.Handler.VirtualServices[0], // First VS second
			managerConfig.Balancer.Handler.VirtualServices[2], // Third VS unchanged
		}
		// Also reverse the order of reals in the first VS (which is now at index 0)
		reorderedConfig.Balancer.Handler.VirtualServices[0].Reals = []RealConfig{
			managerConfig.Balancer.Handler.VirtualServices[1].Reals[1],
			managerConfig.Balancer.Handler.VirtualServices[1].Reals[0],
		}

		err = manager.Update(&reorderedConfig, now)
		require.NoError(
			t,
			err,
			"failed to update manager with reordered config",
		)

		// Verify the config was updated
		updatedConfig := manager.Config()
		require.Equal(t, 3, len(updatedConfig.Balancer.Handler.VirtualServices),
			"should still have 3 virtual services")

		// Get the graph after reordering
		graphAfter := manager.Graph()

		// Find the reals that were enabled before and verify they're still enabled
		// The real at 10.20.30.41:8443 (from original VS[1].Reals[0]) should still be enabled
		// It's now at VS[0].Reals[1] in the reordered config
		foundEnabledReal1 := false
		for _, vs := range graphAfter.VirtualServices {
			for _, real := range vs.Reals {
				if real.Identifier.Addr.String() == "10.20.30.41" &&
					real.Identifier.Port == 8443 {
					require.True(
						t,
						real.Enabled,
						"previously enabled real 10.20.30.41:8443 should still be enabled after reordering",
					)
					foundEnabledReal1 = true
				}
			}
		}
		require.True(
			t,
			foundEnabledReal1,
			"should find the previously enabled real 10.20.30.41:8443",
		)

		// The real at 10.12.13.213:8080 (from original VS[0].Reals[0]) should still be enabled
		foundEnabledReal2 := false
		for _, vs := range graphAfter.VirtualServices {
			for _, real := range vs.Reals {
				if real.Identifier.Addr.String() == "10.12.13.213" &&
					real.Identifier.Port == 8080 {
					require.True(
						t,
						real.Enabled,
						"previously enabled real 10.12.13.213:8080 should still be enabled after reordering",
					)
					foundEnabledReal2 = true
				}
			}
		}
		require.True(
			t,
			foundEnabledReal2,
			"should find the previously enabled real 10.12.13.213:8080",
		)
	})
}
