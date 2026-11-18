//go:build linux && (amd64 || arm64)

package classify_test

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"runtime"
	"syscall"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/classify"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/coracle"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

const arenaSize = 256 << 20

// newContext returns a memory context over a fresh anonymous mapping,
// with an allocator C and Go both draw from.
func newContext(t testing.TB) *layout.MemoryContext {
	t.Helper()
	mapping, err := syscall.Mmap(-1, 0, arenaSize, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANONYMOUS)
	require.NoError(t, err)
	t.Cleanup(func() {
		coracle.Unpoison(mapping)
		require.NoError(t, syscall.Munmap(mapping))
	})
	allocator := (*layout.BlockAllocator)(unsafe.Pointer(&mapping[0]))
	context := (*layout.MemoryContext)(unsafe.Pointer(&mapping[4096]))
	shmalloc.Init(allocator)
	shmalloc.PutArena(allocator, unsafe.Pointer(&mapping[1<<20]), arenaSize-(1<<20))
	shmalloc.ContextInit(context, "classify", allocator)
	return context
}

// randomLineRules returns a reproducible ruleset over the line domain
// that exercises every rule shape the compile tells apart.
func randomLineRules(seed uint64) []classify.LineRule {
	random := rand.New(rand.NewPCG(seed, seed^0x5bd1e995))
	return randomLineRulesOf(seed, random.IntN(48))
}

// randomLineRulesOf returns count reproducible rules over the line
// domain.
func randomLineRulesOf(seed uint64, count int) []classify.LineRule {
	random := rand.New(rand.NewPCG(seed, seed^0x9e3779b9))
	rules := make([]classify.LineRule, count)
	for idx := range rules {
		switch shape := random.IntN(20); {
		case shape == 0:
			rules[idx] = classify.LineRule{Absent: true}
		case shape == 1:
			rules[idx] = classify.LineRule{}
		case shape == 2:
			rules[idx] = classify.LineRule{Ranges: []classify.LineRange{{From: 0, To: 300}}}
		case shape < 6 && idx > 0:
			rules[idx] = rules[random.IntN(idx)]
		default:
			spans := make([]classify.LineRange, 1+random.IntN(3))
			for spanIdx := range spans {
				from := uint32(random.IntN(280))
				spans[spanIdx] = classify.LineRange{From: from, To: from + uint32(random.IntN(40)) - 4}
			}
			rules[idx] = classify.LineRule{Ranges: spans}
		}
	}
	return rules
}

// cLineRules returns the rules as the C oracle takes them.
func cLineRules(rules []classify.LineRule) []coracle.LineRule {
	converted := make([]coracle.LineRule, len(rules))
	for idx, rule := range rules {
		converted[idx].Absent = rule.Absent
		for _, span := range rule.Ranges {
			converted[idx].Ranges = append(converted[idx].Ranges, [2]uint32{span.From, span.To})
		}
	}
	return converted
}

// requireLineMatchesC compiles the rules through C and Go and requires
// the same values, rule groups, group values and maximum value, and the
// same committed attribute.
func requireLineMatchesC(t *testing.T, rules []classify.LineRule) {
	t.Helper()
	context := newContext(t)
	cRules := coracle.NewCLineRules(cLineRules(rules))
	defer cRules.Free()
	want, err := cRules.Compile(unsafe.Pointer(context), "ratelimit:proto")
	require.NoError(t, err)

	got := classify.CompileLine(rules, classify.LineDomain)
	require.Equal(t, want.Values, got.Values, "values")
	require.Equal(t, want.RuleGroups, got.Stage.RuleGroups, "rule groups")
	require.Equal(t, uint32(len(want.Groups)), got.Stage.GroupCount(), "group count")
	for group := range want.Groups {
		require.Equal(t, want.Groups[group], append([]uint32{}, got.Stage.Group(uint32(group))...), "values of group %d", group)
	}
	require.Equal(t, want.MaxValue, got.Stage.MaxValue, "max value")

	var attr layout.ClassifyAttrLine
	require.NoError(t, got.Commit(&attr, context, "ratelimit:proto"))
	require.Equal(t, uint32(classify.LineDomain), attr.Line.Size)
	require.Equal(t, want.Values, unsafe.Slice(attr.Line.Values.Get(), attr.Line.Size), "committed values")
	require.Equal(t, want.ContextName, shm.String(attr.Line.MemoryContext.Get().Name[:]), "context name")
}

// Test_CompileLine_MatchesC verifies that a line compile in Go gives
// every point, rule and group the values the C compile of the module SDK
// gives them, and commits the same attribute.
func Test_CompileLine_MatchesC(t *testing.T) {
	for seed := range uint64(300) {
		requireLineMatchesC(t, randomLineRules(seed))
	}
}

// FuzzCompileLine_MatchesC verifies that any ruleset compiles in Go to
// what it compiles to in C; every four bytes are one interval, a zero
// first byte ending a rule.
func FuzzCompileLine_MatchesC(f *testing.F) {
	f.Add([]byte{1, 0, 10, 0, 0, 1, 5, 0, 20, 0})
	f.Add([]byte{0, 0, 0, 0, 1, 0, 255, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		var rules []classify.LineRule
		var current classify.LineRule
		for len(data) >= 4 {
			if data[0] == 0 {
				rules = append(rules, current)
				current = classify.LineRule{Absent: data[1]&1 == 1}
				data = data[4:]
				continue
			}
			current.Ranges = append(current.Ranges, classify.LineRange{From: uint32(data[1]) + uint32(data[0]&1)*256, To: uint32(data[2]) + uint32(data[3]&1)*256})
			data = data[4:]
		}
		rules = append(rules, current)
		requireLineMatchesC(t, rules)
	})
}

// lineSizes are the ruleset sizes the line compile is measured at.
var lineSizes = []int{100, 1000, 10000}

// Benchmark_CompileLine measures compiling, committing and freeing a line
// attribute through the C compile of the module SDK and natively.
func Benchmark_CompileLine(b *testing.B) {
	for _, count := range lineSizes {
		rules := randomLineRulesOf(1, count)
		b.Run(fmt.Sprintf("c/%d rules", count), func(b *testing.B) {
			context := newContext(b)
			cRules := coracle.NewCLineRules(cLineRules(rules))
			defer cRules.Free()
			b.ResetTimer()
			_, err := cRules.CompileLoop(unsafe.Pointer(context), "ratelimit:proto", b.N)
			require.NoError(b, err)
		})
		b.Run(fmt.Sprintf("native/%d rules", count), func(b *testing.B) {
			context := newContext(b)
			for b.Loop() {
				line := classify.CompileLine(rules, classify.LineDomain)
				var attr layout.ClassifyAttrLine
				require.NoError(b, line.Commit(&attr, context, "ratelimit:proto"))
				classify.FreeLine(&attr)
			}
		})
	}
}

// footprint is what one compile wrote: the 64-byte lines of shared
// memory it ever touched, scratch freed again included, and the Go heap
// it allocated.
type footprint struct {
	Shared uint64
	Heap   uint64
}

// measureFootprint runs a compile over a fresh arena filled with a
// pattern and returns what it wrote.
func measureFootprint(t *testing.T, compile func(context *layout.MemoryContext)) footprint {
	t.Helper()
	mapping, err := syscall.Mmap(-1, 0, arenaSize, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANONYMOUS)
	require.NoError(t, err)
	defer func() {
		coracle.Unpoison(mapping)
		require.NoError(t, syscall.Munmap(mapping))
	}()
	arena := mapping[1<<20:]
	for idx := range arena {
		arena[idx] = 0xCD
	}
	allocator := (*layout.BlockAllocator)(unsafe.Pointer(&mapping[0]))
	context := (*layout.MemoryContext)(unsafe.Pointer(&mapping[4096]))
	shmalloc.Init(allocator)
	shmalloc.PutArena(allocator, unsafe.Pointer(&arena[0]), uintptr(len(arena)))
	shmalloc.ContextInit(context, "classify", allocator)
	// Only the lines the allocator already wrote its free links into
	// differ from the pattern before the compile; they are kept to
	// compare against, every other line is compared to the pattern.
	pattern := bytes.Repeat([]byte{0xCD}, 64)
	linked := map[int][]byte{}
	readArena(arena, func() {
		for line := 0; line < len(arena); line += 64 {
			if !bytes.Equal(arena[line:line+64], pattern) {
				linked[line] = bytes.Clone(arena[line : line+64])
			}
		}
	})

	var heapBefore, heapAfter runtime.MemStats
	runtime.ReadMemStats(&heapBefore)
	compile(context)
	runtime.ReadMemStats(&heapAfter)

	touched := uint64(0)
	readArena(arena, func() {
		for line := 0; line < len(arena); line += 64 {
			before, ok := linked[line]
			if !ok {
				before = pattern
			}
			if !bytes.Equal(arena[line:line+64], before) {
				touched += 64
			}
		}
	})
	return footprint{Shared: touched, Heap: heapAfter.TotalAlloc - heapBefore.TotalAlloc}
}

// readArena runs read with all of the arena addressable, restoring what
// AddressSanitizer marks unaddressable afterwards.
func readArena(arena []byte, read func()) {
	runs := coracle.PoisonedRuns(arena)
	coracle.Unpoison(arena)
	read()
	coracle.Repoison(arena, runs)
}

// Test_CompileLine_UsesLessTemporaryMemoryThanC verifies that compiling a
// line attribute natively, Go heap and shared memory together, writes
// less memory than the C compile writes into shared memory alone.
func Test_CompileLine_UsesLessTemporaryMemoryThanC(t *testing.T) {
	for _, count := range lineSizes {
		rules := randomLineRulesOf(1, count)
		viaC := measureFootprint(t, func(context *layout.MemoryContext) {
			cRules := coracle.NewCLineRules(cLineRules(rules))
			defer cRules.Free()
			_, err := cRules.CompileLoop(unsafe.Pointer(context), "ratelimit:proto", 1)
			require.NoError(t, err)
		})
		native := measureFootprint(t, func(context *layout.MemoryContext) {
			line := classify.CompileLine(rules, classify.LineDomain)
			var attr layout.ClassifyAttrLine
			require.NoError(t, line.Commit(&attr, context, "ratelimit:proto"))
			classify.FreeLine(&attr)
		})
		t.Logf("%d rules: C %d bytes of shared memory; native %d of shared memory and %d of Go heap",
			count, viaC.Shared, native.Shared, native.Heap)
		require.Less(t, native.Shared+native.Heap, viaC.Shared+viaC.Heap, "%d rules", count)
	}
}
