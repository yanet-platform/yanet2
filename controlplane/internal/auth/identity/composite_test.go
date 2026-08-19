package identity_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/identity"
)

var errProviderFailure = errors.New("provider failure")

type testAssertion struct{}

func (m *testAssertion) AssertionType() string {
	return "test"
}

type identityProviderStub struct {
	providerName string
	resolve      func(context.Context, core.Subject) (identity.Identity, error)
}

// newIdentityProviderStub returns a provider with caller-controlled resolution.
func newIdentityProviderStub(
	name string,
	resolve func(context.Context, core.Subject) (identity.Identity, error),
) *identityProviderStub {
	return &identityProviderStub{
		providerName: name,
		resolve:      resolve,
	}
}

func (m *identityProviderStub) Name() string {
	return m.providerName
}

func (m *identityProviderStub) ResolveIdentity(
	ctx context.Context,
	subject core.Subject,
) (identity.Identity, error) {
	return m.resolve(ctx, subject)
}

// Test_CompositeIdentityProvider_UnsupportedSubjectFallsThrough verifies that
// the complete authenticated subject reaches the next applicable provider.
func Test_CompositeIdentityProvider_UnsupportedSubjectFallsThrough(t *testing.T) {
	assertion := &testAssertion{}
	subject := core.Subject{
		Issuer:     "https://issuer.example",
		Identifier: "subject-42",
		Login:      "alice",
		Assertion:  assertion,
	}
	wantIdentity := identity.Identity{
		Username: "alice",
		Groups:   []string{"operators"},
	}

	unsupportedProvider := newIdentityProviderStub(
		"unsupported",
		func(
			ctx context.Context,
			received core.Subject,
		) (identity.Identity, error) {
			return identity.Identity{}, identity.ErrSubjectUnsupported
		},
	)
	resolvingProvider := newIdentityProviderStub(
		"resolving",
		func(
			ctx context.Context,
			received core.Subject,
		) (identity.Identity, error) {
			require.Equal(t, subject, received)
			return wantIdentity, nil
		},
	)
	provider := identity.NewCompositeIdentityProvider([]identity.Provider{
		unsupportedProvider,
		resolvingProvider,
	})

	resolved, err := provider.ResolveIdentity(t.Context(), subject)
	require.NoError(t, err)
	assert.Equal(t, wantIdentity, resolved)
}

// Test_CompositeIdentityProvider_MissingIdentityFallsThrough verifies that an
// absent account can still be supplied by a later applicable provider.
func Test_CompositeIdentityProvider_MissingIdentityFallsThrough(t *testing.T) {
	missingProvider := newIdentityProviderStub(
		"missing",
		func(
			ctx context.Context,
			subject core.Subject,
		) (identity.Identity, error) {
			return identity.Identity{}, identity.ErrIdentityNotFound
		},
	)
	fallbackProvider := newIdentityProviderStub(
		"fallback",
		func(
			ctx context.Context,
			subject core.Subject,
		) (identity.Identity, error) {
			return identity.Identity{Username: "alice"}, nil
		},
	)
	provider := identity.NewCompositeIdentityProvider([]identity.Provider{
		missingProvider,
		fallbackProvider,
	})

	resolved, err := provider.ResolveIdentity(
		t.Context(),
		core.NewLocalSubject("alice"),
	)
	require.NoError(t, err)
	assert.Equal(t, "alice", resolved.Username)
}

// Test_CompositeIdentityProvider_ProviderErrorStopsResolution verifies that a
// provider failure cannot be bypassed by a later identity source.
func Test_CompositeIdentityProvider_ProviderErrorStopsResolution(t *testing.T) {
	fallbackCalled := false
	failingProvider := newIdentityProviderStub(
		"failing",
		func(
			ctx context.Context,
			subject core.Subject,
		) (identity.Identity, error) {
			return identity.Identity{}, errProviderFailure
		},
	)
	fallbackProvider := newIdentityProviderStub(
		"fallback",
		func(
			ctx context.Context,
			subject core.Subject,
		) (identity.Identity, error) {
			fallbackCalled = true
			return identity.Identity{Username: "alice"}, nil
		},
	)
	provider := identity.NewCompositeIdentityProvider([]identity.Provider{
		failingProvider,
		fallbackProvider,
	})

	_, err := provider.ResolveIdentity(
		t.Context(),
		core.NewLocalSubject("alice"),
	)
	require.ErrorIs(t, err, errProviderFailure)
	assert.False(t, fallbackCalled)
}

// Test_CompositeIdentityProvider_AllProvidersUnsupported verifies that an
// unhandled subject remains distinguishable from an absent account.
func Test_CompositeIdentityProvider_AllProvidersUnsupported(t *testing.T) {
	unsupportedProvider := newIdentityProviderStub(
		"unsupported",
		func(
			ctx context.Context,
			subject core.Subject,
		) (identity.Identity, error) {
			return identity.Identity{}, identity.ErrSubjectUnsupported
		},
	)
	provider := identity.NewCompositeIdentityProvider([]identity.Provider{
		unsupportedProvider,
	})

	_, err := provider.ResolveIdentity(t.Context(), core.Subject{
		Issuer:     "https://issuer.example",
		Identifier: "subject-42",
	})
	require.ErrorIs(t, err, identity.ErrSubjectUnsupported)
}
