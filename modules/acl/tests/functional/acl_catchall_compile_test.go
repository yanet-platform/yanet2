package acl_test

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/xnetip"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
)

// The production shape this file pins: a ruleset whose large majority
// is full-range port catch-alls over many distinct, overlapping
// networks, with a port restricted remainder built from a realistic
// pool of repeated port profiles. The vacuous pairs of such rules
// once painted their whole core footprint across every port class of
// the root joints, so the compile memory grew with the class product
// rectangle instead of the ruleset and a mid-size ruleset could
// exhaust a multi-gigabyte agent.
const (
	// The two rule populations below mirror a production ruleset an
	// order of magnitude larger: a mass of port restricted rules
	// drawing repeated sliding port profiles (the port class space)
	// and wide catch-all rules spanning hundreds of networks each
	// (the network core class space). Their cross once allocated the
	// whole class product rectangle of the root joints.
	catchAllRestricted = 1500
	catchAllCatchAlls  = 300

	catchAllRestrictedSrcNets = 30
	catchAllRestrictedDstNets = 15
	catchAllWideSrcNets       = 150
	catchAllWideDstNets       = 75

	catchAllCPMemory = 2 * datasize.GB
	catchAllAgent    = 768 * datasize.MB
)

// catchAllNet builds the network of one fan-out slot. The even
// slots are plain prefixes of varying depth; the odd slots carry
// explicit two level masks with a hole in the middle - the notation
// half of a production ruleset's networks use - so the networks
// nest and cross in both address halves instead of forming one flat
// block, which is what grows the region space of the network core.
func catchAllNet(prefix string, idx, slot int) xnetip.BiContiguous {
	addr := fmt.Sprintf(
		"%s%x:%x::%x:%x",
		prefix,
		uint16(idx),
		uint16(idx*3+slot),
		uint16(idx*5+slot*7),
		uint16(idx*11+slot*13),
	)

	var entry string
	if slot%2 == 0 {
		depth := 33 + (idx*7+slot*3)%31
		entry = fmt.Sprintf("%s/%d", addr, depth)
	} else {
		hiMask := []string{
			"ff00", "fe00", "fc00", "f800", "f000", "e000", "c000",
		}[(idx+slot)%7]
		loMask := []string{
			"ff00", "fe00", "fc00", "f800", "f000", "e000",
		}[(idx*3+slot)%6]
		entry = fmt.Sprintf(
			"%s/ffff:ffff:%s:0:%s:0:0:0",
			addr,
			hiMask,
			loMask,
		)
	}

	net6, err := xnetip.ParseBiContiguous(entry)
	if err != nil {
		panic(fmt.Sprintf("bad network %q: %v", entry, err))
	}
	return net6
}

// catchAllSrcPorts and catchAllDstPorts are the port profile pools of
// the restricted rules: repeated sliding ranges, mirroring the
// corporate port profiles of a real ruleset, whose cross of distinct
// source and destination profiles is what grows the port class space.
func catchAllSrcPorts(idx int) filter.PortRanges {
	from := uint16(1024 + idx%900)
	return filter.PortRanges{{From: from, To: from + 99}}
}

func catchAllDstPorts(idx int) filter.PortRanges {
	from := uint16(16384 + idx%700)
	return filter.PortRanges{{From: from, To: from + 49}}
}

// catchAllRuleset builds the ruleset: two thousand port restricted
// rules over sixty networks each and four hundred full-range port
// catch-alls over three hundred networks each, spread over four
// devices and both transport paths, with every network unique and
// half of them carrying explicit two level masks.
func catchAllRuleset() []cacl.ACLRule {
	devices := []string{"port0", "port1", "port2", "port3"}

	next := 0
	rule := func(vacuous bool) cacl.ACLRule {
		idx := next
		next++

		srcCount := catchAllRestrictedSrcNets
		dstCount := catchAllRestrictedDstNets
		if vacuous {
			srcCount = catchAllWideSrcNets
			dstCount = catchAllWideDstNets
		}

		rule := cacl.ACLRule{
			Devices: filter.Devices{{Name: devices[idx%len(devices)]}},
		}
		if vacuous {
			rule.Actions = []cacl.ACLAction{{Kind: cacl.ActionAllow}}
			rule.SrcPortRanges = allPorts
			rule.DstPortRanges = allPorts
		} else {
			rule.Actions = []cacl.ACLAction{{Kind: cacl.ActionDeny}}
			from := uint16(1024 + idx%900)
			rule.SrcPortRanges = filter.PortRanges{{From: from, To: from + 99}}
			to := uint16(16384 + idx%700)
			rule.DstPortRanges = filter.PortRanges{{From: to, To: to + 49}}
		}
		if idx%2 == 0 {
			rule.ProtoRanges = tcpProto
		} else {
			rule.ProtoRanges = udpProto
		}

		for n := range srcCount {
			rule.Src6s = append(rule.Src6s, catchAllNet("2001:db8:", idx, n))
		}
		for n := range dstCount {
			rule.Dst6s = append(rule.Dst6s, catchAllNet("fd00:", idx, n))
		}
		return rule
	}

	rules := make([]cacl.ACLRule, 0, catchAllRestricted+catchAllCatchAlls)
	for range catchAllRestricted {
		rules = append(rules, rule(false))
	}
	for range catchAllCatchAlls {
		rules = append(rules, rule(true))
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

// TestACL_PortCatchAllRulesetCompileStaysBounded verifies that a
// ruleset dominated by full-range port catch-alls over many distinct
// nested networks compiles within a small agent budget: the catch-all
// rules must resolve through the without-ports decoding instead of
// painting the whole core footprint across the port joints.
func TestACL_PortCatchAllRulesetCompileStaysBounded(t *testing.T) {
	_, agent, backend := setupACLHarnessSized(
		t,
		[]string{"port0", "port1", "port2", "port3"},
		catchAllCPMemory,
		catchAllAgent,
	)

	if catchAllSanitizedRuntime() {
		t.Skip("the sanitizer pads every shared memory allocation with red zones; the budget is pinned by the unsanitized runs")
	}

	handle, err := backend.NewModule("catchall", catchAllRuleset(), "", "")
	require.NoError(t, err)

	used := uint64(catchAllAgent) - agent.BlockAllocatorFreeSize()
	t.Logf("compiled within %s agent: %s used", catchAllAgent, datasize.ByteSize(used))
	require.Less(t, used, uint64(catchAllAgent)*7/8,
		"the catch-all ruleset must stay well below the agent budget")

	require.NoError(t, handle.Free())
}
