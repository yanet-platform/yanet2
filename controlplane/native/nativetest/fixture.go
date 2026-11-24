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
	t.Cleanup(harness.Free)

	raw := (*rawSharedMemory)(harness.SharedMemory().AsRawPtr())
	image := unsafe.Slice((*byte)(raw.Base), raw.Size)
	storage, err := zone.FromMemory(image)
	require.NoError(t, err)
	return &Fixture{Harness: harness, Image: image, Storage: storage}
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
