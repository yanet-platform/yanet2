package modulebase_test

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/ffi/ffitest"
	"github.com/yanet-platform/yanet2/controlplane/modulebase"
)

// nativeAttachConfig returns an attach config of a 4 MB agent serving the
// instance of the storage file.
//
// The storage fixture fails the test when it ends while the file is still
// mapped, so every test using it also checks that nothing stays mapped.
func nativeAttachConfig(path string, instanceID uint32) ffi.AttachConfig {
	config := ffi.DefaultAttachConfig(4 * datasize.MB)
	config.InstanceID = xcfg.NewRequired(instanceID)
	config.MemoryPath = xcfg.MustNonEmptyString(path)
	return config
}

// Test_NewNativeBase_AttachesAgent verifies that a natively attached base
// publishes through a native agent only and unmaps the storage on close.
func Test_NewNativeBase_AttachesAgent(t *testing.T) {
	path := ffitest.NewStorage(t, ffitest.StorageTypes{})

	base, err := modulebase.NewNativeBase(nativeAttachConfig(path, 0), "decap", "[::1]:0")
	require.NoError(t, err)

	require.NotNil(t, base.NativeAgent())
	require.Nil(t, base.Agent())
	require.NoError(t, base.Close())
}

// Test_NewNativeBase_RefusesMissingInstance verifies that an attach to an
// instance the storage does not hold fails and leaves the storage
// unmapped.
func Test_NewNativeBase_RefusesMissingInstance(t *testing.T) {
	path := ffitest.NewStorage(t, ffitest.StorageTypes{})

	_, err := modulebase.NewNativeBase(nativeAttachConfig(path, 1), "decap", "[::1]:0")

	require.ErrorContains(t, err, "no dataplane instance 1")
}
