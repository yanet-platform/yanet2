package ffi_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// createStorageFile returns the path of a zero-filled file of the given size.
//
// This is the state the dataplane leaves its storage in before it writes the
// segment header.
func createStorageFile(t *testing.T, size int64) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "yanet")
	file, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, file.Truncate(size))
	require.NoError(t, file.Close())

	return path
}

// verifies that a segment attached before the dataplane has written its
// header reports not ready and still detaches cleanly.
//
// This is the path the director's readiness backoff takes on a startup
// timeout.
func Test_SharedMemory_Detach_UninitialisedSegment(t *testing.T) {
	path := createStorageFile(t, 2<<20)

	shm, err := ffi.AttachSharedMemory(path)
	require.NoError(t, err)
	require.False(t, shm.DataplaneReady(0))

	require.NoError(t, shm.Detach())
}

// verifies that detaching an already detached handle is a no-op rather than
// a double release.
func Test_SharedMemory_Detach_Twice(t *testing.T) {
	path := createStorageFile(t, 2<<20)

	shm, err := ffi.AttachSharedMemory(path)
	require.NoError(t, err)

	require.NoError(t, shm.Detach())
	require.NoError(t, shm.Detach())
}

// verifies that attaching to a missing storage file fails with an error
// instead of handing out a handle with nothing behind it.
func Test_SharedMemory_Attach_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing")

	shm, err := ffi.AttachSharedMemory(path)
	require.Error(t, err)
	require.Nil(t, shm)
}
