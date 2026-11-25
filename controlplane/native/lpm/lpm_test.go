//go:build linux && (amd64 || arm64)

package lpm_test

import (
	"fmt"
	"net/netip"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/lpm"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// keyRange is an insert of the keys from the start through the end, both
// included, mapped to the value.
type keyRange struct {
	From  []byte
	To    []byte
	Value uint32
}

// prefixRange returns the insert of every address of a prefix.
func prefixRange(prefix string, value uint32) keyRange {
	parsed := netip.MustParsePrefix(prefix)
	from := parsed.Masked().Addr().AsSlice()
	to := parsed.Masked().Addr().AsSlice()
	for bit := parsed.Bits(); bit < len(to)*8; bit++ {
		to[bit/8] |= 0x80 >> (bit % 8)
	}
	return keyRange{From: from, To: to, Value: value}
}

// chunkedRanges returns count /25 ranges over distinct second octets,
// each splitting two pages off the tree, so 40 of them outgrow five page
// chunks and grow the chunk directory three times.
func chunkedRanges(count int) []keyRange {
	ranges := make([]keyRange, 0, count)
	for idx := range count {
		ranges = append(ranges, prefixRange(fmt.Sprintf("10.%d.%d.128/25", idx, idx%7), uint32(idx)))
	}
	return ranges
}

// treeMemory is what the memory of a test's tree holds before its arena.
type treeMemory struct {
	Allocator layout.BlockAllocator
	Context   layout.MemoryContext
	Tree      layout.Lpm
}

// newTreeMemory returns an allocator over a 1 MiB arena, a root memory
// context allocating from it and room for a tree, all in one block of Go
// memory that the relative pointers stay within.
func newTreeMemory() *treeMemory {
	backing := make([]byte, 1<<20)
	memory := (*treeMemory)(unsafe.Pointer(&backing[0]))
	arena := unsafe.Sizeof(*memory)
	shmalloc.Init(&memory.Allocator)
	shmalloc.PutArena(&memory.Allocator, unsafe.Pointer(&backing[arena]), uintptr(len(backing))-arena)
	shmalloc.ContextInit(&memory.Context, "owner", &memory.Allocator)
	return memory
}

// Test_Insert_RefusesKeySizes verifies that keys of no or differing sizes,
// or longer than 16 bytes, are refused before the tree is touched.
//
// The tree has no pages, so any read of one would fault.
func Test_Insert_RefusesKeySizes(t *testing.T) {
	cases := []struct {
		name string
		from []byte
		to   []byte
	}{
		{name: "empty keys", from: []byte{}, to: []byte{}},
		{name: "keys of differing sizes", from: []byte{10, 0, 0, 0}, to: []byte{10, 0}},
		{name: "keys longer than 16 bytes", from: make([]byte, 17), to: make([]byte, 17)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, lpm.Insert(&layout.Lpm{}, tc.from, tc.to, 1))
		})
	}
}

// Test_InitAndInsert_ExhaustionLeavesFreeableTree verifies that a tree
// whose memory runs out while it is initialised or inserted into fails
// as exhausted and leaves a tree whose free gives the allocator back every
// byte and unlinks the tree's memory context.
func Test_InitAndInsert_ExhaustionLeavesFreeableTree(t *testing.T) {
	chunk := unsafe.Sizeof(layout.LpmPage{}) * 16
	cases := []struct {
		name string
		// kept are the requests the allocator's free blocks serve.
		kept []uintptr
	}{
		{name: "no room for the chunk directory"},
		{name: "no room for the first chunk", kept: []uintptr{shm.PtrSize}},
		{name: "no room for a second chunk", kept: []uintptr{shm.PtrSize, chunk, 2 * shm.PtrSize}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := newTreeMemory()
			nativetest.LeaveOnly(t, &memory.Allocator, tc.kept...)
			free := shmalloc.FreeSize(&memory.Allocator)

			err := lpm.Init(&memory.Tree, &memory.Context, "tree")
			for _, insert := range chunkedRanges(40) {
				if err != nil {
					break
				}
				err = lpm.Insert(&memory.Tree, insert.From, insert.To, insert.Value)
			}
			require.ErrorIs(t, err, shmalloc.ErrNoMemory)
			lpm.Free(&memory.Tree)

			require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
			require.True(t, memory.Context.FirstChild.IsNil(), "the tree's memory context is unlinked")
		})
	}
}
