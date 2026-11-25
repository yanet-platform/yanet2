package modulebase_test

import (
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/ffi/ffitest"
	"github.com/yanet-platform/yanet2/controlplane/modulebase"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
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

// Test_NewNativeBase_WaitsForLaterInstance verifies that an attach to a
// ready instance of a storage whose later instance is not ready yet waits
// for it and then attaches, as a module does while the dataplane marks its
// instances ready one after another.
func Test_NewNativeBase_WaitsForLaterInstance(t *testing.T) {
	ready := uint32(1)
	path := ffitest.NewStorage(t, ffitest.StorageTypes{InstanceCount: 2, ReadyInstanceCount: &ready})
	memory, err := ffi.AttachSharedMemory(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, memory.Detach()) })

	waiting := make(chan struct{}, 1)
	core, _ := observer.New(zapcore.DebugLevel)
	log := zap.New(core, zap.Hooks(func(entry zapcore.Entry) error {
		if entry.Message == "waiting for dataplane instances" {
			select {
			case waiting <- struct{}{}:
			default:
			}
		}
		return nil
	}))
	type attached struct {
		base *modulebase.Base
		err  error
	}
	done := make(chan attached, 1)
	go func() {
		base, err := modulebase.NewNativeBase(nativeAttachConfig(path, 0), "decap", "[::1]:0", modulebase.WithLog(log))
		done <- attached{base, err}
	}()

	select {
	case <-waiting:
	case result := <-done:
		t.Fatalf("the attach returned before the later instance was ready: %v", result.err)
	case <-time.After(10 * time.Second):
		t.Fatal("the attach did not wait for the later instance")
	}
	require.NoError(t, ffitest.MarkInstanceReady(memory, 1))

	select {
	case result := <-done:
		require.NoError(t, result.err)
		require.NotNil(t, result.base.NativeAgent())
		require.NoError(t, result.base.Close())
	case <-time.After(10 * time.Second):
		t.Fatal("the attach did not return once every instance was ready")
	}
}

// Test_NewNativeBase_FailsAtOnceOnMissingStorage verifies that an attach
// to a storage file that does not exist fails without waiting for the
// dataplane's instances.
func Test_NewNativeBase_FailsAtOnceOnMissingStorage(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	path := filepath.Join(t.TempDir(), "absent")

	done := make(chan error, 1)
	go func() {
		_, err := modulebase.NewNativeBase(
			nativeAttachConfig(path, 0), "decap", "[::1]:0", modulebase.WithLog(zap.New(core)),
		)
		done <- err
	}()

	select {
	case err := <-done:
		require.ErrorIs(t, err, fs.ErrNotExist)
		require.Zero(t, logs.FilterMessage("waiting for dataplane instances").Len())
	case <-time.After(10 * time.Second):
		t.Fatal("the attach to a missing storage waited instead of failing")
	}
}

// Test_NewNativeBase_GivesUpAtReadyDeadline verifies that an attach to a
// storage whose later instance never becomes ready gives up at the
// deadline it was given, reporting the instance as not ready.
func Test_NewNativeBase_GivesUpAtReadyDeadline(t *testing.T) {
	ready := uint32(1)
	path := ffitest.NewStorage(t, ffitest.StorageTypes{InstanceCount: 2, ReadyInstanceCount: &ready})
	deadline := time.Now().Add(100 * time.Millisecond)

	done := make(chan error, 1)
	go func() {
		_, err := modulebase.NewNativeBase(
			nativeAttachConfig(path, 0), "decap", "[::1]:0", modulebase.WithReadyDeadline(deadline),
		)
		done <- err
	}()

	select {
	case err := <-done:
		require.ErrorIs(t, err, zone.ErrNotReady)
	case <-time.After(10 * time.Second):
		t.Fatal("the attach waited past its deadline")
	}
}
