//go:build linux && (amd64 || arm64)

package classify_test

import (
	"fmt"
	"math/rand/v2"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/classify"
	"github.com/yanet-platform/yanet2/controlplane/native/classify/internal/classifytest"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// requireJoinMatchesReference joins two stages and requires the table
// committed and the joint stage, its rule pairs and the order of every
// pair's classes included, to equal the join built cell by cell.
func requireJoinMatchesReference(t *testing.T, left, right *classify.Stage) {
	t.Helper()
	memory := classifytest.LayOut(make([]byte, memorySize))
	table := classifytest.Alloc[layout.ValueTable](memory)
	joint, err := left.Join(right, table, &memory.Context, "joint")
	require.NoError(t, err)

	want := classifytest.JoinReference(left, right)
	require.Equal(t, want.Table, classifytest.Table(table), "the table")
	require.Equal(t, want.RuleGroups, joint.RuleGroups(), "the pair of every rule")
	require.Equal(t, want.Groups, classifytest.Groups(joint), "the classes of every pair")
	require.Equal(t, want.MaxValue, joint.MaxValue(), "the largest class")
}

// Test_Join_MatchesReference verifies that a join of the stages of
// random rulesets, over groups small, large, empty and whole, equals the
// join built cell by cell.
func Test_Join_MatchesReference(t *testing.T) {
	for seed := range uint64(64) {
		t.Run(fmt.Sprintf("seed %d", seed), func(t *testing.T) {
			count := rand.New(rand.NewPCG(seed, 13)).IntN(40)
			lines, err := classify.CompileLine(randomLineRules(seed, count, 0x100), 0x100)
			require.NoError(t, err)
			devices, err := classify.CompileDevice(randomDeviceRules(seed, count))
			require.NoError(t, err)
			nets, err := classify.CompileNet6(randomNet6Rules(seed, count))
			require.NoError(t, err)
			requireJoinMatchesReference(t, lines.Stage(), devices.Stage())
			requireJoinMatchesReference(t, devices.Stage(), nets.Stage())
		})
	}
}

// Test_Join_RefusesStagesOfOtherRuleCounts verifies that stages grouping
// different counts of rules are refused as invalid before the table is
// allocated.
func Test_Join_RefusesStagesOfOtherRuleCounts(t *testing.T) {
	left, err := classify.CompileDevice(randomDeviceRules(1, 3))
	require.NoError(t, err)
	right, err := classify.CompileDevice(randomDeviceRules(1, 4))
	require.NoError(t, err)
	memory := classifytest.LayOut(make([]byte, memorySize))
	table := classifytest.Alloc[layout.ValueTable](memory)
	free := shmalloc.FreeSize(&memory.Allocator)

	_, err = left.Stage().Join(right.Stage(), table, &memory.Context, "joint")
	require.ErrorIs(t, err, classify.ErrInvalidRule)
	require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
}

// Test_Join_ZeroStagesJoinAsEmpty verifies that zero stages join as
// stages of no rules over one value: one cell of one class.
func Test_Join_ZeroStagesJoinAsEmpty(t *testing.T) {
	memory := classifytest.LayOut(make([]byte, memorySize))
	table := classifytest.Alloc[layout.ValueTable](memory)
	joint, err := (&classify.Stage{}).Join(&classify.Stage{}, table, &memory.Context, "joint")
	require.NoError(t, err)

	require.Equal(t, [][]uint32{{0}}, classifytest.Table(table), "the table")
	require.Zero(t, joint.GroupCount(), "the joint's pairs")
	require.Zero(t, joint.MaxValue(), "the joint's largest class")
}

// splitLines compiles two lines of one ruleset over domains of the given
// points: a first rule matching each side's cover, every point when it
// is empty, then a rule per point of each side, absent from the other,
// so every point is a class of its own.
func splitLines(t *testing.T, leftPoints, rightPoints uint32, leftCover, rightCover []classify.LineRange) (*classify.Line, *classify.Line) {
	t.Helper()
	count := 1 + int(leftPoints) + int(rightPoints)
	leftRules := make([]classify.LineRule, count)
	rightRules := make([]classify.LineRule, count)
	leftRules[0], rightRules[0] = classify.LineRule{Ranges: leftCover}, classify.LineRule{Ranges: rightCover}
	for point := range leftPoints {
		leftRules[1+point] = classify.LineRule{Ranges: []classify.LineRange{{From: point, To: point}}}
		rightRules[1+point] = classify.LineRule{Absent: true}
	}
	for point := range rightPoints {
		leftRules[1+leftPoints+point] = classify.LineRule{Absent: true}
		rightRules[1+leftPoints+point] = classify.LineRule{Ranges: []classify.LineRange{{From: point, To: point}}}
	}
	left, err := classify.CompileLine(leftRules, leftPoints)
	require.NoError(t, err)
	right, err := classify.CompileLine(rightRules, rightPoints)
	require.NoError(t, err)
	return left, right
}

// Test_Join_ExhaustedLeavesTableZeroed verifies that a join refused any
// of its allocations fails as exhausted, leaves the table zeroed and
// gives the allocator back every byte it took.
func Test_Join_ExhaustedLeavesTableZeroed(t *testing.T) {
	left, right := splitLines(t, 3, 5, nil, nil)
	context := unsafe.Sizeof(layout.MemoryContext{})
	cases := []struct {
		name string
		// kept are the requests the allocator's free blocks serve.
		kept []uintptr
	}{
		{name: "no room for the memory context"},
		{name: "no room for the row array", kept: []uintptr{context}},
		{name: "no room for a row", kept: []uintptr{context, 3 * shm.PtrSize}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := classifytest.LayOut(make([]byte, memorySize))
			table := classifytest.Alloc[layout.ValueTable](memory)
			nativetest.LeaveOnly(t, &memory.Allocator, tc.kept...)
			free := shmalloc.FreeSize(&memory.Allocator)

			_, err := left.Stage().Join(right.Stage(), table, &memory.Context, "joint")
			require.ErrorIs(t, err, shmalloc.ErrNoMemory)
			requireZeroed(t, table, "the table")
			require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
			require.True(t, memory.Context.FirstChild.IsNil(), "the table's memory context is unlinked")
		})
	}
}

// fuzzDeviceRules reads count device rules from data: a byte per rule,
// its low bits the count of its devices and its top bit an absent rule,
// then a byte per device; it reads zeros past the end of data.
func fuzzDeviceRules(data []byte, count int) ([]classify.DeviceRule, []byte) {
	next := func() byte {
		if len(data) == 0 {
			return 0
		}
		value := data[0]
		data = data[1:]
		return value
	}
	rules := make([]classify.DeviceRule, count)
	for idx := range rules {
		head := next()
		rules[idx].Absent = head&0x80 != 0
		for range head % 5 {
			rules[idx].IDs = append(rules[idx].IDs, uint32(next()%24))
		}
	}
	return rules, data
}

// FuzzJoin verifies that the stages of any two device rulesets join to
// the join built cell by cell.
func FuzzJoin(f *testing.F) {
	f.Add([]byte{4, 2, 1, 2, 0, 1, 7, 3, 1, 2, 3, 0x81, 5, 1, 9, 0, 2, 4, 4})
	f.Add([]byte{3, 1, 0, 4, 1, 2, 3, 4, 0x80, 1, 5, 0, 1, 6})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			return
		}
		count := int(data[0]) % 32
		leftRules, rest := fuzzDeviceRules(data[1:], count)
		rightRules, _ := fuzzDeviceRules(rest, count)
		left, err := classify.CompileDevice(leftRules)
		require.NoError(t, err)
		right, err := classify.CompileDevice(rightRules)
		require.NoError(t, err)
		requireJoinMatchesReference(t, left.Stage(), right.Stage())
	})
}
