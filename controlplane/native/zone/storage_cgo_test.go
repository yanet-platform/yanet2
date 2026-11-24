//go:build linux && (amd64 || arm64) && cgo

package zone_test

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/ffi/ffitest"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// requirePlacement fails unless every instance of the storage keeps the
// placement the reader assumes: the control-plane zone after the
// dataplane zone and inside the instance, both aligned for their
// structures.
func requirePlacement(t *testing.T, storage *zone.Storage) {
	t.Helper()
	require.NotEmpty(t, storage.Instances)
	for _, instance := range storage.Instances {
		dp := uintptr(unsafe.Pointer(instance.Dp))
		cp := uintptr(unsafe.Pointer(instance.Cp))
		end := dp + uintptr(instance.Dp.StorageSize)
		require.Zero(t, dp%unsafe.Alignof(layout.DpConfig{}), "instance %d: dataplane zone misaligned", instance.Index)
		require.Zero(t, cp%unsafe.Alignof(layout.CpConfig{}), "instance %d: control-plane zone misaligned", instance.Index)
		require.GreaterOrEqual(t, cp, dp+unsafe.Sizeof(layout.DpConfig{}), "instance %d: zones overlap", instance.Index)
		require.LessOrEqual(t, cp+unsafe.Sizeof(layout.CpConfig{}), end, "instance %d: control-plane zone past the instance", instance.Index)
	}
}

// Test_Storage_ProducersKeepPlacement verifies that the storages the tree
// builds, an in-process dataplane and a storage file of two instances,
// keep the zone placement the reader assumes instead of checking.
func Test_Storage_ProducersKeepPlacement(t *testing.T) {
	t.Run("in-process dataplane", func(t *testing.T) {
		harness, _ := newPublishedHarness(t)
		requirePlacement(t, nativeStorage(t, harness))
	})
	t.Run("storage file of two instances", func(t *testing.T) {
		path := ffitest.NewStorage(t, ffitest.StorageTypes{Modules: []string{"blackhole"}, InstanceCount: 2})
		storage, err := zone.Attach(path)
		require.NoError(t, err)
		t.Cleanup(func() { _ = storage.Close() })
		requirePlacement(t, storage)
	})
}
