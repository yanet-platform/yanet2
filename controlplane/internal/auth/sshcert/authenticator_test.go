package sshcert

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stripe/krl"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/identity"
)

// mockIdentityProvider is a test identity provider.
type mockIdentityProvider struct {
	identities map[string]identity.Identity
}

func (m *mockIdentityProvider) Name() string { return "mock" }

func (m *mockIdentityProvider) GetIdentity(
	_ context.Context,
	username string,
) (identity.Identity, error) {
	ident, ok := m.identities[username]
	if !ok {
		return identity.Identity{}, identity.ErrIdentityNotFound
	}

	return ident, nil
}

func newMockIdentityProvider(
	identities ...identity.Identity,
) *mockIdentityProvider {
	m := &mockIdentityProvider{
		identities: map[string]identity.Identity{},
	}
	for _, ident := range identities {
		m.identities[ident.Username] = ident
	}

	return m
}

func TestAuthenticator_Name(t *testing.T) {
	ca := generateCA(t)
	store := NewCAStore([]CAEntry{
		{PublicKey: ca.PublicKey()},
	})
	idp := newMockIdentityProvider()

	auth := NewAuthenticator(store, NewNopRevocationChecker(), idp)
	defer auth.Close()

	assert.Equal(t, "sshcert", auth.Name())
}

func TestAuthenticator_IsTokenSupported(t *testing.T) {
	ca := generateCA(t)
	store := NewCAStore([]CAEntry{
		{PublicKey: ca.PublicKey()},
	})
	idp := newMockIdentityProvider()

	auth := NewAuthenticator(store, NewNopRevocationChecker(), idp)
	defer auth.Close()

	assert.True(t, auth.IsTokenSupported("sshcert eyJ0ZXN0Ig=="))
	assert.False(t, auth.IsTokenSupported("sshkey eyJ0ZXN0Ig=="))
	assert.False(t, auth.IsTokenSupported("basic dGVzdA=="))
}

func TestAuthenticator_HappyPath(t *testing.T) {
	ca := generateCA(t)
	cert, userSigner := generateUserCert(
		t, ca, "alice", 1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	store := NewCAStore([]CAEntry{
		{PublicKey: ca.PublicKey()},
	})
	idp := newMockIdentityProvider(identity.Identity{
		Username: "alice",
		Groups:   []string{"admins"},
	})

	auth := NewAuthenticator(store, NewNopRevocationChecker(), idp,
		WithTimeWindow(10*time.Second),
	)
	defer auth.Close()

	now := time.Now()
	rawToken := signCertToken(
		t,
		userSigner,
		cert,
		"/test.Service/Method",
		now.UnixNano(),
		"nonce-1",
	)

	principal, err := auth.Authenticate(
		context.Background(),
		rawToken,
		&core.RequestInfo{FullMethod: "/test.Service/Method"},
	)
	require.NoError(t, err)
	assert.Equal(t, "alice", principal.User)
	assert.Equal(t, []string{"admins"}, principal.Groups)
	assert.Equal(t, "sshcert", principal.AuthMethod)
	assert.False(t, principal.IsAnonymous)
}

func TestAuthenticator_ExpiredTimestamp(t *testing.T) {
	ca := generateCA(t)
	cert, userSigner := generateUserCert(
		t,
		ca,
		"alice",
		1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	store := NewCAStore([]CAEntry{
		{PublicKey: ca.PublicKey()},
	})
	idp := newMockIdentityProvider(identity.Identity{
		Username: "alice",
	})

	auth := NewAuthenticator(store, NewNopRevocationChecker(), idp,
		WithTimeWindow(5*time.Second),
	)
	defer auth.Close()

	// Timestamp 1 hour ago.
	oldTimestamp := time.Now().Add(-1 * time.Hour).UnixNano()
	rawToken := signCertToken(
		t, userSigner, cert,
		"/test.Service/Method", oldTimestamp, "nonce-1",
	)

	_, err := auth.Authenticate(
		context.Background(),
		rawToken,
		&core.RequestInfo{FullMethod: "/test.Service/Method"},
	)
	require.Error(t, err)
	assertGRPCCode(t, err, codes.Unauthenticated)
}

func TestAuthenticator_MethodBindingMismatch(t *testing.T) {
	ca := generateCA(t)
	cert, userSigner := generateUserCert(
		t,
		ca,
		"alice",
		1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	store := NewCAStore([]CAEntry{
		{PublicKey: ca.PublicKey()},
	})
	idp := newMockIdentityProvider(identity.Identity{
		Username: "alice",
	})

	auth := NewAuthenticator(store, NewNopRevocationChecker(), idp,
		WithTimeWindow(10*time.Second),
	)
	defer auth.Close()

	now := time.Now()
	rawToken := signCertToken(
		t, userSigner, cert,
		"/test.Service/Method", now.UnixNano(), "nonce-1",
	)

	_, err := auth.Authenticate(
		context.Background(),
		rawToken,
		&core.RequestInfo{FullMethod: "/test.Service/OtherMethod"},
	)
	require.Error(t, err)
	assertGRPCCode(t, err, codes.Unauthenticated)
}

func TestAuthenticator_UntrustedCA(t *testing.T) {
	ca := generateCA(t)
	otherCA := generateCA(t)

	cert, userSigner := generateUserCert(
		t,
		ca,
		"alice",
		1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	// Store only has otherCA.
	store := NewCAStore([]CAEntry{
		{PublicKey: otherCA.PublicKey()},
	})
	idp := newMockIdentityProvider(identity.Identity{
		Username: "alice",
	})

	auth := NewAuthenticator(store, NewNopRevocationChecker(), idp,
		WithTimeWindow(10*time.Second),
	)
	defer auth.Close()

	now := time.Now()
	rawToken := signCertToken(
		t, userSigner, cert,
		"/test.Service/Method", now.UnixNano(), "nonce-1",
	)

	_, err := auth.Authenticate(
		context.Background(),
		rawToken,
		&core.RequestInfo{FullMethod: "/test.Service/Method"},
	)
	require.Error(t, err)
	assertGRPCCode(t, err, codes.Unauthenticated)
}

func TestAuthenticator_ExpiredCertificate(t *testing.T) {
	ca := generateCA(t)
	cert, userSigner := generateUserCert(
		t,
		ca,
		"alice",
		1,
		time.Now().Add(-48*time.Hour),
		time.Now().Add(-24*time.Hour),
	)

	store := NewCAStore([]CAEntry{
		{PublicKey: ca.PublicKey()},
	})
	idp := newMockIdentityProvider(identity.Identity{
		Username: "alice",
	})

	auth := NewAuthenticator(store, NewNopRevocationChecker(), idp,
		WithTimeWindow(10*time.Second),
	)
	defer auth.Close()

	now := time.Now()
	rawToken := signCertToken(
		t, userSigner, cert,
		"/test.Service/Method", now.UnixNano(), "nonce-1",
	)

	_, err := auth.Authenticate(
		context.Background(),
		rawToken,
		&core.RequestInfo{FullMethod: "/test.Service/Method"},
	)
	require.Error(t, err)
	assertGRPCCode(t, err, codes.Unauthenticated)
}

func TestAuthenticator_RevokedCertificate(t *testing.T) {
	ca := generateCA(t)
	cert, userSigner := generateUserCert(
		t,
		ca,
		"alice",
		42,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	store := NewCAStore([]CAEntry{
		{PublicKey: ca.PublicKey()},
	})

	krlData := buildKRL(t, ca, []uint64{42})
	k, err := krl.ParseKRL(krlData)
	require.NoError(t, err)
	checker := NewKRLRevocationChecker(k)

	idp := newMockIdentityProvider(identity.Identity{
		Username: "alice",
	})

	auth := NewAuthenticator(store, checker, idp,
		WithTimeWindow(10*time.Second),
	)
	defer auth.Close()

	now := time.Now()
	rawToken := signCertToken(
		t, userSigner, cert,
		"/test.Service/Method", now.UnixNano(), "nonce-1",
	)

	_, err = auth.Authenticate(
		context.Background(),
		rawToken,
		&core.RequestInfo{FullMethod: "/test.Service/Method"},
	)
	require.Error(t, err)
	assertGRPCCode(t, err, codes.Unauthenticated)
}

func TestAuthenticator_HostCertRejected(t *testing.T) {
	ca := generateCA(t)
	cert, userSigner := generateHostCert(t, ca, "host.example.com", 1)

	store := NewCAStore([]CAEntry{
		{PublicKey: ca.PublicKey()},
	})
	idp := newMockIdentityProvider(identity.Identity{
		Username: "host.example.com",
	})

	auth := NewAuthenticator(
		store,
		NewNopRevocationChecker(),
		idp,
		WithTimeWindow(10*time.Second),
	)
	defer auth.Close()

	now := time.Now()
	rawToken := signCertToken(
		t, userSigner, cert,
		"/test.Service/Method", now.UnixNano(), "nonce-1",
	)

	_, err := auth.Authenticate(
		context.Background(),
		rawToken,
		&core.RequestInfo{FullMethod: "/test.Service/Method"},
	)
	require.Error(t, err)
	assertGRPCCode(t, err, codes.Unauthenticated)
}

func TestAuthenticator_DisabledIdentity(t *testing.T) {
	ca := generateCA(t)
	cert, userSigner := generateUserCert(
		t,
		ca,
		"alice",
		1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	store := NewCAStore([]CAEntry{
		{PublicKey: ca.PublicKey()},
	})
	idp := newMockIdentityProvider(identity.Identity{
		Username: "alice",
		Disabled: true,
	})

	auth := NewAuthenticator(store, NewNopRevocationChecker(), idp,
		WithTimeWindow(10*time.Second),
	)
	defer auth.Close()

	now := time.Now()
	rawToken := signCertToken(
		t, userSigner, cert,
		"/test.Service/Method", now.UnixNano(), "nonce-1",
	)

	_, err := auth.Authenticate(
		context.Background(),
		rawToken,
		&core.RequestInfo{FullMethod: "/test.Service/Method"},
	)
	require.Error(t, err)
	assertGRPCCode(t, err, codes.Unauthenticated)
}

func TestAuthenticator_UnknownIdentity(t *testing.T) {
	ca := generateCA(t)
	cert, userSigner := generateUserCert(
		t,
		ca,
		"unknown-user",
		1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	store := NewCAStore([]CAEntry{
		{PublicKey: ca.PublicKey()},
	})
	// Empty identity provider.
	idp := newMockIdentityProvider()

	auth := NewAuthenticator(store, NewNopRevocationChecker(), idp,
		WithTimeWindow(10*time.Second),
	)
	defer auth.Close()

	now := time.Now()
	rawToken := signCertToken(
		t, userSigner, cert,
		"/test.Service/Method", now.UnixNano(), "nonce-1",
	)

	_, err := auth.Authenticate(
		context.Background(),
		rawToken,
		&core.RequestInfo{FullMethod: "/test.Service/Method"},
	)
	require.Error(t, err)
	assertGRPCCode(t, err, codes.Unauthenticated)
}

func TestAuthenticator_NopRevocationChecker(t *testing.T) {
	ca := generateCA(t)
	cert, userSigner := generateUserCert(
		t,
		ca,
		"alice",
		1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	store := NewCAStore([]CAEntry{
		{PublicKey: ca.PublicKey()},
	})
	idp := newMockIdentityProvider(identity.Identity{
		Username: "alice",
		Groups:   []string{"users"},
	})

	// Nop revocation checker skips KRL check.
	auth := NewAuthenticator(store, NewNopRevocationChecker(), idp,
		WithTimeWindow(10*time.Second),
	)
	defer auth.Close()

	now := time.Now()
	rawToken := signCertToken(
		t, userSigner, cert,
		"/test.Service/Method", now.UnixNano(), "nonce-1",
	)

	principal, err := auth.Authenticate(
		context.Background(),
		rawToken,
		&core.RequestInfo{FullMethod: "/test.Service/Method"},
	)
	require.NoError(t, err)
	assert.Equal(t, "alice", principal.User)
}

// assertGRPCCode asserts that the error has the expected gRPC
// status code.
func assertGRPCCode(t *testing.T, err error, code codes.Code) {
	t.Helper()

	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error, got %v", err)
	assert.Equal(t, code, st.Code())
}
