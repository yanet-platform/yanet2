//go:build linux && (amd64 || arm64) && cgo

package lpm_test

import (
	"testing"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/coracle"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/lpm"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// treeFixture is an in-process dataplane with an agent whose memory
// holds a tree header, ready for the tree to be initialised under the
// agent's memory context.
type treeFixture struct {
	*nativetest.Fixture
	owner *agent.Agent
	tree  *layout.Lpm
}

// newTreeFixture returns a tree fixture freed when the test ends.
func newTreeFixture(t *testing.T) *treeFixture {
	t.Helper()
	memory := nativetest.NewFixture(t, dataplaneut.Config{WorkerCount: 1})
	owner, err := agent.Attach(memory.Instance(), "owner", uint64(16*datasize.MB))
	require.NoError(t, err)
	tree := (*layout.Lpm)(shmalloc.ContextAlloc(&owner.Agent.MemoryContext, unsafe.Sizeof(layout.Lpm{})))
	require.NotNil(t, tree)
	return &treeFixture{Fixture: memory, owner: owner, tree: tree}
}

// buildC initialises the tree through C and inserts the ranges.
func (m *treeFixture) buildC(t *testing.T, ranges []keyRange) {
	t.Helper()
	tree := unsafe.Pointer(m.tree)
	require.NoError(t, coracle.LpmInit(tree, unsafe.Pointer(&m.owner.Agent.MemoryContext), "tree"))
	for _, insert := range ranges {
		require.NoError(t, coracle.LpmInsert(tree, insert.From, insert.To, insert.Value))
	}
}

// buildGo initialises the tree from Go and inserts the ranges.
func (m *treeFixture) buildGo(t *testing.T, ranges []keyRange) {
	t.Helper()
	require.NoError(t, lpm.Init(m.tree, &m.owner.Agent.MemoryContext, "tree"))
	for _, insert := range ranges {
		require.NoError(t, lpm.Insert(m.tree, insert.From, insert.To, insert.Value))
	}
}

// treeCases are the trees the build and the free are compared on.
var treeCases = []struct {
	name   string
	ranges []keyRange
}{
	{name: "no ranges"},
	{name: "one ipv4 range", ranges: []keyRange{prefixRange("10.0.0.0/8", 1)}},
	{name: "value with its top bit set", ranges: []keyRange{prefixRange("10.0.0.0/8", 0x80000001)}},
	{
		name: "overlapping ipv4 ranges, the later winning",
		ranges: []keyRange{
			prefixRange("10.0.0.0/8", 1),
			prefixRange("10.1.2.128/25", 2),
			prefixRange("0.0.0.0/0", 3),
			prefixRange("10.1.0.0/16", 4),
		},
	},
	{
		name:   "range off prefix boundaries",
		ranges: []keyRange{{From: []byte{10, 0, 0, 5}, To: []byte{10, 0, 3, 250}, Value: 7}},
	},
	{
		name:   "start past the end",
		ranges: []keyRange{{From: []byte{10, 0, 0, 9}, To: []byte{10, 0, 0, 1}, Value: 7}},
	},
	{
		name: "ipv6 ranges",
		ranges: []keyRange{
			prefixRange("2001:db8::/32", 1),
			prefixRange("2001:db8:1::/48", 2),
			prefixRange("::1/128", 3),
			prefixRange("fe80::/10", 4),
		},
	},
	{name: "chunk directory grown", ranges: chunkedRanges(40)},
}

// Test_Insert_MatchesC verifies that a tree Go initialises and inserts
// into leaves the storage as C building it from the same ranges does.
func Test_Insert_MatchesC(t *testing.T) {
	for _, tc := range treeCases {
		t.Run(tc.name, func(t *testing.T) {
			memory := newTreeFixture(t)
			memory.CompareWithC(
				t,
				func() { memory.buildC(t, tc.ranges) },
				func() { memory.buildGo(t, tc.ranges) },
			)
		})
	}
}

// Test_Free_MatchesC verifies that freeing a tree from Go leaves the
// storage as C's free does.
func Test_Free_MatchesC(t *testing.T) {
	for _, tc := range treeCases {
		t.Run(tc.name, func(t *testing.T) {
			memory := newTreeFixture(t)
			memory.buildC(t, tc.ranges)
			memory.CompareWithC(
				t,
				func() { coracle.LpmFree(unsafe.Pointer(memory.tree)) },
				func() { lpm.Free(memory.tree) },
			)
		})
	}
}

// Test_InitAndInsert_ExhaustionLeavesFreeableTree verifies that a tree
// whose memory runs out while it is initialised or inserted into fails
// as exhausted and leaves a tree whose free gives the agent back every
// byte and unlinks the tree's memory context.
func Test_InitAndInsert_ExhaustionLeavesFreeableTree(t *testing.T) {
	chunk := unsafe.Sizeof(layout.LpmPage{}) * 16
	cases := []struct {
		name string
		// kept are the requests the agent's free blocks serve.
		kept []uintptr
	}{
		{name: "no room for the chunk directory"},
		{name: "no room for the first chunk", kept: []uintptr{shm.PtrSize}},
		{name: "no room for a second chunk", kept: []uintptr{shm.PtrSize, chunk, 2 * shm.PtrSize}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := newTreeFixture(t)
			context := &memory.owner.Agent.MemoryContext
			nativetest.LeaveOnly(t, &memory.owner.Agent.BlockAllocator, tc.kept...)
			free := shmalloc.FreeSize(&memory.owner.Agent.BlockAllocator)

			err := lpm.Init(memory.tree, context, "tree")
			for _, insert := range chunkedRanges(40) {
				if err != nil {
					break
				}
				err = lpm.Insert(memory.tree, insert.From, insert.To, insert.Value)
			}
			require.ErrorIs(t, err, shmalloc.ErrNoMemory)
			lpm.Free(memory.tree)

			require.Equal(t, free, shmalloc.FreeSize(&memory.owner.Agent.BlockAllocator), "the agent's free bytes")
			require.True(t, context.FirstChild.IsNil(), "the tree's memory context is unlinked")
		})
	}
}
