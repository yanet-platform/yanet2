//go:build linux && amd64

// Package nativetest gives the native control plane's tests an in-process
// dataplane and the comparison of what C and Go write into it. Only tests
// import it; it links the dataplane harness through cgo.
package nativetest

import (
	"bytes"
	"testing"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// rawSharedMemory mirrors the C handle struct yanet_shm.
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
// storage; memory sizes default to 64 MB of control-plane zone and 4 MB
// of dataplane zone.
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
// restores the storage from before it and runs the operation through Go:
// both start from the same bytes at the same addresses, so they must end
// at the same bytes.
func (m *Fixture) CompareWithC(t *testing.T, viaC, viaGo func()) {
	t.Helper()
	before := bytes.Clone(m.Image)
	viaC()
	want := bytes.Clone(m.Image)
	copy(m.Image, before)
	viaGo()
	RequireSameBytes(t, want, m.Image)
}

// RequireSameBytes fails at the first byte where got differs from want.
func RequireSameBytes(t *testing.T, want, got []byte) {
	t.Helper()
	if bytes.Equal(want, got) {
		return
	}
	for idx := range want {
		if want[idx] != got[idx] {
			t.Fatalf("storage differs first at offset %#x: %#x, want %#x", idx, got[idx], want[idx])
		}
	}
}
