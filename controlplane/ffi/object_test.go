package ffi_test

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// Test_ObjectConfig_Free_SuccessForgetsPointer verifies that a successful
// free clears the handle, so a repeated free is a no-op that never reaches
// the typed free again.
func Test_ObjectConfig_Free_SuccessForgetsPointer(t *testing.T) {
	handle := ffi.NewObjectConfig(fakeObject())
	outcome := &freeOutcome{}

	require.NoError(t, handle.Free(outcome.free))
	require.Nil(t, handle.AsRawPtr())

	require.NoError(t, handle.Free(outcome.free))
	require.Equal(t, 1, outcome.calls)
}

// Test_ObjectConfig_Free_RefusedKeepsHandle verifies that a refused free
// reports still-referenced and keeps the handle, so a retry reaches the
// typed free again.
func Test_ObjectConfig_Free_RefusedKeepsHandle(t *testing.T) {
	handle := ffi.NewObjectConfig(fakeObject())
	outcome := &freeOutcome{rc: -1, errno: syscall.EAGAIN}

	require.ErrorIs(t, handle.Free(outcome.free), ffi.ErrStillReferenced)
	require.NotNil(t, handle.AsRawPtr())

	outcome.rc, outcome.errno = 0, nil
	require.NoError(t, handle.Free(outcome.free))
	require.Equal(t, 2, outcome.calls)
}

// Test_ObjectConfig_Free_FailureKeepsHandle verifies that any other
// failure is reported with its cause and keeps the handle.
func Test_ObjectConfig_Free_FailureKeepsHandle(t *testing.T) {
	handle := ffi.NewObjectConfig(fakeObject())
	outcome := &freeOutcome{rc: -1, errno: errInjectedFree}

	err := handle.Free(outcome.free)
	require.ErrorIs(t, err, errInjectedFree)
	require.NotErrorIs(t, err, ffi.ErrStillReferenced)
	require.NotNil(t, handle.AsRawPtr())
}
