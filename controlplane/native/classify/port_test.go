//go:build linux && (amd64 || arm64)

package classify_test

import (
	"slices"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/yanet-platform/yanet2/controlplane/native/classify"
	"github.com/yanet-platform/yanet2/controlplane/native/classify/internal/classifytest"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// portMemorySize is the size of a memory a port attribute's line fits.
const portMemorySize = 1 << 20

// portRange draws an interval near the domain's edges or anywhere in it,
// a single port, a short or a long one, or one ending before it starts.
var portRange = rapid.Custom(func(t *rapid.T) classify.PortRange {
	from := rapid.OneOf(
		rapid.Uint16Max(8),
		rapid.Uint16Min(classify.PortDomain-8),
		rapid.Uint16(),
	).Draw(t, "from")
	to := int(from) + rapid.OneOf(rapid.IntRange(-8, 8), rapid.IntRange(-1, classify.PortDomain)).Draw(t, "width")
	return classify.PortRange{From: from, To: uint16(min(max(to, 0), classify.PortDomain-1))}
})

// drawRules draws count rules of items: absent, empty, whole and
// repeated rules, and lists of one to four items; rule builds a rule of
// its items or an absent one, and whole are the items of a whole-domain
// rule, none for an attribute without one.
func drawRules[I, R any](t *rapid.T, label string, count int, item *rapid.Generator[I], whole []I, rule func(items []I, absent bool) R) []R {
	rules := make([]R, count)
	for idx := range rules {
		switch shape := rapid.IntRange(0, 9).Draw(t, label+" shape"); {
		case shape == 0:
			rules[idx] = rule(nil, true)
		case shape == 1:
			rules[idx] = rule(nil, false)
		case shape == 2 && whole != nil:
			rules[idx] = rule(whole, false)
		case shape == 3 && idx > 0:
			rules[idx] = rules[rapid.IntRange(0, idx-1).Draw(t, label+" repeated")]
		default:
			rules[idx] = rule(rapid.SliceOfN(item, 1, 4).Draw(t, label+" items"), false)
		}
	}
	return rules
}

// drawPortRules draws count port rules.
func drawPortRules(t *rapid.T, label string, count int) []classify.PortRule {
	whole := []classify.PortRange{{From: 0, To: classify.PortDomain - 1}}
	return drawRules(t, label, count, portRange, whole, func(ranges []classify.PortRange, absent bool) classify.PortRule {
		return classify.PortRule{Ranges: ranges, Absent: absent}
	})
}

// checkPortMatchesReference compiles and commits the rules and requires
// the class of both ends of every elementary interval to reach, through
// the stage, exactly the rules matching the port.
func checkPortMatchesReference(t *rapid.T, rules []classify.PortRule) {
	memory := classifytest.LayOut(make([]byte, portMemorySize))
	attr := classifytest.Alloc[layout.ClassifyAttrPort](memory)
	ports := classify.CompilePort(rules)
	require.NoError(t, ports.Commit(attr, &memory.Context, "port"))

	classes := classifytest.Values(&attr.Line)
	require.Len(t, classes, classify.PortDomain)
	// A class changes only where an interval starts or ends, so the keys
	// hold every class the stage numbers.
	stage, keys := ports.Stage(), classifytest.PortKeys(rules)
	keyClasses := make([]uint32, len(keys))
	for idx, port := range keys {
		keyClasses[idx] = classes[port]
	}
	slices.Sort(keyClasses)
	wantClasses := make([]uint32, stage.MaxValue()+1)
	for class := range wantClasses {
		wantClasses[class] = uint32(class)
	}
	require.Equal(t, wantClasses, slices.Compact(keyClasses), "the classes of the ports")
	err := classifytest.CheckStage(
		stage,
		keys,
		func(port uint16) uint32 { return classes[port] },
		func(rule int, port uint16) bool { return classifytest.PortMatches(rules[rule], port) },
	)
	require.NoError(t, err)
}

// portProperty is the property that any port ruleset compiles to
// classes whose stage reaches exactly the rules matching each port.
func portProperty(t *rapid.T) {
	checkPortMatchesReference(t, drawPortRules(t, "port", rapid.IntRange(0, 32).Draw(t, "rules")))
}

// Test_CompilePort_MatchesReference verifies that both ends of every
// elementary interval of the port domain get a class whose stage reaches
// exactly the rules matching the port.
func Test_CompilePort_MatchesReference(t *testing.T) {
	rapid.Check(t, portProperty)
}

// FuzzCompilePort verifies the reference property on rulesets the fuzzer
// draws.
func FuzzCompilePort(f *testing.F) {
	f.Fuzz(rapid.MakeFuzz(portProperty))
}

// Test_PortCommit_ExhaustedLeavesAttributeZeroed verifies that a port
// commit refused any of its allocations fails as exhausted, leaves the
// attribute zeroed and gives the allocator back every byte it took.
func Test_PortCommit_ExhaustedLeavesAttributeZeroed(t *testing.T) {
	cases := []struct {
		name string
		// kept are the requests the allocator's free blocks serve.
		kept []uintptr
	}{
		{name: "no room for the memory context"},
		{name: "no room for the values", kept: []uintptr{unsafe.Sizeof(layout.MemoryContext{})}},
	}
	ports := classify.CompilePort([]classify.PortRule{{Ranges: []classify.PortRange{{From: 80, To: 443}}}})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := classifytest.LayOut(make([]byte, portMemorySize))
			attr := classifytest.Alloc[layout.ClassifyAttrPort](memory)
			nativetest.LeaveOnly(t, &memory.Allocator, tc.kept...)
			free := shmalloc.FreeSize(&memory.Allocator)

			require.ErrorIs(t, ports.Commit(attr, &memory.Context, "port"), shmalloc.ErrNoMemory)
			requireZeroed(t, attr, "the attribute")
			require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
			require.True(t, memory.Context.FirstChild.IsNil(), "the line's memory context is unlinked")
		})
	}
}

// Test_FreePort_ReturnsEveryByte verifies that freeing a committed port
// attribute zeroes it and gives the allocator back every byte the commit
// took.
func Test_FreePort_ReturnsEveryByte(t *testing.T) {
	memory := classifytest.LayOut(make([]byte, portMemorySize))
	attr := classifytest.Alloc[layout.ClassifyAttrPort](memory)
	free := shmalloc.FreeSize(&memory.Allocator)
	ports := classify.CompilePort([]classify.PortRule{{Ranges: []classify.PortRange{{From: 1000, To: 2000}}}, {}})
	require.NoError(t, ports.Commit(attr, &memory.Context, "port"))

	classify.FreePort(attr)
	requireZeroed(t, attr, "the attribute")
	require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
	require.True(t, memory.Context.FirstChild.IsNil(), "the line's memory context is unlinked")
}
