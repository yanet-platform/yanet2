package identity_test

import (
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
