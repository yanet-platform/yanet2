package ffi_test

import (
	"errors"
	"syscall"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

var errInjectedFree = errors.New("injected free failure")

// fakeObject returns a pointer the wrappers may cast to their C config
// types without the race detector's pointer checks objecting, since the
// fake free never dereferences it.
func fakeObject() unsafe.Pointer {
	block := make([]byte, 1<<16)
	return unsafe.Pointer(&block[0])
}

// freeOutcome scripts what the typed free reports and counts the calls
// that reached it.
//
// It never hands back a C error chain, which only a cgo package can
// allocate, so the path that reads and releases one is not pinned here.
type freeOutcome struct {
	rc    int
	errno error
	calls int
}

func (m *freeOutcome) free(unsafe.Pointer) (int, unsafe.Pointer, error) {
	m.calls++
	return m.rc, nil, m.errno
}

// Test_ModuleConfig_Free_SuccessForgetsPointer verifies that a successful
// free clears the handle, so a repeated free is a no-op that never reaches
// the typed free again.
func Test_ModuleConfig_Free_SuccessForgetsPointer(t *testing.T) {
	handle := ffi.NewModuleConfig(fakeObject())
	outcome := &freeOutcome{}

	require.NoError(t, handle.Free(outcome.free))
	require.Nil(t, handle.AsRawPtr())

	require.NoError(t, handle.Free(outcome.free))
	require.Equal(t, 1, outcome.calls)
}

// Test_ModuleConfig_Free_RefusedKeepsHandle verifies that a refused free
// reports still-referenced and keeps the handle, so a retry reaches the
// typed free again.
func Test_ModuleConfig_Free_RefusedKeepsHandle(t *testing.T) {
	handle := ffi.NewModuleConfig(fakeObject())
	outcome := &freeOutcome{rc: -1, errno: syscall.EAGAIN}

	require.ErrorIs(t, handle.Free(outcome.free), ffi.ErrStillReferenced)
	require.NotNil(t, handle.AsRawPtr())

	outcome.rc, outcome.errno = 0, nil
	require.NoError(t, handle.Free(outcome.free))
	require.Equal(t, 2, outcome.calls)
}

// Test_ModuleConfig_Free_FailureKeepsHandle verifies that any other
// failure is reported with its cause and keeps the handle.
func Test_ModuleConfig_Free_FailureKeepsHandle(t *testing.T) {
	handle := ffi.NewModuleConfig(fakeObject())
	outcome := &freeOutcome{rc: -1, errno: errInjectedFree}

	err := handle.Free(outcome.free)
	require.ErrorIs(t, err, errInjectedFree)
	require.NotErrorIs(t, err, ffi.ErrStillReferenced)
	require.NotNil(t, handle.AsRawPtr())
}

// Test_ShmDeviceConfig_Free_RefusedKeepsHandle verifies that a device
// handle follows the same refusal contract as a module handle.
func Test_ShmDeviceConfig_Free_RefusedKeepsHandle(t *testing.T) {
	handle := ffi.NewShmDeviceConfig(fakeObject())
	outcome := &freeOutcome{rc: -1, errno: syscall.EAGAIN}

	require.ErrorIs(t, handle.Free(outcome.free), ffi.ErrStillReferenced)
	require.NotNil(t, handle.AsRawPtr())

	outcome.rc, outcome.errno = 0, nil
	require.NoError(t, handle.Free(outcome.free))
	require.Nil(t, handle.AsRawPtr())
}
