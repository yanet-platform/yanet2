//go:build linux && (amd64 || arm64)

package shmalloc_test

import (
	"fmt"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/controlplane/native/internal/coracle"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// contextImplementation is one way to run memory-context operations.
type contextImplementation struct {
	init     func(context unsafe.Pointer, name string, allocator unsafe.Pointer)
	initFrom func(context, parent unsafe.Pointer, name string)
	fini     func(context unsafe.Pointer)
	alloc    func(context unsafe.Pointer, size uintptr) unsafe.Pointer
	free     func(context, block unsafe.Pointer, size uintptr)
	realloc  func(context, data unsafe.Pointer, oldSize, newSize uintptr) unsafe.Pointer
}

var cContext = contextImplementation{
	init:     coracle.ContextInit,
	initFrom: coracle.ContextInitFrom,
	fini:     coracle.ContextFini,
	alloc:    coracle.ContextAlloc,
	free:     coracle.ContextFree,
	realloc:  coracle.ContextRealloc,
}

var goContext = contextImplementation{
	init: func(context unsafe.Pointer, name string, allocator unsafe.Pointer) {
		shmalloc.ContextInit((*layout.MemoryContext)(context), name, (*layout.BlockAllocator)(allocator))
	},
	initFrom: func(context, parent unsafe.Pointer, name string) {
		shmalloc.ContextInitFrom((*layout.MemoryContext)(context), (*layout.MemoryContext)(parent), name)
	},
	fini: func(context unsafe.Pointer) {
		shmalloc.ContextFini((*layout.MemoryContext)(context))
	},
	alloc: func(context unsafe.Pointer, size uintptr) unsafe.Pointer {
		return shmalloc.ContextAlloc((*layout.MemoryContext)(context), size)
	},
	free: func(context, block unsafe.Pointer, size uintptr) {
		shmalloc.ContextFree((*layout.MemoryContext)(context), block, size)
	},
	realloc: func(context, data unsafe.Pointer, oldSize, newSize uintptr) unsafe.Pointer {
		return shmalloc.ContextRealloc((*layout.MemoryContext)(context), data, oldSize, newSize)
	},
}

// newContextRegion returns a region whose allocator C set up over the
// whole arena, for context operations to run on.
func newContextRegion(t *testing.T) *region {
	t.Helper()
	memory := newRegion(t)
	coracle.BlockAllocatorInit(memory.allocator())
	coracle.PutArena(memory.allocator(), memory.arena(), arenaSize)
	return memory
}

// runContexts builds a root context with three children, allocates and
// frees through them, finalises the middle child and returns the region's
// final state.
func runContexts(t *testing.T, implementation contextImplementation) state {
	t.Helper()
	memory := newContextRegion(t)

	implementation.init(memory.context(0), "root", memory.allocator())
	names := []string{
		"first",
		"second",
		"a name well over the sixty-four bytes a context keeps for its name, which C cuts to sixty-three",
	}
	for idx, name := range names {
		implementation.initFrom(memory.context(idx+1), memory.context(0), name)
	}
	var blocks []block
	for idx := range 300 {
		context := memory.context(1 + idx%3)
		size := uintptr(8 + idx*37%4096)
		blocks = append(blocks, block{implementation.alloc(context, size), size})
	}
	for idx := 0; idx < len(blocks); idx += 2 {
		implementation.free(memory.context(1+idx%3), blocks[idx].address, blocks[idx].size)
	}
	implementation.fini(memory.context(2))
	return memory.state()
}

// Test_Context_MatchesC verifies that memory contexts initialised,
// counted, linked into a tree and finalised by Go leave the bytes C
// leaves.
func Test_Context_MatchesC(t *testing.T) {
	requireSameState(t, runContexts(t, cContext), runContexts(t, goContext))
}

// Test_ContextFini_MatchesC verifies that tearing a context tree down in
// any order leaves, after every step, the bytes C leaves.
//
// The root has three children, the middle one two of its own. The head
// child is finalised first, then the middle child while its children
// live, which detaches them, then those children, then one context twice;
// a free of no block in between changes nothing.
func Test_ContextFini_MatchesC(t *testing.T) {
	const (
		root = iota
		first
		middle
		head
		grandchild
		otherGrandchild
	)
	steps := []struct {
		name string
		run  func(implementation contextImplementation, memory *region)
	}{
		{"build the tree", func(implementation contextImplementation, memory *region) {
			implementation.init(memory.context(root), "root", memory.allocator())
			for _, idx := range []int{first, middle, head} {
				implementation.initFrom(memory.context(idx), memory.context(root), fmt.Sprintf("child %d", idx))
			}
			for _, idx := range []int{grandchild, otherGrandchild} {
				implementation.initFrom(memory.context(idx), memory.context(middle), fmt.Sprintf("grandchild %d", idx))
			}
			for idx := range otherGrandchild + 1 {
				implementation.alloc(memory.context(idx), uintptr(16+idx*24))
			}
		}},
		{"free no block", func(implementation contextImplementation, memory *region) {
			implementation.free(memory.context(root), nil, 64)
		}},
		{"finalise the head child", func(implementation contextImplementation, memory *region) {
			implementation.fini(memory.context(head))
		}},
		{"finalise a child with children", func(implementation contextImplementation, memory *region) {
			implementation.fini(memory.context(middle))
		}},
		{"finalise the detached children", func(implementation contextImplementation, memory *region) {
			implementation.fini(memory.context(grandchild))
			implementation.fini(memory.context(otherGrandchild))
		}},
		{"finalise a context twice", func(implementation contextImplementation, memory *region) {
			implementation.fini(memory.context(first))
			implementation.fini(memory.context(first))
		}},
	}
	want := newContextRegion(t)
	got := newContextRegion(t)
	for _, step := range steps {
		step.run(cContext, want)
		step.run(goContext, got)
		requireSameState(t, want.state(), got.state())
	}
}

// Test_ContextFreeLocked_IgnoresNoBlock verifies that a free of no block
// under the allocator's lock leaves the allocator and the context's
// counters as they were.
func Test_ContextFreeLocked_IgnoresNoBlock(t *testing.T) {
	memory := newContextRegion(t)
	allocator := (*layout.BlockAllocator)(memory.allocator())
	context := (*layout.MemoryContext)(memory.context(0))
	shmalloc.ContextInit(context, "root", allocator)
	want := memory.state()

	shmalloc.Lock(allocator)
	shmalloc.ContextFreeLocked(context, nil, 64)
	shmalloc.Unlock(allocator)

	requireSameState(t, want, memory.state())
}

// contextChurn is the Go side of the concurrent context test, as the C
// one runs it: it initialises transient under root and finalises it
// again, rounds times, allocating blocks of sizes through transient,
// persistent and root in turn while it lives, and fails when a block's
// mark was overwritten while it was held.
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
// teardown walk spin on a C thread no test failure can stop.
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

// Test_Context_ConcurrentWithC verifies that goroutines and C threads
// allocating through sibling contexts of one root, and adding and
// removing siblings of their own, lose no block, no count and no sibling.
//
// Every worker keeps a persistent child for its whole run and adds and
// removes a transient one each round, allocating through all three; in
// the end only the persistent children remain under the root.
func Test_Context_ConcurrentWithC(t *testing.T) {
	// Few blocks and many rounds keep the splices of the tree frequent.
	const (
		workers = 4
		rounds  = 5000
		blocks  = 3
	)
	watchdog(t, time.Minute)
	memory := newContextRegion(t)
	allocator := (*layout.BlockAllocator)(memory.allocator())
	root := (*layout.MemoryContext)(memory.context(0))
	shmalloc.ContextInit(root, "root", allocator)
	persistent := func(worker int) unsafe.Pointer { return memory.context(1 + worker) }
	transient := func(worker int) unsafe.Pointer { return memory.context(1 + 2*workers + worker) }
	for worker := range 2 * workers {
		shmalloc.ContextInitFrom((*layout.MemoryContext)(persistent(worker)), root, fmt.Sprintf("worker %d", worker))
	}
	free := shmalloc.FreeSize(allocator)
	sizes := churnSizes()[:blocks]

	var group errgroup.Group
	for worker := range workers {
		group.Go(func() error {
			return contextChurn(root,
				(*layout.MemoryContext)(persistent(worker)),
				(*layout.MemoryContext)(transient(worker)),
				sizes, rounds, byte(1+worker),
			)
		})
		group.Go(func() error {
			overwritten, err := coracle.ContextChurn(memory.context(0),
				persistent(workers+worker), transient(workers+worker),
				sizes, rounds, byte(128+worker),
			)
			if err != nil {
				return err
			}
			if overwritten != 0 {
				return fmt.Errorf("C found %d blocks overwritten", overwritten)
			}
			return nil
		})
	}

	require.NoError(t, group.Wait())
	require.Equal(t, free, shmalloc.FreeSize(allocator))
	requireBalanced(t, root)
	var children []unsafe.Pointer
	for child := root.FirstChild.Get(); child != nil && len(children) <= contextCount; child = child.NextSibling.Get() {
		require.Same(t, root, child.Parent.Get())
		requireBalanced(t, child)
		children = append(children, unsafe.Pointer(child))
	}
	var want []unsafe.Pointer
	for worker := range 2 * workers {
		want = append(want, persistent(worker))
	}
	require.ElementsMatch(t, want, children)
}

// fill writes a pattern over a block, so a move shows which bytes it
// kept.
func fill(address unsafe.Pointer, size uintptr, seed byte) {
	bytes := unsafe.Slice((*byte)(address), size)
	for idx := range bytes {
		bytes[idx] = seed + byte(idx)
	}
}

// runRealloc moves blocks through a context by growing, shrinking,
// freeing and allocating them, and returns where each move landed and the
// region's final state.
func runRealloc(t *testing.T, implementation contextImplementation) ([]int64, state) {
	t.Helper()
	memory := newContextRegion(t)
	context := memory.context(0)
	implementation.init(context, "realloc", memory.allocator())

	moves := []struct {
		oldSize uintptr
		newSize uintptr
	}{
		{100, 300},
		{300, 300},
		{300, 50},
		{50, 0},
		{0, 64},
		{64, 5000},
		{5000, 0},
		{0, 0},
	}
	data := implementation.alloc(context, moves[0].oldSize)
	fill(data, moves[0].oldSize, 1)
	var offsets []int64
	for idx, move := range moves {
		data = implementation.realloc(context, data, move.oldSize, move.newSize)
		offsets = append(offsets, memory.offset(data))
		if data != nil && move.newSize > move.oldSize {
			fill(unsafe.Add(data, move.oldSize), move.newSize-move.oldSize, byte(16*idx))
		}
	}
	return offsets, memory.state()
}

// Test_ContextRealloc_MatchesC verifies that moving a block through a
// memory context lands where C lands it, keeps the bytes C keeps and
// leaves the counters C leaves.
func Test_ContextRealloc_MatchesC(t *testing.T) {
	wantOffsets, wantState := runRealloc(t, cContext)
	gotOffsets, gotState := runRealloc(t, goContext)
	require.Equal(t, wantOffsets, gotOffsets)
	requireSameState(t, wantState, gotState)
}

// Test_ContextRealloc_KeepsBlockWhenExhausted verifies that a move to a
// size no free block holds returns nil and leaves the old block allocated
// and counted, as C does.
func Test_ContextRealloc_KeepsBlockWhenExhausted(t *testing.T) {
	exhaust := func(implementation contextImplementation) (unsafe.Pointer, state) {
		memory := newContextRegion(t)
		context := memory.context(0)
		implementation.init(context, "exhausted", memory.allocator())
		data := implementation.alloc(context, 64)
		fill(data, 64, 7)
		return implementation.realloc(context, data, 64, arenaSize+1), memory.state()
	}
	wantMoved, wantState := exhaust(cContext)
	gotMoved, gotState := exhaust(goContext)
	require.Nil(t, wantMoved)
	require.Nil(t, gotMoved)
	requireSameState(t, wantState, gotState)
}
