package permission_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/permission"
)

// Test_FilePermissionStore_Includes verifies that included permissions reach
// user lookups and missing sources prevent an incomplete store from loading.
func Test_FilePermissionStore_Includes(t *testing.T) {
	directory := t.TempDir()
	fragment := filepath.Join(directory, "users.yaml")
	require.NoError(t, os.WriteFile(fragment, []byte("permissions:\n  users:\n    - username: alice\n      permissions: ['*']\n"), 0o600))
	path := filepath.Join(directory, "main.yaml")
	require.NoError(t, os.WriteFile(path, []byte("include: [users.yaml]\n"), 0o600))
	store, err := permission.NewFilePermissionStore(path)
	require.NoError(t, err)
	require.Len(t, store.GetUserPermissions("alice"), 1)
	require.Empty(t, store.GetUserPermissions("unknown"))
	require.NoError(t, os.Remove(fragment))
	_, err = permission.NewFilePermissionStore(path)
	require.Error(t, err)
}
