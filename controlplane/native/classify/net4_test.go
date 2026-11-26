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
	"github.com/yanet-platform/yanet2/controlplane/native/classify/internal/classifytest"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// randomNet4Rules returns count reproducible IPv4 network rules: nested
// prefixes, including one ending where its parent ends, unmasked
// addresses, and absent, empty, whole-space and repeated rules.
func randomNet4Rules(seed uint64, count int) []classify.Net4Rule {
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

// requireNet4MatchesReference compiles and commits the rules and requires
// the class the committed tree gives both ends of every elementary
// interval to reach, through the stage, exactly the rules matching the
// address.
func requireNet4MatchesReference(t *testing.T, rules []classify.Net4Rule) *classify.Net4 {
	t.Helper()
	memory := classifytest.LayOut(make([]byte, memorySize))
	nets, err := classify.CompileNet4(rules)
	require.NoError(t, err)
	require.NoError(t, nets.Commit(&memory.Net4, &memory.Context, "net4"))

	classes := classifytest.ReadNet4(&memory.Net4)
	err = classifytest.CheckStage(
		nets.Stage(),
		classifytest.Net4Keys(rules),
		classes.Class,
		func(rule int, addr uint32) bool { return classifytest.Net4Matches(rules[rule], addr) },
	)
	require.NoError(t, err)
	return nets
}

// Test_CompileNet4_MatchesReference verifies that both ends of every
// elementary interval of the address space get a class whose stage
// reaches exactly the rules matching the address.
func Test_CompileNet4_MatchesReference(t *testing.T) {
	for seed := range uint64(100) {
		t.Run(fmt.Sprintf("seed %d", seed), func(t *testing.T) {
			random := rand.New(rand.NewPCG(seed, 11))
			requireNet4MatchesReference(t, randomNet4Rules(seed, random.IntN(64)))
		})
	}
}

// Test_CompileNet4_GroupsEqualListsByContent verifies that rules holding
// one network list and rules holding copies of it fall into one group:
// the storage of a list never decides its group.
func Test_CompileNet4_GroupsEqualListsByContent(t *testing.T) {
	shared := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("10.1.0.0/16")}
	copied := append([]netip.Prefix{}, shared...)
	rules := []classify.Net4Rule{
		{Prefixes: shared},
		{Prefixes: copied},
		{Prefixes: shared},
		{Prefixes: shared[:1]},
		{Prefixes: copied[:1]},
	}
	nets := requireNet4MatchesReference(t, rules)
	require.Equal(t, []uint32{0, 0, 0, 1, 1}, nets.Stage().RuleGroups())
}

// Test_CompileNet4_RefusesOtherThanIPv4 verifies that a network that is
// not an IPv4 prefix is refused as an invalid rule.
func Test_CompileNet4_RefusesOtherThanIPv4(t *testing.T) {
	cases := []struct {
		name   string
		prefix netip.Prefix
	}{
		{name: "ipv6 prefix", prefix: netip.MustParsePrefix("2001:db8::/32")},
		{name: "ipv4-mapped ipv6 prefix", prefix: netip.MustParsePrefix("::ffff:10.0.0.0/104")},
		{name: "prefix longer than 32 bits", prefix: netip.PrefixFrom(netip.MustParseAddr("10.0.0.0"), 33)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rules := []classify.Net4Rule{{Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}, {Prefixes: []netip.Prefix{tc.prefix}}}
			_, err := classify.CompileNet4(rules)
			require.ErrorIs(t, err, classify.ErrInvalidRule)
		})
	}
}

// chunkedNet4Rules returns 40 rules of a /25 each over distinct second
// octets, each splitting two pages off the tree, so the tree outgrows its
// first chunk of pages and grows its chunk directory.
func chunkedNet4Rules() []classify.Net4Rule {
	rules := make([]classify.Net4Rule, 40)
	for idx := range rules {
		prefix := netip.MustParsePrefix(fmt.Sprintf("10.%d.%d.128/25", idx, idx%7))
		rules[idx] = classify.Net4Rule{Prefixes: []netip.Prefix{prefix}}
	}
	return rules
}

// Test_Net4Commit_ExhaustedLeavesAttributeZeroed verifies that an IPv4
// network commit refused any of its tree's allocations fails as exhausted,
// leaves the attribute zeroed and gives the allocator back every byte it
// took.
func Test_Net4Commit_ExhaustedLeavesAttributeZeroed(t *testing.T) {
	chunk := unsafe.Sizeof(layout.LpmPage{}) * 16
	cases := []struct {
		name string
		// kept are the requests the allocator's free blocks serve.
		kept []uintptr
	}{
		{name: "no room for the chunk directory"},
		{name: "no room for the first chunk", kept: []uintptr{shm.PtrSize}},
		{name: "no room for the grown chunk directory", kept: []uintptr{shm.PtrSize, chunk}},
		{name: "no room for a second chunk", kept: []uintptr{shm.PtrSize, chunk, 2 * shm.PtrSize}},
	}
	nets, err := classify.CompileNet4(chunkedNet4Rules())
	require.NoError(t, err)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := classifytest.LayOut(make([]byte, memorySize))
			nativetest.LeaveOnly(t, &memory.Allocator, tc.kept...)
			free := shmalloc.FreeSize(&memory.Allocator)

			require.ErrorIs(t, nets.Commit(&memory.Net4, &memory.Context, "net4"), shmalloc.ErrNoMemory)
			requireZeroed(t, &memory.Net4, "the attribute")
			require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
			require.True(t, memory.Context.FirstChild.IsNil(), "the tree's memory context is unlinked")
		})
	}
}

// Test_FreeNet4_ReturnsEveryByte verifies that freeing a committed IPv4
// network attribute zeroes it and gives the allocator back every byte the
// commit took.
func Test_FreeNet4_ReturnsEveryByte(t *testing.T) {
	memory := classifytest.LayOut(make([]byte, memorySize))
	free := shmalloc.FreeSize(&memory.Allocator)
	nets, err := classify.CompileNet4(chunkedNet4Rules())
	require.NoError(t, err)
	require.NoError(t, nets.Commit(&memory.Net4, &memory.Context, "net4"))

	classify.FreeNet4(&memory.Net4)
	requireZeroed(t, &memory.Net4, "the attribute")
	require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
	require.True(t, memory.Context.FirstChild.IsNil(), "the tree's memory context is unlinked")
}

// FuzzCompileNet4 verifies that any IPv4 network ruleset compiles to
// classes whose stage reaches exactly the rules matching each address;
// every six bytes are one prefix, a zero first byte ending a rule.
func FuzzCompileNet4(f *testing.F) {
	f.Add([]byte{1, 10, 0, 0, 0, 8, 1, 10, 1, 0, 0, 16, 0, 0, 0, 0, 0, 0})
	f.Add([]byte{1, 10, 0, 0, 0, 8, 0, 0, 0, 0, 0, 0, 1, 10, 255, 0, 0, 16})
	f.Fuzz(func(t *testing.T, data []byte) {
		var rules []classify.Net4Rule
		var current classify.Net4Rule
		for ; len(data) >= 6; data = data[6:] {
			if data[0] == 0 {
				rules = append(rules, current)
				current = classify.Net4Rule{Absent: data[1]&1 == 1}
				continue
			}
			addr := netip.AddrFrom4([4]byte(data[1:5]))
			current.Prefixes = append(current.Prefixes, netip.PrefixFrom(addr, int(data[5]%33)))
		}
		rules = append(rules, current)
		requireNet4MatchesReference(t, rules)
	})
}
