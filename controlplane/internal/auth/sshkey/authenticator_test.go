package sshkey

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/identity"
)

// setupAuthenticator creates an Authenticator with generated keys and
// identities, returning the authenticator and per-user signers.
func setupAuthenticator(t *testing.T) (*Authenticator, map[string]ssh.Signer) {
	t.Helper()

	signerAlice := generateEd25519Signer(t)
	signerBob := generateRSASigner(t)
	signerCharlie := generateECDSASigner(t)

	keyStore := NewKeyStore(map[string][]KeyEntry{
		"alice": {
			{
				PublicKey: signerAlice.PublicKey(),
				Comment:   "alice ed25519",
			},
		},
		"bob": {
			{
				PublicKey: signerBob.PublicKey(),
				Comment:   "bob rsa",
			},
		},
		"charlie": {
			{
				PublicKey: signerCharlie.PublicKey(),
				Comment:   "charlie ecdsa",
			},
		},
	})

	identityProvider := identity.NewIdentityProvider(map[string]identity.Identity{
		"alice":         {Username: "alice", Groups: []string{"admins"}},
		"bob":           {Username: "bob", Groups: []string{"operators"}},
		"charlie":       {Username: "charlie", Groups: []string{"readers"}},
		"disabled_user": {Username: "disabled_user", Disabled: true},
	})

	auth := NewAuthenticator(keyStore, identityProvider,
		WithTimeWindow(5*time.Second),
	)

	signers := map[string]ssh.Signer{
		"alice":   signerAlice,
		"bob":     signerBob,
		"charlie": signerCharlie,
	}

	return auth, signers
}

func TestAuthenticator_IsTokenSupported(t *testing.T) {
	auth := &Authenticator{}

	tests := []struct {
		name  string
		token string
		want  bool
	}{
		{"sshkey prefix", "sshkey abc", true},
		{"SSHKEY prefix", "SSHKEY abc", true},
		{"SshKey prefix", "SshKey abc", true},
		{"basic prefix", "basic abc", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, auth.IsTokenSupported(tt.token))
		})
	}
}

func TestAuthenticate_Ed25519(t *testing.T) {
	auth, signers := setupAuthenticator(t)

	now := time.Now()
	method := "/test.Service/TestMethod"
	reqInfo := &core.RequestInfo{FullMethod: method}

	token := signToken(t, signers["alice"], "alice", method, now.UnixNano(), "nonce-1")

	principal, err := auth.Authenticate(context.Background(), token, reqInfo)
	require.NoError(t, err)
	assert.Equal(t, "alice", principal.User)
	assert.Equal(t, []string{"admins"}, principal.Groups)
	assert.Equal(t, "sshkey", principal.AuthMethod)
	assert.False(t, principal.IsAnonymous)
}

func TestAuthenticate_RSA(t *testing.T) {
	auth, signers := setupAuthenticator(t)

	now := time.Now()
	method := "/test.Service/TestMethod"
	reqInfo := &core.RequestInfo{FullMethod: method}

	token := signToken(t, signers["bob"], "bob", method, now.UnixNano(), "nonce-1")

	principal, err := auth.Authenticate(context.Background(), token, reqInfo)
	require.NoError(t, err)
	assert.Equal(t, "bob", principal.User)
	assert.Equal(t, []string{"operators"}, principal.Groups)
}

func TestAuthenticate_ECDSA(t *testing.T) {
	auth, signers := setupAuthenticator(t)

	now := time.Now()
	method := "/test.Service/TestMethod"
	reqInfo := &core.RequestInfo{FullMethod: method}

	token := signToken(t, signers["charlie"], "charlie", method, now.UnixNano(), "nonce-1")

	principal, err := auth.Authenticate(context.Background(), token, reqInfo)
	require.NoError(t, err)
	assert.Equal(t, "charlie", principal.User)
	assert.Equal(t, []string{"readers"}, principal.Groups)
}

func TestAuthenticate_ExpiredTimestamp(t *testing.T) {
	auth, signers := setupAuthenticator(t)

	now := time.Now()
	method := "/test.Service/TestMethod"
	reqInfo := &core.RequestInfo{FullMethod: method}

	oldTimestamp := now.Add(-10 * time.Second).UnixNano()
	token := signToken(t, signers["alice"], "alice", method, oldTimestamp, "nonce-1")

	_, err := auth.Authenticate(context.Background(), token, reqInfo)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unauthenticated, st.Code())
}

func TestAuthenticate_MethodBindingMismatch(t *testing.T) {
	auth, signers := setupAuthenticator(t)

	now := time.Now()
	reqInfo := &core.RequestInfo{FullMethod: "/test.Service/TestMethod"}

	token := signToken(
		t, signers["alice"], "alice",
		"/other.Service/OtherMethod", now.UnixNano(), "nonce-1",
	)

	_, err := auth.Authenticate(context.Background(), token, reqInfo)
	requireGRPCError(t, err, codes.Unauthenticated,
		`method binding mismatch: token method "/other.Service/OtherMethod" != request method "/test.Service/TestMethod"`,
	)
}

func TestAuthenticate_UnknownUser(t *testing.T) {
	auth, signers := setupAuthenticator(t)

	now := time.Now()
	method := "/test.Service/TestMethod"
	reqInfo := &core.RequestInfo{FullMethod: method}

	token := signToken(t, signers["alice"], "unknown_user", method, now.UnixNano(), "nonce-1")

	_, err := auth.Authenticate(context.Background(), token, reqInfo)
	requireGRPCError(t, err, codes.Unauthenticated,
		`no SSH keys found for user "unknown_user"`,
	)
}

func TestAuthenticate_WrongSignature(t *testing.T) {
	auth, signers := setupAuthenticator(t)

	now := time.Now()
	method := "/test.Service/TestMethod"
	reqInfo := &core.RequestInfo{FullMethod: method}

	// Sign with bob's key but claim to be alice.
	token := signToken(t, signers["bob"], "alice", method, now.UnixNano(), "nonce-1")

	_, err := auth.Authenticate(context.Background(), token, reqInfo)
	requireGRPCError(t, err, codes.Unauthenticated,
		"signature verification failed: signature verification failed",
	)
}

func TestAuthenticate_DisabledIdentity(t *testing.T) {
	signer := generateEd25519Signer(t)

	keyStore := NewKeyStore(map[string][]KeyEntry{
		"alice": {
			{PublicKey: signer.PublicKey(), Comment: "key"},
		},
	})

	identityProvider := identity.NewIdentityProvider(map[string]identity.Identity{
		"alice": {Username: "alice", Disabled: true},
	})

	auth := NewAuthenticator(keyStore, identityProvider)

	method := "/test.Service/Method"
	reqInfo := &core.RequestInfo{FullMethod: method}
	now := time.Now()

	token := signToken(t, signer, "alice", method, now.UnixNano(), "nonce-1")

	_, err := auth.Authenticate(context.Background(), token, reqInfo)
	requireGRPCError(t, err, codes.Unauthenticated,
		`account "alice" is disabled`,
	)
}

func TestAuthenticate_DisabledIdentityNoKeys(t *testing.T) {
	auth, signers := setupAuthenticator(t)

	now := time.Now()
	method := "/test.Service/TestMethod"
	reqInfo := &core.RequestInfo{FullMethod: method}

	// disabled_user exists in identities but has no keys in the
	// key store, so it fails on key lookup.
	token := signToken(t, signers["alice"], "disabled_user", method, now.UnixNano(), "nonce-1")

	_, err := auth.Authenticate(context.Background(), token, reqInfo)
	requireGRPCError(t, err, codes.Unauthenticated,
		`no SSH keys found for user "disabled_user"`,
	)
}

func TestAuthenticate_InvalidTokenFormat(t *testing.T) {
	auth, _ := setupAuthenticator(t)
	reqInfo := &core.RequestInfo{FullMethod: "/test.Service/TestMethod"}

	_, err := auth.Authenticate(context.Background(), "sshkey invalid", reqInfo)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unauthenticated, st.Code())
}

func TestAuthenticate_NilRequestInfo(t *testing.T) {
	auth, signers := setupAuthenticator(t)

	now := time.Now()
	method := "/test.Service/TestMethod"

	token := signToken(t, signers["alice"], "alice", method, now.UnixNano(), "nonce-1")

	principal, err := auth.Authenticate(context.Background(), token, nil)
	require.NoError(t, err)
	assert.Equal(t, "alice", principal.User)
}

func TestAuthenticate_EmptyFullMethod(t *testing.T) {
	auth, signers := setupAuthenticator(t)

	now := time.Now()
	method := "/test.Service/TestMethod"

	token := signToken(t, signers["alice"], "alice", method, now.UnixNano(), "nonce-1")

	emptyReqInfo := &core.RequestInfo{}
	principal, err := auth.Authenticate(context.Background(), token, emptyReqInfo)
	require.NoError(t, err)
	assert.Equal(t, "alice", principal.User)
}
