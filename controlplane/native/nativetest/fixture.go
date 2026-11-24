//go:build linux && (amd64 || arm64) && cgo

// Package nativetest gives the native control plane's tests an in-process
// dataplane and compares what C and Go write into it.
//
// Only tests import it: it links the dataplane harness through cgo.
package nativetest

import (
	"bytes"
	"testing"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/coracle"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// rawSharedMemory mirrors the C handle struct yanet_shm: the base and
// size of the storage in this process.
type rawSharedMemory struct {
	Base unsafe.Pointer
	Size uintptr
}

// Fixture is an in-process dataplane whose storage both the cgo control
// plane and the native one reach.
type Fixture struct {
	Harness *dataplaneut.Harness
	// Image is the whole storage.
	Image   []byte
	Storage *zone.Storage
	// abandoned keeps the harness from being freed when the test ends.
	abandoned bool
}

// NewFixture returns a dataplane harness of the configuration with its
// storage, freed when the test ends.
//
// Unset memory sizes default to a 64 MB control-plane zone and a 4 MB
// dataplane zone.
func NewFixture(t testing.TB, config dataplaneut.Config) *Fixture {
	t.Helper()
	if config.CPMemory == 0 {
		config.CPMemory = uint64(64 * datasize.MB)
	}
	if config.DPMemory == 0 {
		config.DPMemory = uint64(4 * datasize.MB)
	}
	harness, err := dataplaneut.NewHarness(config)
	require.NoError(t, err)
	m := &Fixture{Harness: harness}
	t.Cleanup(func() {
		if !m.abandoned {
			harness.Free()
		}
	})

	raw := (*rawSharedMemory)(harness.SharedMemory().AsRawPtr())
	m.Image = unsafe.Slice((*byte)(raw.Base), raw.Size)
	m.Storage, err = zone.FromMemory(m.Image)
	require.NoError(t, err)
	return m
}

// Abandon keeps the dataplane's memory past the end of the test, for a
// test that fails while an operation still runs on it.
func (m *Fixture) Abandon() {
	m.abandoned = true
}

// Instance returns the first dataplane instance.
func (m *Fixture) Instance() *zone.Instance {
	return m.Storage.Instances[0]
}

// CompareWithC runs an operation through C, keeps the storage it leaves,
// restores the storage from before it and runs the operation through Go.
//
// Both start from the same bytes at the same addresses, so they must end
// at the same bytes. In AddressSanitizer builds the storage's
// unaddressable runs are kept and restored with its bytes and must end
// the same too.
func (m *Fixture) CompareWithC(t *testing.T, viaC, viaGo func()) {
	t.Helper()
	before, beforeRuns := coracle.Snapshot(m.Image)
	viaC()
	want, wantRuns := coracle.Snapshot(m.Image)
	coracle.Restore(m.Image, before, beforeRuns)
	viaGo()
	got, gotRuns := coracle.Snapshot(m.Image)
	RequireSameBytes(t, want, got)
	require.Equal(t, wantRuns, gotRuns, "unaddressable runs differ")
}

// RequireSameBytes fails at the first byte where the storage differs
// from the expected one.
func RequireSameBytes(t *testing.T, want, got []byte) {
	t.Helper()
	if bytes.Equal(want, got) {
		return
	}
	require.Len(t, got, len(want), "storage sizes differ")
	for idx := range want {
		if want[idx] != got[idx] {
			t.Fatalf("storage differs first at offset %#x: %#x, want %#x", idx, got[idx], want[idx])
		}
	}
}

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
