//go:build linux && (amd64 || arm64)

package classify_test

import (
	"encoding/binary"
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

// randomPortRulesOf returns count reproducible port rules: single ports,
// small and wide ranges, the domain edges, and the shapes the compile
// tells apart - absent, empty, whole domain, reversed and repeated rules.
func randomPortRulesOf(seed uint64, count int) []classify.PortRule {
	random := rand.New(rand.NewPCG(seed, seed^0x2545f491))
	rules := make([]classify.PortRule, count)
	for idx := range rules {
		switch shape := random.IntN(20); {
		case shape == 0:
			rules[idx] = classify.PortRule{Absent: true}
		case shape == 1:
			rules[idx] = classify.PortRule{}
		case shape == 2:
			rules[idx] = classify.PortRule{Ranges: []classify.PortRange{{From: 0, To: 0xffff}}}
		case shape < 6 && idx > 0:
			rules[idx] = rules[random.IntN(idx)]
		default:
			spans := make([]classify.PortRange, 1+random.IntN(4))
			for spanIdx := range spans {
				from := uint16(random.IntN(1 << 16))
				switch kind := random.IntN(10); {
				case kind < 4:
					spans[spanIdx] = classify.PortRange{From: from, To: from}
				case kind < 7:
					spans[spanIdx] = classify.PortRange{From: from, To: from + uint16(random.IntN(64))}
				case kind < 8:
					spans[spanIdx] = classify.PortRange{From: from, To: 0xffff}
				case kind < 9:
					spans[spanIdx] = classify.PortRange{From: 0, To: from}
				default:
					spans[spanIdx] = classify.PortRange{From: from, To: from - uint16(random.IntN(16))}
				}
			}
			rules[idx] = classify.PortRule{Ranges: spans}
		}
	}
	return rules
}

// cPortRules returns the rules as the C oracle takes them.
func cPortRules(rules []classify.PortRule) []coracle.PortRule {
	converted := make([]coracle.PortRule, len(rules))
	for idx, rule := range rules {
		converted[idx].Absent = rule.Absent
		for _, span := range rule.Ranges {
			converted[idx].Ranges = append(converted[idx].Ranges, [2]uint16{span.From, span.To})
		}
	}
	return converted
}

// requirePortsMatchC compiles the rules through C and Go and requires the
// same value for every port, the same rule groups, group values and
// maximum value, and the same committed attribute.
func requirePortsMatchC(t *testing.T, rules []classify.PortRule) {
	t.Helper()
	context := newContext(t)
	cRules := coracle.NewCPortRules(cPortRules(rules))
	defer cRules.Free()
	want, err := cRules.Compile(unsafe.Pointer(context), "ratelimit:port")
	require.NoError(t, err)

	got := classify.CompilePorts(rules)
	require.Equal(t, want.RuleGroups, got.Stage.RuleGroups, "rule groups")
	require.Equal(t, uint32(len(want.Groups)), got.Stage.GroupCount(), "group count")
	for group := range want.Groups {
		require.Equal(t, want.Groups[group], append([]uint32{}, got.Stage.Group(uint32(group))...), "values of group %d", group)
	}
	require.Equal(t, want.MaxValue, got.Stage.MaxValue, "max value")

	attr := newAttr[layout.ClassifyAttrPort](t, context)
	require.NoError(t, got.Commit(attr, context, "ratelimit:port"))
	defer classify.FreePort(attr)
	require.Equal(t, uint32(classify.PortDomain), attr.Line.Size)
	require.Equal(t, want.Values, unsafe.Slice(attr.Line.Values.Get(), attr.Line.Size), "committed values")
	require.Equal(t, want.ContextName, shm.String(attr.Line.MemoryContext.Get().Name[:]), "context name")
	for port := 0; port < classify.PortDomain; port += 257 {
		require.Equal(t, want.Values[port], got.Value(uint16(port)), "value of port %d", port)
	}
}

// Test_CompilePorts_MatchesC verifies that a port compile in Go gives
// every port, rule and group the values the C compile of the module SDK
// gives them, and commits the same attribute.
func Test_CompilePorts_MatchesC(t *testing.T) {
	for seed := range uint64(200) {
		t.Run(fmt.Sprintf("seed %d", seed), func(t *testing.T) {
			random := rand.New(rand.NewPCG(seed, 7))
			requirePortsMatchC(t, randomPortRulesOf(seed, random.IntN(64)))
		})
	}
}

// FuzzCompilePorts_MatchesC verifies that any port ruleset compiles in
// Go to what it compiles to in C; every five bytes are one interval, a
// zero first byte ending a rule.
func FuzzCompilePorts_MatchesC(f *testing.F) {
	f.Add([]byte{1, 0, 80, 0, 80, 1, 0, 0, 255, 255, 0, 0, 0, 0, 0})
	f.Add([]byte{1, 0, 0, 255, 255, 0, 1, 0, 0, 0, 1, 1, 0, 0, 2})
	f.Fuzz(func(t *testing.T, data []byte) {
		var rules []classify.PortRule
		var current classify.PortRule
		for len(data) >= 5 {
			if data[0] == 0 {
				rules = append(rules, current)
				current = classify.PortRule{Absent: data[1]&1 == 1}
				data = data[5:]
				continue
			}
			current.Ranges = append(current.Ranges, classify.PortRange{
				From: binary.BigEndian.Uint16(data[1:3]),
				To:   binary.BigEndian.Uint16(data[3:5]),
			})
			data = data[5:]
		}
		rules = append(rules, current)
		requirePortsMatchC(t, rules)
	})
}

// portSizes are the ruleset sizes the port compile is measured at.
var portSizes = []int{100, 1000, 10000}

// Benchmark_CompilePorts measures compiling, committing and freeing a
// port attribute through the C compile of the module SDK and natively.
func Benchmark_CompilePorts(b *testing.B) {
	for _, count := range portSizes {
		rules := randomPortRulesOf(1, count)
		b.Run(fmt.Sprintf("c/%d rules", count), func(b *testing.B) {
			context := newContextOf(b, arenaSize)
			cRules := coracle.NewCPortRules(cPortRules(rules))
			defer cRules.Free()
			b.ResetTimer()
			_, err := cRules.CompileLoop(unsafe.Pointer(context), "ratelimit:port", b.N)
			require.NoError(b, err)
		})
		b.Run(fmt.Sprintf("native/%d rules", count), func(b *testing.B) {
			context := newContextOf(b, arenaSize)
			for b.Loop() {
				ports := classify.CompilePorts(rules)
				attr := newAttr[layout.ClassifyAttrPort](b, context)
				require.NoError(b, ports.Commit(attr, context, "ratelimit:port"))
				classify.FreePort(attr)
			}
		})
	}
}

// Test_CompilePorts_UsesLessTemporaryMemoryThanC verifies that compiling
// a port attribute natively, Go heap and shared memory together, writes
// less memory than the C compile writes into shared memory alone.
func Test_CompilePorts_UsesLessTemporaryMemoryThanC(t *testing.T) {
	for _, count := range portSizes {
		rules := randomPortRulesOf(1, count)
		// The rules are copied to C before the measure, as the native
		// compile gets them ready in Go.
		cRules := coracle.NewCPortRules(cPortRules(rules))
		defer cRules.Free()
		viaC := measureFootprint(t, func(context *layout.MemoryContext) {
			_, err := cRules.CompileLoop(unsafe.Pointer(context), "ratelimit:port", 1)
			require.NoError(t, err)
		})
		native := measureFootprint(t, func(context *layout.MemoryContext) {
			ports := classify.CompilePorts(rules)
			attr := newAttr[layout.ClassifyAttrPort](t, context)
			require.NoError(t, ports.Commit(attr, context, "ratelimit:port"))
			classify.FreePort(attr)
		})
		t.Logf("%d rules: C %d bytes of shared memory; native %d of shared memory and %d of Go heap",
			count, viaC.Shared, native.Shared, native.Heap)
		require.Less(t, native.Shared+native.Heap, viaC.Shared+viaC.Heap, "%d rules", count)
	}
}
