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
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// memorySize is the size of a test's memory: the largest prefix tree a
// random ruleset commits fits it.
const memorySize = 16 << 20

// requireZeroed requires every byte of a value to be zero.
func requireZeroed[T any](t *testing.T, value *T, what string) {
	t.Helper()
	size := unsafe.Sizeof(*value)
	require.Equal(t, make([]byte, size), unsafe.Slice((*byte)(unsafe.Pointer(value)), size), what)
}

// lineSizes are the domain sizes consumers compile lines over: a byte,
// a byte and one more as the C smoke test's layer 4 line, a VLAN
// identifier and a port.
var lineSizes = []uint32{0x100, 0x101, 0x1000, 0x10000}

// randomLineRules returns count reproducible rules over a domain of size
// points: absent, empty, whole-domain and repeated rules, and intervals
// that reach past the domain or end before they start.
func randomLineRules(seed uint64, count int, size uint32) []classify.LineRule {
	random := rand.New(rand.NewPCG(seed, uint64(size)))
	rules := make([]classify.LineRule, count)
	for idx := range rules {
		switch shape := random.IntN(20); {
		case shape == 0:
			rules[idx] = classify.LineRule{Absent: true}
		case shape == 1:
			rules[idx] = classify.LineRule{}
		case shape == 2:
			rules[idx] = classify.LineRule{Ranges: []classify.LineRange{{From: 0, To: size + 44}}}
		case shape < 6 && idx > 0:
			rules[idx] = rules[random.IntN(idx)]
		default:
			spans := make([]classify.LineRange, 1+random.IntN(3))
			for spanIdx := range spans {
				from := random.Uint32N(size + size/8)
				spans[spanIdx] = classify.LineRange{From: from, To: from + random.Uint32N(size/6) - 4}
			}
			rules[idx] = classify.LineRule{Ranges: spans}
		}
	}
	return rules
}

// requireLineMatchesReference compiles and commits the rules over a
// domain of size points and requires the class of both ends of every
// elementary interval to reach, through the stage, exactly the rules
// matching the point.
func requireLineMatchesReference(t *testing.T, rules []classify.LineRule, size uint32) {
	t.Helper()
	memory := classifytest.LayOut(make([]byte, memorySize))
	line, err := classify.CompileLine(rules, size)
	require.NoError(t, err)
	require.NoError(t, line.Commit(&memory.Line, &memory.Context, "line"))

	classes := classifytest.Values(&memory.Line.Line)
	require.Len(t, classes, int(size))
	err = classifytest.CheckStage(
		line.Stage(),
		classifytest.LineKeys(rules, size),
		func(point uint32) uint32 { return classes[point] },
		func(rule int, point uint32) bool { return classifytest.LineMatches(rules[rule], point) },
	)
	require.NoError(t, err)
}

// Test_CompileLine_MatchesReference verifies that both ends of every
// elementary interval of a line domain get a class whose stage reaches
// exactly the rules matching the point.
func Test_CompileLine_MatchesReference(t *testing.T) {
	for _, size := range lineSizes {
		for seed := range uint64(16) {
			t.Run(fmt.Sprintf("%#x points, seed %d", size, seed), func(t *testing.T) {
				requireLineMatchesReference(t, randomLineRules(seed, 3*int(seed), size), size)
			})
		}
	}
}

// Test_LineCommit_ExhaustedLeavesAttributeZeroed verifies that a line
// commit refused any of its allocations fails as exhausted, leaves the
// attribute zeroed and gives the allocator back every byte it took.
func Test_LineCommit_ExhaustedLeavesAttributeZeroed(t *testing.T) {
	cases := []struct {
		name string
		// kept are the requests the allocator's free blocks serve.
		kept []uintptr
	}{
		{name: "no room for the memory context"},
		{name: "no room for the values", kept: []uintptr{unsafe.Sizeof(layout.MemoryContext{})}},
	}
	line, err := classify.CompileLine(randomLineRules(1, 16, 0x100), 0x100)
	require.NoError(t, err)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := classifytest.LayOut(make([]byte, memorySize))
			nativetest.LeaveOnly(t, &memory.Allocator, tc.kept...)
			free := shmalloc.FreeSize(&memory.Allocator)

			require.ErrorIs(t, line.Commit(&memory.Line, &memory.Context, "line"), shmalloc.ErrNoMemory)
			requireZeroed(t, &memory.Line, "the attribute")
			require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
			require.True(t, memory.Context.FirstChild.IsNil(), "the line's memory context is unlinked")
		})
	}
}

// Test_FreeLine_ReturnsEveryByte verifies that freeing a committed line
// attribute zeroes it and gives the allocator back every byte the commit
// took.
func Test_FreeLine_ReturnsEveryByte(t *testing.T) {
	memory := classifytest.LayOut(make([]byte, memorySize))
	free := shmalloc.FreeSize(&memory.Allocator)
	line, err := classify.CompileLine(randomLineRules(1, 16, 0x10000), 0x10000)
	require.NoError(t, err)
	require.NoError(t, line.Commit(&memory.Line, &memory.Context, "line"))

	classify.FreeLine(&memory.Line)
	requireZeroed(t, &memory.Line, "the attribute")
	require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
	require.True(t, memory.Context.FirstChild.IsNil(), "the line's memory context is unlinked")
}

// Test_CompileLine_RefusesEmptyDomain verifies that a line over a domain
// of no points is refused as an invalid rule.
func Test_CompileLine_RefusesEmptyDomain(t *testing.T) {
	_, err := classify.CompileLine([]classify.LineRule{{Ranges: []classify.LineRange{{From: 0, To: 1}}}}, 0)
	require.ErrorIs(t, err, classify.ErrInvalidRule)
}

// FuzzCompileLine verifies that any ruleset over a byte domain compiles
// to classes whose stage reaches exactly the rules matching each point;
// every four bytes are one interval, a zero first byte ending a rule.
func FuzzCompileLine(f *testing.F) {
	f.Add([]byte{1, 0, 10, 0, 0, 1, 5, 0, 20, 0})
	f.Add([]byte{0, 0, 0, 0, 1, 0, 255, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		var rules []classify.LineRule
		var current classify.LineRule
		for ; len(data) >= 4; data = data[4:] {
			if data[0] == 0 {
				rules = append(rules, current)
				current = classify.LineRule{Absent: data[1]&1 == 1}
				continue
			}
			current.Ranges = append(current.Ranges, classify.LineRange{
				From: uint32(data[1]) + uint32(data[0]&1)*256,
				To:   uint32(data[2]) + uint32(data[3]&1)*256,
			})
		}
		rules = append(rules, current)
		requireLineMatchesReference(t, rules, 0x100)
	})
}
