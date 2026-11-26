package permission_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/permission"
)

// Test_FilePermissionStore_Reload verifies that invalid updates preserve both
// rule maps, valid updates replace them, and a literal path is not a codec.
func Test_FilePermissionStore_Reload(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "permissions.zst")
	initial := "permissions: {groups: [{name: operators, permissions: ['/test.Service/Group']}], users: [{username: alice, permissions: ['/test.Service/User']}] }\n"
	changed := "permissions: {groups: [{name: operators, permissions: ['/test.Service/NewGroup']}], users: [{username: alice, permissions: ['/test.Service/NewUser']}] }\n"
	require.NoError(t, os.WriteFile(path, []byte(initial), 0o600))
	store, err := permission.NewFilePermissionStore(path)
	require.NoError(t, err)

	for _, tc := range []struct {
		name       string
		content    string
		remove     bool
		wantErr    bool
		groupGrant string
		userGrant  string
	}{
		{name: "initial permissions", content: initial, groupGrant: "/test.Service/Group", userGrant: "/test.Service/User"},
		{name: "invalid yaml", content: "permissions: [", wantErr: true, groupGrant: "/test.Service/Group", userGrant: "/test.Service/User"},
		{name: "invalid group glob", content: "permissions: {groups: [{name: operators, permissions: ['[']}]}", wantErr: true, groupGrant: "/test.Service/Group", userGrant: "/test.Service/User"},
		{name: "invalid user glob after valid group change", content: "permissions: {groups: [{name: operators, permissions: ['/test.Service/NewGroup']}], users: [{username: alice, permissions: ['[']}]}", wantErr: true, groupGrant: "/test.Service/Group", userGrant: "/test.Service/User"},
		{name: "unavailable file", remove: true, wantErr: true, groupGrant: "/test.Service/Group", userGrant: "/test.Service/User"},
		{name: "recovered with new rules", content: changed, groupGrant: "/test.Service/NewGroup", userGrant: "/test.Service/NewUser"},
		{name: "empty document revokes all grants", content: ""},
		{name: "restored permissions", content: initial, groupGrant: "/test.Service/Group", userGrant: "/test.Service/User"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.remove {
				require.NoError(t, os.Remove(path))
			} else {
				require.NoError(t, os.WriteFile(path, []byte(tc.content), 0o600))
			}
			err := store.Reload()
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			for _, rules := range []struct {
				permissions []permission.Permission
				grant       string
			}{
				{store.GetGroupPermissions([]string{"operators"}), tc.groupGrant},
				{store.GetUserPermissions("alice"), tc.userGrant},
			} {
				if rules.grant == "" {
					require.Empty(t, rules.permissions)
					continue
				}
				require.Len(t, rules.permissions, 1)
				require.True(t, rules.permissions[0].Match(rules.grant))
				require.False(t, rules.permissions[0].Match("/test.Service/Unrelated"))
			}
		})
	}
}
