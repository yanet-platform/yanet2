package ffi_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

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

// Test_SharedMemory_TruncatedStorage_ExitsOnSIGBUS verifies that a fault in a
// mapped segment terminates the process without running shared-memory cleanup.
func Test_SharedMemory_TruncatedStorage_ExitsOnSIGBUS(t *testing.T) {
	const storageEnv = "YANET_TEST_SIGBUS_STORAGE"
	const closeStderrEnv = "YANET_TEST_SIGBUS_CLOSE_STDERR"
	if path := os.Getenv(storageEnv); path != "" {
		sharedMemory, err := ffi.AttachSharedMemory(path)
		require.NoError(t, err)
		require.False(t, sharedMemory.DataplaneReady(0))
		defer func() {
			fmt.Fprintln(os.Stdout, "cleanup ran")
		}()
		if os.Getenv(closeStderrEnv) == "true" {
			require.NoError(t, os.Stderr.Close())
		}

		require.NoError(t, os.Truncate(path, 0))
		sharedMemory.DataplaneReady(0)
		t.Fatal("access to truncated storage returned")
	}

	for _, tc := range []struct {
		name        string
		closeStderr bool
	}{
		{name: "logs SIGBUS to stderr before exiting"},
		{name: "exits even when stderr is closed", closeStderr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := createStorageFile(t, 2<<20)
			executable, err := os.Executable()
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx,
				executable,
				"-test.run=^Test_SharedMemory_TruncatedStorage_ExitsOnSIGBUS$",
			)
			command.Env = append(os.Environ(),
				storageEnv+"="+path,
				closeStderrEnv+"="+strconv.FormatBool(tc.closeStderr),
			)
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			err = command.Run()
			require.NoError(t, ctx.Err(), "faulting process did not exit")
			var exitError *exec.ExitError
			require.ErrorAs(t, err, &exitError)
			require.Equal(t, 135, exitError.ExitCode())
			require.Empty(t, stdout.String(), "cleanup must not run after SIGBUS")
			if tc.closeStderr {
				require.Empty(t, stderr.String())
			} else {
				require.Contains(t, stderr.String(), "SIGBUS")
			}
		})
	}
}
