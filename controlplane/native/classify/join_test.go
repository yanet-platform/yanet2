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

// randomStage returns a reproducible stage of ruleCount rules over dim
// region values: groups of distinct values in random order, among them
// empty ones, ones holding over half of the values and ones holding all
// of them, and rules outside the projection.
func randomStage(random *rand.Rand, ruleCount int, dim int) classify.Stage {
	stage := classify.Stage{Starts: []uint32{0}, MaxValue: uint32(dim - 1)}
	groupCount := 1 + random.IntN(8)
	for range groupCount {
		count := 0
		switch shape := random.IntN(6); {
		case shape == 0:
		case shape == 1:
			count = dim
		case shape == 2:
			count = dim/2 + 1 + random.IntN((dim+1)/2)
		default:
			count = 1 + random.IntN(max(1, dim/3))
		}
		for _, value := range random.Perm(dim)[:min(count, dim)] {
			stage.Values = append(stage.Values, uint32(value))
		}
		stage.Starts = append(stage.Starts, uint32(len(stage.Values)))
	}
	stage.RuleGroups = make([]uint32, ruleCount)
	for idx := range stage.RuleGroups {
		stage.RuleGroups[idx] = uint32(random.IntN(groupCount))
		if random.IntN(8) == 0 {
			stage.RuleGroups[idx] = classify.InvalidGroup
		}
	}
	return stage
}

// cStage returns a stage as the C oracle takes it.
func cStage(stage *classify.Stage) coracle.Stage {
	return coracle.Stage{RuleGroups: stage.RuleGroups, Values: stage.Values, Starts: stage.Starts, MaxValue: stage.MaxValue}
}

// tableRows returns the cells of a value table in shared memory, row
// after row.
func tableRows(table *layout.ValueTable) [][]uint32 {
	rows := unsafe.Slice(table.Values.Get(), table.VDim)
	cells := make([][]uint32, table.VDim)
	for v := range rows {
		cells[v] = append([]uint32{}, unsafe.Slice(rows[v].Get(), table.HDim)...)
	}
	return cells
}

// requireJoinMatchesC joins two stages through C and Go and requires the
// same table, rule pairs, classes of every pair and maximum value.
func requireJoinMatchesC(t *testing.T, left, right classify.Stage) {
	t.Helper()
	context := newContext(t)
	cLeft, err := coracle.NewCStage(unsafe.Pointer(context), cStage(&left))
	require.NoError(t, err)
	defer cLeft.Free()
	cRight, err := coracle.NewCStage(unsafe.Pointer(context), cStage(&right))
	require.NoError(t, err)
	defer cRight.Free()
	want, err := coracle.Join(unsafe.Pointer(context), "ratelimit:nets", cLeft, cRight)
	require.NoError(t, err)

	table := newAttr[layout.ValueTable](t, context)
	joint, err := classify.Join(&left, &right, table, context, "ratelimit:nets")
	require.NoError(t, err)
	defer classify.FreeJoin(table)
	got := joint.Stage()
	require.Equal(t, want.RuleGroups, got.RuleGroups, "rule pairs")
	require.Equal(t, uint32(len(want.Groups)), got.GroupCount(), "pair count")
	for pair := range want.Groups {
		require.Equal(t, want.Groups[pair], append([]uint32{}, got.Group(uint32(pair))...), "classes of pair %d", pair)
	}
	require.Equal(t, want.MaxValue, got.MaxValue, "max value")
	require.Equal(t, want.Table, tableRows(table), "table")
	require.Equal(t, want.ContextName, shm.String(table.MemoryContext.Get().Name[:]), "context name")
}

// Test_Join_MatchesC verifies that a join in Go gives every cell, rule
// and pair the classes the C join of the module SDK gives them, over
// groups small, large, empty and whole.
func Test_Join_MatchesC(t *testing.T) {
	for seed := range uint64(300) {
		t.Run(fmt.Sprintf("seed %d", seed), func(t *testing.T) {
			random := rand.New(rand.NewPCG(seed, 13))
			ruleCount := random.IntN(40)
			left := randomStage(random, ruleCount, 1+random.IntN(30))
			right := randomStage(random, ruleCount, 1+random.IntN(30))
			requireJoinMatchesC(t, left, right)
		})
	}
}

// Test_Join_MatchesCOverCompiledAttributes verifies that joining the
// stages of compiled network attributes gives what the C join does.
func Test_Join_MatchesCOverCompiledAttributes(t *testing.T) {
	for seed := range uint64(20) {
		src, err := classify.CompileNet4(randomNet4RulesOf(seed, 60))
		require.NoError(t, err)
		dst, err := classify.CompileNet4(randomNet4RulesOf(seed+1000, 60))
		require.NoError(t, err)
		requireJoinMatchesC(t, src.Stage, dst.Stage)
	}
}

// fuzzStage reads a stage of ruleCount rules from data: a dimension, a
// group count, a 24-bit membership mask per group and a group per rule,
// the one past the last outside the projection; it reads zeros past the
// end of data.
func fuzzStage(data []byte, ruleCount int) (classify.Stage, []byte) {
	next := func() byte {
		if len(data) == 0 {
			return 0
		}
		value := data[0]
		data = data[1:]
		return value
	}
	dim := 1 + int(next())%24
	stage := classify.Stage{Starts: []uint32{0}, MaxValue: uint32(dim - 1)}
	groupCount := 1 + int(next())%6
	for range groupCount {
		mask := uint32(next()) | uint32(next())<<8 | uint32(next())<<16
		for value := range dim {
			if mask>>value&1 == 1 {
				stage.Values = append(stage.Values, uint32(value))
			}
		}
		stage.Starts = append(stage.Starts, uint32(len(stage.Values)))
	}
	stage.RuleGroups = make([]uint32, ruleCount)
	for idx := range stage.RuleGroups {
		stage.RuleGroups[idx] = uint32(int(next()) % (groupCount + 1))
		if stage.RuleGroups[idx] == uint32(groupCount) {
			stage.RuleGroups[idx] = classify.InvalidGroup
		}
	}
	return stage, data
}

// FuzzJoin_MatchesC verifies that any two stages join in Go to what they
// join to in C.
func FuzzJoin_MatchesC(f *testing.F) {
	f.Add([]byte{4, 7, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
	f.Add([]byte{20, 2, 255, 255, 15, 0, 0, 0, 0, 1, 2, 30, 1, 255, 255, 255, 1, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			return
		}
		ruleCount := int(data[0]) % 32
		left, rest := fuzzStage(data[1:], ruleCount)
		right, _ := fuzzStage(rest, ruleCount)
		requireJoinMatchesC(t, left, right)
	})
}

// joinSizes are the ruleset sizes the join is measured at.
var joinSizes = []int{100, 1000, 3000}

// netStages returns the source and destination stages of count rules.
func netStages(t testing.TB, count int) (classify.Stage, classify.Stage) {
	t.Helper()
	src, err := classify.CompileNet4(randomNet4RulesOf(1, count))
	require.NoError(t, err)
	dst, err := classify.CompileNet4(randomNet4RulesOf(2, count))
	require.NoError(t, err)
	return src.Stage, dst.Stage
}

// Benchmark_Join measures joining the source and destination network
// stages of a ruleset, committing the table and freeing it, through the
// C join of the module SDK and natively.
func Benchmark_Join(b *testing.B) {
	for _, count := range joinSizes {
		left, right := netStages(b, count)
		b.Run(fmt.Sprintf("c/%d rules", count), func(b *testing.B) {
			context := newContext(b)
			cLeft, err := coracle.NewCStage(unsafe.Pointer(context), cStage(&left))
			require.NoError(b, err)
			defer cLeft.Free()
			cRight, err := coracle.NewCStage(unsafe.Pointer(context), cStage(&right))
			require.NoError(b, err)
			defer cRight.Free()
			b.ResetTimer()
			_, err = coracle.JoinLoop(unsafe.Pointer(context), "ratelimit:nets", cLeft, cRight, b.N)
			require.NoError(b, err)
		})
		b.Run(fmt.Sprintf("native/%d rules", count), func(b *testing.B) {
			context := newContext(b)
			table := newAttr[layout.ValueTable](b, context)
			for b.Loop() {
				_, err := classify.Join(&left, &right, table, context, "ratelimit:nets")
				require.NoError(b, err)
				classify.FreeJoin(table)
			}
		})
	}
}

// Test_Join_UsesLessTemporaryMemoryThanC verifies that joining two
// network stages natively, Go heap and shared memory together, writes
// less memory than the C join writes into shared memory and the heap.
func Test_Join_UsesLessTemporaryMemoryThanC(t *testing.T) {
	for _, count := range joinSizes {
		left, right := netStages(t, count)
		// The C stages live in an arena of their own, as the native ones
		// live in the Go heap before the measure.
		stages := newContext(t)
		cLeft, err := coracle.NewCStage(unsafe.Pointer(stages), cStage(&left))
		require.NoError(t, err)
		defer cLeft.Free()
		cRight, err := coracle.NewCStage(unsafe.Pointer(stages), cStage(&right))
		require.NoError(t, err)
		defer cRight.Free()
		viaC := measureFootprint(t, func(context *layout.MemoryContext) {
			_, err := coracle.JoinLoop(unsafe.Pointer(context), "ratelimit:nets", cLeft, cRight, 1)
			require.NoError(t, err)
		})
		native := measureFootprint(t, func(context *layout.MemoryContext) {
			table := newAttr[layout.ValueTable](t, context)
			_, err := classify.Join(&left, &right, table, context, "ratelimit:nets")
			require.NoError(t, err)
			classify.FreeJoin(table)
		})
		t.Logf("%d rules, %d by %d values: C %d bytes of shared memory and %d of heap; native %d of shared memory and %d of Go heap",
			count, left.MaxValue+1, right.MaxValue+1, viaC.Shared, viaC.Heap, native.Shared, native.Heap)
		require.Less(t, native.Shared+native.Heap, viaC.Shared+viaC.Heap, "%d rules", count)
	}
}
