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
)

func TestAuthenticator_Name(t *testing.T) {
	ca := generateCA(t)
	store := NewCAStore([]CAEntry{
		{PublicKey: ca.PublicKey()},
	})

	auth := NewAuthenticator(store, NewNopRevocationChecker())
	defer auth.Close()

	assert.Equal(t, "sshcert", auth.Name())
}

func TestAuthenticator_IsTokenSupported(t *testing.T) {
	ca := generateCA(t)
	store := NewCAStore([]CAEntry{
		{PublicKey: ca.PublicKey()},
	})

	auth := NewAuthenticator(store, NewNopRevocationChecker())
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

	auth := NewAuthenticator(store, NewNopRevocationChecker(),
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

	authInfo, err := auth.Authenticate(
		context.Background(),
		rawToken,
		&core.RequestInfo{FullMethod: "/test.Service/Method"},
	)
	require.NoError(t, err)
	assert.Equal(t, "alice", authInfo.Username)
	assert.Equal(t, "sshcert", authInfo.AuthMethod)
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

	auth := NewAuthenticator(store, NewNopRevocationChecker(),
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

	auth := NewAuthenticator(store, NewNopRevocationChecker(),
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

	auth := NewAuthenticator(store, NewNopRevocationChecker(),
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

	auth := NewAuthenticator(store, NewNopRevocationChecker(),
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

	auth := NewAuthenticator(store, checker,
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

	auth := NewAuthenticator(
		store,
		NewNopRevocationChecker(),
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

	// Nop revocation checker skips KRL check.
	auth := NewAuthenticator(store, NewNopRevocationChecker(),
		WithTimeWindow(10*time.Second),
	)
	defer auth.Close()

	now := time.Now()
	rawToken := signCertToken(
		t, userSigner, cert,
		"/test.Service/Method", now.UnixNano(), "nonce-1",
	)

	authInfo, err := auth.Authenticate(
		context.Background(),
		rawToken,
		&core.RequestInfo{FullMethod: "/test.Service/Method"},
	)
	require.NoError(t, err)
	assert.Equal(t, "alice", authInfo.Username)
	assert.Equal(t, "sshcert", authInfo.AuthMethod)
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
