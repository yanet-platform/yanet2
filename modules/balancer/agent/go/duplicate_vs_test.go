package balancer

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	mock "github.com/yanet-platform/yanet2/mock/go"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Helper function to check if a string contains any of the given substrings
func containsAny(s string, substrs ...string) bool {
	for _, substr := range substrs {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

// Helper function to create a basic VS config
func createVsConfig(
	addr string,
	port uint32,
	proto balancerpb.TransportProto,
) *balancerpb.VirtualService {
	return &balancerpb.VirtualService{
		Id: &balancerpb.VsIdentifier{
			Addr: &balancerpb.Addr{
				Bytes: netip.MustParseAddr(addr).AsSlice(),
			},
			Port:  port,
			Proto: proto,
		},
		Flags: &balancerpb.VsFlags{
			FixMss: true,
		},
		Scheduler: balancerpb.VsScheduler_SOURCE_HASH,
		Reals: []*balancerpb.Real{
			{
				Id: &balancerpb.RelativeRealIdentifier{
					Ip: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
					},
					Port: 8080,
				},
				SrcAddr: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("172.16.0.0").AsSlice(),
				},
				SrcMask: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("255.255.255.0").AsSlice(),
				},
				Weight: 100,
			},
		},
	}
}

// Helper function to create a base balancer config
func createBaseConfig(
	vs []*balancerpb.VirtualService,
) *balancerpb.BalancerConfig {
	return &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SessionsTimeouts: &balancerpb.SessionsTimeouts{
				TcpSynAck: 10,
				TcpSyn:    20,
				TcpFin:    15,
				Tcp:       100,
				Udp:       11,
				Default:   19,
			},
			Vs: vs,
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
			SessionTableMaxLoadFactor: func() *float32 { v := float32(0.75); return &v }(),
			RefreshPeriod:             durationpb.New(0),
			Wlc: &balancerpb.WlcConfig{
				Power:     func() *uint64 { v := uint64(2); return &v }(),
				MaxWeight: func() *uint32 { v := uint32(1000); return &v }(),
			},
		},
	}
}

func TestDuplicateVsRejected(t *testing.T) {
	// Create mock Yanet instance
	m, err := mock.NewYanetMock(&mock.YanetMockConfig{
		AgentsMemory: 512 * datasize.MB,
		DpMemory:     16 * datasize.MB,
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
	agent, err := NewBalancerAgent(m.SharedMemory(), 256*datasize.MB, log)
	require.NoError(t, err, "failed to create balancer agent")
	require.NotNil(t, agent, "balancer agent is nil")

	t.Run("DuplicateIPv4Vs_SamePortAndProto", func(t *testing.T) {
		// Create config with two VS having same IPv4 addr:port:proto
		vs := []*balancerpb.VirtualService{
			createVsConfig("192.168.1.100", 80, balancerpb.TransportProto_TCP),
			createVsConfig(
				"192.168.1.100",
				80,
				balancerpb.TransportProto_TCP,
			), // Duplicate
		}
		config := createBaseConfig(vs)

		err := agent.NewBalancerManager("test_ipv4_dup", config)
		require.Error(t, err, "expected error for duplicate IPv4 VS")
		errMsg := err.Error()
		assert.True(t,
			containsAny(errMsg, "duplicate", "match"),
			"error should mention 'duplicate' or 'match', got: %s", errMsg,
		)
	})

	t.Run("DuplicateIPv6Vs_SamePortAndProto", func(t *testing.T) {
		// Create config with two VS having same IPv6 addr:port:proto
		vs := []*balancerpb.VirtualService{
			createVsConfig("2001:db8::100", 443, balancerpb.TransportProto_TCP),
			createVsConfig(
				"2001:db8::100",
				443,
				balancerpb.TransportProto_TCP,
			), // Duplicate
		}
		config := createBaseConfig(vs)

		err := agent.NewBalancerManager("test_ipv6_dup", config)
		require.Error(t, err, "expected error for duplicate IPv6 VS")
		errMsg := err.Error()
		assert.True(t,
			containsAny(errMsg, "duplicate", "match"),
			"error should mention 'duplicate' or 'match', got: %s", errMsg)
	})

	t.Run("SameIP_DifferentPort_Allowed", func(t *testing.T) {
		// Same IP but different port should be allowed
		vs := []*balancerpb.VirtualService{
			createVsConfig("192.168.1.100", 80, balancerpb.TransportProto_TCP),
			createVsConfig(
				"192.168.1.100",
				443,
				balancerpb.TransportProto_TCP,
			), // Different port
		}
		config := createBaseConfig(vs)

		err := agent.NewBalancerManager("test_diff_port", config)
		require.NoError(t, err, "same IP with different port should be allowed")
	})

	t.Run("SameIP_SamePort_DifferentProto_Allowed", func(t *testing.T) {
		// Same IP:port but different protocol should be allowed
		vs := []*balancerpb.VirtualService{
			createVsConfig("192.168.1.100", 80, balancerpb.TransportProto_TCP),
			createVsConfig(
				"192.168.1.100",
				80,
				balancerpb.TransportProto_UDP,
			), // Different proto
		}
		config := createBaseConfig(vs)

		err := agent.NewBalancerManager("test_diff_proto", config)
		require.NoError(
			t,
			err,
			"same IP:port with different protocol should be allowed",
		)
	})

	t.Run("LargeConfig_FewIPv4Duplicates", func(t *testing.T) {
		// Create large config with many unique VS and a few duplicates
		vs := []*balancerpb.VirtualService{}

		// Add 50 unique IPv4 VS
		for i := 0; i < 50; i++ {
			addr := fmt.Sprintf("10.0.%d.%d", i/256, i%256)
			vs = append(
				vs,
				createVsConfig(addr, 80, balancerpb.TransportProto_TCP),
			)
		}

		// Add a duplicate at position 25
		vs = append(
			vs,
			createVsConfig("10.0.0.25", 80, balancerpb.TransportProto_TCP),
		)

		config := createBaseConfig(vs)

		err := agent.NewBalancerManager("test_large_ipv4_dup", config)
		require.Error(t, err, "expected error for duplicate in large config")
		errMsg := err.Error()
		assert.True(t,
			containsAny(errMsg, "duplicate", "match"),
			"error should mention 'duplicate' or 'match', got: %s", errMsg)
	})

	t.Run("LargeConfig_FewIPv6Duplicates", func(t *testing.T) {
		// Create large config with many unique VS and a few duplicates
		vs := []*balancerpb.VirtualService{}

		// Add 50 unique IPv6 VS
		for i := 0; i < 50; i++ {
			addr := fmt.Sprintf("2001:db8::%x", i)
			vs = append(
				vs,
				createVsConfig(addr, 443, balancerpb.TransportProto_TCP),
			)
		}

		// Add a duplicate at position 30
		vs = append(
			vs,
			createVsConfig("2001:db8::1e", 443, balancerpb.TransportProto_TCP),
		)

		config := createBaseConfig(vs)

		err := agent.NewBalancerManager("test_large_ipv6_dup", config)
		require.Error(t, err, "expected error for duplicate in large config")
		errMsg := err.Error()
		assert.True(t,
			containsAny(errMsg, "duplicate", "match"),
			"error should mention 'duplicate' or 'match', got: %s", errMsg)
	})

	t.Run("ManyIPv4_NoDuplicates_WithIPv6Duplicates", func(t *testing.T) {
		// Many unique IPv4 VS but with IPv6 duplicates
		vs := []*balancerpb.VirtualService{}

		// Add 30 unique IPv4 VS
		for i := 0; i < 30; i++ {
			addr := fmt.Sprintf("10.1.%d.%d", i/256, i%256)
			vs = append(
				vs,
				createVsConfig(addr, 80, balancerpb.TransportProto_TCP),
			)
		}

		// Add IPv6 VS with duplicates
		vs = append(
			vs,
			createVsConfig("2001:db8::a", 443, balancerpb.TransportProto_TCP),
		)
		vs = append(
			vs,
			createVsConfig("2001:db8::b", 443, balancerpb.TransportProto_TCP),
		)
		vs = append(
			vs,
			createVsConfig("2001:db8::a", 443, balancerpb.TransportProto_TCP),
		) // Duplicate

		config := createBaseConfig(vs)

		err := agent.NewBalancerManager("test_ipv4_ok_ipv6_dup", config)
		require.Error(
			t,
			err,
			"expected error for IPv6 duplicate despite unique IPv4",
		)
		errMsg := err.Error()
		assert.True(t,
			containsAny(errMsg, "duplicate", "match"),
			"error should mention 'duplicate' or 'match', got: %s", errMsg)
	})

	t.Run("ManyIPv6_NoDuplicates_WithIPv4Duplicates", func(t *testing.T) {
		// Many unique IPv6 VS but with IPv4 duplicates
		vs := []*balancerpb.VirtualService{}

		// Add 30 unique IPv6 VS
		for i := range 30 {
			addr := fmt.Sprintf("2001:db8::%x", i+100)
			vs = append(
				vs,
				createVsConfig(addr, 443, balancerpb.TransportProto_TCP),
			)
		}

		// Add IPv4 VS with duplicates
		vs = append(
			vs,
			createVsConfig("192.168.10.1", 80, balancerpb.TransportProto_TCP),
		)
		vs = append(
			vs,
			createVsConfig("192.168.10.2", 80, balancerpb.TransportProto_TCP),
		)
		vs = append(
			vs,
			createVsConfig("192.168.10.1", 80, balancerpb.TransportProto_TCP),
		) // Duplicate

		config := createBaseConfig(vs)

		err := agent.NewBalancerManager("test_ipv6_ok_ipv4_dup", config)
		require.Error(
			t,
			err,
			"expected error for IPv4 duplicate despite unique IPv6",
		)
		errMsg := err.Error()
		assert.True(t,
			containsAny(errMsg, "duplicate", "match"),
			"error should mention 'duplicate' or 'match', got: %s", errMsg)
	})

	t.Run("NoDuplicates_OnCreate_DuplicateOnUpdate", func(t *testing.T) {
		// Create manager with unique VS
		vs := []*balancerpb.VirtualService{
			createVsConfig("192.168.2.1", 80, balancerpb.TransportProto_TCP),
			createVsConfig("192.168.2.2", 80, balancerpb.TransportProto_TCP),
		}
		config := createBaseConfig(vs)

		err := agent.NewBalancerManager("test_update_dup", config)
		require.NoError(t, err, "initial config should be valid")

		// Get the manager
		manager, err := agent.BalancerManager("test_update_dup")
		require.NoError(t, err, "failed to get manager")

		// Try to update with duplicate VS
		vsUpdate := []*balancerpb.VirtualService{
			createVsConfig("192.168.2.1", 80, balancerpb.TransportProto_TCP),
			createVsConfig("192.168.2.2", 80, balancerpb.TransportProto_TCP),
			createVsConfig(
				"192.168.2.1",
				80,
				balancerpb.TransportProto_TCP,
			), // Duplicate
		}
		updateConfig := &balancerpb.BalancerConfig{
			PacketHandler: &balancerpb.PacketHandlerConfig{
				Vs: vsUpdate,
			},
		}

		_, err = manager.Update(updateConfig, m.CurrentTime())
		require.Error(t, err, "expected error when updating with duplicate VS")
		errMsg := err.Error()
		assert.True(t,
			containsAny(errMsg, "duplicate", "match"),
			"error should mention 'duplicate' or 'match', got: %s", errMsg)
	})

	t.Run("MultipleDuplicates_InSameConfig", func(t *testing.T) {
		// Config with multiple different duplicates
		vs := []*balancerpb.VirtualService{
			createVsConfig("192.168.3.1", 80, balancerpb.TransportProto_TCP),
			createVsConfig(
				"192.168.3.1",
				80,
				balancerpb.TransportProto_TCP,
			), // Duplicate 1
			createVsConfig("192.168.3.2", 443, balancerpb.TransportProto_TCP),
			createVsConfig(
				"192.168.3.2",
				443,
				balancerpb.TransportProto_TCP,
			), // Duplicate 2
		}
		config := createBaseConfig(vs)

		err := agent.NewBalancerManager("test_multi_dup", config)
		require.Error(t, err, "expected error for multiple duplicates")
		errMsg := err.Error()
		assert.True(t,
			containsAny(errMsg, "duplicate", "match"),
			"error should mention 'duplicate' or 'match', got: %s", errMsg)
	})

	t.Run("TripleDuplicate_SameVS", func(t *testing.T) {
		// Three instances of the same VS
		vs := []*balancerpb.VirtualService{
			createVsConfig("192.168.4.1", 80, balancerpb.TransportProto_TCP),
			createVsConfig(
				"192.168.4.1",
				80,
				balancerpb.TransportProto_TCP,
			), // Duplicate 1
			createVsConfig(
				"192.168.4.1",
				80,
				balancerpb.TransportProto_TCP,
			), // Duplicate 2
		}
		config := createBaseConfig(vs)

		err := agent.NewBalancerManager("test_triple_dup", config)
		require.Error(t, err, "expected error for triple duplicate")
		errMsg := err.Error()
		assert.True(t,
			containsAny(errMsg, "duplicate", "match"),
			"error should mention 'duplicate' or 'match', got: %s", errMsg)
	})
}
