package identity_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/identity"
)

// Test_IdentityProvider_ResolveByLogin verifies that an external subject can
// resolve a local account through its optional login alias.
func Test_IdentityProvider_ResolveByLogin(t *testing.T) {
	wantIdentity := identity.Identity{
		Username: "alice",
		Groups:   []string{"operators"},
	}
	provider := newFileProvider(t, "identities: [{username: alice, groups: [operators]}]\n")
	subject := core.Subject{
		Issuer:     "https://issuer.example",
		Identifier: "subject-42",
		Login:      "alice",
	}

	resolved, err := provider.ResolveIdentity(t.Context(), subject)
	require.NoError(t, err)
	assert.Equal(t, wantIdentity, resolved)
	resolved.Groups[0] = "changed"
	again, err := provider.ResolveIdentity(t.Context(), subject)
	require.NoError(t, err)
	assert.Equal(t, wantIdentity, again)
}

// Test_IdentityProvider_MissingLoginIsUnsupported verifies that a lookup-only
// provider does not reinterpret a stable external identifier as an account name.
func Test_IdentityProvider_MissingLoginIsUnsupported(t *testing.T) {
	provider := newFileProvider(t, "identities: []\n")

	_, err := provider.ResolveIdentity(t.Context(), core.Subject{
		Issuer:     "https://issuer.example",
		Identifier: "subject-42",
	})
	require.ErrorIs(t, err, identity.ErrSubjectUnsupported)
}

// newFileProvider uses YAML at a literal path whose suffix is not a codec.
func newFileProvider(t *testing.T, content string) *identity.FileProvider {
	t.Helper()
	path := filepath.Join(t.TempDir(), "identities.zst")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	provider, err := identity.NewFileProvider(path)
	require.NoError(t, err)
	return provider
}

// Test_FileProvider_Reload verifies that failed edits preserve the last valid
// identities and that recovery, disabling and removal apply without reopening.
func Test_FileProvider_Reload(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "identities.zst")
	initial := "identities: [{username: alice, groups: [operators]}]\n"
	disabled := "identities: [{username: alice, groups: [readers], disabled: true}]\n"
	require.NoError(t, os.WriteFile(path, []byte(initial), 0o600))
	provider, err := identity.NewFileProvider(path)
	require.NoError(t, err)

	for _, tc := range []struct {
		name     string
		content  string
		remove   bool
		wantErr  bool
		disabled bool
		missing  bool
	}{
		{name: "unchanged bytes", content: initial},
		{name: "changed membership and disabled account", content: disabled, disabled: true},
		{name: "invalid yaml", content: "identities: not-a-list", wantErr: true, disabled: true},
		{name: "invalid later entry", content: "identities: [{username: alice}, {groups: [operators]}]", wantErr: true, disabled: true},
		{name: "unavailable file", remove: true, wantErr: true, disabled: true},
		{name: "recovered prior bytes", content: disabled, disabled: true},
		{name: "empty policy removes account", content: "", missing: true},
		{name: "unchanged empty policy", content: "", missing: true},
		{name: "restored account", content: initial},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.remove {
				require.NoError(t, os.Remove(path))
			} else {
				require.NoError(t, os.WriteFile(path, []byte(tc.content), 0o600))
			}
			err := provider.Reload()
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			account, err := provider.ResolveIdentity(t.Context(), core.NewLocalSubject("alice"))
			if tc.missing {
				require.ErrorIs(t, err, identity.ErrIdentityNotFound)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.disabled, account.Disabled)
			group := "operators"
			if tc.disabled {
				group = "readers"
			}
			require.Equal(t, []string{group}, account.Groups)
		})
	}
}
