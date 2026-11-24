//go:build linux && (amd64 || arm64)

package zone_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/ffi/ffitest"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// craftedStorage returns a storage image of one instance whose dataplane
// zone carries this build's ready magic and points at a zeroed
// control-plane zone right behind it, and that dataplane zone.
//
// The image maps tail more zeroed bytes after the instance, starting at its
// 8-byte aligned end.
func craftedStorage(tail uintptr) ([]byte, *layout.DpConfig) {
	dpSize := (unsafe.Sizeof(layout.DpConfig{}) + 7) &^ 7
	instanceSize := (dpSize + unsafe.Sizeof(layout.CpConfig{}) + 7) &^ 7
	words := make([]uint64, (instanceSize+tail+7)/8)
	data := unsafe.Slice((*byte)(unsafe.Pointer(&words[0])), len(words)*8)

	dp := (*layout.DpConfig)(unsafe.Pointer(&data[0]))
	dp.InstanceCount = 1
	dp.StorageSize = uint64(instanceSize)
	dp.CpConfig.Set((*layout.CpConfig)(unsafe.Pointer(&data[dpSize])))
	dp.ReadyMagic = layout.DpConfigReadyMagic
	return data, dp
}

// Test_Storage_ReadyMagic verifies that only a dataplane marked ready with
// the magic this build was generated with is attached; any other is
// refused as not ready or as another version, naming both versions.
func Test_Storage_ReadyMagic(t *testing.T) {
	const abiMask = uint64(0xffff) << 16
	want := layout.DpConfigReadyMagic
	otherABI := want&^abiMask | uint64(layout.ModuleABIVersion+1)<<16

	cases := []struct {
		name    string
		magic   uint64
		wantErr error
		message string
	}{
		{"matching dataplane", want, nil, ""},
		{"zeroed storage", 0, zone.ErrNotReady, ""},
		{"foreign high word", want ^ 1<<32, zone.ErrNotReady, ""},
		{
			"other module ABI version", otherABI, zone.ErrVersionMismatch,
			fmt.Sprintf("ABI version %d, this build reads %d", layout.ModuleABIVersion+1, layout.ModuleABIVersion),
		},
		{
			"other layout counter", want ^ 1, zone.ErrVersionMismatch,
			fmt.Sprintf("layout counter %d, this build reads %d", (want^1)&0xffff, want&0xffff),
		},
		{
			"magic from before the ABI was carried", 0xDEAD10CC0000000F, zone.ErrVersionMismatch,
			fmt.Sprintf("ABI version 0, this build reads %d", layout.ModuleABIVersion),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, dp := craftedStorage(0)
			dp.ReadyMagic = tc.magic
			storage, err := zone.FromMemory(data)
			if tc.wantErr == nil {
				require.NoError(t, err)
				require.Len(t, storage.Instances, 1)
				return
			}
			require.ErrorIs(t, err, tc.wantErr)
			require.ErrorContains(t, err, tc.message)
		})
	}
}

// Test_Storage_ReadyMagicCarriesABIVersion verifies that the magic this
// build expects carries the module ABI version it was generated with, so
// an ABI bump changes the magic.
func Test_Storage_ReadyMagicCarriesABIVersion(t *testing.T) {
	require.Equal(t, uint64(layout.ModuleABIVersion), layout.DpConfigReadyMagic>>16&0xffff)
}

// Test_Storage_RefusesMalformedInstances verifies that a storage whose
// instances do not fit it, or that declares none, is refused rather than
// read.
func Test_Storage_RefusesMalformedInstances(t *testing.T) {
	cases := []struct {
		name    string
		corrupt func(data []byte, dp *layout.DpConfig) []byte
		message string
	}{
		{
			"zero instance count",
			func(data []byte, dp *layout.DpConfig) []byte {
				dp.InstanceCount = 0
				return data
			},
			"declares no instances",
		},
		{
			"second instance outside the storage",
			func(data []byte, dp *layout.DpConfig) []byte {
				dp.InstanceCount = 2
				return data
			},
			"instance 1 at offset",
		},
		{
			"control-plane zone outside the mapping",
			func(data []byte, _ *layout.DpConfig) []byte {
				return data[:unsafe.Sizeof(layout.DpConfig{})]
			},
			"control-plane zone outside the storage",
		},
		{
			"control-plane zone missing",
			func(data []byte, dp *layout.DpConfig) []byte {
				dp.CpConfig.Set(nil)
				return data
			},
			"control-plane zone outside the storage",
		},
		{
			"zero storage size",
			func(data []byte, dp *layout.DpConfig) []byte {
				dp.StorageSize = 0
				return data
			},
			"zero storage size",
		},
		{
			"storage too short for the dataplane zone",
			func(data []byte, dp *layout.DpConfig) []byte {
				dp.StorageSize = 8
				return data
			},
			"cannot hold its dataplane zone",
		},
		{
			"control-plane zone outside the instance",
			func(data []byte, dp *layout.DpConfig) []byte {
				dp.StorageSize = uint64(uintptr(unsafe.Pointer(dp.CpConfig.Get())) - uintptr(unsafe.Pointer(&data[0])))
				return data
			},
			"control-plane zone outside the instance",
		},
		{
			"storage size past the mapping",
			func(data []byte, dp *layout.DpConfig) []byte {
				dp.StorageSize = uint64(len(data)) + 8
				return data
			},
			"runs past the mapping",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, dp := craftedStorage(0)
			_, err := zone.FromMemory(tc.corrupt(data, dp))
			require.ErrorContains(t, err, tc.message)
		})
	}
}

// Test_Storage_Attach verifies that a storage file is mapped and read as
// the memory it holds, that a file too short for a dataplane zone is
// refused, and that closing twice is harmless.
func Test_Storage_Attach(t *testing.T) {
	data, _ := craftedStorage(0)
	cases := []struct {
		name    string
		size    int
		message string
	}{
		{"whole storage", len(data), ""},
		{"truncated inside the dataplane zone", int(unsafe.Sizeof(layout.DpConfig{})) - 1, "cannot hold a dataplane zone"},
		{"truncated before the control-plane zone", int(unsafe.Sizeof(layout.DpConfig{})), "control-plane zone outside the storage"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "yanet")
			require.NoError(t, os.WriteFile(path, data[:tc.size], 0o600))
			storage, err := zone.Attach(path)
			if tc.message != "" {
				require.ErrorContains(t, err, tc.message)
				return
			}
			require.NoError(t, err)
			require.Len(t, storage.Instances, 1)
			require.NoError(t, storage.Close())
			require.NoError(t, storage.Close())
		})
	}
}

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
