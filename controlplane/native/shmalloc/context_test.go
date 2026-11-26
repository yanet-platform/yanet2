//go:build linux && (amd64 || arm64)

package shmalloc_test

import (
	"bytes"
	"fmt"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// Test_ContextFreeLocked_IgnoresNoBlock verifies that a free of no block
// under the allocator's lock leaves the allocator and the context's
// counters as they were.
func Test_ContextFreeLocked_IgnoresNoBlock(t *testing.T) {
	type memory struct {
		Allocator layout.BlockAllocator
		Context   layout.MemoryContext
	}
	zones := new(memory)
	shmalloc.Init(&zones.Allocator)
	shmalloc.ContextInit(&zones.Context, "root", &zones.Allocator)
	image := unsafe.Slice((*byte)(unsafe.Pointer(zones)), unsafe.Sizeof(*zones))
	want := bytes.Clone(image)

	shmalloc.Lock(&zones.Allocator)
	shmalloc.ContextFreeLocked(&zones.Context, nil, 64)
	shmalloc.Unlock(&zones.Allocator)

	require.Equal(t, want, image)
}

// contextChurn initialises transient under root and finalises it again,
// rounds times, allocating blocks of sizes through transient, persistent
// and root in turn while it lives, and fails when a block's mark was
// overwritten while it was held.
func contextChurn(root, persistent, transient *layout.MemoryContext, sizes []uintptr, rounds int, mark byte) error {
	owners := []*layout.MemoryContext{transient, persistent, root}
	blocks := make([][]byte, len(sizes))
	for range rounds {
		shmalloc.ContextInitFrom(transient, root, "transient")
		for idx, size := range sizes {
			address := shmalloc.ContextAlloc(owners[idx%3], size)
			if address == nil {
				return fmt.Errorf("no block of %d bytes", size)
			}
			blocks[idx] = unsafe.Slice((*byte)(address), size)
			for pos := range blocks[idx] {
				blocks[idx][pos] = mark
			}
		}
		for idx, block := range blocks {
			for pos, value := range block {
				if value != mark {
					return fmt.Errorf("block of %d bytes overwritten at %d", len(block), pos)
				}
			}
			shmalloc.ContextFree(owners[idx%3], unsafe.Pointer(&block[0]), sizes[idx])
		}
		shmalloc.ContextFini(transient)
	}
	return nil
}

// watchdog aborts the test binary when the test runs past limit.
//
// A context tree whose sibling links a race tangled into a cycle makes a
// teardown walk spin, which no test failure can stop.
func watchdog(t *testing.T, limit time.Duration) {
	t.Helper()
	timer := time.AfterFunc(limit, func() {
		panic(fmt.Sprintf("%s did not finish in %s", t.Name(), limit))
	})
	t.Cleanup(func() { timer.Stop() })
}

// requireBalanced fails when a context's frees do not match its
// allocations.
func requireBalanced(t *testing.T, context *layout.MemoryContext) {
	t.Helper()
	require.Equal(t, context.BallocCount, context.BfreeCount, "context %q", shm.String(context.Name[:]))
	require.Equal(t, context.BallocSize, context.BfreeSize, "context %q", shm.String(context.Name[:]))
}

// Test_Context_ConcurrentChurn verifies that goroutines allocating
// through sibling contexts of one root, and adding and removing siblings
// of their own, lose no block, no count and no sibling.
//
// Every worker keeps a persistent child for its whole run and adds and
// removes a transient one each round, allocating through all three; in
// the end only the persistent children remain under the root.
func Test_Context_ConcurrentChurn(t *testing.T) {
	// Few blocks and many rounds keep the splices of the tree frequent.
	const (
		workers = 8
		rounds  = 5000
		blocks  = 3
	)
	watchdog(t, time.Minute)
	memory, allocator := newAllocatorRegion(t)
	root := (*layout.MemoryContext)(memory.context(0))
	shmalloc.ContextInit(root, "root", allocator)
	persistent := func(worker int) *layout.MemoryContext {
		return (*layout.MemoryContext)(memory.context(1 + worker))
	}
	transient := func(worker int) *layout.MemoryContext {
		return (*layout.MemoryContext)(memory.context(1 + workers + worker))
	}
	for worker := range workers {
		shmalloc.ContextInitFrom(persistent(worker), root, fmt.Sprintf("worker %d", worker))
	}
	free := shmalloc.FreeSize(allocator)
	sizes := churnSizes()[:blocks]

	var group errgroup.Group
	for worker := range workers {
		group.Go(func() error {
			return contextChurn(root, persistent(worker), transient(worker), sizes, rounds, byte(1+worker))
		})
	}

	require.NoError(t, group.Wait())
	require.Equal(t, free, shmalloc.FreeSize(allocator))
	requireBalanced(t, root)
	var children []*layout.MemoryContext
	for child := root.FirstChild.Get(); child != nil && len(children) <= contextCount; child = child.NextSibling.Get() {
		require.Same(t, root, child.Parent.Get())
		requireBalanced(t, child)
		children = append(children, child)
	}
	var want []*layout.MemoryContext
	for worker := range workers {
		want = append(want, persistent(worker))
	}
	require.ElementsMatch(t, want, children)
}

// Test_ContextRealloc_MovesBlock verifies that a move keeps the bytes both
// sizes cover, counts an allocation of the new size and a free of the old
// one, if any, against the context, and that freeing the moved block
// returns every byte to the allocator.
func Test_ContextRealloc_MovesBlock(t *testing.T) {
	cases := []struct {
		name       string
		oldSize    uintptr
		newSize    uintptr
		allocCount uint64
		freeCount  uint64
	}{
		{name: "grow", oldSize: 64, newSize: 200, allocCount: 1, freeCount: 1},
		{name: "shrink", oldSize: 200, newSize: 64, allocCount: 1, freeCount: 1},
		{name: "from no block", oldSize: 0, newSize: 64, allocCount: 1, freeCount: 0},
		{name: "to zero size", oldSize: 64, newSize: 0, allocCount: 0, freeCount: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory, allocator := newAllocatorRegion(t)
			context := (*layout.MemoryContext)(memory.context(0))
			shmalloc.ContextInit(context, "realloc", allocator)
			free := shmalloc.FreeSize(allocator)
			var data unsafe.Pointer
			if tc.oldSize != 0 {
				data = shmalloc.ContextAlloc(context, tc.oldSize)
				require.NotNil(t, data)
				block := unsafe.Slice((*byte)(data), tc.oldSize)
				for idx := range block {
					block[idx] = byte(7 + idx)
				}
			}
			kept := min(tc.oldSize, tc.newSize)
			var want []byte
			if kept != 0 {
				want = bytes.Clone(unsafe.Slice((*byte)(data), kept))
			}
			allocCount, allocSize := context.BallocCount, context.BallocSize
			freeCount, freeSize := context.BfreeCount, context.BfreeSize

			moved := shmalloc.ContextRealloc(context, data, tc.oldSize, tc.newSize)

			require.Equal(t, tc.newSize == 0, moved == nil)
			if kept != 0 {
				require.Equal(t, want, unsafe.Slice((*byte)(moved), kept), "the kept bytes")
			}
			require.Equal(t, allocCount+tc.allocCount, context.BallocCount)
			require.Equal(t, allocSize+tc.allocCount*uint64(tc.newSize), context.BallocSize)
			require.Equal(t, freeCount+tc.freeCount, context.BfreeCount)
			require.Equal(t, freeSize+tc.freeCount*uint64(tc.oldSize), context.BfreeSize)

			shmalloc.ContextFree(context, moved, tc.newSize)
			require.Equal(t, free, shmalloc.FreeSize(allocator), "free bytes after the final free")
		})
	}
}

// Test_ContextRealloc_KeepsBlockWhenExhausted verifies that a move to a
// size no free block holds returns nil and leaves the old block
// allocated, its bytes and the context's counts as they were.
func Test_ContextRealloc_KeepsBlockWhenExhausted(t *testing.T) {
	memory, allocator := newAllocatorRegion(t)
	context := (*layout.MemoryContext)(memory.context(0))
	shmalloc.ContextInit(context, "exhausted", allocator)
	data := shmalloc.ContextAlloc(context, 64)
	require.NotNil(t, data)
	block := unsafe.Slice((*byte)(data), 64)
	for idx := range block {
		block[idx] = byte(7 + idx)
	}
	want := bytes.Clone(block)
	counts := bytes.Clone(bytesOf(context))
	free := shmalloc.FreeSize(allocator)

	require.Nil(t, shmalloc.ContextRealloc(context, data, 64, arenaSize+1))

	require.Equal(t, want, block)
	require.Equal(t, counts, bytesOf(context), "the context")
	require.Equal(t, free, shmalloc.FreeSize(allocator))
}
