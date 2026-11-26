//go:build linux && (amd64 || arm64) && asan

package shmalloc_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
)

// Test_PutArena_PoisonsFreeBlocks verifies that in AddressSanitizer
// builds the start of a fresh arena is left unaddressable, so a read of a
// free block is reported.
func Test_PutArena_PoisonsFreeBlocks(t *testing.T) {
	memory, _ := newAllocatorRegion(t)

	require.True(t, nativetest.Unaddressable(memory.arena()))
}
