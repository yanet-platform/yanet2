package modulebase_test

import (
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
