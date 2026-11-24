//go:build linux && (amd64 || arm64)

package shmalloc_test

import (
	"bytes"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
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
