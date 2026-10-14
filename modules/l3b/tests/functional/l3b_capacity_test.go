package l3b_test

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/xnetip"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/l3b/tests/internal/probe"
	"github.com/yanet-platform/yanet2/objects/l3b/bindings/go/cl3bobject"
)

// newCapacityAgent returns an initially unpublished harness agent with
// the l3b objects loaded for capacity accounting.
//
// Callers may publish services when checking destination links.
func newCapacityAgent(t *testing.T) *ffi.Agent {
	t.Helper()

	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(64 * datasize.MB),
		DPMemory:      uint64(4 * datasize.MB),
		WorkerCount:   1,
		Modules:       []string{"l3b"},
		ObjectsToLoad: []string{"l3b_virtual_service", "l3b_session_table"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	agent, err := harness.SharedMemory().AgentAttach(
		"l3b-capacity-test", 0, 16*datasize.MB,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	return agent
}

// Test_L3bProbe_ServiceSmoke verifies that included constructors compile both
// address families while leaving their caller-owned table intact on release.
func Test_L3bProbe_ServiceSmoke(t *testing.T) {
	agent := newCapacityAgent(t)
	table, err := probe.NewTable(agent, "probe-table")
	require.NoError(t, err)
	require.True(t, probe.SeedTable(table))
	service, witness, err := probe.NewService(agent, table, "probe-service", probe.Masks{}, false)
	require.NoError(t, err)
	require.Equal(t, probe.Witness{IPv4Decoded: true, IPv6Entered: true}, witness)
	require.NoError(t, probe.FreeService(&service))
	require.True(t, probe.HasSentinel(table))
	require.NoError(t, probe.FreeTable(&table))
}

// Test_L3bProbe_RawMasks verifies that both production entrypoints reject holes
// inside either half while accepting independently contiguous IPv6 halves.
func Test_L3bProbe_RawMasks(t *testing.T) {
	cases := []struct {
		name  string
		masks probe.Masks
		valid bool
	}{
		{name: "IPv4 hole", masks: probe.Masks{IPv4: [4]byte{0xff, 0x7f}}},
		{name: "IPv6 first half hole", masks: probe.Masks{IPv6: [16]byte{0xff, 0x7f}}},
		{name: "IPv6 second half hole", masks: probe.Masks{IPv6: [16]byte{8: 0xff, 9: 0x7f}}},
		{name: "IPv6 cross boundary gap", masks: probe.Masks{IPv6: [16]byte{0xff, 0xff, 8: 0xff, 9: 0xff}}, valid: true},
		{name: "valid first IPv4 network then invalid second", masks: probe.Masks{AdditionalIPv4: [][4]byte{{0xff, 0x7f}}}},
		{name: "valid first IPv6 network then second with first half hole", masks: probe.Masks{AdditionalIPv6: [][16]byte{{0xff, 0x7f}}}},
		{name: "valid first IPv6 network then second with second half hole", masks: probe.Masks{AdditionalIPv6: [][16]byte{{8: 0xff, 9: 0x7f}}}},
		{name: "two valid IPv4 networks", masks: probe.Masks{AdditionalIPv4: [][4]byte{{0xff, 0xff}}}, valid: true},
		{name: "two valid IPv6 networks", masks: probe.Masks{AdditionalIPv6: [][16]byte{{0xff, 0xff}}}, valid: true},
		{name: "valid first IPv6 network then second with cross boundary gap", masks: probe.Masks{AdditionalIPv6: [][16]byte{{0xff, 0xff, 8: 0xff, 9: 0xff}}}, valid: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agent := newCapacityAgent(t)
			table, err := probe.NewTable(agent, "raw-table")
			require.NoError(t, err)
			require.True(t, probe.SeedTable(table))
			published, witness, err := probe.NewService(agent, table, "published-service", probe.Masks{}, false)
			require.NoError(t, err)
			require.Equal(t, probe.Witness{IPv4Decoded: true, IPv6Entered: true}, witness)
			require.NoError(t, agent.UpdateObjects([]ffi.ObjectConfig{published}))
			capacity := agent.BlockAllocatorFreeSize()
			service, witness, err := probe.NewService(agent, table, "raw-service", tc.masks, false)
			if tc.valid {
				require.NoError(t, err)
				require.Equal(t, probe.Witness{IPv4Decoded: true, IPv6Entered: true}, witness)
				require.NoError(t, probe.FreeService(&service))
			} else {
				require.Error(t, err)
				require.Nil(t, service.AsRawPtr())
				require.Equal(t, probe.Witness{}, witness)
			}
			require.Equal(t, capacity, agent.BlockAllocatorFreeSize())
			require.True(t, probe.HasSentinel(table))
			module, err := probe.NewModule(agent, "raw-module")
			require.NoError(t, err)
			witness, err = probe.UpdateModule(module, "published-service", tc.masks, false)
			if tc.valid {
				require.NoError(t, err)
				require.Equal(t, probe.Witness{IPv4Decoded: true, IPv6Entered: true}, witness)
			} else {
				require.Error(t, err)
				require.Equal(t, probe.Witness{}, witness)
			}
			// Failed preflight still owns the earlier service link allocations.
			require.NoError(t, probe.FreeModule(&module))
			require.Equal(t, capacity, agent.BlockAllocatorFreeSize())
			require.True(t, probe.HasSentinel(table))
		})
	}
}

// Test_L3bProbe_SourceFailureRestoresCapacity verifies that a fault after IPv4
// decode releases partial state without destroying a borrowed live table.
func Test_L3bProbe_SourceFailureRestoresCapacity(t *testing.T) {
	agent := newCapacityAgent(t)
	table, err := probe.NewTable(agent, "source-table")
	require.NoError(t, err)
	require.True(t, probe.SeedTable(table))
	service, witness, err := probe.NewService(agent, table, "source-service", probe.Masks{}, false)
	require.NoError(t, err)
	require.Equal(t, probe.Witness{IPv4Decoded: true, IPv6Entered: true}, witness)
	require.NoError(t, probe.FreeService(&service))
	capacity := agent.BlockAllocatorFreeSize()
	for range 3 {
		service, witness, err = probe.NewService(agent, table, "source-service", probe.Masks{}, true)
		require.Error(t, err)
		require.Nil(t, service.AsRawPtr())
		require.Equal(t, probe.Witness{IPv4Decoded: true, IPv6Entered: true, Faults: 1}, witness)
		require.Equal(t, capacity, agent.BlockAllocatorFreeSize())
		require.True(t, probe.HasSentinel(table))
		service, witness, err = probe.NewService(agent, table, "source-service", probe.Masks{}, false)
		require.NoError(t, err)
		require.Equal(t, probe.Witness{IPv4Decoded: true, IPv6Entered: true}, witness)
		require.NoError(t, probe.FreeService(&service))
		require.Equal(t, capacity, agent.BlockAllocatorFreeSize())
		require.True(t, probe.HasSentinel(table))
	}
	require.NoError(t, probe.FreeTable(&table))
}

// Test_L3bProbe_DestinationFailureRestoresCapacity verifies that successful and
// faulted dual-family updates release all candidate-owned classifiers and links.
func Test_L3bProbe_DestinationFailureRestoresCapacity(t *testing.T) {
	agent := newCapacityAgent(t)
	table, err := probe.NewTable(agent, "destination-table")
	require.NoError(t, err)
	require.True(t, probe.SeedTable(table))
	service, witness, err := probe.NewService(agent, table, "destination-service", probe.Masks{}, false)
	require.NoError(t, err)
	require.Equal(t, probe.Witness{IPv4Decoded: true, IPv6Entered: true}, witness)
	require.NoError(t, agent.UpdateObjects([]ffi.ObjectConfig{service}))
	module, err := probe.NewModule(agent, "destination-module")
	require.NoError(t, err)
	witness, err = probe.UpdateModule(module, "destination-service", probe.Masks{}, false)
	require.NoError(t, err)
	require.Equal(t, probe.Witness{IPv4Decoded: true, IPv6Entered: true}, witness)
	require.NoError(t, probe.FreeModule(&module))
	capacity := agent.BlockAllocatorFreeSize()
	for range 3 {
		module, err = probe.NewModule(agent, "destination-module")
		require.NoError(t, err)
		witness, err = probe.UpdateModule(module, "destination-service", probe.Masks{}, true)
		require.Error(t, err)
		require.Equal(t, probe.Witness{IPv4Decoded: true, IPv6Entered: true, Faults: 1}, witness)
		require.NoError(t, probe.FreeModule(&module))
		require.Equal(t, capacity, agent.BlockAllocatorFreeSize())
		require.True(t, probe.HasSentinel(table))
		module, err = probe.NewModule(agent, "destination-module")
		require.NoError(t, err)
		witness, err = probe.UpdateModule(module, "destination-service", probe.Masks{}, false)
		require.NoError(t, err)
		require.Equal(t, probe.Witness{IPv4Decoded: true, IPv6Entered: true}, witness)
		require.NoError(t, probe.FreeModule(&module))
		require.Equal(t, capacity, agent.BlockAllocatorFreeSize())
		require.True(t, probe.HasSentinel(table))
	}
}

// TestL3bVirtualService_RepeatedCreateReturnsAgentCapacity verifies that
// alternating compiled and rule-less services preserve post-warm-up capacity.
//
// Each round creates and frees a dual-family service, then a rule-less service.
func TestL3bVirtualService_RepeatedCreateReturnsAgentCapacity(t *testing.T) {
	agent := newCapacityAgent(t)

	dualFamily := cl3bobject.VirtualServiceConfig{
		SourceFilterRules: []cl3bobject.SourceFilterRule{{
			Net4s:      []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			Net6s:      []xnetip.BiContiguous{filter.UnspecifiedIPv6},
			PortRanges: filter.PortRanges{{From: 1, To: 65535}},
		}},
		RingCapacity: 4,
	}

	createAndFree := func(config cl3bobject.VirtualServiceConfig) {
		table, err := cl3bobject.CreateSessionTable(
			agent, "capacity", 1, 64,
		)
		require.NoError(t, err)

		service, err := cl3bobject.CreateVirtualService(
			agent, "capacity", config, table,
		)
		require.NoError(t, err)

		// The service borrows the table, so it must go away first.
		require.NoError(t, service.Free())
		require.NoError(t, table.Free())
	}

	// The capacity reference is measured after one compiled/rule-less warm-up round.
	createAndFree(dualFamily)
	createAndFree(cl3bobject.VirtualServiceConfig{})

	base := agent.BlockAllocatorFreeSize()
	for range 3 {
		createAndFree(dualFamily)
		createAndFree(cl3bobject.VirtualServiceConfig{})
	}
	require.Equal(
		t,
		base,
		agent.BlockAllocatorFreeSize(),
		"repeated create and free cycles lost agent capacity",
	)
}
