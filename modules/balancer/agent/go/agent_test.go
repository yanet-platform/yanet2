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

func TestBalancerAgent(t *testing.T) {
	// Create mock Yanet instance
	m, err := mock.NewYanetMock(&mock.YanetMockConfig{
		AgentsMemory: 1 << 27,
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

	// Create logger for tests
	log := zap.NewNop().Sugar()

	// Create balancer agent
	agent, err := NewBalancerAgent(m.SharedMemory(), 32*datasize.MB, log)
	require.NoError(t, err, "failed to create balancer agent")
	require.NotNil(t, agent, "balancer agent is nil")

	// Verify initial state - no managers
	managers := agent.Managers()
	assert.Empty(t, managers, "expected no managers initially")

	// Define first manager configuration with zero refresh period
	firstManagerConfig := &balancerpb.BalancerConfig{
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
					Flags: &balancerpb.VsFlags{
						FixMss: true,
					},
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
					AllowedSrcs: []*balancerpb.Net{
						{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("192.1.1.1").
									AsSlice(),
							},
							Size: 24,
						},
					},
					Peers: []*balancerpb.Addr{
						{Bytes: netip.MustParseAddr("12.1.1.3").AsSlice()},
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
			},
		},
		State: &balancerpb.StateConfig{
			SessionTableCapacity:      func() *uint64 { v := uint64(1000); return &v }(),
			SessionTableMaxLoadFactor: nil,
			RefreshPeriod:             durationpb.New(0), // Zero refresh period
			Wlc:                       nil,
		},
	}

	t.Run("NewBalancerManager_First", func(t *testing.T) {
		err := agent.NewBalancerManager("balancer0", firstManagerConfig)
		require.NoError(t, err, "failed to create first manager")
	})

	t.Run("BalancerManager_First", func(t *testing.T) {
		manager, err := agent.BalancerManager("balancer0")
		require.NoError(t, err, "failed to get first manager")
		require.NotNil(t, manager, "manager is nil")
		assert.Equal(t, "balancer0", manager.Name())
	})

	t.Run("Managers_One", func(t *testing.T) {
		managers := agent.Managers()
		assert.Len(t, managers, 1, "expected one manager")
		assert.Contains(t, managers, "balancer0")
	})

	// Define second manager configuration with zero refresh period
	secondManagerConfig := &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SessionsTimeouts: &balancerpb.SessionsTimeouts{
				TcpSynAck: 15,
				TcpSyn:    25,
				TcpFin:    20,
				Tcp:       120,
				Udp:       15,
				Default:   25,
			},
			Vs: []*balancerpb.VirtualService{
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("20.20.30.40").AsSlice(),
						},
						Port:  443,
						Proto: balancerpb.TransportProto_TCP,
					},
					Flags: &balancerpb.VsFlags{
						Ops: true,
						Gre: true,
					},
					Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
					Reals: []*balancerpb.Real{
						{
							Id: &balancerpb.RelativeRealIdentifier{
								Ip: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("20.20.30.40").
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
							Weight: 150,
						},
					},
					AllowedSrcs: []*balancerpb.Net{
						{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("192.2.2.0").
									AsSlice(),
							},
							Size: 24,
						},
					},
					Peers: []*balancerpb.Addr{
						{Bytes: netip.MustParseAddr("12.2.2.3").AsSlice()},
					},
				},
			},
			SourceAddressV4: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("20.20.30.40").AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("2001:db8::a").AsSlice(),
			},
			DecapAddresses: []*balancerpb.Addr{
				{Bytes: netip.MustParseAddr("10.15.12.216").AsSlice()},
			},
		},
		State: &balancerpb.StateConfig{
			SessionTableCapacity:      func() *uint64 { v := uint64(2000); return &v }(),
			SessionTableMaxLoadFactor: nil,
			RefreshPeriod:             durationpb.New(0), // Zero refresh period
			Wlc:                       nil,
		},
	}

	t.Run("NewBalancerManager_Second", func(t *testing.T) {
		err := agent.NewBalancerManager("balancer1", secondManagerConfig)
		require.NoError(t, err, "failed to create second manager")
	})

	t.Run("Managers_Two", func(t *testing.T) {
		managers := agent.Managers()
		assert.Len(t, managers, 2, "expected two managers")
		assert.Contains(t, managers, "balancer0")
		assert.Contains(t, managers, "balancer1")
	})

	t.Run("BalancerManager_Both", func(t *testing.T) {
		// Test retrieving each manager
		manager0, err := agent.BalancerManager("balancer0")
		require.NoError(t, err, "failed to get balancer0")
		assert.Equal(t, "balancer0", manager0.Name())

		manager1, err := agent.BalancerManager("balancer1")
		require.NoError(t, err, "failed to get balancer1")
		assert.Equal(t, "balancer1", manager1.Name())
	})

	t.Run("NewBalancerManager_DuplicateName", func(t *testing.T) {
		// Attempt to create manager with existing name
		err := agent.NewBalancerManager("balancer0", firstManagerConfig)
		require.Error(
			t,
			err,
			"expected error when creating manager with duplicate name",
		)
		assert.Contains(
			t,
			err.Error(),
			"already exists",
			"error should mention manager already exists",
		)
	})

	t.Run("BalancerManager_NonExistent", func(t *testing.T) {
		// Attempt to retrieve non-existent manager
		_, err := agent.BalancerManager("nonexistent")
		require.Error(
			t,
			err,
			"expected error when retrieving non-existent manager",
		)
		assert.Contains(
			t,
			err.Error(),
			"not found",
			"error should mention manager not found",
		)
	})

	t.Run("UpdateManager_First", func(t *testing.T) {
		// Update first manager configuration
		newSessionTimeouts := balancerpb.SessionsTimeouts{
			TcpSynAck: 30,  // Changed from 10
			TcpSyn:    40,  // Changed from 20
			TcpFin:    35,  // Changed from 15
			Tcp:       200, // Changed from 100
			Udp:       21,  // Changed from 11
			Default:   39,  // Changed from 19
		}

		update := &balancerpb.BalancerConfig{
			PacketHandler: &balancerpb.PacketHandlerConfig{
				SessionsTimeouts: &newSessionTimeouts,
			},
		}

		manager, err := agent.BalancerManager("balancer0")
		require.NoError(t, err, "failed to get manager")

		// Get config before update for comparison
		configBeforeUpdate := manager.Config()

		err = manager.Update(update, m.CurrentTime())
		require.NoError(t, err, "failed to update manager")

		// Verify update by retrieving manager again
		manager, err = agent.BalancerManager("balancer0")
		require.NoError(t, err, "failed to get manager after update")

		newConfig := manager.Config()

		// Verify only the session timeouts were updated
		assert.Equal(
			t,
			newSessionTimeouts.TcpSynAck,
			newConfig.PacketHandler.SessionsTimeouts.TcpSynAck,
		)
		assert.Equal(
			t,
			newSessionTimeouts.TcpSyn,
			newConfig.PacketHandler.SessionsTimeouts.TcpSyn,
		)
		assert.Equal(
			t,
			newSessionTimeouts.TcpFin,
			newConfig.PacketHandler.SessionsTimeouts.TcpFin,
		)
		assert.Equal(
			t,
			newSessionTimeouts.Tcp,
			newConfig.PacketHandler.SessionsTimeouts.Tcp,
		)
		assert.Equal(
			t,
			newSessionTimeouts.Udp,
			newConfig.PacketHandler.SessionsTimeouts.Udp,
		)
		assert.Equal(
			t,
			newSessionTimeouts.Default,
			newConfig.PacketHandler.SessionsTimeouts.Default,
		)

		// Verify other fields remain unchanged (compare with config before update)
		assert.Equal(
			t,
			configBeforeUpdate.PacketHandler.Vs[0].AllowedSrcs[0].Addr.Bytes,
			newConfig.PacketHandler.Vs[0].AllowedSrcs[0].Addr.Bytes,
		)
		assert.Equal(
			t,
			configBeforeUpdate.State.SessionTableMaxLoadFactor,
			newConfig.State.SessionTableMaxLoadFactor,
		)
	})

	t.Run("UpdateManager_ConsecutiveCalls", func(t *testing.T) {
		// Verify consecutive calls return updated config
		manager1, err := agent.BalancerManager("balancer0")
		require.NoError(t, err, "failed to get manager (first call)")
		config1 := manager1.Config()

		manager2, err := agent.BalancerManager("balancer0")
		require.NoError(t, err, "failed to get manager (second call)")
		config2 := manager2.Config()

		// Both calls should return the same updated values
		assert.Equal(
			t,
			config1.PacketHandler.SessionsTimeouts.TcpSynAck,
			config2.PacketHandler.SessionsTimeouts.TcpSynAck,
		)
		assert.Equal(
			t,
			config1.PacketHandler.SessionsTimeouts.Tcp,
			config2.PacketHandler.SessionsTimeouts.Tcp,
		)
		assert.Equal(
			t,
			uint32(30),
			config2.PacketHandler.SessionsTimeouts.TcpSynAck,
		)
		assert.Equal(t, uint32(200), config2.PacketHandler.SessionsTimeouts.Tcp)
	})

	t.Run("UpdateManager_Second", func(t *testing.T) {
		// Update second manager configuration - update source addresses
		newSourceV4 := &balancerpb.Addr{
			Bytes: netip.MustParseAddr("30.30.40.50").
				AsSlice(),
			// Changed from 20, 20, 30, 40
		}
		newSourceV6 := &balancerpb.Addr{
			Bytes: netip.MustParseAddr("2001:db8::14").
				AsSlice(),
			// Changed last byte from 10 to 20
		}

		update := &balancerpb.BalancerConfig{
			PacketHandler: &balancerpb.PacketHandlerConfig{
				SourceAddressV4: newSourceV4,
				SourceAddressV6: newSourceV6,
			},
		}

		manager, err := agent.BalancerManager("balancer1")
		require.NoError(t, err, "failed to get manager")

		// Get config before update for comparison
		configBeforeUpdate := manager.Config()

		err = manager.Update(update, m.CurrentTime())
		require.NoError(t, err, "failed to update manager")

		// Verify update by retrieving manager again
		manager, err = agent.BalancerManager("balancer1")
		require.NoError(t, err, "failed to get manager after update")

		newConfig := manager.Config()

		// Verify only the source addresses were updated
		assert.Equal(
			t,
			newSourceV4.Bytes,
			newConfig.PacketHandler.SourceAddressV4.Bytes,
		)
		assert.Equal(
			t,
			newSourceV6.Bytes,
			newConfig.PacketHandler.SourceAddressV6.Bytes,
		)

		// Verify other fields remain unchanged (compare with config before update)
		assert.Equal(
			t,
			configBeforeUpdate.PacketHandler.SessionsTimeouts,
			newConfig.PacketHandler.SessionsTimeouts,
		)
		assert.Equal(
			t,
			configBeforeUpdate.PacketHandler.DecapAddresses,
			newConfig.PacketHandler.DecapAddresses,
		)
		assert.Equal(
			t,
			configBeforeUpdate.PacketHandler.Vs[0].AllowedSrcs[0].Addr.Bytes,
			newConfig.PacketHandler.Vs[0].AllowedSrcs[0].Addr.Bytes,
		)
		assert.Equal(
			t,
			configBeforeUpdate.PacketHandler.Vs[0].Id,
			newConfig.PacketHandler.Vs[0].Id,
		)
		assert.Equal(
			t,
			configBeforeUpdate.State.SessionTableMaxLoadFactor,
			newConfig.State.SessionTableMaxLoadFactor,
		)
		assert.Equal(
			t,
			configBeforeUpdate.State.SessionTableCapacity,
			newConfig.State.SessionTableCapacity,
		)
	})
}
