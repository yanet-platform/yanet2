//go:build linux && (amd64 || arm64)

package classify_test

import (
	"encoding/binary"
	"net/netip"
	"slices"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/yanet-platform/yanet2/controlplane/native/classify"
	"github.com/yanet-platform/yanet2/controlplane/native/classify/internal/classifytest"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// drawJointStage draws the stage of a port attribute or of the joint of
// it with a fragment attribute, over count rules.
func drawJointStage(t *rapid.T, label string, count int) *classify.Stage {
	ports := classify.CompilePort(drawPortRules(t, label, count))
	if !rapid.Bool().Draw(t, label+" joined") {
		return ports.Stage()
	}
	fragments, err := classify.CompileFragment(rapid.SliceOfN(fragmentRule, count, count).Draw(t, label+" fragments"))
	require.NoError(t, err)
	memory := classifytest.LayOut(make([]byte, portMemorySize))
	joint, err := ports.Stage().Join(fragments.Stage(), classifytest.Alloc[layout.ValueTable](memory), &memory.Context, "joint")
	require.NoError(t, err)
	return joint
}

// checkRuleTableMatchesReference joins two stages into a rule table and
// requires every cell to hold the first member rule reaching it, and
// every row to be stored as the reference numbers it.
func checkRuleTableMatchesReference(t *rapid.T, left, right *classify.Stage, members []bool) {
	memory := classifytest.LayOut(make([]byte, memorySize))
	table := classifytest.Alloc[layout.RuleTable](memory)
	require.NoError(t, left.JoinRules(right, members, table, &memory.Context, "rules"))

	cells, classMap := classifytest.RuleTableReference(left, right, members)
	require.Equal(t, cells, classifytest.RuleTable(table), "the cells")
	require.Equal(t, classMap, classifytest.Values(&table.ClassMap), "the stored row of every left value")
	require.Equal(t, slices.Max(classMap)+1, table.ClassCount, "the stored rows")
}

// ruleTableProperty is the property that the rule table of any two
// stages of port and fragment attributes and their joints holds the
// first member rule of every cell.
func ruleTableProperty(t *rapid.T) {
	count := rapid.IntRange(0, 24).Draw(t, "rules")
	left := drawJointStage(t, "left", count)
	right := drawJointStage(t, "right", count)
	members := rapid.SliceOfN(rapid.Bool(), count, count).Draw(t, "members")
	checkRuleTableMatchesReference(t, left, right, members)
}

// Test_JoinRules_MatchesReference verifies that every cell of the rule
// table of two stages holds the first member rule reaching it, and that
// equal rows are stored once in order of first appearance.
func Test_JoinRules_MatchesReference(t *testing.T) {
	rapid.Check(t, ruleTableProperty)
}

// FuzzJoinRules verifies the reference property on stages the fuzzer
// draws.
func FuzzJoinRules(f *testing.F) {
	f.Fuzz(rapid.MakeFuzz(ruleTableProperty))
}

// Test_JoinRules_RefusesOtherRuleCounts verifies that stages or members
// of different rule counts are refused as invalid before the table is
// allocated.
func Test_JoinRules_RefusesOtherRuleCounts(t *testing.T) {
	three := classify.CompilePort(make([]classify.PortRule, 3)).Stage()
	four := classify.CompilePort(make([]classify.PortRule, 4)).Stage()
	cases := []struct {
		name    string
		right   *classify.Stage
		members []bool
	}{
		{name: "stages of other rule counts", right: four, members: make([]bool, 3)},
		{name: "members of another rule count", right: three, members: make([]bool, 4)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := classifytest.LayOut(make([]byte, memorySize))
			table := classifytest.Alloc[layout.RuleTable](memory)
			free := shmalloc.FreeSize(&memory.Allocator)

			err := three.JoinRules(tc.right, tc.members, table, &memory.Context, "rules")
			require.ErrorIs(t, err, classify.ErrInvalidRule)
			require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
		})
	}
}

// Test_JoinRules_ExhaustedLeavesTableZeroed verifies that a rule table
// join refused any of its allocations fails as exhausted, leaves the
// table zeroed and gives the allocator back every byte it took.
func Test_JoinRules_ExhaustedLeavesTableZeroed(t *testing.T) {
	// Every cell takes the first rule, so the three rows of five columns
	// are stored as one.
	left, right := splitLines(t, 3, 5, nil, nil)
	members := slices.Repeat([]bool{true}, 1+3+5)
	context := unsafe.Sizeof(layout.MemoryContext{})
	cases := []struct {
		name string
		// kept are the requests the allocator's free blocks serve.
		kept []uintptr
	}{
		{name: "no room for the memory context"},
		{name: "no room for a row", kept: []uintptr{context}},
		{name: "no room for the row array", kept: []uintptr{context, 5 * 4}},
		{name: "no room for the class map's memory context", kept: []uintptr{context, 5 * 4, shm.PtrSize}},
		{name: "no room for the class map", kept: []uintptr{context, 5 * 4, shm.PtrSize, context}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := classifytest.LayOut(make([]byte, memorySize))
			table := classifytest.Alloc[layout.RuleTable](memory)
			nativetest.LeaveOnly(t, &memory.Allocator, tc.kept...)
			free := shmalloc.FreeSize(&memory.Allocator)

			err := left.Stage().JoinRules(right.Stage(), members, table, &memory.Context, "rules")
			require.ErrorIs(t, err, shmalloc.ErrNoMemory)
			requireZeroed(t, table, "the table")
			require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
			require.True(t, memory.Context.FirstChild.IsNil(), "the table's memory context is unlinked")
		})
	}
}

// Test_FreeRuleTable_ReturnsEveryByte verifies that freeing a rule table
// zeroes it and gives the allocator back every byte the join took.
func Test_FreeRuleTable_ReturnsEveryByte(t *testing.T) {
	left, right := splitLines(t, 4, 6, []classify.LineRange{{From: 1, To: 2}}, nil)
	memory := classifytest.LayOut(make([]byte, memorySize))
	table := classifytest.Alloc[layout.RuleTable](memory)
	free := shmalloc.FreeSize(&memory.Allocator)
	require.NoError(t, left.Stage().JoinRules(right.Stage(), slices.Repeat([]bool{true}, 1+4+6), table, &memory.Context, "rules"))

	classify.FreeRuleTable(table)
	requireZeroed(t, table, "the table")
	require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
	require.True(t, memory.Context.FirstChild.IsNil(), "the table's memory context is unlinked")
}

// Test_JoinRules_TitlesTableWithEntryName verifies that a rule table
// titles its memory context with the plain entry name and its class map
// with the classes leaf under it, as classify_join_rules names them.
func Test_JoinRules_TitlesTableWithEntryName(t *testing.T) {
	left, right := splitLines(t, 2, 2, nil, nil)
	memory := classifytest.LayOut(make([]byte, memorySize))
	table := classifytest.Alloc[layout.RuleTable](memory)
	require.NoError(t, left.Stage().JoinRules(right.Stage(), slices.Repeat([]bool{true}, 1+2+2), table, &memory.Context, "ip4:udp_root_joint"))

	require.Equal(t, []string{"ip4:udp_root_joint"}, rootChildren(memory))
	require.Equal(t, "classes", shm.String(table.ClassMap.MemoryContext.Get().Name[:]))
}

// commitLineRuleTable joins two lines into a rule table in a memory of size
// bytes and returns the table and the rule every pair of points resolves
// to; the table is freed at the end of the test, which requires every
// byte back.
func commitLineRuleTable(t *testing.T, size int, left, right *classify.Line, members []bool) (*layout.RuleTable, func(leftPoint, rightPoint uint32) uint32) {
	t.Helper()
	memory := classifytest.LayOut(make([]byte, size))
	table := classifytest.Alloc[layout.RuleTable](memory)
	free := shmalloc.FreeSize(&memory.Allocator)
	require.NoError(t, left.Stage().JoinRules(right.Stage(), members, table, &memory.Context, "join"))
	t.Cleanup(func() {
		classify.FreeRuleTable(table)
		require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
	})

	leftClasses, rightClasses := classifyLine(t, left), classifyLine(t, right)
	rows := classifytest.RuleTable(table)
	return table, func(leftPoint, rightPoint uint32) uint32 {
		return rows[leftClasses[leftPoint]][rightClasses[rightPoint]]
	}
}

// classifyLine returns the class of every point of a compiled line, as
// its commit writes them.
func classifyLine(t *testing.T, line *classify.Line) []uint32 {
	t.Helper()
	memory := classifytest.LayOut(make([]byte, memorySize))
	require.NoError(t, line.Commit(&memory.Line, &memory.Context, "line"))
	return classifytest.Values(&memory.Line.Line)
}

// Test_JoinRules_FirstMatchProjection verifies that every cell of a rule
// table holds the first rule of the projection covering it, a rule
// outside the projection never winning, and that equal rows and the
// uncovered one are stored once.
//
// It is the first-match projection scenario of the C library's join
// test.
func Test_JoinRules_FirstMatchProjection(t *testing.T) {
	point := func(from, to uint32) classify.LineRule {
		return classify.LineRule{Ranges: []classify.LineRange{{From: from, To: to}}}
	}
	left, err := classify.CompileLine([]classify.LineRule{point(3, 3), point(0, 1), point(0, 1), point(2, 2), point(0, 1)}, 4)
	require.NoError(t, err)
	right, err := classify.CompileLine([]classify.LineRule{point(3, 3), point(1, 2), point(0, 1), point(1, 2), point(1, 2)}, 4)
	require.NoError(t, err)
	table, ruleOf := commitLineRuleTable(t, memorySize, left, right, []bool{false, true, true, true, true})

	invalid := classify.InvalidRule
	want := [4][4]uint32{
		{2, 1, 1, invalid},
		{2, 1, 1, invalid},
		{invalid, 3, 3, invalid},
		{invalid, invalid, invalid, invalid},
	}
	for leftPoint := range uint32(4) {
		for rightPoint := range uint32(4) {
			require.Equal(t, want[leftPoint][rightPoint], ruleOf(leftPoint, rightPoint), "the rule of cell %d, %d", leftPoint, rightPoint)
		}
	}
	require.Equal(t, uint32(3), table.ClassCount, "the stored rows")
}

// Test_JoinRules_DuplicateRowsFit verifies that the equal rows of a wide
// rule table are stored once.
//
// It is the duplicate rows scenario of the C library's join test.
func Test_JoinRules_DuplicateRowsFit(t *testing.T) {
	left, right := splitLines(t, 8192, 257, nil, nil)
	members := make([]bool, 1+8192+257)
	members[0] = true
	table, ruleOf := commitLineRuleTable(t, memorySize, left, right, members)

	require.Equal(t, uint32(0), ruleOf(0, 0))
	require.Equal(t, uint32(0), ruleOf(8191, 256))
	require.Equal(t, uint32(1), table.ClassCount, "the stored rows")
}

// Test_JoinRules_UncoveredClasses verifies that a projection covering
// one class of a large space stores one row shared by every uncovered
// class.
//
// It is the uncovered classes scenario of the C library's join test.
func Test_JoinRules_UncoveredClasses(t *testing.T) {
	left, right := splitLines(t, 4096, 257, []classify.LineRange{{From: 0, To: 0}}, nil)
	members := make([]bool, 1+4096+257)
	members[0] = true
	table, ruleOf := commitLineRuleTable(t, memorySize, left, right, members)

	require.Equal(t, uint32(2), table.ClassCount, "the stored rows")
	require.Equal(t, uint32(0), ruleOf(0, 256))
	for leftPoint := uint32(1); leftPoint < 4096; leftPoint++ {
		require.Equal(t, classify.InvalidRule, ruleOf(leftPoint, 128), "the rule of row %d", leftPoint)
	}
}

// Test_JoinRules_FailedJoinCleanup verifies that a rule table a small
// memory cannot hold fails as exhausted, leaves the table zeroed and the
// memory as it found it, so the same memory serves a smaller join next.
//
// It is the failed join cleanup scenario of the C library's join test.
func Test_JoinRules_FailedJoinCleanup(t *testing.T) {
	left, big := splitLines(t, 64, 16384, nil, nil)
	smallLeft, small := splitLines(t, 64, 256, nil, nil)
	memory := classifytest.LayOut(make([]byte, 16<<10))
	table := classifytest.Alloc[layout.RuleTable](memory)
	free := shmalloc.FreeSize(&memory.Allocator)
	members := func(count int) []bool {
		members := make([]bool, count)
		members[0] = true
		return members
	}

	err := left.Stage().JoinRules(big.Stage(), members(1+64+16384), table, &memory.Context, "join")
	require.ErrorIs(t, err, shmalloc.ErrNoMemory)
	requireZeroed(t, table, "the table")
	require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes after the failure")

	require.NoError(t, smallLeft.Stage().JoinRules(small.Stage(), members(1+64+256), table, &memory.Context, "retry"))
	require.Equal(t, uint32(1), table.ClassCount, "the retried table's stored rows")
	classify.FreeRuleTable(table)
	require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes after the retry")
}

// zeroClassFilter is what the zero class scenario commits: the joint of
// two stages, its rule map, the rule table and the joint of it with a
// suffix stage, and the rule map of that joint.
type zeroClassFilter struct {
	First      layout.ValueTable
	Decoder    layout.Vline
	RuleTable  layout.RuleTable
	Final      layout.ValueTable
	FinalRules layout.Vline
}

// Test_JoinRules_ZeroClassConsumers verifies that class zero of a joint
// resolves to the right rule through the joint's decode, a rule table
// and a later join, whether the first cell is covered or not.
//
// It is the zero class consumers scenario of the C library's join test;
// the coverage map and the compact join it also checks are not ported.
func Test_JoinRules_ZeroClassConsumers(t *testing.T) {
	invalid := classify.InvalidRule
	point := func(at uint32) classify.LineRule {
		return classify.LineRule{Ranges: []classify.LineRange{{From: at, To: at}}}
	}
	cases := []struct {
		name string
		// firstPoint is the point the second stage's first rule matches.
		firstPoint uint32
		want       []uint32
	}{
		{name: "first cell covered", firstPoint: 0, want: []uint32{0, invalid, 1, invalid}},
		{name: "first cell uncovered", firstPoint: 1, want: []uint32{invalid, 0, 1, invalid}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Every point of the lines is a class of its own.
			a, err := classify.CompileLine([]classify.LineRule{point(0), point(1)}, 2)
			require.NoError(t, err)
			b, err := classify.CompileLine([]classify.LineRule{point(tc.firstPoint), point(1)}, 2)
			require.NoError(t, err)
			memory := classifytest.LayOut(make([]byte, memorySize))
			filter := classifytest.Alloc[zeroClassFilter](memory)
			context := &memory.Context
			members := slices.Repeat([]bool{true}, 2)

			joined, err := a.Stage().Join(b.Stage(), &filter.First, context, "first")
			require.NoError(t, err)
			require.NoError(t, joined.Decode(members, &filter.Decoder, context, "decode"))
			require.NoError(t, joined.JoinRules(a.Stage(), members, &filter.RuleTable, context, "rules"))
			final, err := joined.Join(a.Stage(), &filter.Final, context, "final")
			require.NoError(t, err)
			require.NoError(t, final.Decode(members, &filter.FinalRules, context, "final-decode"))

			first, decoder := classifytest.Table(&filter.First), classifytest.Values(&filter.Decoder)
			rules := classifytest.RuleTable(&filter.RuleTable)
			finalTable, finalRules := classifytest.Table(&filter.Final), classifytest.Values(&filter.FinalRules)
			lhs, rhs, suffix := []uint32{0, 0, 1, 1}, []uint32{0, 1, 1, 1}, []uint32{0, 0, 1, 0}
			for idx := range lhs {
				class := first[lhs[idx]][rhs[idx]]
				if idx == 0 {
					require.Zero(t, class, "the class of the first cell")
				}
				wantDecoded := tc.want[idx]
				if idx == 3 {
					wantDecoded = 1
				}
				require.Equal(t, wantDecoded, decoder[class], "the decoded rule of cell %d", idx)
				require.Equal(t, tc.want[idx], rules[class][suffix[idx]], "the rule table's rule of cell %d", idx)
				require.Equal(t, tc.want[idx], finalRules[finalTable[class][suffix[idx]]], "the final joint's rule of cell %d", idx)
			}
		})
	}
}

// net4Prefix draws a prefix under a few shared networks, so prefixes
// nest and overlap, or anywhere, its address unmasked at times.
var net4Prefix = rapid.Custom(func(t *rapid.T) netip.Prefix {
	base := rapid.OneOf(rapid.SampledFrom([]uint32{0x0a00_0000, 0x0a01_0000, 0xc0a8_0000}), rapid.Uint32()).Draw(t, "base")
	var addr [4]byte
	binary.BigEndian.PutUint32(addr[:], base|rapid.Uint32Max(0xffff).Draw(t, "host"))
	return netip.PrefixFrom(netip.AddrFrom4(addr), rapid.IntRange(0, 32).Draw(t, "bits"))
})

// net6Half draws an IPv6 network under a few shared high halves, each
// half's mask a prefix of any length, its address unmasked at times.
var net6Half = rapid.Custom(func(t *rapid.T) classify.Net6Network {
	base := rapid.OneOf(rapid.SampledFrom([]uint64{0x2a02_06b8_0000_0000, 0x2001_0db8_0000_0000}), rapid.Uint64()).Draw(t, "base")
	hi := base | rapid.Uint64Max(0xffff_ffff).Draw(t, "hi host")
	lo := rapid.OneOf(rapid.Uint64Max(0xff), rapid.Uint64()).Draw(t, "lo")
	return net6Network(hi, rapid.IntRange(0, 64).Draw(t, "hi bits"), lo, rapid.IntRange(0, 64).Draw(t, "lo bits"))
})

// protocolRange draws an interval of the protocol byte, one reaching
// past it or one ending before it starts.
var protocolRange = rapid.Custom(func(t *rapid.T) classify.LineRange {
	from := rapid.Uint32Max(0x108).Draw(t, "from")
	return classify.LineRange{From: from, To: from + rapid.Uint32Max(0x40).Draw(t, "width") - 2}
})

// drawNet4Rules draws count IPv4 network rules.
func drawNet4Rules(t *rapid.T, label string, count int) []classify.Net4Rule {
	return drawRules(t, label, count, net4Prefix, nil, func(prefixes []netip.Prefix, absent bool) classify.Net4Rule {
		return classify.Net4Rule{Prefixes: prefixes, Absent: absent}
	})
}

// drawNet6Rules draws count IPv6 network rules.
func drawNet6Rules(t *rapid.T, label string, count int) []classify.Net6Rule {
	return drawRules(t, label, count, net6Half, nil, func(networks []classify.Net6Network, absent bool) classify.Net6Rule {
		return classify.Net6Rule{Networks: networks, Absent: absent}
	})
}

// drawProtocolRules draws count rules of the protocol byte.
func drawProtocolRules(t *rapid.T, label string, count int) []classify.LineRule {
	return drawRules(t, label, count, protocolRange, nil, func(ranges []classify.LineRange, absent bool) classify.LineRule {
		return classify.LineRule{Ranges: ranges, Absent: absent}
	})
}

// drawDeviceRules draws count rules of the first eight devices.
func drawDeviceRules(t *rapid.T, label string, count int) []classify.DeviceRule {
	return drawRules(t, label, count, rapid.Uint32Max(7), nil, func(ids []uint32, absent bool) classify.DeviceRule {
		return classify.DeviceRule{IDs: ids, Absent: absent}
	})
}

// drawPick returns a picker of one of a count of keys, drawn.
func drawPick(t *rapid.T) func(label string, count int) int {
	return func(label string, count int) int {
		return rapid.IntRange(0, count-1).Draw(t, label)
	}
}

// ratelimitRuleset is a ruleset as the ratelimit classifiers of both
// families read it, rule by rule: the networks, protocols and
// destination ports of each family, a rule outside a family absent from
// all four of its attributes and no member of its rule table.
type ratelimitRuleset struct {
	Source4, Destination4 []classify.Net4Rule
	Source6, Destination6 []classify.Net6Rule
	Protocol4, Protocol6  []classify.LineRule
	Port4, Port6          []classify.PortRule
}

// drawRatelimitRuleset draws a ratelimit-shaped ruleset whose rules share
// their protocols and ports between the families they belong to.
func drawRatelimitRuleset(t *rapid.T) ratelimitRuleset {
	count := rapid.IntRange(0, 24).Draw(t, "rules")
	ruleset := ratelimitRuleset{
		Source4:      drawNet4Rules(t, "source4", count),
		Destination4: drawNet4Rules(t, "destination4", count),
		Source6:      drawNet6Rules(t, "source6", count),
		Destination6: drawNet6Rules(t, "destination6", count),
		Protocol4:    drawProtocolRules(t, "protocol", count),
		Port4:        drawPortRules(t, "port", count),
	}
	ruleset.Protocol6, ruleset.Port6 = slices.Clone(ruleset.Protocol4), slices.Clone(ruleset.Port4)
	for idx := range count {
		absent4 := ruleset.Source4[idx].Absent || ruleset.Destination4[idx].Absent
		ruleset.Source4[idx].Absent, ruleset.Destination4[idx].Absent = absent4, absent4
		ruleset.Protocol4[idx].Absent = ruleset.Protocol4[idx].Absent || absent4
		ruleset.Port4[idx].Absent = ruleset.Port4[idx].Absent || absent4
		absent6 := ruleset.Source6[idx].Absent || ruleset.Destination6[idx].Absent
		ruleset.Source6[idx].Absent, ruleset.Destination6[idx].Absent = absent6, absent6
		ruleset.Protocol6[idx].Absent = ruleset.Protocol6[idx].Absent || absent6
		ruleset.Port6[idx].Absent = ruleset.Port6[idx].Absent || absent6
	}
	return ruleset
}

// ratelimitShaped is what a ratelimit-shaped classifier commits for each
// family: the networks of both sides joined, the protocols and the
// destination ports joined, and the rule table of both joints.
type ratelimitShaped struct {
	Source4, Destination4 layout.ClassifyAttrNet4
	Source6, Destination6 layout.ClassifyAttrNet6
	Protocol4, Protocol6  layout.ClassifyAttrLine
	Port4, Port6          layout.ClassifyAttrPort
	Nets4, Transport4     layout.ValueTable
	Nets6, Transport6     layout.ValueTable
	Rules4, Rules6        layout.RuleTable
}

// commitRatelimitShaped compiles the ruleset as the ratelimit module does
// and commits the classifier into a holder carved from the memory.
func commitRatelimitShaped(t require.TestingT, memory *classifytest.Memory, ruleset ratelimitRuleset) *ratelimitShaped {
	holder := classifytest.Alloc[ratelimitShaped](memory)
	context := &memory.Context
	transport := func(protocols []classify.LineRule, ports []classify.PortRule, protocolAttr *layout.ClassifyAttrLine, portAttr *layout.ClassifyAttrPort, table *layout.ValueTable, family string) *classify.Stage {
		protocol, err := classify.CompileLine(protocols, 0x100)
		require.NoError(t, err)
		require.NoError(t, protocol.Commit(protocolAttr, context, "ratelimit:proto"+family))
		port := classify.CompilePort(ports)
		require.NoError(t, port.Commit(portAttr, context, "ratelimit:port"+family))
		joint, err := protocol.Stage().Join(port.Stage(), table, context, "ratelimit:transport"+family)
		require.NoError(t, err)
		return joint
	}
	members4, members6 := make([]bool, len(ruleset.Source4)), make([]bool, len(ruleset.Source6))
	for idx := range members4 {
		members4[idx], members6[idx] = !ruleset.Source4[idx].Absent, !ruleset.Source6[idx].Absent
	}

	source4, err := classify.CompileNet4(ruleset.Source4)
	require.NoError(t, err)
	require.NoError(t, source4.Commit(&holder.Source4, context, "ratelimit:src4"))
	destination4, err := classify.CompileNet4(ruleset.Destination4)
	require.NoError(t, err)
	require.NoError(t, destination4.Commit(&holder.Destination4, context, "ratelimit:dst4"))
	nets4, err := source4.Stage().Join(destination4.Stage(), &holder.Nets4, context, "ratelimit:nets4")
	require.NoError(t, err)
	transport4 := transport(ruleset.Protocol4, ruleset.Port4, &holder.Protocol4, &holder.Port4, &holder.Transport4, "4")
	require.NoError(t, nets4.JoinRules(transport4, members4, &holder.Rules4, context, "ratelimit:rules4"))

	source6, err := classify.CompileNet6(ruleset.Source6, &holder.Source6, context, "ratelimit:src6")
	require.NoError(t, err)
	require.NoError(t, source6.Commit())
	destination6, err := classify.CompileNet6(ruleset.Destination6, &holder.Destination6, context, "ratelimit:dst6")
	require.NoError(t, err)
	require.NoError(t, destination6.Commit())
	nets6, err := source6.Stage().Join(destination6.Stage(), &holder.Nets6, context, "ratelimit:nets6")
	require.NoError(t, err)
	transport6 := transport(ruleset.Protocol6, ruleset.Port6, &holder.Protocol6, &holder.Port6, &holder.Transport6, "6")
	require.NoError(t, nets6.JoinRules(transport6, members6, &holder.Rules6, context, "ratelimit:rules6"))
	return holder
}

// freeRatelimitShaped frees a committed ratelimit-shaped classifier and
// its holder.
func freeRatelimitShaped(memory *classifytest.Memory, holder *ratelimitShaped) {
	classify.FreeRuleTable(&holder.Rules4)
	classify.FreeRuleTable(&holder.Rules6)
	for _, table := range []*layout.ValueTable{&holder.Nets4, &holder.Transport4, &holder.Nets6, &holder.Transport6} {
		classify.FreeJoin(table)
	}
	classify.FreeNet4(&holder.Source4)
	classify.FreeNet4(&holder.Destination4)
	classify.FreeNet6(&holder.Source6)
	classify.FreeNet6(&holder.Destination6)
	classify.FreeLine(&holder.Protocol4)
	classify.FreeLine(&holder.Protocol6)
	classify.FreePort(&holder.Port4)
	classify.FreePort(&holder.Port6)
	shmalloc.ContextFree(&memory.Context, unsafe.Pointer(holder), unsafe.Sizeof(*holder))
}

// ratelimitPacket is what a ratelimit-shaped classifier reads of a packet
// of either family.
type ratelimitPacket struct {
	IPv6                  bool
	Source4, Destination4 uint32
	Source6, Destination6 classifytest.Net6Key
	Protocol              uint32
	Port                  uint16
}

// ratelimitKeys are the elementary keys of the attributes of a
// ratelimit-shaped ruleset that packets take their fields from.
type ratelimitKeys struct {
	Source4, Destination4          []uint32
	Source6Hi, Source6Lo           []uint64
	Destination6Hi, Destination6Lo []uint64
	Ports                          []uint16
}

// newRatelimitKeys returns the keys of a ruleset's attributes.
func newRatelimitKeys(ruleset ratelimitRuleset) ratelimitKeys {
	keys := ratelimitKeys{
		Source4:      classifytest.Net4Keys(ruleset.Source4),
		Destination4: classifytest.Net4Keys(ruleset.Destination4),
		Ports:        classifytest.PortKeys(slices.Concat(ruleset.Port4, ruleset.Port6)),
	}
	keys.Source6Hi, keys.Source6Lo = classifytest.Net6HalfKeys(ruleset.Source6)
	keys.Destination6Hi, keys.Destination6Lo = classifytest.Net6HalfKeys(ruleset.Destination6)
	return keys
}

// Packet returns a packet whose family and protocol pick picks, and
// whose other fields it picks among the keys.
func (m ratelimitKeys) Packet(pick func(label string, count int) int) ratelimitPacket {
	return ratelimitPacket{
		IPv6:         pick("family", 2) == 1,
		Source4:      m.Source4[pick("source4", len(m.Source4))],
		Destination4: m.Destination4[pick("destination4", len(m.Destination4))],
		Source6: classifytest.Net6Key{
			Hi: m.Source6Hi[pick("source6 hi", len(m.Source6Hi))],
			Lo: m.Source6Lo[pick("source6 lo", len(m.Source6Lo))],
		},
		Destination6: classifytest.Net6Key{
			Hi: m.Destination6Hi[pick("destination6 hi", len(m.Destination6Hi))],
			Lo: m.Destination6Lo[pick("destination6 lo", len(m.Destination6Lo))],
		},
		Protocol: uint32(pick("protocol", 0x100)),
		Port:     m.Ports[pick("port", len(m.Ports))],
	}
}

// FirstMatch returns the first rule of the packet's family matching it,
// or the invalid rule.
func (m ratelimitRuleset) FirstMatch(packet ratelimitPacket) uint32 {
	for idx := range m.Source4 {
		var matches bool
		if packet.IPv6 {
			matches = classifytest.Net6Matches(m.Source6[idx], packet.Source6) &&
				classifytest.Net6Matches(m.Destination6[idx], packet.Destination6) &&
				classifytest.LineMatches(m.Protocol6[idx], packet.Protocol) &&
				classifytest.PortMatches(m.Port6[idx], packet.Port)
		} else {
			matches = classifytest.Net4Matches(m.Source4[idx], packet.Source4) &&
				classifytest.Net4Matches(m.Destination4[idx], packet.Destination4) &&
				classifytest.LineMatches(m.Protocol4[idx], packet.Protocol) &&
				classifytest.PortMatches(m.Port4[idx], packet.Port)
		}
		if matches {
			return uint32(idx)
		}
	}
	return classify.InvalidRule
}

// ratelimitClassifier resolves packets through a committed
// ratelimit-shaped classifier, as the dataplane reads it.
type ratelimitClassifier struct {
	source4, destination4                classifytest.Stretches[uint32]
	source6, destination6                classifytest.Net6Classes
	protocol4, protocol6, port4, port6   []uint32
	nets4, transport4, nets6, transport6 [][]uint32
	rules4, rules6                       [][]uint32
}

// readRatelimitShaped reads a committed ratelimit-shaped classifier.
func readRatelimitShaped(holder *ratelimitShaped) ratelimitClassifier {
	return ratelimitClassifier{
		source4:      classifytest.ReadNet4(&holder.Source4),
		destination4: classifytest.ReadNet4(&holder.Destination4),
		source6:      classifytest.ReadNet6(&holder.Source6),
		destination6: classifytest.ReadNet6(&holder.Destination6),
		protocol4:    classifytest.Values(&holder.Protocol4.Line),
		protocol6:    classifytest.Values(&holder.Protocol6.Line),
		port4:        classifytest.Values(&holder.Port4.Line),
		port6:        classifytest.Values(&holder.Port6.Line),
		nets4:        classifytest.Table(&holder.Nets4),
		transport4:   classifytest.Table(&holder.Transport4),
		nets6:        classifytest.Table(&holder.Nets6),
		transport6:   classifytest.Table(&holder.Transport6),
		rules4:       classifytest.RuleTable(&holder.Rules4),
		rules6:       classifytest.RuleTable(&holder.Rules6),
	}
}

// Classify returns the rule a packet resolves to.
func (m ratelimitClassifier) Classify(packet ratelimitPacket) uint32 {
	if packet.IPv6 {
		nets := m.nets6[m.source6.Class(packet.Source6)][m.destination6.Class(packet.Destination6)]
		return m.rules6[nets][m.transport6[m.protocol6[packet.Protocol]][m.port6[packet.Port]]]
	}
	nets := m.nets4[m.source4.Class(packet.Source4)][m.destination4.Class(packet.Destination4)]
	return m.rules4[nets][m.transport4[m.protocol4[packet.Protocol]][m.port4[packet.Port]]]
}

// Test_JoinRules_RatelimitShapedResolvesFirstMatch verifies that packets
// of either family classified through the lookups of a ratelimit-shaped
// classifier resolve to the first rule of their family matching them,
// and that freeing the classifier gives every byte back.
func Test_JoinRules_RatelimitShapedResolvesFirstMatch(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		ruleset := drawRatelimitRuleset(t)
		memory := classifytest.LayOut(make([]byte, memorySize))
		free := shmalloc.FreeSize(&memory.Allocator)
		holder := commitRatelimitShaped(t, memory, ruleset)

		classifier, keys := readRatelimitShaped(holder), newRatelimitKeys(ruleset)
		for range rapid.IntRange(1, 32).Draw(t, "packets") {
			packet := keys.Packet(drawPick(t))
			require.Equal(t, ruleset.FirstMatch(packet), classifier.Classify(packet), "the rule of %+v", packet)
		}
		freeRatelimitShaped(memory, holder)
		require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
	})
}

// aclRuleset is an IPv4 ACL-shaped ruleset, rule by rule: its devices,
// networks and protocols, its fragment constraint in the plain
// projection and its port ranges in the port-scoped one; a rule
// restricting a port side is port-scoped, any other plain.
type aclRuleset struct {
	Devices                       []classify.DeviceRule
	Sources, Destinations         []classify.Net4Rule
	Protocols                     []classify.LineRule
	Fragments                     []classify.FragmentRule
	SourcePorts, DestinationPorts []classify.PortRule
	PortScoped                    []bool
}

// drawACLRuleset draws an ACL-shaped ruleset.
func drawACLRuleset(t *rapid.T) aclRuleset {
	count := rapid.IntRange(0, 24).Draw(t, "rules")
	ruleset := aclRuleset{
		Devices:          drawDeviceRules(t, "device", count),
		Sources:          drawNet4Rules(t, "source", count),
		Destinations:     drawNet4Rules(t, "destination", count),
		Protocols:        drawProtocolRules(t, "protocol", count),
		Fragments:        rapid.SliceOfN(fragmentRule, count, count).Draw(t, "fragments"),
		SourcePorts:      drawPortRules(t, "source port", count),
		DestinationPorts: drawPortRules(t, "destination port", count),
		PortScoped:       make([]bool, count),
	}
	for idx := range count {
		scoped := len(ruleset.SourcePorts[idx].Ranges) > 0 || len(ruleset.DestinationPorts[idx].Ranges) > 0
		ruleset.PortScoped[idx] = scoped
		ruleset.Fragments[idx].Absent = ruleset.Fragments[idx].Absent || scoped
		ruleset.SourcePorts[idx].Absent = ruleset.SourcePorts[idx].Absent || !scoped
		ruleset.DestinationPorts[idx].Absent = ruleset.DestinationPorts[idx].Absent || !scoped
	}
	return ruleset
}

// aclShaped is what an ACL-shaped classifier commits: the core of the
// device, the networks joined and the protocol over both projections,
// the plain filter joining the fragment attribute onto the core with its
// rule map, and the port-scoped filter's rule table of the core and the
// joint of both port sides.
type aclShaped struct {
	Device                      layout.ClassifyAttrDevice
	Source, Destination         layout.ClassifyAttrNet4
	Protocol                    layout.ClassifyAttrLine
	Fragment                    layout.ClassifyAttrIpfrag
	SourcePort, DestinationPort layout.ClassifyAttrPort
	Nets, Mid, Core, Root       layout.ValueTable
	Ports                       layout.ValueTable
	PlainRules                  layout.Vline
	PortRules                   layout.RuleTable
}

// commitACLShaped compiles the ruleset as the ACL module composes an
// IPv4 family and commits the classifier into a holder carved from the
// memory.
func commitACLShaped(t require.TestingT, memory *classifytest.Memory, ruleset aclRuleset) *aclShaped {
	holder := classifytest.Alloc[aclShaped](memory)
	context := &memory.Context

	device, err := classify.CompileDevice(ruleset.Devices)
	require.NoError(t, err)
	require.NoError(t, device.Commit(&holder.Device, context, "ip4:device"))
	source, err := classify.CompileNet4(ruleset.Sources)
	require.NoError(t, err)
	require.NoError(t, source.Commit(&holder.Source, context, "ip4:net4_src"))
	destination, err := classify.CompileNet4(ruleset.Destinations)
	require.NoError(t, err)
	require.NoError(t, destination.Commit(&holder.Destination, context, "ip4:net4_dst"))
	nets, err := source.Stage().Join(destination.Stage(), &holder.Nets, context, "ip4:nets_joint")
	require.NoError(t, err)
	mid, err := device.Stage().Join(nets, &holder.Mid, context, "ip4:mid_joint")
	require.NoError(t, err)
	protocol, err := classify.CompileLine(ruleset.Protocols, 0x100)
	require.NoError(t, err)
	require.NoError(t, protocol.Commit(&holder.Protocol, context, "ip4:ipproto"))
	core, err := mid.Join(protocol.Stage(), &holder.Core, context, "ip4:proto_joint")
	require.NoError(t, err)

	plain := make([]bool, len(ruleset.PortScoped))
	for idx, scoped := range ruleset.PortScoped {
		plain[idx] = !scoped
	}
	fragment, err := classify.CompileFragment(ruleset.Fragments)
	require.NoError(t, err)
	require.NoError(t, fragment.Commit(&holder.Fragment, context, "ip4:ipfrag"))
	root, err := core.Join(fragment.Stage(), &holder.Root, context, "ip4:root_joint")
	require.NoError(t, err)
	require.NoError(t, root.Decode(plain, &holder.PlainRules, context, "ip4:rules"))

	sourcePort := classify.CompilePort(ruleset.SourcePorts)
	require.NoError(t, sourcePort.Commit(&holder.SourcePort, context, "ip4:src_port"))
	destinationPort := classify.CompilePort(ruleset.DestinationPorts)
	require.NoError(t, destinationPort.Commit(&holder.DestinationPort, context, "ip4:dst_port"))
	ports, err := sourcePort.Stage().Join(destinationPort.Stage(), &holder.Ports, context, "ip4:ports_joint")
	require.NoError(t, err)
	require.NoError(t, core.JoinRules(ports, ruleset.PortScoped, &holder.PortRules, context, "ip4:udp_root_joint"))
	return holder
}

// freeACLShaped frees a committed ACL-shaped classifier and its holder.
func freeACLShaped(memory *classifytest.Memory, holder *aclShaped) {
	classify.FreeRuleTable(&holder.PortRules)
	classify.FreeDecode(&holder.PlainRules)
	for _, table := range []*layout.ValueTable{&holder.Nets, &holder.Mid, &holder.Core, &holder.Root, &holder.Ports} {
		classify.FreeJoin(table)
	}
	classify.FreeDevice(&holder.Device)
	classify.FreeNet4(&holder.Source)
	classify.FreeNet4(&holder.Destination)
	classify.FreeLine(&holder.Protocol)
	classify.FreeFragment(&holder.Fragment)
	classify.FreePort(&holder.SourcePort)
	classify.FreePort(&holder.DestinationPort)
	shmalloc.ContextFree(&memory.Context, unsafe.Pointer(holder), unsafe.Sizeof(*holder))
}

// aclPacket is what an ACL-shaped classifier reads of an IPv4 packet.
type aclPacket struct {
	Device                      uint32
	Source, Destination         uint32
	Protocol                    uint32
	Fragment                    bool
	SourcePort, DestinationPort uint16
}

// FirstMatch returns the first rule of each projection matching a
// packet, or the invalid rule.
func (m aclRuleset) FirstMatch(packet aclPacket) (uint32, uint32) {
	plainRule, scopedRule := classify.InvalidRule, classify.InvalidRule
	for idx, scoped := range slices.Backward(m.PortScoped) {
		if !classifytest.DeviceMatches(m.Devices[idx], packet.Device) ||
			!classifytest.Net4Matches(m.Sources[idx], packet.Source) ||
			!classifytest.Net4Matches(m.Destinations[idx], packet.Destination) ||
			!classifytest.LineMatches(m.Protocols[idx], packet.Protocol) {
			continue
		}
		if !scoped && classifytest.FragmentMatches(m.Fragments[idx], packet.Fragment) {
			plainRule = uint32(idx)
		}
		if scoped && classifytest.PortMatches(m.SourcePorts[idx], packet.SourcePort) &&
			classifytest.PortMatches(m.DestinationPorts[idx], packet.DestinationPort) {
			scopedRule = uint32(idx)
		}
	}
	return plainRule, scopedRule
}

// Test_JoinRules_ACLShapedResolvesFirstMatch verifies that packets
// classified through the lookups of an ACL-shaped classifier resolve to
// the first rule of the plain and of the port-scoped projection matching
// them, and that freeing the classifier gives every byte back.
func Test_JoinRules_ACLShapedResolvesFirstMatch(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		ruleset := drawACLRuleset(t)
		memory := classifytest.LayOut(make([]byte, memorySize))
		free := shmalloc.FreeSize(&memory.Allocator)
		holder := commitACLShaped(t, memory, ruleset)

		devices := classifytest.Values(&holder.Device.Line)
		source, destination := classifytest.ReadNet4(&holder.Source), classifytest.ReadNet4(&holder.Destination)
		protocols := classifytest.Values(&holder.Protocol.Line)
		sourcePorts, destinationPorts := classifytest.Values(&holder.SourcePort.Line), classifytest.Values(&holder.DestinationPort.Line)
		nets, mid, core := classifytest.Table(&holder.Nets), classifytest.Table(&holder.Mid), classifytest.Table(&holder.Core)
		root, ports := classifytest.Table(&holder.Root), classifytest.Table(&holder.Ports)
		plainRules, portRules := classifytest.Values(&holder.PlainRules), classifytest.RuleTable(&holder.PortRules)
		sourceKeys, destinationKeys := classifytest.Net4Keys(ruleset.Sources), classifytest.Net4Keys(ruleset.Destinations)
		sourcePortKeys, destinationPortKeys := classifytest.PortKeys(ruleset.SourcePorts), classifytest.PortKeys(ruleset.DestinationPorts)
		for range rapid.IntRange(1, 32).Draw(t, "packets") {
			packet := aclPacket{
				Device:          rapid.Uint32Max(uint32(len(devices)-1)).Draw(t, "device"),
				Source:          rapid.SampledFrom(sourceKeys).Draw(t, "source"),
				Destination:     rapid.SampledFrom(destinationKeys).Draw(t, "destination"),
				Protocol:        rapid.Uint32Max(0xff).Draw(t, "protocol"),
				Fragment:        rapid.Bool().Draw(t, "fragment"),
				SourcePort:      rapid.SampledFrom(sourcePortKeys).Draw(t, "source port"),
				DestinationPort: rapid.SampledFrom(destinationPortKeys).Draw(t, "destination port"),
			}
			coreClass := core[mid[devices[packet.Device]][nets[source.Class(packet.Source)][destination.Class(packet.Destination)]]][protocols[packet.Protocol]]
			wantPlain, wantScoped := ruleset.FirstMatch(packet)
			require.Equal(t, wantPlain, plainRules[root[coreClass][fragmentClass(&holder.Fragment, packet.Fragment)]], "the plain rule of %+v", packet)
			require.Equal(t, wantScoped, portRules[coreClass][ports[sourcePorts[packet.SourcePort]][destinationPorts[packet.DestinationPort]]], "the port-scoped rule of %+v", packet)
		}
		freeACLShaped(memory, holder)
		require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
	})
}
