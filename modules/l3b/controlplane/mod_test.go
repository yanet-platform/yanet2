package l3b_test

import (
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
	l3b "github.com/yanet-platform/yanet2/modules/l3b/controlplane"
)

// Test_NewL3BModule_MissingMemoryFile verifies that attachment failure reports
// the configured shared-memory path without returning a module.
func Test_NewL3BModule_MissingMemoryFile(t *testing.T) {
	memoryPath := filepath.Join(t.TempDir(), "missing-shared-memory")
	config := l3b.DefaultConfig()
	config.InstanceID = xcfg.NewRequired(uint32(0))
	config.MemoryPath = xcfg.MustNonEmptyString(memoryPath)

	module, err := l3b.NewL3BModule(config)
	if module != nil {
		t.Cleanup(func() { require.NoError(t, module.Close()) })
	}
	require.Nil(t, module)
	require.ErrorIs(t, err, fs.ErrNotExist)
	require.ErrorContains(t, err, memoryPath)
}
