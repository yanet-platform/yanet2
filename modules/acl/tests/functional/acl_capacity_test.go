package acl_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/xnetip"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
)

// TestACLModuleConfig_RepeatedCompileReturnsAgentCapacity verifies that
// repeated unpublished create/free cycles of one ruleset stop consuming
// the agent arena's free capacity after the first cycle, across the
// empty, single-family and dual-family rulesets.
func TestACLModuleConfig_RepeatedCompileReturnsAgentCapacity(t *testing.T) {
	_, agent, backend := setupACLHarness(t, []string{"port0"})

	rule4 := allow4Rule(
		[]xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		[]xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		tcpProto,
	)
	rule6 := allow6Rule(
		[]xnetip.BiContiguous{filter.UnspecifiedIPv6},
		[]xnetip.BiContiguous{filter.UnspecifiedIPv6},
		tcpProto,
	)

	cases := []struct {
		name  string
		rules []cacl.ACLRule
	}{
		{"empty ruleset", nil},
		{"ipv4-only ruleset", []cacl.ACLRule{rule4}},
		{"ipv6-only ruleset", []cacl.ACLRule{rule6}},
		{"dual-family ruleset", []cacl.ACLRule{rule4, rule6}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			compileAndFree := func() {
				handle, err := backend.NewModule("capacity", tc.rules, "", "")
				require.NoError(t, err)
				require.NoError(t, handle.Free())
			}

			// Warm-up cycle: the first compile may split fresh blocks
			// of each size class, so only the cycles after it must be
			// capacity-neutral.
			compileAndFree()

			base := agent.BlockAllocatorFreeSize()
			for range 3 {
				compileAndFree()
			}
			require.Equal(
				t,
				base,
				agent.BlockAllocatorFreeSize(),
				"repeated create/free cycles lost agent capacity",
			)
		})
	}
}
