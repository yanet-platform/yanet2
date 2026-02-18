package balancer_test

import (
	"fmt"
	"math/rand"
	"net/netip"
	"testing"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/tests/go/utils"
	"google.golang.org/protobuf/types/known/durationpb"
)

// TestVSFilterRecompilation tests that VS filters (vs_v4 and vs_v6) are not
// recompiled when the set of virtual service identifiers matches the previous
// configuration, even if the order is different.
//
// The test uses 20-30 virtual services to ensure measurable timing differences
// between filter recompilation and filter reuse.
func TestVSFilterRecompilation(t *testing.T) {
	const (
		numIPv4VS     = 20 // Number of IPv4 virtual services
		numIPv6VS     = 20 // Number of IPv6 virtual services
		numRealsPerVS = 2  // Reals per VS
	)

	// Create initial configuration with 20 IPv4 and 20 IPv6 virtual services
	initialConfig := createLargeBalancerConfig(numIPv4VS, numIPv6VS, numRealsPerVS)

	// Setup test
	ts, err := utils.Make(&utils.TestConfig{
		Mock:     utils.SingleWorkerMockConfig(1024*datasize.MB, 4*datasize.MB),
		Balancer: initialConfig,
		AgentMemory: func() *datasize.ByteSize {
			memory := 512 * datasize.MB
			return &memory
		}(),
	})
	require.NoError(t, err)
	defer ts.Free()

	// Establish baseline: measure time for full recompilation (both filters)
	var baselineTime time.Duration
	t.Run("Baseline_BothFiltersRecompile", func(t *testing.T) {
		// Create config with completely different VS identifiers
		newConfig := createLargeBalancerConfig(numIPv4VS, numIPv6VS, numRealsPerVS)
		// Change all IPv4 addresses
		for i := range newConfig.PacketHandler.Vs {
			vs := newConfig.PacketHandler.Vs[i]
			addr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
			if addr.Is4() {
				// Change to 10.100.x.1 range
				newAddr := netip.MustParseAddr(fmt.Sprintf("10.100.%d.1", i+1))
				vs.Id.Addr.Bytes = newAddr.AsSlice()
			} else {
				// Change to fd00:100::x range
				newAddr := netip.MustParseAddr(fmt.Sprintf("fd00:100::%x", i+1))
				vs.Id.Addr.Bytes = newAddr.AsSlice()
			}
		}

		start := time.Now()
		err := ts.Balancer.Update(newConfig, ts.Mock.CurrentTime())
		baselineTime = time.Since(start)

		require.NoError(t, err)
		t.Logf("Baseline (both filters recompile): %.2f ms", baselineTime.Seconds()*1000)
	})

	// Test Case 1: Same IPv4 VS set, different order - IPv4 filter should NOT recompile
	t.Run("SameIPv4Set_DifferentOrder", func(t *testing.T) {
		config := ts.Balancer.Config()

		// Shuffle only IPv4 virtual services
		shuffledConfig := shuffleVSByProtocol(config, true, false)

		start := time.Now()
		err := ts.Balancer.Update(shuffledConfig, ts.Mock.CurrentTime())
		updateTime := time.Since(start)

		require.NoError(t, err)

		percentage := (updateTime.Seconds() / baselineTime.Seconds()) * 100
		t.Logf("Same IPv4 set (shuffled): %.2f ms (%.1f%% of baseline)",
			updateTime.Seconds()*1000, percentage)

		// Should be significantly faster than baseline (< 60% since only IPv6 might need work)
		assert.Less(t, percentage, 60.0,
			"IPv4 filter should not recompile when VS set matches")
	})

	// Test Case 2: Same IPv6 VS set, different order - IPv6 filter should NOT recompile
	t.Run("SameIPv6Set_DifferentOrder", func(t *testing.T) {
		config := ts.Balancer.Config()

		// Shuffle only IPv6 virtual services
		shuffledConfig := shuffleVSByProtocol(config, false, true)

		start := time.Now()
		err := ts.Balancer.Update(shuffledConfig, ts.Mock.CurrentTime())
		updateTime := time.Since(start)

		require.NoError(t, err)

		percentage := (updateTime.Seconds() / baselineTime.Seconds()) * 100
		t.Logf("Same IPv6 set (shuffled): %.2f ms (%.1f%% of baseline)",
			updateTime.Seconds()*1000, percentage)

		// Should be significantly faster than baseline
		assert.Less(t, percentage, 60.0,
			"IPv6 filter should not recompile when VS set matches")
	})

	// Test Case 3: Both sets same, different order - neither filter should recompile
	t.Run("BothSets_DifferentOrder", func(t *testing.T) {
		config := ts.Balancer.Config()

		// Shuffle both IPv4 and IPv6 virtual services
		shuffledConfig := shuffleVSByProtocol(config, true, true)

		start := time.Now()
		err := ts.Balancer.Update(shuffledConfig, ts.Mock.CurrentTime())
		updateTime := time.Since(start)

		require.NoError(t, err)

		percentage := (updateTime.Seconds() / baselineTime.Seconds()) * 100
		t.Logf("Both sets same (shuffled): %.2f ms (%.1f%% of baseline)",
			updateTime.Seconds()*1000, percentage)

		// Should be much faster than baseline (< 30% since neither filter recompiles)
		assert.Less(t, percentage, 30.0,
			"Neither filter should recompile when both VS sets match")
	})

	// Test Case 4: IPv4 set matches, IPv6 set changes - only IPv6 should recompile
	t.Run("IPv4Matches_IPv6Changes", func(t *testing.T) {
		config := ts.Balancer.Config()

		// Keep IPv4 same (shuffled), change IPv6
		modifiedConfig := shuffleVSByProtocol(config, true, false)

		// Change all IPv6 addresses
		for i := range modifiedConfig.PacketHandler.Vs {
			vs := modifiedConfig.PacketHandler.Vs[i]
			addr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
			if addr.Is6() {
				newAddr := netip.MustParseAddr(fmt.Sprintf("fd00:200::%x", i+1))
				vs.Id.Addr.Bytes = newAddr.AsSlice()
			}
		}

		start := time.Now()
		err := ts.Balancer.Update(modifiedConfig, ts.Mock.CurrentTime())
		updateTime := time.Since(start)

		require.NoError(t, err)

		percentage := (updateTime.Seconds() / baselineTime.Seconds()) * 100
		t.Logf("IPv4 matches, IPv6 changes: %.2f ms (%.1f%% of baseline)",
			updateTime.Seconds()*1000, percentage)

		// Should be around 50-70% of baseline (only IPv6 filter recompiles)
		assert.Greater(t, percentage, 40.0,
			"Should take time to recompile IPv6 filter")
		assert.Less(t, percentage, 80.0,
			"Should be faster than full recompilation since IPv4 filter is reused")
	})

	// Test Case 5: IPv6 set matches, IPv4 set changes - only IPv4 should recompile
	t.Run("IPv6Matches_IPv4Changes", func(t *testing.T) {
		config := ts.Balancer.Config()

		// Keep IPv6 same (shuffled), change IPv4
		modifiedConfig := shuffleVSByProtocol(config, false, true)

		// Change all IPv4 addresses
		for i := range modifiedConfig.PacketHandler.Vs {
			vs := modifiedConfig.PacketHandler.Vs[i]
			addr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
			if addr.Is4() {
				newAddr := netip.MustParseAddr(fmt.Sprintf("10.200.%d.1", i+1))
				vs.Id.Addr.Bytes = newAddr.AsSlice()
			}
		}

		start := time.Now()
		err := ts.Balancer.Update(modifiedConfig, ts.Mock.CurrentTime())
		updateTime := time.Since(start)

		require.NoError(t, err)

		percentage := (updateTime.Seconds() / baselineTime.Seconds()) * 100
		t.Logf("IPv6 matches, IPv4 changes: %.2f ms (%.1f%% of baseline)",
			updateTime.Seconds()*1000, percentage)

		// Should be around 50-70% of baseline (only IPv4 filter recompiles)
		assert.Greater(t, percentage, 40.0,
			"Should take time to recompile IPv4 filter")
		assert.Less(t, percentage, 80.0,
			"Should be faster than full recompilation since IPv6 filter is reused")
	})

	// Test Case 6: Add VS to existing set - filter should recompile
	t.Run("AddVS_FilterRecompiles", func(t *testing.T) {
		config := ts.Balancer.Config()

		// Add one more IPv4 VS
		newVS := createVirtualServiceSimple(
			netip.MustParseAddr("10.0.99.1"),
			80,
			balancerpb.TransportProto_TCP,
			balancerpb.VsScheduler_ROUND_ROBIN,
			false,
			createReals([]netip.Addr{
				netip.MustParseAddr("192.168.99.1"),
				netip.MustParseAddr("192.168.99.2"),
			}),
		)

		modifiedConfig := cloneBalancerConfig(config)
		modifiedConfig.PacketHandler.Vs = append(modifiedConfig.PacketHandler.Vs, newVS)

		start := time.Now()
		err := ts.Balancer.Update(modifiedConfig, ts.Mock.CurrentTime())
		updateTime := time.Since(start)

		require.NoError(t, err)

		percentage := (updateTime.Seconds() / baselineTime.Seconds()) * 100
		t.Logf("Add VS (count changes): %.2f ms (%.1f%% of baseline)",
			updateTime.Seconds()*1000, percentage)

		// Should recompile (around 50% since only IPv4 filter affected)
		assert.Greater(t, percentage, 40.0,
			"Should recompile filter when VS count changes")
	})

	// Test Case 7: Remove VS from existing set - filter should recompile
	t.Run("RemoveVS_FilterRecompiles", func(t *testing.T) {
		config := ts.Balancer.Config()

		// Remove last IPv4 VS
		modifiedConfig := cloneBalancerConfig(config)
		var newVS []*balancerpb.VirtualService
		for _, vs := range modifiedConfig.PacketHandler.Vs {
			addr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
			// Keep all IPv6 and all but last IPv4
			if addr.Is6() {
				newVS = append(newVS, vs)
			} else if addr.Is4() {
				// Skip the VS we added in previous test (10.0.99.1)
				if addr.String() != "10.0.99.1" {
					newVS = append(newVS, vs)
				}
			}
		}
		modifiedConfig.PacketHandler.Vs = newVS

		start := time.Now()
		err := ts.Balancer.Update(modifiedConfig, ts.Mock.CurrentTime())
		updateTime := time.Since(start)

		require.NoError(t, err)

		percentage := (updateTime.Seconds() / baselineTime.Seconds()) * 100
		t.Logf("Remove VS (count changes): %.2f ms (%.1f%% of baseline)",
			updateTime.Seconds()*1000, percentage)

		// Should recompile (around 50% since only IPv4 filter affected)
		assert.Greater(t, percentage, 40.0,
			"Should recompile filter when VS count changes")
	})
}

// createLargeBalancerConfig creates a balancer configuration with specified
// number of IPv4 and IPv6 virtual services
func createLargeBalancerConfig(
	numIPv4 int,
	numIPv6 int,
	realsPerVS int,
) *balancerpb.BalancerConfig {
	var virtualServices []*balancerpb.VirtualService

	// Create IPv4 virtual services (10.0.1.1 through 10.0.N.1)
	for i := 1; i <= numIPv4; i++ {
		vsIP := netip.MustParseAddr(fmt.Sprintf("10.0.%d.1", i))
		reals := make([]netip.Addr, realsPerVS)
		for j := 0; j < realsPerVS; j++ {
			reals[j] = netip.MustParseAddr(fmt.Sprintf("192.168.%d.%d", i, j+1))
		}
		vs := createVirtualServiceSimple(
			vsIP,
			80,
			balancerpb.TransportProto_TCP,
			balancerpb.VsScheduler_ROUND_ROBIN,
			false,
			createReals(reals),
		)
		virtualServices = append(virtualServices, vs)
	}

	// Create IPv6 virtual services (fd00::1 through fd00::N)
	for i := 1; i <= numIPv6; i++ {
		vsIP := netip.MustParseAddr(fmt.Sprintf("fd00::%x", i))
		reals := make([]netip.Addr, realsPerVS)
		for j := 0; j < realsPerVS; j++ {
			reals[j] = netip.MustParseAddr(fmt.Sprintf("fd00:100:%d::%d", i, j+1))
		}
		vs := createVirtualServiceSimple(
			vsIP,
			80,
			balancerpb.TransportProto_TCP,
			balancerpb.VsScheduler_ROUND_ROBIN,
			false,
			createReals(reals),
		)
		virtualServices = append(virtualServices, vs)
	}

	return &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("5.5.5.5").AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("fe80::1").AsSlice(),
			},
			Vs:             virtualServices,
			DecapAddresses: []*balancerpb.Addr{},
			SessionsTimeouts: &balancerpb.SessionsTimeouts{
				TcpSynAck: 600,
				TcpSyn:    600,
				TcpFin:    600,
				Tcp:       600,
				Udp:       600,
				Default:   600,
			},
		},
		State: &balancerpb.StateConfig{
			SessionTableCapacity:      func() *uint64 { v := uint64(1000); return &v }(),
			SessionTableMaxLoadFactor: func() *float32 { v := float32(0.8); return &v }(),
			RefreshPeriod:             durationpb.New(0),
			Wlc: &balancerpb.WlcConfig{
				Power:     func() *uint64 { v := uint64(10); return &v }(),
				MaxWeight: func() *uint32 { v := uint32(1000); return &v }(),
			},
		},
	}
}

// createVirtualServiceSimple creates a VirtualService without ACL rules
func createVirtualServiceSimple(
	ip netip.Addr,
	port uint16,
	proto balancerpb.TransportProto,
	scheduler balancerpb.VsScheduler,
	ops bool,
	reals []*balancerpb.Real,
) *balancerpb.VirtualService {
	return &balancerpb.VirtualService{
		Id: &balancerpb.VsIdentifier{
			Addr:  &balancerpb.Addr{Bytes: ip.AsSlice()},
			Port:  uint32(port),
			Proto: proto,
		},
		Scheduler:   scheduler,
		AllowedSrcs: nil, // No ACL rules
		Flags: &balancerpb.VsFlags{
			Gre:    false,
			FixMss: false,
			Ops:    ops,
			PureL3: false,
			Wlc:    false,
		},
		Reals: reals,
		Peers: []*balancerpb.Addr{},
	}
}

// createReals creates real server configurations
func createReals(ips []netip.Addr) []*balancerpb.Real {
	reals := make([]*balancerpb.Real, len(ips))
	for i, ip := range ips {
		reals[i] = &balancerpb.Real{
			Id: &balancerpb.RelativeRealIdentifier{
				Ip:   &balancerpb.Addr{Bytes: ip.AsSlice()},
				Port: 0,
			},
			Weight: 1,
			SrcAddr: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("4.4.4.4").AsSlice(),
			},
			SrcMask: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("255.255.255.255").AsSlice(),
			},
		}
	}
	return reals
}

// shuffleVSByProtocol shuffles virtual services by protocol
func shuffleVSByProtocol(
	config *balancerpb.BalancerConfig,
	shuffleIPv4 bool,
	shuffleIPv6 bool,
) *balancerpb.BalancerConfig {
	newConfig := cloneBalancerConfig(config)

	var ipv4VS []*balancerpb.VirtualService
	var ipv6VS []*balancerpb.VirtualService

	// Separate IPv4 and IPv6 virtual services
	for _, vs := range newConfig.PacketHandler.Vs {
		addr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
		if addr.Is4() {
			ipv4VS = append(ipv4VS, vs)
		} else {
			ipv6VS = append(ipv6VS, vs)
		}
	}

	// Shuffle if requested
	if shuffleIPv4 {
		rand.Shuffle(len(ipv4VS), func(i, j int) {
			ipv4VS[i], ipv4VS[j] = ipv4VS[j], ipv4VS[i]
		})
	}
	if shuffleIPv6 {
		rand.Shuffle(len(ipv6VS), func(i, j int) {
			ipv6VS[i], ipv6VS[j] = ipv6VS[j], ipv6VS[i]
		})
	}

	// Combine back
	newConfig.PacketHandler.Vs = append(ipv4VS, ipv6VS...)

	return newConfig
}

// cloneBalancerConfig creates a deep copy of balancer configuration
func cloneBalancerConfig(config *balancerpb.BalancerConfig) *balancerpb.BalancerConfig {
	// Create new config with copied values
	newConfig := &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: &balancerpb.Addr{
				Bytes: append([]byte{}, config.PacketHandler.SourceAddressV4.Bytes...),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: append([]byte{}, config.PacketHandler.SourceAddressV6.Bytes...),
			},
			Vs:             make([]*balancerpb.VirtualService, len(config.PacketHandler.Vs)),
			DecapAddresses: config.PacketHandler.DecapAddresses,
			SessionsTimeouts: &balancerpb.SessionsTimeouts{
				TcpSynAck: config.PacketHandler.SessionsTimeouts.TcpSynAck,
				TcpSyn:    config.PacketHandler.SessionsTimeouts.TcpSyn,
				TcpFin:    config.PacketHandler.SessionsTimeouts.TcpFin,
				Tcp:       config.PacketHandler.SessionsTimeouts.Tcp,
				Udp:       config.PacketHandler.SessionsTimeouts.Udp,
				Default:   config.PacketHandler.SessionsTimeouts.Default,
			},
		},
		State: &balancerpb.StateConfig{
			SessionTableCapacity:      config.State.SessionTableCapacity,
			SessionTableMaxLoadFactor: config.State.SessionTableMaxLoadFactor,
			RefreshPeriod:             config.State.RefreshPeriod,
			Wlc:                       config.State.Wlc,
		},
	}

	// Deep copy virtual services
	for i, vs := range config.PacketHandler.Vs {
		newVS := &balancerpb.VirtualService{
			Id: &balancerpb.VsIdentifier{
				Addr:  &balancerpb.Addr{Bytes: append([]byte{}, vs.Id.Addr.Bytes...)},
				Port:  vs.Id.Port,
				Proto: vs.Id.Proto,
			},
			Scheduler:   vs.Scheduler,
			AllowedSrcs: vs.AllowedSrcs,
			Flags:       vs.Flags,
			Reals:       make([]*balancerpb.Real, len(vs.Reals)),
			Peers:       vs.Peers,
		}

		// Copy reals
		for j, real := range vs.Reals {
			newVS.Reals[j] = &balancerpb.Real{
				Id: &balancerpb.RelativeRealIdentifier{
					Ip:   &balancerpb.Addr{Bytes: append([]byte{}, real.Id.Ip.Bytes...)},
					Port: real.Id.Port,
				},
				Weight:  real.Weight,
				SrcAddr: real.SrcAddr,
				SrcMask: real.SrcMask,
			}
		}

		newConfig.PacketHandler.Vs[i] = newVS
	}

	return newConfig
}
