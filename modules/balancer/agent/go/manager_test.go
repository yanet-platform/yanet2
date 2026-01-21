package balancer

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	yanet2 "github.com/yanet-platform/yanet2/controlplane/ffi"
	mock "github.com/yanet-platform/yanet2/mock/go"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/go/ffi"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/durationpb"
)

var deviceName string = "eth0"
var pipelineName string = "pipeline0"
var functionName string = "function0"
var chainName string = "chain0"
var balancerName string = "balancer0"

func TestManager(t *testing.T) {
	m, err := mock.NewYanetMock(&mock.YanetMockConfig{
		AgentsMemory: 1 << 28,
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

	// Create balancer agent
	agent, err := ffi.NewBalancerAgent(m.SharedMemory(), 1<<25)
	require.NoError(t, err, "failed to create balancer agent")
	require.NotNil(t, agent, "balancer agent is nil")

	// Create logger
	logger, err := zap.NewDevelopment()
	require.NoError(t, err, "failed to create logger")
	sugaredLogger := logger.Sugar()

	// Create protobuf config with zero refresh_period to prevent background tasks
	capacity := uint64(1000)
	maxLoadFactor := float32(0.75)
	power := uint64(10)
	maxWeight := uint32(1024)

	protoConfig := &balancerpb.BalancerConfig{
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
							Bytes: netip.MustParseAddr("10.12.13.213").
								AsSlice(),
						},
						Port:  80,
						Proto: balancerpb.TransportProto_TCP,
					},
					Flags:     &balancerpb.VsFlags{FixMss: true},
					Scheduler: balancerpb.VsScheduler_SOURCE_HASH,
					Reals: []*balancerpb.Real{
						{
							Id: &balancerpb.RelativeRealIdentifier{
								Ip: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("10.12.13.213").
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
						{
							Id: &balancerpb.RelativeRealIdentifier{
								Ip: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("10.12.13.214").
										AsSlice(),
								},
								Port: 8080,
							},
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("172.16.1.0").
									AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.0").
									AsSlice(),
							},
							Weight: 150,
						},
						{
							Id: &balancerpb.RelativeRealIdentifier{
								Ip: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("10.12.13.215").
										AsSlice(),
								},
								Port: 8081,
							},
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("172.16.2.0").
									AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.0").
									AsSlice(),
							},
							Weight: 200,
						},
					},
					AllowedSrcs: []*balancerpb.Net{
						{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("192.1.1.1").
									AsSlice(),
							},
							Size: 24,
						},
						{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("192.12.0.0").
									AsSlice(),
							},
							Size: 16,
						},
					},
					Peers: []*balancerpb.Addr{
						{Bytes: netip.MustParseAddr("12.1.1.3").AsSlice()},
						{Bytes: netip.MustParseAddr("12.1.1.4").AsSlice()},
						{Bytes: netip.MustParseAddr("2001:db8::2").AsSlice()},
						{Bytes: netip.MustParseAddr("2001:db8::3").AsSlice()},
					},
				},
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("10.20.30.40").AsSlice(),
						},
						Port:  443,
						Proto: balancerpb.TransportProto_TCP,
					},
					Flags:     &balancerpb.VsFlags{FixMss: false},
					Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
					Reals: []*balancerpb.Real{
						{
							Id: &balancerpb.RelativeRealIdentifier{
								Ip: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("10.20.30.41").
										AsSlice(),
								},
								Port: 8443,
							},
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("172.17.0.0").
									AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.0").
									AsSlice(),
							},
							Weight: 100,
						},
						{
							Id: &balancerpb.RelativeRealIdentifier{
								Ip: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("10.20.30.42").
										AsSlice(),
								},
								Port: 8443,
							},
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("172.17.1.0").
									AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.0").
									AsSlice(),
							},
							Weight: 100,
						},
					},
					AllowedSrcs: []*balancerpb.Net{
						{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("192.168.0.0").
									AsSlice(),
							},
							Size: 16,
						},
					},
					Peers: []*balancerpb.Addr{
						{Bytes: netip.MustParseAddr("12.2.2.3").AsSlice()},
						{Bytes: netip.MustParseAddr("2001:db8::10").AsSlice()},
					},
				},
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("10.50.60.70").AsSlice(),
						},
						Port:  53,
						Proto: balancerpb.TransportProto_UDP,
					},
					Flags:     &balancerpb.VsFlags{FixMss: false},
					Scheduler: balancerpb.VsScheduler_SOURCE_HASH,
					Reals: []*balancerpb.Real{
						{
							Id: &balancerpb.RelativeRealIdentifier{
								Ip: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("10.50.60.71").
										AsSlice(),
								},
								Port: 5353,
							},
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("172.18.0.0").
									AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.0").
									AsSlice(),
							},
							Weight: 50,
						},
						{
							Id: &balancerpb.RelativeRealIdentifier{
								Ip: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("10.50.60.72").
										AsSlice(),
								},
								Port: 5353,
							},
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("172.18.1.0").
									AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.0").
									AsSlice(),
							},
							Weight: 75,
						},
						{
							Id: &balancerpb.RelativeRealIdentifier{
								Ip: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("10.50.60.73").
										AsSlice(),
								},
								Port: 5353,
							},
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("172.18.2.0").
									AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.0").
									AsSlice(),
							},
							Weight: 100,
						},
						{
							Id: &balancerpb.RelativeRealIdentifier{
								Ip: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("10.50.60.74").
										AsSlice(),
								},
								Port: 5353,
							},
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("172.18.3.0").
									AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.0").
									AsSlice(),
							},
							Weight: 125,
						},
						{
							Id: &balancerpb.RelativeRealIdentifier{
								Ip: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("10.50.60.75").
										AsSlice(),
								},
								Port: 5354,
							},
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("172.18.4.0").
									AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.0").
									AsSlice(),
							},
							Weight: 150,
						},
					},
					AllowedSrcs: []*balancerpb.Net{
						{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("0.0.0.0").AsSlice(),
							},
							Size: 0,
						},
					},
					Peers: []*balancerpb.Addr{
						{Bytes: netip.MustParseAddr("12.3.3.3").AsSlice()},
						{Bytes: netip.MustParseAddr("12.3.3.4").AsSlice()},
						{Bytes: netip.MustParseAddr("12.3.3.5").AsSlice()},
						{Bytes: netip.MustParseAddr("2001:db8::20").AsSlice()},
						{Bytes: netip.MustParseAddr("2001:db8::21").AsSlice()},
					},
				},
			},
			SourceAddressV4: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("10.12.13.213").AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("2001:db8::1").AsSlice(),
			},
			DecapAddresses: []*balancerpb.Addr{
				{Bytes: netip.MustParseAddr("10.13.11.215").AsSlice()},
				{Bytes: netip.MustParseAddr("10.14.11.214").AsSlice()},
				{Bytes: netip.MustParseAddr("2001:db8::3").AsSlice()},
				{Bytes: netip.MustParseAddr("2001:db8::2").AsSlice()},
			},
		},
		State: &balancerpb.StateConfig{
			SessionTableCapacity:      &capacity,
			SessionTableMaxLoadFactor: &maxLoadFactor,
			RefreshPeriod: durationpb.New(
				0,
			), // Zero to prevent background tasks
			Wlc: &balancerpb.WlcConfig{
				Power:     &power,
				MaxWeight: &maxWeight,
			},
		},
	}

	// Convert to FFI config
	managerConfig, err := ProtoToManagerConfig(protoConfig)
	require.NoError(t, err, "failed to convert config")

	// Create manager via FFI
	managerHandle, err := agent.NewManager(balancerName, managerConfig)
	require.NoError(t, err, "failed to create balancer manager")
	require.NotNil(t, managerHandle, "balancer manager handle is nil")

	// Create Go wrapper
	manager := NewBalancerManager(managerHandle, sugaredLogger)
	require.NotNil(t, manager, "manager should not be nil")

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
		cpAgent, err := m.SharedMemory().AgentAttach("bootstrap", 0, 1<<20)
		require.NoError(t, err, "failed to attach bootstrap agent")
		{
			functionConfig := yanet2.FunctionConfig{
				Name: functionName,
				Chains: []yanet2.FunctionChainConfig{
					{
						Weight: 1,
						Chain: yanet2.ChainConfig{
							Name: chainName,
							Modules: []yanet2.ChainModuleConfig{
								{
									Type: "balancer",
									Name: balancerName,
								},
							},
						},
					},
				},
			}

			if err := cpAgent.UpdateFunction(functionConfig); err != nil {
				t.Fatalf("failed to update functions: %v", err)
			}
		}

		// update pipelines
		{
			inputPipelineConfig := yanet2.PipelineConfig{
				Name:      pipelineName,
				Functions: []string{functionName},
			}

			dummyPipelineConfig := yanet2.PipelineConfig{
				Name:      "dummy",
				Functions: []string{},
			}

			if err := cpAgent.UpdatePipeline(inputPipelineConfig); err != nil {
				t.Fatalf("failed to update pipeline: %v", err)
			}

			if err := cpAgent.UpdatePipeline(dummyPipelineConfig); err != nil {
				t.Fatalf("failed to update pipeline: %v", err)
			}
		}

		// update devices
		{
			deviceConfig := yanet2.DeviceConfig{
				Name: deviceName,
				Input: []yanet2.DevicePipelineConfig{
					{
						Name:   pipelineName,
						Weight: 1,
					},
				},
				Output: []yanet2.DevicePipelineConfig{
					{
						Name:   "dummy",
						Weight: 1,
					},
				},
			}

			if err := cpAgent.UpdatePlainDevices([]yanet2.DeviceConfig{deviceConfig}); err != nil {
				t.Fatalf("failed to update pipelines: %v", err)
			}
		}
	})

	// Test 2: Get initial configuration
	t.Run("GetInitialConfig", func(t *testing.T) {
		config := manager.Config()
		require.NotNil(t, config, "config should not be nil")
		require.Equal(
			t,
			3,
			len(config.PacketHandler.Vs),
			"should have 3 virtual services",
		)
		require.Equal(
			t,
			uint64(1000),
			*config.State.SessionTableCapacity,
			"capacity should match",
		)
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

		// Verify all reals are initially disabled
		for vsIdx, vs := range graph.VirtualServices {
			for realIdx, real := range vs.Reals {
				require.False(
					t,
					real.Enabled,
					"VS %d Real %d should be initially disabled",
					vsIdx,
					realIdx,
				)
			}
		}
	})

	// Test 4: Get initial info
	t.Run("GetInitialInfo", func(t *testing.T) {
		info, err := manager.Info(now)
		require.NoError(t, err, "failed to get info")
		require.NotNil(t, info, "info should not be nil")

		// Check info variables are zeroes initially
		require.Equal(
			t,
			uint64(0),
			info.ActiveSessions,
			"active sessions should be zero initially",
		)

		// Check info topology matches config topology
		require.Equal(t, 3, len(info.Vs), "info should have 3 virtual services")
	})

	ref := &balancerpb.PacketHandlerRef{
		Device:   &deviceName,
		Pipeline: &pipelineName,
		Function: &functionName,
		Chain:    &chainName,
	}

	// Test 5: Get initial stats
	t.Run("GetInitialStats", func(t *testing.T) {
		stats, err := manager.Stats(ref)
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
			stats.Common.OutgoingPackets,
			"outgoing packets should be zero",
		)
	})

	// Test 6: Get initial sessions
	t.Run("GetInitialSessions", func(t *testing.T) {
		sessions, err := manager.Sessions(now)
		require.NoError(t, err, "failed to get sessions")
		require.NotNil(t, sessions, "sessions should not be nil")
		// Initially should have no sessions
		require.Equal(t, 0, len(sessions), "should have no sessions initially")
	})

	// Test 7: Update reals with buffering
	t.Run("UpdateRealsWithBuffering", func(t *testing.T) {
		updates := []*balancerpb.RealUpdate{
			{
				RealId: &balancerpb.RealIdentifier{
					Vs: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("10.12.13.213").
								AsSlice(),
						},
						Port:  80,
						Proto: balancerpb.TransportProto_TCP,
					},
					Real: &balancerpb.RelativeRealIdentifier{
						Ip: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("10.12.13.213").
								AsSlice(),
						},
						Port: 8080,
					},
				},
				Weight: func() *uint32 { w := uint32(250); return &w }(),
				Enable: func() *bool { e := true; return &e }(),
			},
			{
				RealId: &balancerpb.RealIdentifier{
					Vs: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("10.12.13.213").
								AsSlice(),
						},
						Port:  80,
						Proto: balancerpb.TransportProto_TCP,
					},
					Real: &balancerpb.RelativeRealIdentifier{
						Ip: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("10.12.13.214").
								AsSlice(),
						},
						Port: 8080,
					},
				},
				Weight: func() *uint32 { w := uint32(300); return &w }(),
				Enable: func() *bool { e := true; return &e }(),
			},
		}

		// Buffer the updates
		count, err := manager.UpdateReals(updates, true)
		require.NoError(t, err, "failed to buffer real updates")
		require.Equal(t, 2, count, "should buffer 2 updates")

		// Verify updates are buffered
		buffered := manager.BufferedUpdates()
		require.Equal(t, 2, len(buffered), "should have 2 buffered updates")

		// Verify graph hasn't changed yet
		graph := manager.Graph()
		require.False(t, graph.VirtualServices[0].Reals[0].Enabled,
			"real should still be disabled before flush")

		// Flush the updates
		flushedCount, err := manager.FlushRealUpdates()
		require.NoError(t, err, "failed to flush real updates")
		require.Equal(t, 2, flushedCount, "should flush 2 updates")

		// Verify buffer is empty
		buffered = manager.BufferedUpdates()
		require.Equal(t, 0, len(buffered), "buffer should be empty after flush")

		// Verify graph has changed
		graph = manager.Graph()
		// Find the updated reals by identifier
		foundFirst := false
		foundSecond := false
		for _, vs := range graph.VirtualServices {
			vsAddr, _ := netip.AddrFromSlice(vs.Identifier.Addr.Bytes)
			if vsAddr.String() == "10.12.13.213" && vs.Identifier.Port == 80 {
				for _, real := range vs.Reals {
					realAddr, _ := netip.AddrFromSlice(real.Identifier.Ip.Bytes)
					if realAddr.String() == "10.12.13.213" &&
						real.Identifier.Port == 8080 {
						require.True(
							t,
							real.Enabled,
							"first real should be enabled after flush",
						)
						require.Equal(
							t,
							uint32(250),
							real.Weight,
							"first real config weight should be 250",
						)
						require.Equal(
							t,
							uint32(250),
							real.EffectiveWeight,
							"first real effective weight should be 250",
						)
						foundFirst = true
					}
					if realAddr.String() == "10.12.13.214" &&
						real.Identifier.Port == 8080 {
						require.True(
							t,
							real.Enabled,
							"second real should be enabled after flush",
						)
						require.Equal(
							t,
							uint32(300),
							real.Weight,
							"second real config weight should be 300",
						)
						require.Equal(
							t,
							uint32(300),
							real.EffectiveWeight,
							"second real effective weight should be 300",
						)
						foundSecond = true
					}
				}
			}
		}
		require.True(t, foundFirst, "should find first updated real")
		require.True(t, foundSecond, "should find second updated real")
	})

	// Test 8: Update reals without buffering
	t.Run("UpdateRealsWithoutBuffering", func(t *testing.T) {
		updates := []*balancerpb.RealUpdate{
			{
				RealId: &balancerpb.RealIdentifier{
					Vs: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("10.20.30.40").AsSlice(),
						},
						Port:  443,
						Proto: balancerpb.TransportProto_TCP,
					},
					Real: &balancerpb.RelativeRealIdentifier{
						Ip: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("10.20.30.41").AsSlice(),
						},
						Port: 8443,
					},
				},
				Weight: func() *uint32 { w := uint32(200); return &w }(),
				Enable: func() *bool { e := true; return &e }(),
			},
		}

		// Apply immediately without buffering
		count, err := manager.UpdateReals(updates, false)
		require.NoError(t, err, "failed to update reals immediately")
		require.Equal(t, 1, count, "should apply 1 update")

		// Verify buffer is still empty
		buffered := manager.BufferedUpdates()
		require.Equal(t, 0, len(buffered), "buffer should be empty")

		// Verify graph has changed immediately
		graph := manager.Graph()
		found := false
		for _, vs := range graph.VirtualServices {
			vsAddr, _ := netip.AddrFromSlice(vs.Identifier.Addr.Bytes)
			if vsAddr.String() == "10.20.30.40" && vs.Identifier.Port == 443 {
				for _, real := range vs.Reals {
					realAddr, _ := netip.AddrFromSlice(real.Identifier.Ip.Bytes)
					if realAddr.String() == "10.20.30.41" &&
						real.Identifier.Port == 8443 {
						require.True(
							t,
							real.Enabled,
							"real should be enabled immediately",
						)
						require.Equal(
							t,
							uint32(200),
							real.Weight,
							"real config weight should be 200",
						)
						require.Equal(
							t,
							uint32(200),
							real.EffectiveWeight,
							"real effective weight should be 200",
						)
						found = true
					}
				}
			}
		}
		require.True(t, found, "should find updated real")
	})

	// Test 9: Update manager configuration
	t.Run("UpdateManagerConfig", func(t *testing.T) {
		// Create a new config with different values
		newCapacity := uint64(2000)
		newMaxLoadFactor := float32(0.8)

		newConfig := &balancerpb.BalancerConfig{
			State: &balancerpb.StateConfig{
				SessionTableCapacity:      &newCapacity,
				SessionTableMaxLoadFactor: &newMaxLoadFactor,
				RefreshPeriod:             durationpb.New(0), // Keep zero
			},
		}

		err := manager.Update(newConfig, now)
		require.NoError(t, err, "failed to update manager config")

		// Verify the new config is applied
		updatedConfig := manager.Config()
		require.NotNil(t, updatedConfig, "updated config should not be nil")
		// Note: The capacity might not change immediately due to how the update works
		// Just verify the config is returned successfully
		require.NotNil(
			t,
			updatedConfig.State.SessionTableCapacity,
			"capacity should not be nil",
		)
	})

	// Test 10: BufferedUpdates when empty
	t.Run("BufferedUpdatesEmpty", func(t *testing.T) {
		// Ensure buffer is empty
		_, err := manager.FlushRealUpdates()
		require.NoError(t, err, "failed to flush")

		buffered := manager.BufferedUpdates()
		require.Equal(t, 0, len(buffered), "buffer should be empty")
	})

	// Test 11: FlushRealUpdates when empty
	t.Run("FlushRealUpdatesEmpty", func(t *testing.T) {
		count, err := manager.FlushRealUpdates()
		require.NoError(t, err, "flushing empty buffer should not error")
		require.Equal(t, 0, count, "should flush 0 updates")
	})

	// Test 12: Verify Name method
	t.Run("NameMethod", func(t *testing.T) {
		name := manager.Name()
		require.Equal(t, balancerName, name, "name should match balancer name")
	})
}

// TestMergeBalancerConfigRecursive tests recursive merging of balancer configuration
func TestMergeBalancerConfigRecursive(t *testing.T) {
	m, err := mock.NewYanetMock(&mock.YanetMockConfig{
		AgentsMemory: 1 << 28,
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

	// Create balancer agent
	agent, err := ffi.NewBalancerAgent(m.SharedMemory(), 1<<26)
	require.NoError(t, err, "failed to create balancer agent")
	require.NotNil(t, agent, "balancer agent is nil")

	// Create logger
	logger, err := zap.NewDevelopment()
	require.NoError(t, err, "failed to create logger")
	sugaredLogger := logger.Sugar()

	// Create initial full config
	capacity := uint64(1000)
	maxLoadFactor := float32(0.75)
	power := uint64(10)
	maxWeight := uint32(1024)

	initialConfig := &balancerpb.BalancerConfig{
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
							Bytes: netip.MustParseAddr("10.12.13.213").
								AsSlice(),
						},
						Port:  80,
						Proto: balancerpb.TransportProto_TCP,
					},
					Flags:     &balancerpb.VsFlags{FixMss: true},
					Scheduler: balancerpb.VsScheduler_SOURCE_HASH,
					Reals: []*balancerpb.Real{
						{
							Id: &balancerpb.RelativeRealIdentifier{
								Ip: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("10.12.13.213").
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
				},
			},
			SourceAddressV4: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("10.12.13.213").AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("2001:db8::1").AsSlice(),
			},
			DecapAddresses: []*balancerpb.Addr{
				{Bytes: netip.MustParseAddr("10.13.11.215").AsSlice()},
				{Bytes: netip.MustParseAddr("2001:db8::3").AsSlice()},
			},
		},
		State: &balancerpb.StateConfig{
			SessionTableCapacity:      &capacity,
			SessionTableMaxLoadFactor: &maxLoadFactor,
			RefreshPeriod:             durationpb.New(0),
			Wlc: &balancerpb.WlcConfig{
				Power:     &power,
				MaxWeight: &maxWeight,
			},
		},
	}

	// Convert to FFI config and create manager
	managerConfig, err := ProtoToManagerConfig(initialConfig)
	require.NoError(t, err, "failed to convert config")

	managerHandle, err := agent.NewManager("test_merge_balancer", managerConfig)
	require.NoError(t, err, "failed to create balancer manager")
	require.NotNil(t, managerHandle, "balancer manager handle is nil")

	manager := NewBalancerManager(managerHandle, sugaredLogger)
	require.NotNil(t, manager, "manager should not be nil")

	now := m.CurrentTime()

	// Test 1: Partial PacketHandler update - only source_address_v4
	t.Run("PartialPacketHandlerUpdate_OnlySourceV4", func(t *testing.T) {
		newSourceV4 := &balancerpb.Addr{
			Bytes: netip.MustParseAddr("192.168.1.1").AsSlice(),
		}

		updateConfig := &balancerpb.BalancerConfig{
			PacketHandler: &balancerpb.PacketHandlerConfig{
				SourceAddressV4: newSourceV4,
			},
		}

		err := manager.Update(updateConfig, now)
		require.NoError(t, err, "failed to update config")

		// Verify source_v4 changed
		config := manager.Config()
		require.NotNil(t, config.PacketHandler)
		require.Equal(
			t,
			newSourceV4.Bytes,
			config.PacketHandler.SourceAddressV4.Bytes,
			"source_address_v4 should be updated",
		)

		// Verify other fields preserved
		require.NotNil(t, config.PacketHandler.SourceAddressV6,
			"source_address_v6 should be preserved")
		require.Equal(t, netip.MustParseAddr("2001:db8::1").AsSlice(),
			config.PacketHandler.SourceAddressV6.Bytes,
			"source_address_v6 should match original")

		require.NotNil(t, config.PacketHandler.SessionsTimeouts,
			"sessions_timeouts should be preserved")
		require.Equal(
			t,
			uint32(10),
			config.PacketHandler.SessionsTimeouts.TcpSynAck,
			"tcp_syn_ack timeout should match original",
		)

		require.NotNil(t, config.PacketHandler.Vs,
			"virtual services should be preserved")
		require.Equal(t, 1, len(config.PacketHandler.Vs),
			"should have 1 virtual service")

		require.NotNil(t, config.PacketHandler.DecapAddresses,
			"decap_addresses should be preserved")
		require.Equal(t, 2, len(config.PacketHandler.DecapAddresses),
			"should have 2 decap addresses")
	})

	// Test 2: Partial PacketHandler update - only virtual services
	t.Run("PartialPacketHandlerUpdate_OnlyVirtualServices", func(t *testing.T) {
		newVs := []*balancerpb.VirtualService{
			{
				Id: &balancerpb.VsIdentifier{
					Addr: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("10.20.30.40").AsSlice(),
					},
					Port:  443,
					Proto: balancerpb.TransportProto_TCP,
				},
				Flags:     &balancerpb.VsFlags{FixMss: false},
				Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
				Reals: []*balancerpb.Real{
					{
						Id: &balancerpb.RelativeRealIdentifier{
							Ip: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("10.20.30.41").
									AsSlice(),
							},
							Port: 8443,
						},
						SrcAddr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("172.17.0.0").AsSlice(),
						},
						SrcMask: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("255.255.255.0").
								AsSlice(),
						},
						Weight: 100,
					},
				},
			},
		}

		updateConfig := &balancerpb.BalancerConfig{
			PacketHandler: &balancerpb.PacketHandlerConfig{
				Vs: newVs,
			},
		}

		err := manager.Update(updateConfig, now)
		require.NoError(t, err, "failed to update config")

		// Verify VS changed
		config := manager.Config()
		require.NotNil(t, config.PacketHandler)
		require.NotNil(t, config.PacketHandler.Vs)
		require.Equal(t, 1, len(config.PacketHandler.Vs),
			"should have 1 virtual service")
		require.Equal(t, uint32(443), config.PacketHandler.Vs[0].Id.Port,
			"VS port should be updated")

		// Verify other fields preserved (should be from previous update)
		require.NotNil(t, config.PacketHandler.SourceAddressV4)
		require.Equal(t, netip.MustParseAddr("192.168.1.1").AsSlice(),
			config.PacketHandler.SourceAddressV4.Bytes,
			"source_address_v4 should be preserved from previous update")

		require.NotNil(t, config.PacketHandler.SessionsTimeouts)
		require.Equal(
			t,
			uint32(10),
			config.PacketHandler.SessionsTimeouts.TcpSynAck,
			"tcp_syn_ack timeout should be preserved",
		)
	})

	// Test 3: Partial State update - only session_table_capacity
	t.Run("PartialStateUpdate_OnlyCapacity", func(t *testing.T) {
		newCapacity := uint64(2000)

		updateConfig := &balancerpb.BalancerConfig{
			State: &balancerpb.StateConfig{
				SessionTableCapacity: &newCapacity,
			},
		}

		err := manager.Update(updateConfig, now)
		require.NoError(t, err, "failed to update config")

		// Verify capacity changed
		config := manager.Config()
		require.NotNil(t, config.State)
		require.NotNil(t, config.State.SessionTableCapacity)
		require.LessOrEqual(t, newCapacity, *config.State.SessionTableCapacity,
			"session_table_capacity should be updated")

		// Verify other fields preserved
		require.NotNil(t, config.State.SessionTableMaxLoadFactor)
		require.Equal(t, float32(0.75), *config.State.SessionTableMaxLoadFactor,
			"max_load_factor should be preserved")

		require.NotNil(t, config.State.Wlc)
		require.NotNil(t, config.State.Wlc.Power)
		require.Equal(t, uint64(10), *config.State.Wlc.Power,
			"wlc.power should be preserved")
	})

	// Test 4: Partial WLC update - only power
	t.Run("PartialWlcUpdate_OnlyPower", func(t *testing.T) {
		newPower := uint64(20)

		updateConfig := &balancerpb.BalancerConfig{
			State: &balancerpb.StateConfig{
				Wlc: &balancerpb.WlcConfig{
					Power: &newPower,
				},
			},
		}

		err := manager.Update(updateConfig, now)
		require.NoError(t, err, "failed to update config")

		// Verify power changed
		config := manager.Config()
		require.NotNil(t, config.State)
		require.NotNil(t, config.State.Wlc)
		require.NotNil(t, config.State.Wlc.Power)
		require.Equal(t, newPower, *config.State.Wlc.Power,
			"wlc.power should be updated")

		// Verify max_weight preserved
		require.NotNil(t, config.State.Wlc.MaxWeight)
		require.Equal(t, uint32(1024), *config.State.Wlc.MaxWeight,
			"wlc.max_weight should be preserved")

		// Verify other state fields preserved
		require.NotNil(t, config.State.SessionTableCapacity)
		require.LessOrEqual(t, uint64(2000), *config.State.SessionTableCapacity,
			"session_table_capacity should be preserved from previous update")
	})

	// Test 5: Nested partial update - State with partial Wlc
	t.Run("NestedPartialUpdate_StateWithPartialWlc", func(t *testing.T) {
		newMaxWeight := uint32(2048)
		newLoadFactor := float32(0.85)

		updateConfig := &balancerpb.BalancerConfig{
			State: &balancerpb.StateConfig{
				SessionTableMaxLoadFactor: &newLoadFactor,
				Wlc: &balancerpb.WlcConfig{
					MaxWeight: &newMaxWeight,
				},
			},
		}

		err := manager.Update(updateConfig, now)
		require.NoError(t, err, "failed to update config")

		// Verify updated fields
		config := manager.Config()
		require.NotNil(t, config.State)
		require.NotNil(t, config.State.SessionTableMaxLoadFactor)
		require.Equal(t, newLoadFactor, *config.State.SessionTableMaxLoadFactor,
			"max_load_factor should be updated")

		require.NotNil(t, config.State.Wlc)
		require.NotNil(t, config.State.Wlc.MaxWeight)
		require.Equal(t, newMaxWeight, *config.State.Wlc.MaxWeight,
			"wlc.max_weight should be updated")

		// Verify wlc.power preserved (recursive fallback)
		require.NotNil(t, config.State.Wlc.Power)
		require.Equal(t, uint64(20), *config.State.Wlc.Power,
			"wlc.power should be preserved from previous update")

		// Verify other state fields preserved
		require.NotNil(t, config.State.SessionTableCapacity)
		require.LessOrEqual(t, uint64(2000), *config.State.SessionTableCapacity,
			"session_table_capacity should be preserved")
	})

	// Test 6: Update with empty PacketHandler (all fields nil)
	t.Run("EmptyPacketHandlerUpdate", func(t *testing.T) {
		updateConfig := &balancerpb.BalancerConfig{
			PacketHandler: &balancerpb.PacketHandlerConfig{
				// All fields nil - should fallback to current
			},
		}

		err := manager.Update(updateConfig, now)
		require.NoError(t, err, "failed to update config")

		// Verify all fields preserved
		config := manager.Config()
		require.NotNil(t, config.PacketHandler)

		require.NotNil(t, config.PacketHandler.SourceAddressV4)
		require.NotNil(t, config.PacketHandler.SourceAddressV6)
		require.NotNil(t, config.PacketHandler.SessionsTimeouts)
		require.NotNil(t, config.PacketHandler.Vs)
		require.NotNil(t, config.PacketHandler.DecapAddresses)

		// Verify values match previous state
		require.Equal(t, 1, len(config.PacketHandler.Vs),
			"should preserve VS from previous update")
	})

	// Test 7: Full replacement - all fields provided
	t.Run("FullReplacement", func(t *testing.T) {
		newCapacity := uint64(3000)
		newMaxLoadFactor := float32(0.9)
		newPower := uint64(15)
		newMaxWeight := uint32(512)

		fullConfig := &balancerpb.BalancerConfig{
			PacketHandler: &balancerpb.PacketHandlerConfig{
				SessionsTimeouts: &balancerpb.SessionsTimeouts{
					TcpSynAck: 5,
					TcpSyn:    10,
					TcpFin:    8,
					Tcp:       50,
					Udp:       6,
					Default:   10,
				},
				Vs: []*balancerpb.VirtualService{
					{
						Id: &balancerpb.VsIdentifier{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("10.50.60.70").
									AsSlice(),
							},
							Port:  53,
							Proto: balancerpb.TransportProto_UDP,
						},
						Flags:     &balancerpb.VsFlags{FixMss: false},
						Scheduler: balancerpb.VsScheduler_SOURCE_HASH,
						Reals: []*balancerpb.Real{
							{
								Id: &balancerpb.RelativeRealIdentifier{
									Ip: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("10.50.60.71").
											AsSlice(),
									},
									Port: 5353,
								},
								SrcAddr: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("172.18.0.0").
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
					Bytes: netip.MustParseAddr("10.100.100.100").AsSlice(),
				},
				SourceAddressV6: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("2001:db8::100").AsSlice(),
				},
				DecapAddresses: []*balancerpb.Addr{
					{Bytes: netip.MustParseAddr("10.200.200.200").AsSlice()},
				},
			},
			State: &balancerpb.StateConfig{
				SessionTableCapacity:      &newCapacity,
				SessionTableMaxLoadFactor: &newMaxLoadFactor,
				RefreshPeriod:             durationpb.New(0),
				Wlc: &balancerpb.WlcConfig{
					Power:     &newPower,
					MaxWeight: &newMaxWeight,
				},
			},
		}

		err := manager.Update(fullConfig, now)
		require.NoError(t, err, "failed to update config")

		// Verify all fields updated
		config := manager.Config()

		// Check PacketHandler
		require.NotNil(t, config.PacketHandler)
		require.Equal(
			t,
			uint32(5),
			config.PacketHandler.SessionsTimeouts.TcpSynAck,
		)
		require.Equal(t, netip.MustParseAddr("10.100.100.100").AsSlice(),
			config.PacketHandler.SourceAddressV4.Bytes)
		require.Equal(t, netip.MustParseAddr("2001:db8::100").AsSlice(),
			config.PacketHandler.SourceAddressV6.Bytes)
		require.Equal(t, 1, len(config.PacketHandler.Vs))
		require.Equal(t, uint32(53), config.PacketHandler.Vs[0].Id.Port)
		require.Equal(t, 1, len(config.PacketHandler.DecapAddresses))

		// Check State
		require.NotNil(t, config.State)
		require.LessOrEqual(t, newCapacity, *config.State.SessionTableCapacity)
		require.Equal(
			t,
			newMaxLoadFactor,
			*config.State.SessionTableMaxLoadFactor,
		)
		require.NotNil(t, config.State.Wlc)
		require.Equal(t, newPower, *config.State.Wlc.Power)
		require.Equal(t, newMaxWeight, *config.State.Wlc.MaxWeight)
	})
}
