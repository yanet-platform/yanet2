//go:build linux && (amd64 || arm64)

package decapshm_test

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
	"github.com/yanet-platform/yanet2/modules/decap/controlplane/decapshm"
)

// manyPrefixes returns count /25 prefixes over distinct second octets,
// each splitting two pages off the ipv4 tree, so 40 of them outgrow five
// page chunks.
func manyPrefixes(count int) []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, count)
	for idx := range count {
		prefixes = append(prefixes, netip.MustParsePrefix(fmt.Sprintf("10.%d.%d.128/25", idx, idx%7)))
	}
	return prefixes
}

// benchmarkPrefixes returns 256 /24 prefixes over 16 second octets, a
// tree of 18 pages.
func benchmarkPrefixes() []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, 256)
	for idx := range 256 {
		prefixes = append(prefixes, netip.MustParsePrefix(fmt.Sprintf("10.%d.%d.0/24", idx%16, idx/16)))
	}
	return prefixes
}

// ownerMemory is what the memory of a test's agent holds before its
// arena: a dataplane zone header whose module table names decap, and the
// agent.
type ownerMemory struct {
	Dp      layout.DpConfig
	Modules [1]layout.DpModule
	Agent   layout.Agent
}

// newOwner returns an agent over a 1 MiB arena serving a dataplane that
// knows the decap module, all in one block of Go memory that the relative
// pointers stay within.
func newOwner() *agent.Agent {
	backing := make([]byte, 1<<20)
	memory := (*ownerMemory)(unsafe.Pointer(&backing[0]))
	shm.CopyString(memory.Modules[0].Name[:], "decap")
	memory.Dp.ModuleCount = uint64(len(memory.Modules))
	memory.Dp.DpModules.Set(&memory.Modules[0])

	memory.Agent.DpConfig.Set(&memory.Dp)
	arena := unsafe.Sizeof(*memory)
	shmalloc.Init(&memory.Agent.BlockAllocator)
	shmalloc.PutArena(&memory.Agent.BlockAllocator, unsafe.Pointer(&backing[arena]), uintptr(len(backing))-arena)
	shmalloc.ContextInit(&memory.Agent.MemoryContext, "owner", &memory.Agent.BlockAllocator)
	return &agent.Agent{Instance: &zone.Instance{Dp: &memory.Dp}, Agent: &memory.Agent}
}

// Test_NewConfig_RefusesInvalidInput verifies that a name the module's
// field cannot hold whole and a prefix naming no network are refused
// before the agent is touched.
//
// The owner has no agent, so any allocation from it would fault.
func Test_NewConfig_RefusesInvalidInput(t *testing.T) {
	valid := netip.MustParsePrefix("10.0.0.0/8")
	cases := []struct {
		name     string
		config   string
		prefixes []netip.Prefix
		want     error
	}{
		{
			name:   "name without room for its terminator",
			config: strings.Repeat("n", len(layout.CpModule{}.Name)),
			want:   cpconfig.ErrInvalidName,
		},
		{
			name:     "zero prefix",
			config:   "d",
			prefixes: []netip.Prefix{{}},
			want:     decapshm.ErrInvalidPrefix,
		},
		{
			name:     "prefix longer than its address",
			config:   "d",
			prefixes: []netip.Prefix{netip.PrefixFrom(netip.MustParseAddr("10.0.0.0"), 33)},
			want:     decapshm.ErrInvalidPrefix,
		},
		{
			name:     "invalid prefix after a valid one",
			config:   "d",
			prefixes: []netip.Prefix{valid, {}},
			want:     decapshm.ErrInvalidPrefix,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decapshm.NewConfig(&agent.Agent{}, tc.config, tc.prefixes)
			require.ErrorIs(t, err, tc.want)
		})
	}
}

// Test_AddPrefix_RefusesInvalidPrefix verifies that a prefix naming no
// network is refused before the configuration is touched.
//
// The module heads no configuration, so any write to its trees would
// fault.
func Test_AddPrefix_RefusesInvalidPrefix(t *testing.T) {
	cases := []struct {
		name   string
		prefix netip.Prefix
	}{
		{name: "zero prefix"},
		{name: "prefix longer than its address", prefix: netip.PrefixFrom(netip.MustParseAddr("10.0.0.0"), 33)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorIs(t, decapshm.AddPrefix(&layout.CpModule{}, tc.prefix), decapshm.ErrInvalidPrefix)
		})
	}
}

// Test_NewConfig_ExhaustionFreesEverything verifies that a decap build
// the agent's memory runs out for fails as exhausted where it ran out,
// gives the agent back every byte, unlinks its memory contexts and
// returns its hold on the agent.
func Test_NewConfig_ExhaustionFreesEverything(t *testing.T) {
	chunk := unsafe.Sizeof(layout.LpmPage{}) * 16
	cases := []struct {
		name string
		// kept are the requests the agent's free blocks serve.
		kept []uintptr
		// place is the step the build fails in.
		place string
	}{
		{name: "no room for the configuration", place: "allocate decap module"},
		{name: "no room for the module counters", kept: []uintptr{unsafe.Sizeof(layout.DecapModuleConfig{})}, place: "register"},
		{name: "no room for the ipv4 tree", kept: []uintptr{chunk}, place: "init ipv4 prefix tree"},
		{name: "no room for the ipv6 tree", kept: []uintptr{2 * chunk}, place: "init ipv6 prefix tree"},
		{name: "no room for the prefixes", kept: []uintptr{4 * chunk}, place: "add prefix"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owner := newOwner()
			allocator := &owner.Agent.BlockAllocator
			nativetest.LeaveOnly(t, allocator, tc.kept...)
			free := shmalloc.FreeSize(allocator)

			_, err := decapshm.NewConfig(owner, "d", manyPrefixes(40))

			require.ErrorIs(t, err, shmalloc.ErrNoMemory)
			require.ErrorContains(t, err, tc.place)
			require.Equal(t, free, shmalloc.FreeSize(allocator), "the agent's free bytes")
			require.True(t, owner.Agent.MemoryContext.FirstChild.IsNil(), "the agent's memory contexts")
			require.Zero(t, owner.Agent.LoadedEntityCount, "the agent's holds")
		})
	}
}

// Test_FreeConfig_ReturnsEveryByte verifies that freeing a decap
// configuration gives its agent back every byte the build took, unlinks
// its memory contexts and returns the hold the free took on the agent.
func Test_FreeConfig_ReturnsEveryByte(t *testing.T) {
	owner := newOwner()
	allocator := &owner.Agent.BlockAllocator
	free := shmalloc.FreeSize(allocator)
	module, err := decapshm.NewConfig(owner, "d", append(manyPrefixes(40), netip.MustParsePrefix("2001:db8::/32")))
	require.NoError(t, err)

	require.NoError(t, decapshm.FreeConfig(module))

	require.Equal(t, free, shmalloc.FreeSize(allocator), "the agent's free bytes")
	require.True(t, owner.Agent.MemoryContext.FirstChild.IsNil(), "the agent's memory contexts")
	require.Zero(t, owner.Agent.LoadedEntityCount, "the agent's holds")
}

// Benchmark_NewConfig measures building a decap configuration of 256
// prefixes; each build is freed outside the measurement.
func Benchmark_NewConfig(b *testing.B) {
	prefixes := benchmarkPrefixes()
	owner := newOwner()
	for b.Loop() {
		module, err := decapshm.NewConfig(owner, "d", prefixes)
		require.NoError(b, err)
		b.StopTimer()
		require.NoError(b, decapshm.FreeConfig(module))
		b.StartTimer()
	}
}
