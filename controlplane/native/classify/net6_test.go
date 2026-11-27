//go:build linux && (amd64 || arm64)

package classify_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"net/netip"
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

// net6Network returns the IPv6 network of a high half address and
// prefix length and a low half address and prefix length.
func net6Network(hi uint64, hiBits int, lo uint64, loBits int) classify.Net6Network {
	var network classify.Net6Network
	binary.BigEndian.PutUint64(network.Addr[:8], hi)
	binary.BigEndian.PutUint64(network.Addr[8:], lo)
	binary.BigEndian.PutUint64(network.Mask[:8], ^uint64(0)<<(64-hiBits))
	binary.BigEndian.PutUint64(network.Mask[8:], ^uint64(0)<<(64-loBits))
	return network
}

// randomNet6Rules returns count reproducible IPv6 network rules:
// prefixes, networks masking a high and a low prefix, networks nested in
// either half, hosts under a few shared high halves, which split the low
// half finely, unmasked addresses, networks without a mask, and absent,
// empty and repeated rules.
func randomNet6Rules(seed uint64, count int) []classify.Net6Rule {
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
		hi, lo := created.Hi, created.Lo
		if random.IntN(4) == 0 {
			hi |= random.Uint64() >> created.HiBits
			lo |= random.Uint64() >> created.LoBits
		}
		return net6Network(hi, created.HiBits, lo, created.LoBits)
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
			nets := make([]classify.Net6Network, 1+random.IntN(3))
			for netIdx := range nets {
				nets[netIdx] = network()
			}
			rules[idx] = classify.Net6Rule{Networks: nets}
		}
	}
	return rules
}

// requireNet6MatchesReference compiles and commits the rules into a
// memory laid out over block and requires the class the committed
// attribute gives every cross of the ends of the elementary intervals of
// both halves to reach, through the stage, exactly the rules matching
// the address.
func requireNet6MatchesReference(t *testing.T, block []byte, rules []classify.Net6Rule) *classify.Net6 {
	t.Helper()
	memory := classifytest.LayOut(block)
	attr := classifytest.Alloc[layout.ClassifyAttrNet6](memory)
	nets, err := classify.CompileNet6(rules, attr, &memory.Context, "net6")
	require.NoError(t, err)

	classes := classifytest.ReadNet6(attr)
	err = classifytest.CheckStage(
		nets.Stage(),
		classifytest.Net6Keys(rules),
		classes.Class,
		func(rule int, key classifytest.Net6Key) bool { return classifytest.Net6Matches(rules[rule], key) },
	)
	require.NoError(t, err)
	return nets
}

// Test_CompileNet6_MatchesReference verifies that every cross of the
// ends of the elementary intervals of both halves gets a class whose
// stage reaches exactly the rules matching the address, on memory a
// previous owner left dirty, so no cell may rely on zeroed memory.
func Test_CompileNet6_MatchesReference(t *testing.T) {
	for seed := range uint64(60) {
		t.Run(fmt.Sprintf("seed %d", seed), func(t *testing.T) {
			random := rand.New(rand.NewPCG(seed, 17))
			dirty := bytes.Repeat([]byte{0x5a}, memorySize)
			requireNet6MatchesReference(t, dirty, randomNet6Rules(seed, random.IntN(24)))
		})
	}
}

// Test_CompileNet6_GroupsEqualListsByContent verifies that rules holding
// one network list and rules holding copies of it, unmasked addresses
// included, fall into one group.
func Test_CompileNet6_GroupsEqualListsByContent(t *testing.T) {
	shared := []classify.Net6Network{
		net6Network(0x2001_0db8_0000_0000, 32, 0, 0),
		net6Network(0x2001_0db8_0001_0000, 64, 0xff, 120-64),
	}
	copied := append([]classify.Net6Network{}, shared...)
	copied[0].Addr[15] = 1
	rules := []classify.Net6Rule{
		{Networks: shared},
		{Networks: copied},
		{Networks: shared},
		{Networks: shared[:1]},
		{Networks: copied[:1]},
	}
	nets := requireNet6MatchesReference(t, make([]byte, memorySize), rules)
	require.Equal(t, []uint32{0, 0, 0, 1, 1}, nets.Stage().RuleGroups())
}

// Test_CompileNet6_RefusesMaskHalvesThatAreNotPrefixes verifies that a
// network a half of whose mask is not a prefix is refused as an invalid
// rule before the compile allocates anything.
func Test_CompileNet6_RefusesMaskHalvesThatAreNotPrefixes(t *testing.T) {
	cases := []struct {
		name string
		// hole is the byte of the mask cleared in a /128 mask.
		hole int
	}{
		{name: "high half", hole: 2},
		{name: "low half", hole: 12},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			network := net6Network(0x2001_0db8_0000_0000, 64, 1, 64)
			network.Mask[tc.hole] = 0
			rules := []classify.Net6Rule{{Networks: []classify.Net6Network{net6Network(0x2001_0db8_0000_0000, 32, 0, 0)}}, {Networks: []classify.Net6Network{network}}}
			memory := classifytest.LayOut(make([]byte, memorySize))
			attr := classifytest.Alloc[layout.ClassifyAttrNet6](memory)
			free := shmalloc.FreeSize(&memory.Allocator)

			_, err := classify.CompileNet6(rules, attr, &memory.Context, "net6")
			require.ErrorIs(t, err, classify.ErrInvalidRule)
			requireZeroed(t, attr, "the attribute")
			require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
		})
	}
}

// Test_CompileNet6_ExhaustedLeavesAttributeZeroed verifies that an IPv6
// network compile refused any of its allocations fails as exhausted,
// leaves the attribute zeroed and gives the allocator back every byte it
// took.
func Test_CompileNet6_ExhaustedLeavesAttributeZeroed(t *testing.T) {
	context := unsafe.Sizeof(layout.MemoryContext{})
	chunk := unsafe.Sizeof(layout.LpmPage{}) * 16
	// A host splits the row of its high half into the classes on both
	// sides of it and its own: one row of two cells.
	cells := 2 * unsafe.Sizeof(uint32(0))
	cases := []struct {
		name string
		// kept are the requests the allocator's free blocks serve.
		kept []uintptr
	}{
		{name: "no room for the table's memory context"},
		{name: "no room for the table's row array", kept: []uintptr{context}},
		{name: "no room for a table row", kept: []uintptr{context, shm.PtrSize}},
		{name: "no room for the high half's tree", kept: []uintptr{context, shm.PtrSize, cells}},
		{name: "no room for the low half's tree", kept: []uintptr{context, shm.PtrSize, cells, shm.PtrSize, chunk}},
	}
	host := net6Network(0x2001_0db8_0000_0000, 64, 1, 64)
	rules := []classify.Net6Rule{{Networks: []classify.Net6Network{host}}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := classifytest.LayOut(make([]byte, memorySize))
			attr := classifytest.Alloc[layout.ClassifyAttrNet6](memory)
			nativetest.LeaveOnly(t, &memory.Allocator, tc.kept...)
			free := shmalloc.FreeSize(&memory.Allocator)

			_, err := classify.CompileNet6(rules, attr, &memory.Context, "net6")
			require.ErrorIs(t, err, shmalloc.ErrNoMemory)
			requireZeroed(t, attr, "the attribute")
			require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
			require.True(t, memory.Context.FirstChild.IsNil(), "the attribute's memory contexts are unlinked")
		})
	}
}

// FuzzCompileNet6 verifies that any IPv6 network ruleset compiles to
// classes whose stage reaches exactly the rules matching each address;
// every five bytes are one network, a high half top byte and length and
// a low half top byte and length, a zero first byte ending a rule.
func FuzzCompileNet6(f *testing.F) {
	f.Add([]byte{1, 0x2a, 32, 0, 0, 1, 0x2a, 48, 0, 0, 0, 0, 0, 0, 0, 1, 0x2a, 40, 0x10, 32})
	f.Add([]byte{1, 0x20, 64, 0x80, 1, 1, 0x20, 64, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		var rules []classify.Net6Rule
		var current classify.Net6Rule
		for ; len(data) >= 5; data = data[5:] {
			if data[0] == 0 {
				rules = append(rules, current)
				current = classify.Net6Rule{Absent: data[1]&1 == 1}
				continue
			}
			hi := uint64(data[1])<<56 | uint64(data[0])<<28
			lo := uint64(data[3])<<56 | uint64(data[0])<<20
			current.Networks = append(current.Networks, net6Network(hi, int(data[2]%65), lo, int(data[4]%65)))
		}
		rules = append(rules, current)
		requireNet6MatchesReference(t, make([]byte, memorySize), rules)
	})
}

// addr6 returns the bytes of an IPv6 address.
func addr6(text string) [16]byte {
	return netip.MustParseAddr(text).As16()
}

// key6 returns the halves of an IPv6 address.
func key6(addr [16]byte) classifytest.Net6Key {
	return classifytest.Net6Key{Hi: binary.BigEndian.Uint64(addr[:8]), Lo: binary.BigEndian.Uint64(addr[8:])}
}

// net6Filter is what the destination scenarios commit: the attribute
// and the rule map of its ruleset.
type net6Filter struct {
	Dst     layout.ClassifyAttrNet6
	RuleMap layout.Vline
}

// commitNet6Filter compiles the destination networks of the rules,
// decodes every rule and returns the rule an address resolves to through
// the committed lookups; the filter is freed at the end of the test,
// which requires every byte back.
func commitNet6Filter(t *testing.T, rules []classify.Net6Rule) func(destination [16]byte) uint32 {
	t.Helper()
	memory := classifytest.LayOut(make([]byte, memorySize))
	filter := classifytest.Alloc[net6Filter](memory)
	free := shmalloc.FreeSize(&memory.Allocator)
	nets, err := classify.CompileNet6(rules, &filter.Dst, &memory.Context, "test_net6_dst")
	require.NoError(t, err)
	members := make([]bool, len(rules))
	for rule := range members {
		members[rule] = true
	}
	require.NoError(t, nets.Stage().Decode(members, &filter.RuleMap, &memory.Context, "rule_map"))
	t.Cleanup(func() {
		classify.FreeDecode(&filter.RuleMap)
		classify.FreeNet6(&filter.Dst)
		require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
	})

	classes := classifytest.ReadNet6(&filter.Dst)
	ruleMap := classifytest.Values(&filter.RuleMap)
	return func(destination [16]byte) uint32 {
		return ruleMap[classes.Class(key6(destination))]
	}
}

// Test_CompileNet6_MaskSplit verifies that a mask spanning both halves
// matches exactly the addresses inside its intervals, with the bytes
// each half leaves unmasked free.
func Test_CompileNet6_MaskSplit(t *testing.T) {
	base := addr6("2001:db8:1100:0:55::")
	classifyDestination := commitNet6Filter(t, []classify.Net6Rule{{Networks: []classify.Net6Network{net6Network(0x2001_0db8_1100_0000, 40, 0x0055_0000_0000_0000, 24)}}})

	insideHi, insideLo, outsideHi, outsideLo := base, base, base, base
	insideHi[5], insideHi[7] = 0xff, 0x99
	insideLo[11], insideLo[15] = 0xff, 0x77
	outsideHi[4] = 0x12
	outsideLo[9] = 0x56
	require.Equal(t, uint32(0), classifyDestination(base), "the same address")
	require.Equal(t, uint32(0), classifyDestination(insideHi), "the unmasked high half bytes")
	require.Equal(t, uint32(0), classifyDestination(insideLo), "the unmasked low half bytes")
	require.Equal(t, classify.InvalidRule, classifyDestination(outsideHi), "an address outside the high interval")
	require.Equal(t, classify.InvalidRule, classifyDestination(outsideLo), "an address outside the low interval")
}

// Test_CompileNet6_SharedNetworks verifies that a network several rules
// share is grouped once and every address resolves to the first rule
// covering it.
func Test_CompileNet6_SharedNetworks(t *testing.T) {
	first := net6Network(0xbb00_0000_0000_0000, 8, 0, 0)
	second := net6Network(0xcc00_0000_0000_0000, 8, 0, 0)
	classifyDestination := commitNet6Filter(t, []classify.Net6Rule{
		{Networks: []classify.Net6Network{first}},
		{Networks: []classify.Net6Network{first, second}},
		{Networks: []classify.Net6Network{second}},
		{Networks: []classify.Net6Network{first}},
	})

	require.Equal(t, uint32(0), classifyDestination(addr6("bb00::")))
	require.Equal(t, uint32(1), classifyDestination(addr6("cc00::")))
	require.Equal(t, classify.InvalidRule, classifyDestination(addr6("dd00::")))
}

// Test_CompileNet6_NestedNetworks verifies that nested networks split
// the address space and a network without a mask closes it, the first
// matching rule winning.
func Test_CompileNet6_NestedNetworks(t *testing.T) {
	specific := addr6("2001:db8:1::")
	broad := addr6("2001:db8::")
	classifyDestination := commitNet6Filter(t, []classify.Net6Rule{
		{Networks: []classify.Net6Network{net6Network(0x2001_0db8_0001_0000, 48, 0, 0)}},
		{Networks: []classify.Net6Network{net6Network(0x2001_0db8_0000_0000, 32, 0, 0)}},
		{Networks: []classify.Net6Network{net6Network(0x2001_0db8_0000_0000, 0, 0, 0)}},
	})

	broadOnly := broad
	broadOnly[5] = 0x99
	require.Equal(t, uint32(0), classifyDestination(specific), "inside both networks")
	require.Equal(t, uint32(1), classifyDestination(broadOnly), "inside the broad network only")
	require.Equal(t, uint32(2), classifyDestination(addr6("dd00::")), "outside every masked network")
}

// net6PairFilter is what the source and destination scenario commits:
// both attributes, their joint and the rule map of the joint.
type net6PairFilter struct {
	Src     layout.ClassifyAttrNet6
	Dst     layout.ClassifyAttrNet6
	Joint   layout.ValueTable
	RuleMap layout.Vline
}

// Test_CompileNet6_SrcDst verifies that the source and destination
// attributes join into the classes of their pairs and an unmatched pair
// resolves to no rule.
func Test_CompileNet6_SrcDst(t *testing.T) {
	sources := []classify.Net6Network{net6Network(0xbb00_0000_0000_0000, 8, 0, 0), net6Network(0xcc00_0000_0000_0000, 8, 0, 0)}
	destinations := []classify.Net6Network{net6Network(0x1100_0000_0000_0000, 8, 0, 0), net6Network(0x2200_0000_0000_0000, 8, 0, 0)}
	sourceRules := []classify.Net6Rule{{Networks: sources[:1]}, {Networks: sources[:1]}, {Networks: sources[1:]}}
	destinationRules := []classify.Net6Rule{{Networks: destinations[:1]}, {Networks: destinations[1:]}, {Networks: destinations[:1]}}

	memory := classifytest.LayOut(make([]byte, memorySize))
	filter := classifytest.Alloc[net6PairFilter](memory)
	free := shmalloc.FreeSize(&memory.Allocator)
	source, err := classify.CompileNet6(sourceRules, &filter.Src, &memory.Context, "test_net6_src")
	require.NoError(t, err)
	destination, err := classify.CompileNet6(destinationRules, &filter.Dst, &memory.Context, "test_net6_dst")
	require.NoError(t, err)
	pair, err := source.Stage().Join(destination.Stage(), &filter.Joint, &memory.Context, "joint")
	require.NoError(t, err)
	require.NoError(t, pair.Decode([]bool{true, true, true}, &filter.RuleMap, &memory.Context, "rule_map"))

	sourceClasses, destinationClasses := classifytest.ReadNet6(&filter.Src), classifytest.ReadNet6(&filter.Dst)
	joint, ruleMap := classifytest.Table(&filter.Joint), classifytest.Values(&filter.RuleMap)
	classifyBoth := func(source, dest string) uint32 {
		return ruleMap[joint[sourceClasses.Class(key6(addr6(source)))][destinationClasses.Class(key6(addr6(dest)))]]
	}
	require.Equal(t, uint32(0), classifyBoth("bb00::", "1100::"))
	require.Equal(t, uint32(1), classifyBoth("bb00::", "2200::"))
	require.Equal(t, uint32(2), classifyBoth("cc00::", "1100::"))
	require.Equal(t, classify.InvalidRule, classifyBoth("cc00::", "2200::"))

	classify.FreeDecode(&filter.RuleMap)
	classify.FreeJoin(&filter.Joint)
	classify.FreeNet6(&filter.Src)
	classify.FreeNet6(&filter.Dst)
	require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
}

// Test_CompileNet6_FailedCompileZeroesAttr verifies that a compile
// refused its first allocation fails, frees nothing it did not allocate
// and leaves an attribute that held garbage zeroed.
func Test_CompileNet6_FailedCompileZeroesAttr(t *testing.T) {
	rules := []classify.Net6Rule{{Networks: []classify.Net6Network{net6Network(0x2000_0000_0000_0000, 16, 0, 8)}}}
	memory := classifytest.LayOut(make([]byte, memorySize))
	attr := classifytest.Alloc[layout.ClassifyAttrNet6](memory)
	garbage := unsafe.Slice((*byte)(unsafe.Pointer(attr)), unsafe.Sizeof(*attr))
	for idx := range garbage {
		garbage[idx] = 0x5a
	}
	nativetest.LeaveOnly(t, &memory.Allocator)
	free := shmalloc.FreeSize(&memory.Allocator)

	_, err := classify.CompileNet6(rules, attr, &memory.Context, "failed_compile")
	require.ErrorIs(t, err, shmalloc.ErrNoMemory)
	requireZeroed(t, attr, "the attribute")
	require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
	require.Zero(t, memory.Context.BfreeCount, "the frees of the context")
}
