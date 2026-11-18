//go:build linux && (amd64 || arm64)

package classify_test

import (
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/classify"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/coracle"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
)

// randomNet4RulesOf returns count reproducible IPv4 network rules: nested
// prefixes, including one ending where its parent ends, unmasked
// addresses, and absent, empty, whole-space and repeated rules.
func randomNet4RulesOf(seed uint64, count int) []classify.Net4Rule {
	random := rand.New(rand.NewPCG(seed, seed^0x7f4a7c15))
	var known []netip.Prefix
	prefix := func() netip.Prefix {
		if len(known) > 0 && random.IntN(3) == 0 {
			parent := known[random.IntN(len(known))]
			bits := min(32, parent.Bits()+1+random.IntN(12))
			addr := parent.Addr().As4()
			value := binary.BigEndian.Uint32(addr[:])
			if random.IntN(4) == 0 {
				value |= ^uint32(0) >> parent.Bits()
			} else {
				value |= random.Uint32() >> parent.Bits()
			}
			binary.BigEndian.PutUint32(addr[:], value)
			return netip.PrefixFrom(netip.AddrFrom4(addr), bits)
		}
		var addr [4]byte
		binary.BigEndian.PutUint32(addr[:], random.Uint32())
		created := netip.PrefixFrom(netip.AddrFrom4(addr), 4+random.IntN(29))
		known = append(known, created.Masked())
		return created
	}
	rules := make([]classify.Net4Rule, count)
	for idx := range rules {
		switch shape := random.IntN(20); {
		case shape == 0:
			rules[idx] = classify.Net4Rule{Absent: true}
		case shape == 1:
			rules[idx] = classify.Net4Rule{}
		case shape == 2:
			rules[idx] = classify.Net4Rule{Prefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), prefix()}}
		case shape < 6 && idx > 0:
			rules[idx] = rules[random.IntN(idx)]
		default:
			prefixes := make([]netip.Prefix, 1+random.IntN(4))
			for netIdx := range prefixes {
				prefixes[netIdx] = prefix()
			}
			rules[idx] = classify.Net4Rule{Prefixes: prefixes}
		}
	}
	return rules
}

// cNet4Rules returns the rules as the C oracle takes them.
func cNet4Rules(rules []classify.Net4Rule) []coracle.Net4Rule {
	converted := make([]coracle.Net4Rule, len(rules))
	for idx, rule := range rules {
		converted[idx] = coracle.Net4Rule{Prefixes: rule.Prefixes, Absent: rule.Absent}
	}
	return converted
}

// requireNet4MatchesC compiles the rules through C and Go and requires
// the same rule groups, group values and maximum value, and committed
// trees that answer every lookup alike.
func requireNet4MatchesC(t *testing.T, rules []classify.Net4Rule) {
	t.Helper()
	context := newContext(t)
	cRules := coracle.NewCNet4Rules(cNet4Rules(rules))
	defer cRules.Free()
	want, err := cRules.Compile(unsafe.Pointer(context), "ratelimit:src4")
	require.NoError(t, err)

	got, err := classify.CompileNet4(rules)
	require.NoError(t, err)
	require.Equal(t, want.RuleGroups, got.Stage.RuleGroups, "rule groups")
	require.Equal(t, uint32(len(want.Groups)), got.Stage.GroupCount(), "group count")
	for group := range want.Groups {
		require.Equal(t, want.Groups[group], append([]uint32{}, got.Stage.Group(uint32(group))...), "values of group %d", group)
	}
	require.Equal(t, want.MaxValue, got.Stage.MaxValue, "max value")

	attr := newAttr[layout.ClassifyAttrNet4](t, context)
	require.NoError(t, got.Commit(attr, context, "ratelimit:src4"))
	defer classify.FreeNet4(attr)
	require.Equal(t, want.Stretches, coracle.Stretches(&attr.Lpm, 4), "lookups")
	require.Equal(t, want.ContextName, shm.String(attr.Lpm.MemoryContext.Name[:]), "context name")
	for _, stretch := range want.Stretches {
		addr := netip.AddrFrom4([4]byte([]byte(stretch.From)))
		require.Equal(t, stretch.Value, got.Value(addr), "value at %s", addr)
	}
}

// Test_CompileNet4_MatchesC verifies that an IPv4 network compile in Go
// gives every rule and group the values the C compile gives them, and
// commits a tree that answers every lookup as the C one does.
func Test_CompileNet4_MatchesC(t *testing.T) {
	for seed := range uint64(200) {
		random := rand.New(rand.NewPCG(seed, 11))
		requireNet4MatchesC(t, randomNet4RulesOf(seed, random.IntN(64)))
	}
}

// FuzzCompileNet4_MatchesC verifies that any IPv4 network ruleset
// compiles in Go to what it compiles to in C; every six bytes are one
// prefix, a zero first byte ending a rule.
func FuzzCompileNet4_MatchesC(f *testing.F) {
	f.Add([]byte{1, 10, 0, 0, 0, 8, 1, 10, 1, 0, 0, 16, 0, 0, 0, 0, 0, 0})
	f.Add([]byte{1, 10, 0, 0, 0, 8, 0, 0, 0, 0, 0, 0, 1, 10, 255, 0, 0, 16})
	f.Fuzz(func(t *testing.T, data []byte) {
		var rules []classify.Net4Rule
		var current classify.Net4Rule
		for len(data) >= 6 {
			if data[0] == 0 {
				rules = append(rules, current)
				current = classify.Net4Rule{Absent: data[1]&1 == 1}
				data = data[6:]
				continue
			}
			addr := netip.AddrFrom4([4]byte(data[1:5]))
			current.Prefixes = append(current.Prefixes, netip.PrefixFrom(addr, int(data[5]%33)))
			data = data[6:]
		}
		rules = append(rules, current)
		requireNet4MatchesC(t, rules)
	})
}

// net4Sizes are the ruleset sizes the IPv4 network compile is measured
// at.
var net4Sizes = []int{100, 1000, 10000}

// Benchmark_CompileNet4 measures compiling, committing and freeing an
// IPv4 network attribute through the C compile and natively.
func Benchmark_CompileNet4(b *testing.B) {
	for _, count := range net4Sizes {
		rules := randomNet4RulesOf(1, count)
		b.Run(fmt.Sprintf("c/%d rules", count), func(b *testing.B) {
			context := newContext(b)
			cRules := coracle.NewCNet4Rules(cNet4Rules(rules))
			defer cRules.Free()
			b.ResetTimer()
			_, err := cRules.CompileLoop(unsafe.Pointer(context), "ratelimit:src4", b.N)
			require.NoError(b, err)
		})
		b.Run(fmt.Sprintf("native/%d rules", count), func(b *testing.B) {
			context := newContext(b)
			for b.Loop() {
				nets, err := classify.CompileNet4(rules)
				require.NoError(b, err)
				attr := newAttr[layout.ClassifyAttrNet4](b, context)
				require.NoError(b, nets.Commit(attr, context, "ratelimit:src4"))
				classify.FreeNet4(attr)
			}
		})
	}
}

// Test_CompileNet4_UsesLessTemporaryMemoryThanC verifies that compiling
// an IPv4 network attribute natively, Go heap and shared memory
// together, writes less memory than the C compile writes into shared
// memory alone.
func Test_CompileNet4_UsesLessTemporaryMemoryThanC(t *testing.T) {
	for _, count := range net4Sizes {
		rules := randomNet4RulesOf(1, count)
		// The rules are copied to C before the measure, as the native
		// compile gets them ready in Go.
		cRules := coracle.NewCNet4Rules(cNet4Rules(rules))
		defer cRules.Free()
		viaC := measureFootprint(t, func(context *layout.MemoryContext) {
			_, err := cRules.CompileLoop(unsafe.Pointer(context), "ratelimit:src4", 1)
			require.NoError(t, err)
		})
		native := measureFootprint(t, func(context *layout.MemoryContext) {
			nets, err := classify.CompileNet4(rules)
			require.NoError(t, err)
			attr := newAttr[layout.ClassifyAttrNet4](t, context)
			require.NoError(t, nets.Commit(attr, context, "ratelimit:src4"))
			classify.FreeNet4(attr)
		})
		t.Logf("%d rules: C %d bytes of shared memory; native %d of shared memory and %d of Go heap",
			count, viaC.Shared, native.Shared, native.Heap)
		require.Less(t, native.Shared+native.Heap, viaC.Shared+viaC.Heap, "%d rules", count)
	}
}
