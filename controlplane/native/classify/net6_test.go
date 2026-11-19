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

// net6Network returns the IPv6 network of a high half address and
// prefix length and a low half address and prefix length.
func net6Network(hi uint64, hiBits int, lo uint64, loBits int) classify.Net6Network {
	var network classify.Net6Network
	hiMask, loMask := ^uint64(0)<<(64-hiBits), ^uint64(0)<<(64-loBits)
	binary.BigEndian.PutUint64(network.Addr[:8], hi)
	binary.BigEndian.PutUint64(network.Addr[8:], lo)
	binary.BigEndian.PutUint64(network.Mask[:8], hiMask)
	binary.BigEndian.PutUint64(network.Mask[8:], loMask)
	return network
}

// randomNet6RulesOf returns count reproducible IPv6 network rules:
// prefixes, networks masking a high and a low prefix, networks nested in
// either half, hosts under a few shared high halves, which split the low
// half finely, networks without a mask, and absent, empty and repeated
// rules.
func randomNet6RulesOf(seed uint64, count int) []classify.Net6Rule {
	random := rand.New(rand.NewPCG(seed, seed^0x1b873593))
	type known struct {
		Hi, Lo         uint64
		HiBits, LoBits int
	}
	var networks []known
	bases := [3]uint64{random.Uint64(), random.Uint64(), random.Uint64()}
	network := func() classify.Net6Network {
		var created known
		switch shape := random.IntN(8); {
		case shape >= 6:
			created = known{Hi: bases[random.IntN(len(bases))], HiBits: 64, Lo: random.Uint64(), LoBits: 64 - random.IntN(4)*16}
		case len(networks) > 0 && shape < 2:
			parent := networks[random.IntN(len(networks))]
			created = parent
			if parent.HiBits < 64 && random.IntN(2) == 0 {
				created.HiBits = min(64, parent.HiBits+1+random.IntN(16))
				created.Hi = parent.Hi | random.Uint64()>>parent.HiBits
			} else {
				created.LoBits = min(64, parent.LoBits+1+random.IntN(32))
				created.Lo = parent.Lo | random.Uint64()>>parent.LoBits
			}
		case shape == 2:
			created = known{Hi: random.Uint64() & 0xffff_ff00_0000_0000, HiBits: 24 + random.IntN(32), Lo: random.Uint64(), LoBits: 1 + random.IntN(64)}
		default:
			length := 8 + random.IntN(121)
			created = known{Hi: random.Uint64() & 0xff00_ffff_ffff_ffff, HiBits: min(64, length), Lo: random.Uint64(), LoBits: max(0, length-64)}
		}
		created.Hi &= ^uint64(0) << (64 - created.HiBits)
		created.Lo &= ^uint64(0) << (64 - created.LoBits)
		networks = append(networks, created)
		return net6Network(created.Hi, created.HiBits, created.Lo, created.LoBits)
	}
	rules := make([]classify.Net6Rule, count)
	for idx := range rules {
		switch shape := random.IntN(20); {
		case shape == 0:
			rules[idx] = classify.Net6Rule{Absent: true}
		case shape == 1:
			rules[idx] = classify.Net6Rule{}
		case shape == 2:
			rules[idx] = classify.Net6Rule{Networks: []classify.Net6Network{network(), {}}}
		case shape < 6 && idx > 0:
			rules[idx] = rules[random.IntN(idx)]
		default:
			nets := make([]classify.Net6Network, 1+random.IntN(4)+random.IntN(2)*random.IntN(24))
			for netIdx := range nets {
				nets[netIdx] = network()
			}
			rules[idx] = classify.Net6Rule{Networks: nets}
		}
	}
	return rules
}

// cNet6Rules returns the rules as the C oracle takes them.
func cNet6Rules(rules []classify.Net6Rule) []coracle.Net6Rule {
	converted := make([]coracle.Net6Rule, len(rules))
	for idx, rule := range rules {
		converted[idx].Absent = rule.Absent
		for _, network := range rule.Networks {
			converted[idx].Networks = append(converted[idx].Networks, coracle.Net6Network{Addr: network.Addr, Mask: network.Mask})
		}
	}
	return converted
}

// committedTable returns the cells of a committed table, or nil for a
// table never allocated.
func committedTable(table *layout.ValueTable) [][]uint32 {
	if table.Values.Get() == nil {
		return nil
	}
	return tableRows(table)
}

// requireNet6MatchesC compiles the rules through C and Go and requires
// the same rule groups, group values and maximum value, and committed
// halves and tables that answer every lookup alike.
func requireNet6MatchesC(t *testing.T, rules []classify.Net6Rule) {
	t.Helper()
	context := newContext(t)
	cRules := coracle.NewCNet6Rules(cNet6Rules(rules))
	defer cRules.Free()
	want, err := cRules.Compile(unsafe.Pointer(context), "ratelimit:src6")
	require.NoError(t, err)

	attr := newAttr[layout.ClassifyAttrNet6](t, context)
	got, err := classify.CompileNet6(rules, attr, context, "ratelimit:src6")
	require.NoError(t, err)
	defer classify.FreeNet6(attr)
	require.Equal(t, want.RuleGroups, got.RuleGroups, "rule groups")
	require.Equal(t, uint32(len(want.Groups)), got.GroupCount(), "group count")
	for group := range want.Groups {
		require.Equal(t, want.Groups[group], append([]uint32{}, got.Group(uint32(group))...), "values of group %d", group)
	}
	require.Equal(t, want.MaxValue, got.MaxValue, "max value")
	require.Equal(t, want.Stretches, coracle.Stretches(&attr.Hi, 8), "high half lookups")
	require.Equal(t, want.LoStretches, coracle.Stretches(&attr.Lo, 8), "low half lookups")
	require.Equal(t, want.Table, committedTable(&attr.Comb), "table")
	require.Equal(t, want.ContextName, shm.String(attr.Hi.MemoryContext.Name[:]), "context name")
}

// Test_CompileNet6_MatchesC verifies that an IPv6 network compile in Go
// gives every rule and group the values the C compile gives them, and
// commits halves and a table that answer every lookup as the C ones do.
func Test_CompileNet6_MatchesC(t *testing.T) {
	for seed := range uint64(300) {
		t.Run(fmt.Sprintf("seed %d", seed), func(t *testing.T) {
			random := rand.New(rand.NewPCG(seed, 17))
			requireNet6MatchesC(t, randomNet6RulesOf(seed, random.IntN(160)))
		})
	}
}

// Test_CompileNet6_GroupsEqualListsByContent verifies that rules sharing
// one network list and rules holding copies of it compile as C compiles
// them: the storage of a list never decides its group.
func Test_CompileNet6_GroupsEqualListsByContent(t *testing.T) {
	shared := randomNet6RulesOf(5, 8)[7].Networks
	copied := append([]classify.Net6Network{}, shared...)
	rules := []classify.Net6Rule{{Networks: shared}, {Networks: copied}, {Networks: shared}, {Networks: shared[:1]}, {Networks: copied[:1]}}
	requireNet6MatchesC(t, rules)
}

// Test_CompileNet6_RejectsMaskHalvesThatAreNotPrefixes verifies that a
// network whose mask half is not a prefix fails the compile, where C
// would read past its intervals.
func Test_CompileNet6_RejectsMaskHalvesThatAreNotPrefixes(t *testing.T) {
	network := net6Network(0x2a02_06b8_0000_0000, 32, 0, 0)
	network.Mask[2] = 0
	context := newContext(t)
	attr := newAttr[layout.ClassifyAttrNet6](t, context)
	_, err := classify.CompileNet6([]classify.Net6Rule{{Networks: []classify.Net6Network{network}}}, attr, context, "ratelimit:src6")
	require.Error(t, err)
}

// FuzzCompileNet6_MatchesC verifies that any IPv6 network ruleset
// compiles in Go to what it compiles to in C; every five bytes are one
// network - a high half top byte and length, a low half top byte and
// length - a zero first byte ending a rule.
func FuzzCompileNet6_MatchesC(f *testing.F) {
	f.Add([]byte{1, 0x2a, 32, 0, 0, 1, 0x2a, 48, 0, 0, 0, 0, 0, 0, 0, 1, 0x2a, 40, 0x10, 32})
	f.Add([]byte{1, 0x20, 64, 0x80, 1, 1, 0x20, 64, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		var rules []classify.Net6Rule
		var current classify.Net6Rule
		for len(data) >= 5 {
			if data[0] == 0 {
				rules = append(rules, current)
				current = classify.Net6Rule{Absent: data[1]&1 == 1}
				data = data[5:]
				continue
			}
			hiBits, loBits := int(data[2]%65), int(data[4]%65)
			hi := (uint64(data[1])<<56 | uint64(data[0])<<28) & (^uint64(0) << (64 - hiBits))
			lo := (uint64(data[3])<<56 | uint64(data[0])<<20) & (^uint64(0) << (64 - loBits))
			current.Networks = append(current.Networks, net6Network(hi, hiBits, lo, loBits))
			data = data[5:]
		}
		rules = append(rules, current)
		requireNet6MatchesC(t, rules)
	})
}

// net6Sizes are the ruleset sizes the IPv6 network compile is measured
// at.
var net6Sizes = []int{100, 1000, 3000}

// Benchmark_CompileNet6 measures compiling, committing and freeing an
// IPv6 network attribute through the C compile and natively.
func Benchmark_CompileNet6(b *testing.B) {
	for _, count := range net6Sizes {
		rules := randomNet6RulesOf(1, count)
		b.Run(fmt.Sprintf("c/%d rules", count), func(b *testing.B) {
			context := newContext(b)
			cRules := coracle.NewCNet6Rules(cNet6Rules(rules))
			defer cRules.Free()
			b.ResetTimer()
			_, err := cRules.CompileLoop(unsafe.Pointer(context), "ratelimit:src6", b.N)
			require.NoError(b, err)
		})
		b.Run(fmt.Sprintf("native/%d rules", count), func(b *testing.B) {
			context := newContext(b)
			for b.Loop() {
				attr := newAttr[layout.ClassifyAttrNet6](b, context)
				_, err := classify.CompileNet6(rules, attr, context, "ratelimit:src6")
				require.NoError(b, err)
				classify.FreeNet6(attr)
			}
		})
	}
}

// Test_CompileNet6_UsesLessTemporaryMemoryThanC verifies that compiling
// an IPv6 network attribute natively, Go heap and shared memory
// together, writes less memory than the C compile writes into shared
// memory alone.
func Test_CompileNet6_UsesLessTemporaryMemoryThanC(t *testing.T) {
	for _, count := range net6Sizes {
		rules := randomNet6RulesOf(1, count)
		// The rules are copied to C before the measure, as the native
		// compile gets them ready in Go.
		cRules := coracle.NewCNet6Rules(cNet6Rules(rules))
		defer cRules.Free()
		viaC := measureFootprintOf(t, arenaSize, func(context *layout.MemoryContext) {
			_, err := cRules.CompileLoop(unsafe.Pointer(context), "ratelimit:src6", 1)
			require.NoError(t, err)
		})
		native := measureFootprintOf(t, arenaSize, func(context *layout.MemoryContext) {
			attr := newAttr[layout.ClassifyAttrNet6](t, context)
			_, err := classify.CompileNet6(rules, attr, context, "ratelimit:src6")
			require.NoError(t, err)
			classify.FreeNet6(attr)
		})
		t.Logf("%d rules: C %d bytes of shared memory; native %d of shared memory and %d of Go heap",
			count, viaC.Shared, native.Shared, native.Heap)
		require.Less(t, native.Shared+native.Heap, viaC.Shared+viaC.Heap, "%d rules", count)
	}
}
