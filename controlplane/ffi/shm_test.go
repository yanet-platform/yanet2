package ffi_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	testshm "github.com/yanet-platform/yanet2/common/go/testutils/shm"
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

// Test_SharedMemory_AgentAttach_UninitialisedSegment verifies that unpublished
// storage fails before trusting its instance range or traversing its layout.
func Test_SharedMemory_AgentAttach_UninitialisedSegment(t *testing.T) {
	for _, tc := range []struct {
		name     string
		instance uint32
	}{
		{name: "first instance before publication", instance: 0},
		{name: "maximum index before publication", instance: math.MaxUint32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := createStorageFile(t, 2<<20)
			memory, err := ffi.AttachSharedMemory(path)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, memory.Detach()) })
			agent, err := memory.AgentAttach("uninitialised", tc.instance, 64*datasize.MB)
			if agent != nil {
				t.Cleanup(func() { require.NoError(t, agent.Close()) })
			}
			require.Nil(t, agent)
			require.ErrorContains(t, err, "dataplane shared memory is not ready")
		})
	}
}

// Test_SharedMemory_AgentAttach_InstanceBounds verifies that the last real
// instance attaches and invalid indices fail before traversing unmapped storage.
func Test_SharedMemory_AgentAttach_InstanceBounds(t *testing.T) {
	for _, tc := range []struct {
		name     string
		instance uint32
		valid    bool
	}{
		{name: "last valid instance", instance: 0, valid: true},
		{name: "first index beyond the single instance", instance: 1},
		{name: "maximum index cannot traverse the mapping", instance: math.MaxUint32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := testshm.NewStorage(t)
			memory, err := ffi.AttachSharedMemory(path)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, memory.Detach()) })
			agents := memory.DPConfig(0).Agents()
			agent, err := memory.AgentAttach("boundary", tc.instance, 64*datasize.MB)
			if agent != nil {
				t.Cleanup(func() { require.NoError(t, agent.Close()) })
			}
			if tc.valid {
				require.NoError(t, err)
				require.NotNil(t, agent)
				return
			}
			require.Nil(t, agent)
			require.ErrorContains(t, err, "out of range [0, 1)")
			require.Equal(t, agents, memory.DPConfig(0).Agents())
		})
	}
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
		destination string
		full        bool
		closeReader bool
	}{
		{name: "logs SIGBUS to stderr before exiting", destination: "capture"},
		{name: "logs SIGBUS to socket stderr", destination: "socket"},
		{name: "exits even when stderr is closed", destination: "closed"},
		{name: "exits without draining a full blocking pipe", destination: "pipe", full: true},
		{name: "exits without draining a full blocking socket", destination: "socket", full: true},
		{name: "exits when the stderr pipe reader is closed", destination: "pipe", closeReader: true},
		{name: "exits when the stderr socket reader is closed", destination: "socket", closeReader: true},
		{name: "skips potentially blocking file output", destination: "file"},
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
				closeStderrEnv+"="+strconv.FormatBool(tc.destination == "closed"),
			)
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			var reader, writer *os.File
			switch tc.destination {
			case "pipe", "socket":
				reader, writer = blockingStderrStream(t, tc.destination, tc.full)
				if tc.closeReader {
					require.NoError(t, reader.Close())
				}
				command.Stderr = writer
			case "file":
				writer, err = os.CreateTemp(t.TempDir(), "stderr")
				require.NoError(t, err)
				t.Cleanup(func() { _ = writer.Close() })
				command.Stderr = writer
			}
			err = command.Run()
			info, statError := os.Stat(path)
			require.NoError(t, statError)
			require.Zero(t, info.Size(), "child must reach the truncated mapping")
			require.NoError(t, ctx.Err(), "faulting process did not exit")
			var exitError *exec.ExitError
			require.ErrorAs(t, err, &exitError)
			require.Equal(t, 135, exitError.ExitCode())
			require.Empty(t, stdout.String(), "cleanup must not run after SIGBUS")
			switch {
			case tc.full || tc.closeReader:
				// No reader drains the stream before the process exits.
			case tc.destination == "closed":
				require.Empty(t, stderr.String())
			case tc.destination == "file":
				info, err := writer.Stat()
				require.NoError(t, err)
				require.Zero(t, info.Size())
			case tc.destination == "socket":
				require.NoError(t, writer.Close())
				diagnostic, err := io.ReadAll(reader)
				require.NoError(t, err)
				require.Contains(t, string(diagnostic), "SIGBUS")
			default:
				require.Contains(t, stderr.String(), "SIGBUS")
			}
		})
	}
}

// blockingStderrStream returns a blocking pipe or socket pair, optionally
// filled until the next write would wait for a reader.
func blockingStderrStream(t *testing.T, destination string, full bool) (*os.File, *os.File) {
	t.Helper()
	var reader, writer *os.File
	if destination == "socket" {
		descriptors, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
		require.NoError(t, err)
		reader = os.NewFile(uintptr(descriptors[0]), "stderr-reader")
		writer = os.NewFile(uintptr(descriptors[1]), "stderr-writer")
	} else {
		var err error
		reader, writer, err = os.Pipe()
		require.NoError(t, err)
	}
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})
	if full {
		descriptor := int(writer.Fd())
		require.NoError(t, unix.SetNonblock(descriptor, true))
		payload := make([]byte, 4096)
		for {
			_, err := unix.Write(descriptor, payload)
			if errors.Is(err, unix.EAGAIN) {
				break
			}
			require.NoError(t, err)
		}
		require.NoError(t, unix.SetNonblock(descriptor, false))
	}
	return reader, writer
}
