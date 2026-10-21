package acl_test

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/xnetip"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
)

// The case this file pins: full-range port catch-alls resolve on the plain
// path, yet their networks split the network core the port paths share.
//
// The root joint of a port path joins the core classes with the port
// classes of the port restricted rules. It spans only the core classes
// those rules tell apart, so the catch-alls share one row; a joint over
// the whole core grows with the product of both class spaces instead of
// the ruleset.
const (
	// Each catch-all owns a core class and each restricted rule a port
	// class; the restricted rules of a path share one core footprint.
	catchAllVacuous    = 2048
	catchAllRestricted = 2048

	// The agent holds the compile peak with a third to spare; the joints
	// over the whole core need more than three times the agent.
	catchAllAgent = 16 * datasize.MB
)

// catchAllProto alternates the rules between TCP and UDP, so the port
// restricted rules give both transport paths a root joint to pin.
func catchAllProto(idx int) filter.ProtoRanges {
	if idx%2 == 0 {
		return tcpProto
	}
	return udpProto
}

// catchAllRuleset builds the port restricted rules followed by the
// full-range port catch-alls.
//
// Each restricted rule takes one destination port over one shared
// network, each catch-all one network inside it.
func catchAllRuleset() []cacl.ACLRule {
	shared := []xnetip.BiContiguous{xnetip.MustParseBiContiguous("3fff::/20")}
	anyNetwork := []xnetip.BiContiguous{xnetip.MustParseBiContiguous("::/0")}
	devices := filter.Devices{{Name: "port0"}}

	rules := make([]cacl.ACLRule, 0, catchAllRestricted+catchAllVacuous)
	for idx := range catchAllRestricted {
		port := uint16(1024 + idx)
		rules = append(rules, cacl.ACLRule{
			Devices:       devices,
			Actions:       []cacl.ACLAction{{Kind: cacl.ActionDeny}},
			ProtoRanges:   catchAllProto(idx),
			SrcPortRanges: allPorts,
			DstPortRanges: filter.PortRanges{{From: port, To: port}},
			Src6s:         shared,
			Dst6s:         anyNetwork,
		})
	}
	for idx := range catchAllVacuous {
		network := xnetip.MustParseBiContiguous(
			fmt.Sprintf("3fff:0:%x::/48", idx),
		)
		rules = append(rules, cacl.ACLRule{
			Devices:       devices,
			Actions:       []cacl.ACLAction{{Kind: cacl.ActionAllow}},
			ProtoRanges:   catchAllProto(idx),
			SrcPortRanges: allPorts,
			DstPortRanges: allPorts,
			Src6s:         []xnetip.BiContiguous{network},
			Dst6s:         anyNetwork,
		})
	}
	return rules
}

// catchAllSanitizedRuntime reports whether the address sanitizer
// runtime is loaded into this process, which the shared memory block
// allocator compensates with per allocation red zones.
func catchAllSanitizedRuntime() bool {
	maps, err := os.ReadFile("/proc/self/maps")
	return err == nil && bytes.Contains(maps, []byte("libasan"))
}

// Test_ACLModuleConfig_PortCatchAllsStayBounded verifies that full-range
// port catch-alls splitting the network core compile within a small agent.
//
// No root joint may span the core classes only the catch-alls tell apart.
func Test_ACLModuleConfig_PortCatchAllsStayBounded(t *testing.T) {
	_, agent, backend := setupACLHarnessSized(
		t,
		[]string{"port0"},
		aclCPSize,
		catchAllAgent,
	)

	if catchAllSanitizedRuntime() {
		t.Skip("the sanitizer pads every shared memory allocation with red zones; the budget is pinned by the unsanitized runs")
	}

	handle, err := backend.NewModule("catchall", catchAllRuleset(), "", "")
	require.NoError(t, err)

	used := uint64(catchAllAgent) - agent.BlockAllocatorFreeSize()
	t.Logf("compiled within %s agent: %s used", catchAllAgent, datasize.ByteSize(used))
	require.Less(
		t, used, uint64(catchAllAgent)*7/8,
		"the catch-all ruleset must stay well below the agent budget",
	)

	require.NoError(t, handle.Free())
}

// Test_CatchAllRuleset_DistinctClasses verifies that every catch-all lists
// its own network and every port restricted rule its own destination port.
func Test_CatchAllRuleset_DistinctClasses(t *testing.T) {
	networks := map[xnetip.BiContiguous]struct{}{}
	ports := map[filter.PortRange]struct{}{}
	for _, rule := range catchAllRuleset() {
		if slices.Equal(rule.DstPortRanges, allPorts) {
			networks[rule.Src6s[0]] = struct{}{}
			continue
		}
		ports[rule.DstPortRanges[0]] = struct{}{}
	}

	require.Len(t, networks, catchAllVacuous)
	require.Len(t, ports, catchAllRestricted)
}
