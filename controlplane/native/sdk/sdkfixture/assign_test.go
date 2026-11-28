//go:build sdk_rust_fixtures && linux && (amd64 || arm64)

package sdkfixture_test

import (
	"fmt"
	"math"
	"net/netip"
	"os"
	"slices"
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/classify"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/sdk/sdkfixture"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// Test_NewAssignConfig_RefusesInvalidInput verifies that a name its field
// cannot hold, a rule of an unknown family and a reversed range are refused
// before the agent is touched.
//
// The owner has no agent, so a build that reaches for its memory panics,
// which fails the case.
func Test_NewAssignConfig_RefusesInvalidInput(t *testing.T) {
	cases := []struct {
		name   string
		config string
		rule   sdkfixture.AssignRule
		want   error
	}{
		{
			name:   "name without room for its terminator",
			config: strings.Repeat("n", len(layout.CpModule{}.Name)),
			want:   cpconfig.ErrInvalidName,
		},
		{
			name:   "unknown family",
			config: "config",
			rule:   sdkfixture.AssignRule{Family: sdkfixture.AssignIPv6 + 1},
			want:   classify.ErrInvalidRule,
		},
		{
			name:   "protocol range that ends before it starts",
			config: "config",
			rule:   sdkfixture.AssignRule{Protos: []classify.LineRange{{From: 17, To: 6}}},
			want:   classify.ErrInvalidRule,
		},
		{
			name:   "port range that ends before it starts",
			config: "config",
			rule:   sdkfixture.AssignRule{Ports: []classify.PortRange{{From: 80, To: 53}}},
			want:   classify.ErrInvalidRule,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			require.NotPanics(t, func() {
				_, err = sdkfixture.NewAssignConfig(&agent.Agent{}, tc.config, []sdkfixture.AssignRule{tc.rule})
			})

			require.ErrorIs(t, err, tc.want)
		})
	}
}

// lookupTreesEnv names the environment variable of the file the
// classifiers of the Rust lookup bench are written to.
const lookupTreesEnv = "YANET_CLASSIFY_LOOKUP_TREES"

// lookupKeys is the count of keys each classifier of the bench is
// looked up at: one burst.
const lookupKeys = 32

// lookupMemorySize is the bytes of the block the bench's classifiers are
// built in.
const lookupMemorySize = 4 << 20

// lookupKey4 is an IPv4 key of the bench: the addresses, the protocol and
// the destination port.
type lookupKey4 struct {
	Src, Dst [4]byte
	Proto    uint8
	Port     uint16
}

// lookupKey6 is an IPv6 key of the bench.
type lookupKey6 struct {
	Src, Dst [16]byte
	Proto    uint8
	Port     uint16
}

// lookupTransport is a key of the bench's classifier of either family.
type lookupTransport struct {
	Proto uint8
	Port  uint16
}

// lookupTrees is the head of the block the Rust lookup bench reads: its
// own size, the classifiers, and the keys of each classifier with the
// index of the first rule each key matches, -1 for none.
//
// The bench mirrors it field for field and checks the size.
type lookupTrees struct {
	Size        uint64
	Classifiers sdkfixture.AssignConfig
	Keys4       [lookupKeys]lookupKey4
	Want4       [lookupKeys]int32
	Keys6       [lookupKeys]lookupKey6
	Want6       [lookupKeys]int32
	Transport   [lookupKeys]lookupTransport
	WantAny     [lookupKeys]int32
}

// lookupMemory is the block of the bench: the head at its start, then the
// allocator and the root context whose arena is the rest of the block.
type lookupMemory struct {
	Trees     lookupTrees
	Allocator layout.BlockAllocator
	Context   layout.MemoryContext
}

// lookupRules returns the rules of the bench: eight IPv4 rules of
// distinct source networks and shared transport ranges, eight IPv6 ones
// whose overlapping high halves need combination rows, and the odd IPv4
// ones again for either family, by protocol and port only.
func lookupRules() []sdkfixture.AssignRule {
	var v4, v6, either []sdkfixture.AssignRule
	for idx := range 8 {
		protos := []classify.LineRange{{From: 6, To: 17}}
		ports := []classify.PortRange{{From: 1000, To: uint16(2000 + idx*100)}}
		v4 = append(v4, sdkfixture.AssignRule{
			Family: sdkfixture.AssignIPv4,
			Src:    []netip.Prefix{netip.PrefixFrom(netip.AddrFrom4([4]byte{10, byte(idx), 0, 0}), 16)},
			Dst:    []netip.Prefix{netip.MustParsePrefix("172.16.0.0/12")},
			Protos: protos,
			Ports:  ports,
		})
		v6 = append(v6, sdkfixture.AssignRule{
			Family: sdkfixture.AssignIPv6,
			Src:    []netip.Prefix{netip.MustParsePrefix(fmt.Sprintf("2001:db8::%x:0:0/96", idx))},
			Dst:    []netip.Prefix{netip.MustParsePrefix("2001:db9::/64")},
			Protos: protos,
			Ports:  ports,
		})
		if idx%2 == 1 {
			either = append(either, sdkfixture.AssignRule{Family: sdkfixture.AssignAny, Protos: protos, Ports: ports})
		}
	}
	return slices.Concat(v4, v6, either)
}

// lookupEdges returns the protocols and the ports the bench's keys cycle
// through, ascending: both ends of every range of the rules, the values
// just outside them and the ends of the domains.
func lookupEdges(rules []sdkfixture.AssignRule) ([]uint8, []uint16) {
	protos := []uint8{0, math.MaxUint8}
	ports := []uint16{0, math.MaxUint16}
	for _, rule := range rules {
		for _, span := range rule.Protos {
			for _, proto := range []uint32{span.From - 1, span.From, span.To, span.To + 1} {
				if proto <= math.MaxUint8 {
					protos = append(protos, uint8(proto))
				}
			}
		}
		for _, span := range rule.Ports {
			ports = append(ports, span.From-1, span.From, span.To, span.To+1)
		}
	}
	slices.Sort(protos)
	slices.Sort(ports)
	return slices.Compact(protos), slices.Compact(ports)
}

// firstLookupRule returns the index of the first rule of the family that
// matches a packet, -1 for none, reading the rules one by one; an empty
// list of networks, protocols or ports matches every value.
func firstLookupRule(rules []sdkfixture.AssignRule, family sdkfixture.AssignFamily, src, dst netip.Addr, proto uint8, port uint16) int32 {
	inPrefixes := func(prefixes []netip.Prefix, addr netip.Addr) bool {
		return len(prefixes) == 0 || slices.ContainsFunc(prefixes, func(prefix netip.Prefix) bool {
			return prefix.Contains(addr)
		})
	}
	return int32(slices.IndexFunc(rules, func(rule sdkfixture.AssignRule) bool {
		return rule.Family == family &&
			inPrefixes(rule.Src, src) &&
			inPrefixes(rule.Dst, dst) &&
			(len(rule.Protos) == 0 || slices.ContainsFunc(rule.Protos, func(span classify.LineRange) bool {
				return span.From <= uint32(proto) && uint32(proto) <= span.To
			})) &&
			(len(rule.Ports) == 0 || slices.ContainsFunc(rule.Ports, func(span classify.PortRange) bool {
				return span.From <= port && port <= span.To
			}))
	}))
}

// Test_BuildAssignClassifiers_LookupBenchTrees verifies that the
// classifiers of the Rust lookup bench build in one block, and writes the
// block to the file the environment names for the bench.
//
// The block holds relative pointers only, so the bench reads a copy of it
// at any address of the same page alignment. Its head carries the keys
// and the first rule each matches, which the bench requires its lookups
// to find.
func Test_BuildAssignClassifiers_LookupBenchTrees(t *testing.T) {
	block, err := unix.Mmap(-1, 0, lookupMemorySize, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_PRIVATE|unix.MAP_ANONYMOUS)
	require.NoError(t, err)
	t.Cleanup(func() {
		nativetest.Unpoison(block)
		require.NoError(t, unix.Munmap(block))
	})
	memory := (*lookupMemory)(unsafe.Pointer(&block[0]))
	arena := unsafe.Sizeof(*memory)
	shmalloc.Init(&memory.Allocator)
	shmalloc.PutArena(&memory.Allocator, unsafe.Pointer(&block[arena]), uintptr(len(block))-arena)
	shmalloc.ContextInit(&memory.Context, "lookup bench", &memory.Allocator)

	rules := lookupRules()
	trees := &memory.Trees
	trees.Size = uint64(unsafe.Sizeof(*trees))
	require.NoError(t, sdkfixture.BuildAssignClassifiers(&memory.Context, &trees.Classifiers, rules))

	protos, ports := lookupEdges(rules)
	dst4 := netip.MustParseAddr("172.17.0.1")
	dst6 := netip.MustParseAddr("2001:db9::1")
	for idx := range lookupKeys {
		proto, port := protos[idx%len(protos)], ports[idx%len(ports)]
		src4 := netip.AddrFrom4([4]byte{10, byte(idx % 10), 0, 1})
		trees.Keys4[idx] = lookupKey4{Src: src4.As4(), Dst: dst4.As4(), Proto: proto, Port: port}
		trees.Want4[idx] = firstLookupRule(rules, sdkfixture.AssignIPv4, src4, dst4, proto, port)
		src6 := netip.MustParseAddr(fmt.Sprintf("2001:db8::%x:0:1", idx%10))
		trees.Keys6[idx] = lookupKey6{Src: src6.As16(), Dst: dst6.As16(), Proto: proto, Port: port}
		trees.Want6[idx] = firstLookupRule(rules, sdkfixture.AssignIPv6, src6, dst6, proto, port)
		trees.Transport[idx] = lookupTransport{Proto: proto, Port: port}
		trees.WantAny[idx] = firstLookupRule(rules, sdkfixture.AssignAny, netip.Addr{}, netip.Addr{}, proto, port)
	}

	if path := os.Getenv(lookupTreesEnv); path != "" {
		require.NoError(t, os.WriteFile(path, block, 0o644))
	}
}
