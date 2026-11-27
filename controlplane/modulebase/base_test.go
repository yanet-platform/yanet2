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
	"github.com/yanet-platform/yanet2/controlplane/modulebase"
	"github.com/yanet-platform/yanet2/controlplane/native/agent"
)

// nativeAttachConfig returns an attach config of a 4 MB agent serving the
// instance of the storage file.
//
// The storage fixture fails the test when it ends while the file is still
// mapped, so every test using it also checks that nothing stays mapped.
func nativeAttachConfig(path string, instanceID uint32) agent.AttachConfig {
	config := agent.DefaultAttachConfig(4 * datasize.MB)
	config.InstanceID = xcfg.NewRequired(instanceID)
	config.MemoryPath = xcfg.MustNonEmptyString(path)
	return config
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
