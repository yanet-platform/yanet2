package l3b_test

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/xnetip"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/objects/l3b/bindings/go/cl3bobject"
)

// newCapacityAgent returns an agent over a harness with the l3b objects
// loaded and nothing published, for capacity accounting over create and
// free cycles alone.
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

// TestL3bVirtualService_RepeatedCreateReturnsAgentCapacity verifies that
// repeated create and free cycles of a virtual service stop consuming the
// agent arena's free capacity after the first cycle, across services
// without source rules and with dual-family rules.
//
// A compiled service precedes the rule-less one in every round: its freed
// object block carries real classifier state, and the rule-less create
// reusing it must not free that stale state as its own.
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

	// Warm-up round: the first create may split fresh blocks of each
	// size class, so only the rounds after it must be capacity-neutral.
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
