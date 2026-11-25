//go:build linux && (amd64 || arm64)

package nativetest

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// LeaveOnly takes every free block of the allocator but one block for
// each of the request sizes, and returns the function giving them back.
//
// A size is what a caller asks the allocator for, red zones of sanitizer
// builds excluded; a later request no kept block serves fails. The kept
// blocks are taken first, so each one sits in the pool its size selects;
// the pools are then emptied from the largest down, so no larger block is
// left to split.
func LeaveOnly(t testing.TB, allocator *layout.BlockAllocator, sizes ...uintptr) func() {
	t.Helper()
	var kept []unsafe.Pointer
	for _, size := range sizes {
		block := shmalloc.Alloc(allocator, size)
		require.NotNil(t, block)
		kept = append(kept, block)
	}
	type taken struct {
		Block unsafe.Pointer
		Size  uintptr
	}
	var blocks []taken
	for pool := shmalloc.PoolCount - 1; pool >= 0; pool-- {
		size := BlockRequest(uintptr(1) << (shmalloc.MinBits + pool))
		if size == 0 {
			continue
		}
		for block := shmalloc.Alloc(allocator, size); block != nil; block = shmalloc.Alloc(allocator, size) {
			blocks = append(blocks, taken{Block: block, Size: size})
		}
	}
	for idx, block := range kept {
		shmalloc.Free(allocator, block, sizes[idx])
	}
	return func() {
		for _, block := range blocks {
			shmalloc.Free(allocator, block.Block, block.Size)
		}
	}
}

// BlockRequest returns the largest request a block of that size serves,
// or zero for a block the red zones of sanitizer builds fill.
//
// The request is the size less those red zones.
func BlockRequest(size uintptr) uintptr {
	if size <= 2*shmalloc.RedZone {
		return 0
	}
	return size - 2*shmalloc.RedZone
}
