//go:build sdk_rust_fixtures

package cfixture_test

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/lib/rust/yanet-sdk-fixture-api/bindings/go/cfixture"
)

// rules4 covers priority, a terminal allow and the unmatched address domain.
func rules4(t testing.TB) []cfixture.Rule4 {
	t.Helper()
	deny, err := cfixture.NewRule4(netip.MustParsePrefix("10.1.0.0/16"), netip.MustParsePrefix("192.0.2.0/24"), []byte{2, 1}, "blocked")
	require.NoError(t, err)
	allow, err := cfixture.NewRule4(netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("192.0.2.0/24"), []byte{2, 0}, "allowed")
	require.NoError(t, err)
	return []cfixture.Rule4{deny, allow}
}

// firewallFrame returns a parsed UDP frame whose source selects a rule class.
func firewallFrame(source, destination string) []byte {
	frame := ipv4Frame(destination)
	copy(frame[26:30], netip.MustParseAddr(source).AsSlice())
	return frame
}

// Test_Firewall4_PriorityAndUnmatchedAddresses verifies that native classes
// resolve original first-rule indices without confusing source and destination.
func Test_Firewall4_PriorityAndUnmatchedAddresses(t *testing.T) {
	bed := newBed(t, func(config *dataplaneut.Config) { config.Modules = append(config.Modules, "firewall4") })
	config, err := cfixture.NewFirewall4(bed.agent, "rules4", rules4(t), 0)
	require.NoError(t, err)
	bed.freeAtEnd(config)
	instance := &instance{moduleType: "firewall4", name: "rules4", config: config}
	bed.serveBeforeForward(instance)
	for _, tc := range []struct {
		name, source, destination string
		pass                      bool
	}{
		{name: "earlier narrow rule denies", source: "10.1.2.3", destination: "192.0.2.1"},
		{name: "later broad rule allows", source: "10.2.2.3", destination: "192.0.2.1", pass: true},
		{name: "source outside partition matches none", source: "11.2.2.3", destination: "192.0.2.1"},
		{name: "destination outside partition matches none", source: "10.2.2.3", destination: "198.51.100.1"},
	} {
		t.Run(
			tc.name,
			func(t *testing.T) {
				frame := firewallFrame(tc.source, tc.destination)
				result := bed.run(0, frame)
				if tc.pass {
					require.Equal(t, [][]byte{frame}, result.Output)
				} else {
					require.Equal(t, [][]byte{frame}, result.Drop)
				}
			},
		)
	}
	require.Equal(t, []uint64{1, 42}, bed.runtimeCounter(instance, "blocked")[0])
	require.Equal(t, []uint64{1, 42}, bed.runtimeCounter(instance, "allowed")[0])
}

// Test_Firewall4_NullProjectionPreservesRuleIndices verifies that a removed
// original rule leaves the next rule's target and counter index intact.
func Test_Firewall4_NullProjectionPreservesRuleIndices(t *testing.T) {
	bed := newBed(t, func(config *dataplaneut.Config) { config.Modules = append(config.Modules, "firewall4") })
	rules := rules4(t)
	rules[0].Disable()
	config, err := cfixture.NewFirewall4(bed.agent, "rules4", rules, 0)
	require.NoError(t, err)
	bed.freeAtEnd(config)
	instance := &instance{moduleType: "firewall4", name: "rules4", config: config}
	bed.serveBeforeForward(instance)
	frame := firewallFrame("10.1.2.3", "192.0.2.1")
	require.Equal(t, [][]byte{frame}, bed.run(0, frame).Output)
	require.Equal(t, []uint64{0, 0}, bed.runtimeCounter(instance, "blocked")[0])
	require.Equal(t, []uint64{1, 42}, bed.runtimeCounter(instance, "allowed")[0])
}

// Test_Firewall4_ProjectionFaultsReleaseEveryNativeStage verifies that errors,
// panics and getter destruction after reads restore the allocator completely.
func Test_Firewall4_ProjectionFaultsReleaseEveryNativeStage(t *testing.T) {
	bed := newBed(t, func(config *dataplaneut.Config) { config.Modules = append(config.Modules, "firewall4") })
	for _, tc := range []struct {
		name    string
		fault   uint64
		message string
	}{
		{"getter domain error", 1, "network projection fault"},
		{"getter panic", 2, "network projection panic"},
		{"completed getter destructor panic", 3, "network getter destructor"},
		{"decode domain error after joints", 4, "decode projection fault"},
		{"counter name destructor after registration", 5, "counter name destructor"},
	} {
		t.Run(
			tc.name,
			func(t *testing.T) {
				_, err := cfixture.NewFirewall4(bed.agent, "rules4", rules4(t), tc.fault)
				require.ErrorContains(t, err, tc.message)
				require.Equal(t, bed.baseline, bed.agent.BlockAllocatorFreeSize())
			},
		)
	}
	rules := rules4(t)
	rules[0].InvalidMask()
	_, err := cfixture.NewFirewall4(bed.agent, "rules4", rules, 0)
	require.ErrorIs(t, err, ffi.ErrInvalidArgument)
	require.ErrorContains(t, err, "classifier input is invalid")
	require.Equal(t, bed.baseline, bed.agent.BlockAllocatorFreeSize())
}

// Test_Firewall4_NativeAllocationFailuresReleasePartialOutputs verifies that
// failed builds at sampled capacities return all completed native nodes.
func Test_Firewall4_NativeAllocationFailuresReleasePartialOutputs(t *testing.T) {
	bed := newBed(
		t,
		func(config *dataplaneut.Config) {
			config.CPMemory = uint64(256 * datasize.MB)
			config.Modules = append(config.Modules, "firewall4")
		},
	)
	succeeded := false
	for limit := 0; limit < 2<<20; limit += 64 << 10 {
		complete := func() bool {
			agent, err := bed.harness.SharedMemory().AgentAttach(fmt.Sprintf("fault-sweep-%d", limit), 0, datasize.ByteSize(limit))
			if err != nil {
				require.Zero(t, limit, "the agent attaches at any limit but zero")
				return false
			}
			defer func() { require.NoError(t, agent.CleanUp()) }()
			before := agent.BlockAllocatorFreeSize()
			config, err := cfixture.NewFirewall4(agent, "rules4", rules4(t), 0)
			if err == nil {
				require.NoError(t, config.Free())
			} else if !exhaustedOrUntagged(err) {
				require.FailNow(t, "unexpected refusal", "%d bytes: %v", limit, err)
			}
			require.Equal(t, before, agent.BlockAllocatorFreeSize(), "allocation budget %d", limit)
			return err == nil
		}()
		if complete {
			succeeded = true
			break
		}
	}
	require.True(t, succeeded, "the sweep must reach a complete graph")
}

// runtimeCounter reads a rule's packet/byte pair from its published namespace.
func (m *bed) runtimeCounter(instance *instance, name string) [][]uint64 {
	m.t.Helper()
	path := instance.path
	counters, err := m.harness.SharedMemory().DPConfig(0).ModuleRuntimeCounters(path.Device, path.Pipeline, path.Function, path.Chain, path.ModuleType, path.ModuleName, []string{name})
	require.NoError(m.t, err)
	require.Len(m.t, counters, 1)
	return counters[0].Values
}

// Test_Firewall4_CountOnlyStopsAtTheFirstRule verifies that a counted first
// match without a terminal action drops instead of continuing to a later allow.
func Test_Firewall4_CountOnlyStopsAtTheFirstRule(t *testing.T) {
	bed := newBed(t, func(config *dataplaneut.Config) { config.Modules = append(config.Modules, "firewall4") })
	count, err := cfixture.NewRule4(netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("192.0.2.0/24"), []byte{2}, "count_only")
	require.NoError(t, err)
	allow, err := cfixture.NewRule4(netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("192.0.2.0/24"), []byte{2, 0}, "later_allow")
	require.NoError(t, err)
	config, err := cfixture.NewFirewall4(bed.agent, "rules4", []cfixture.Rule4{count, allow}, 0)
	require.NoError(t, err)
	bed.freeAtEnd(config)
	instance := &instance{moduleType: "firewall4", name: "rules4", config: config}
	bed.serveBeforeForward(instance)
	frame := firewallFrame("10.1.2.3", "192.0.2.1")
	require.Equal(t, [][]byte{frame}, bed.run(0, frame).Drop)
	require.Equal(t, []uint64{1, 42}, bed.runtimeCounter(instance, "count_only")[0])
	require.Equal(t, []uint64{0, 0}, bed.runtimeCounter(instance, "later_allow")[0])
}
