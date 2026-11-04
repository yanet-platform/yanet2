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
	provider := identity.NewIdentityProvider(map[string]identity.Identity{
		"alice": wantIdentity,
	})
	subject := core.Subject{
		Issuer:     "https://issuer.example",
		Identifier: "subject-42",
		Login:      "alice",
	}

	resolved, err := provider.ResolveIdentity(t.Context(), subject)
	require.NoError(t, err)
	assert.Equal(t, wantIdentity, resolved)
}

// Test_IdentityProvider_MissingLoginIsUnsupported verifies that a lookup-only
// provider does not reinterpret a stable external identifier as an account name.
func Test_IdentityProvider_MissingLoginIsUnsupported(t *testing.T) {
	provider := identity.NewIdentityProvider(map[string]identity.Identity{})

	_, err := provider.ResolveIdentity(t.Context(), core.Subject{
		Issuer:     "https://issuer.example",
		Identifier: "subject-42",
	})
	require.ErrorIs(t, err, identity.ErrSubjectUnsupported)
}

// Test_IdentityProvider_Includes verifies that composed identity data is
// available through the public lookup and missing fragments fail loading.
func Test_IdentityProvider_Includes(t *testing.T) {
	directory := t.TempDir()
	fragment := filepath.Join(directory, "users.yaml")
	require.NoError(t, os.WriteFile(fragment, []byte("identities:\n  - username: alice\n    groups: [operators]\n"), 0o600))
	path := filepath.Join(directory, "main.yaml")
	require.NoError(t, os.WriteFile(path, []byte("include: [users.yaml]\n"), 0o600))
	provider, err := identity.NewIdentityProviderFromFile(path)
	require.NoError(t, err)
	resolved, err := provider.ResolveIdentity(t.Context(), core.Subject{Login: "alice"})
	require.NoError(t, err)
	require.Equal(t, []string{"operators"}, resolved.Groups)
	require.NoError(t, os.Remove(fragment))
	_, err = identity.NewIdentityProviderFromFile(path)
	require.Error(t, err)
}
