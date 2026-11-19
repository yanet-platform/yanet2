//go:build linux && (amd64 || arm64)

package classify_test

import (
	"fmt"
	"math/rand/v2"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/classify"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/coracle"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
)

// joint returns the joint of two stages, its table released at once.
func joint(t testing.TB, left, right *classify.Stage) *classify.Joint {
	t.Helper()
	context := newContext(t)
	table := newAttr[layout.ValueTable](t, context)
	joint, err := classify.Join(left, right, table, context, "ratelimit:nets")
	require.NoError(t, err)
	classify.FreeJoin(table)
	return joint
}

// committedRuleTable returns the class map and the rows of a committed
// rule table.
func committedRuleTable(table *layout.RuleTable) ([]uint32, [][]uint32) {
	classMap := append([]uint32{}, unsafe.Slice(table.ClassMap.Values.Get(), table.ClassMap.Size)...)
	pointers := unsafe.Slice(table.Rows.Get(), table.ClassCount)
	rows := make([][]uint32, len(pointers))
	for idx := range pointers {
		rows[idx] = append([]uint32{}, unsafe.Slice(pointers[idx].Get(), table.HDim)...)
	}
	return classMap, rows
}

// requireRuleTableMatchesC joins a joint and a stage into a rule table
// through C and Go and requires the same class map, the same rows and
// the same memory contexts.
func requireRuleTableMatchesC(t *testing.T, left *classify.Joint, right *classify.Stage, members []bool) {
	t.Helper()
	context := newContext(t)
	cLeft, err := coracle.NewCStage(unsafe.Pointer(context), cStage(left.Stage()))
	require.NoError(t, err)
	defer cLeft.Free()
	cRight, err := coracle.NewCStage(unsafe.Pointer(context), cStage(right))
	require.NoError(t, err)
	defer cRight.Free()
	want, err := coracle.JoinRules(unsafe.Pointer(context), "ratelimit:rules", cLeft, cRight, members)
	require.NoError(t, err)

	table := newAttr[layout.RuleTable](t, context)
	require.NoError(t, classify.JoinRules(left, right, members, table, context, "ratelimit:rules"))
	defer classify.FreeRuleTable(table)
	classMap, rows := committedRuleTable(table)
	require.Equal(t, want.ClassMap, classMap, "class map")
	require.Equal(t, want.Rows, rows, "rows")
	require.Equal(t, want.ContextName, shm.String(table.MemoryContext.Get().Name[:]), "context name")
	require.Equal(t, want.ClassesContextName, shm.String(table.ClassMap.MemoryContext.Get().Name[:]), "class map context name")
}

// randomMembers returns which of count rules are members, most of them.
func randomMembers(random *rand.Rand, count int) []bool {
	members := make([]bool, count)
	for idx := range members {
		members[idx] = random.IntN(6) != 0
	}
	return members
}

// Test_JoinRules_MatchesC verifies that a rule table join in Go gives
// every left class the row of first-match rules the C join of the module
// SDK gives it, rows interned alike, over joints of random stages.
func Test_JoinRules_MatchesC(t *testing.T) {
	for seed := range uint64(300) {
		t.Run(fmt.Sprintf("seed %d", seed), func(t *testing.T) {
			random := rand.New(rand.NewPCG(seed, 19))
			ruleCount := random.IntN(48)
			src := randomStage(random, ruleCount, 1+random.IntN(24))
			dst := randomStage(random, ruleCount, 1+random.IntN(24))
			transport := randomStage(random, ruleCount, 1+random.IntN(24))
			requireRuleTableMatchesC(t, joint(t, &src, &dst), &transport, randomMembers(random, ruleCount))
		})
	}
}

// Test_JoinRules_MatchesCOverCompiledJoints verifies that the rule table
// of the joints of compiled network and transport attributes is the one
// C builds.
func Test_JoinRules_MatchesCOverCompiledJoints(t *testing.T) {
	for seed := range uint64(10) {
		const count = 80
		src, err := classify.CompileNet4(randomNet4RulesOf(seed, count))
		require.NoError(t, err)
		dst, err := classify.CompileNet4(randomNet4RulesOf(seed+1000, count))
		require.NoError(t, err)
		proto := classify.CompileLine(randomLineRulesOf(seed, count), classify.LineDomain)
		port := classify.CompilePorts(randomPortRulesOf(seed, count))
		transport := joint(t, &proto.Stage, &port.Stage)
		random := rand.New(rand.NewPCG(seed, 23))
		requireRuleTableMatchesC(t, joint(t, &src.Stage, &dst.Stage), transport.Stage(), randomMembers(random, count))
	}
}

// FuzzJoinRules_MatchesC verifies that any two stages joined and a third
// one join into the rule table C builds; the first byte is the rule
// count, the stages follow, then a member flag a rule.
func FuzzJoinRules_MatchesC(f *testing.F) {
	f.Add([]byte{6, 4, 2, 3, 0, 0, 5, 0, 0, 0, 1, 2, 0, 1, 3, 3, 1, 6, 0, 0, 1, 1, 0, 1, 2, 4, 1, 0, 0, 0, 1, 2, 1, 1, 1, 1, 0, 1})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			return
		}
		ruleCount := int(data[0]) % 24
		src, rest := fuzzStage(data[1:], ruleCount)
		dst, rest := fuzzStage(rest, ruleCount)
		transport, rest := fuzzStage(rest, ruleCount)
		members := make([]bool, ruleCount)
		for idx := range members {
			members[idx] = idx >= len(rest) || rest[idx]&1 == 1
		}
		requireRuleTableMatchesC(t, joint(t, &src, &dst), &transport, members)
	})
}

// ruleTableSizes are the ruleset sizes the rule table is measured at: the
// transport joint of random rules outgrows a block of shared memory a
// row past them, where a production one stays a few hundred classes.
var ruleTableSizes = []int{100, 300}

// Benchmark_JoinRules measures building and freeing the rule table of
// the network and transport joints of a ruleset through the C join of
// the module SDK and natively.
func Benchmark_JoinRules(b *testing.B) {
	for _, count := range ruleTableSizes {
		left, right := netStages(b, count)
		nets := joint(b, &left, &right)
		proto := classify.CompileLine(randomLineRulesOf(1, count), classify.LineDomain)
		port := classify.CompilePorts(randomPortRulesOf(1, count))
		transport := joint(b, &proto.Stage, &port.Stage).Stage()
		members := randomMembers(rand.New(rand.NewPCG(1, 2)), count)
		b.Run(fmt.Sprintf("c/%d rules", count), func(b *testing.B) {
			context := newContextOf(b, arenaSize)
			cLeft, err := coracle.NewCStage(unsafe.Pointer(context), cStage(nets.Stage()))
			require.NoError(b, err)
			defer cLeft.Free()
			cRight, err := coracle.NewCStage(unsafe.Pointer(context), cStage(transport))
			require.NoError(b, err)
			defer cRight.Free()
			b.ResetTimer()
			_, err = coracle.JoinRulesLoop(unsafe.Pointer(context), "ratelimit:rules", cLeft, cRight, members, b.N)
			require.NoError(b, err)
		})
		b.Run(fmt.Sprintf("native/%d rules", count), func(b *testing.B) {
			context := newContextOf(b, arenaSize)
			table := newAttr[layout.RuleTable](b, context)
			for b.Loop() {
				require.NoError(b, classify.JoinRules(nets, transport, members, table, context, "ratelimit:rules"))
				classify.FreeRuleTable(table)
			}
		})
	}
}

// Test_JoinRules_UsesLessTemporaryMemoryThanC verifies that building a
// rule table natively, Go heap and shared memory together, writes less
// memory than the C join writes into shared memory and the heap.
func Test_JoinRules_UsesLessTemporaryMemoryThanC(t *testing.T) {
	for _, count := range ruleTableSizes {
		left, right := netStages(t, count)
		nets := joint(t, &left, &right)
		proto := classify.CompileLine(randomLineRulesOf(1, count), classify.LineDomain)
		port := classify.CompilePorts(randomPortRulesOf(1, count))
		transport := joint(t, &proto.Stage, &port.Stage).Stage()
		members := randomMembers(rand.New(rand.NewPCG(1, 2)), count)
		// The C stages live in an arena of their own, as the native ones
		// live in the Go heap before the measure.
		stages := newContext(t)
		cLeft, err := coracle.NewCStage(unsafe.Pointer(stages), cStage(nets.Stage()))
		require.NoError(t, err)
		defer cLeft.Free()
		cRight, err := coracle.NewCStage(unsafe.Pointer(stages), cStage(transport))
		require.NoError(t, err)
		defer cRight.Free()
		viaC := measureFootprintOf(t, arenaSize, func(context *layout.MemoryContext) {
			_, err := coracle.JoinRulesLoop(unsafe.Pointer(context), "ratelimit:rules", cLeft, cRight, members, 1)
			require.NoError(t, err)
		})
		native := measureFootprintOf(t, arenaSize, func(context *layout.MemoryContext) {
			table := newAttr[layout.RuleTable](t, context)
			require.NoError(t, classify.JoinRules(nets, transport, members, table, context, "ratelimit:rules"))
			classify.FreeRuleTable(table)
		})
		t.Logf("%d rules, %d by %d classes: C %d bytes of shared memory and %d of heap; native %d of shared memory and %d of Go heap",
			count, nets.ClassCount(), transport.MaxValue+1, viaC.Shared, viaC.Heap, native.Shared, native.Heap)
		require.Less(t, native.Shared+native.Heap, viaC.Shared+viaC.Heap, "%d rules", count)
	}
}
