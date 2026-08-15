package sshkey_test

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/sshkey"
)

func authenticateKeyToken(
	t *testing.T,
	keys map[string][]sshkey.KeyEntry,
	rawToken string,
) error {
	t.Helper()

	authenticator := sshkey.NewAuthenticator(
		sshkey.NewKeyStore(keys),
		sshkey.WithTimeWindow(time.Minute),
	)
	_, err := authenticator.Authenticate(
		t.Context(),
		rawToken,
		&core.RequestInfo{FullMethod: "/test.Service/Method"},
	)
	return err
}

func TestVerifySignature_Ed25519(t *testing.T) {
	signer := generateTestEd25519Signer(t)
	token := signTestToken(
		t, signer, "alice", "/test.Service/Method",
		time.Now().UnixNano(), "nonce-1",
	)

	err := authenticateKeyToken(t, map[string][]sshkey.KeyEntry{
		"alice": {{PublicKey: signer.PublicKey()}},
	}, token)
	require.NoError(t, err)
}

func TestVerifySignature_RSA(t *testing.T) {
	signer := generateTestRSASigner(t)
	token := signTestToken(
		t, signer, "alice", "/test.Service/Method",
		time.Now().UnixNano(), "nonce-1",
	)

	err := authenticateKeyToken(t, map[string][]sshkey.KeyEntry{
		"alice": {{PublicKey: signer.PublicKey()}},
	}, token)
	require.NoError(t, err)
}

func TestVerifySignature_ECDSA(t *testing.T) {
	signer := generateTestECDSASigner(t)
	token := signTestToken(
		t, signer, "alice", "/test.Service/Method",
		time.Now().UnixNano(), "nonce-1",
	)

	err := authenticateKeyToken(t, map[string][]sshkey.KeyEntry{
		"alice": {{PublicKey: signer.PublicKey()}},
	}, token)
	require.NoError(t, err)
}

func TestVerifySignature_WrongKey(t *testing.T) {
	signerEd25519 := generateTestEd25519Signer(t)
	signerRSA := generateTestRSASigner(t)
	token := signTestToken(
		t, signerEd25519, "alice", "/test.Service/Method",
		time.Now().UnixNano(), "nonce-1",
	)

	err := authenticateKeyToken(t, map[string][]sshkey.KeyEntry{
		"alice": {{PublicKey: signerRSA.PublicKey()}},
	}, token)
	require.Error(t, err)
	require.Contains(t, err.Error(), "signature verification failed")
}

func TestVerifySignature_MultipleKeysMatchesSecond(t *testing.T) {
	signerEd25519 := generateTestEd25519Signer(t)
	signerRSA := generateTestRSASigner(t)
	token := signTestToken(
		t, signerRSA, "alice", "/test.Service/Method",
		time.Now().UnixNano(), "nonce-1",
	)

	err := authenticateKeyToken(t, map[string][]sshkey.KeyEntry{
		"alice": {
			{PublicKey: signerEd25519.PublicKey()},
			{PublicKey: signerRSA.PublicKey()},
		},
	}, token)
	require.NoError(t, err)
}

func TestVerifySignature_NoKeys(t *testing.T) {
	signer := generateTestEd25519Signer(t)
	token := signTestToken(
		t, signer, "alice", "/test.Service/Method",
		time.Now().UnixNano(), "nonce-1",
	)

	err := authenticateKeyToken(t, map[string][]sshkey.KeyEntry{}, token)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no SSH keys found")
}

func TestVerifySignature_InvalidBase64(t *testing.T) {
	signer := generateTestEd25519Signer(t)
	token := rawKeyToken(t, "alice", "!!!invalid!!!")

	err := authenticateKeyToken(t, map[string][]sshkey.KeyEntry{
		"alice": {{PublicKey: signer.PublicKey()}},
	}, token)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid base64 signature")
}

func TestVerifySignature_InvalidSSHSignatureFormat(t *testing.T) {
	signer := generateTestEd25519Signer(t)
	token := rawKeyToken(t, "alice", base64.StdEncoding.EncodeToString([]byte("not-a-valid-ssh-sig")))

	err := authenticateKeyToken(t, map[string][]sshkey.KeyEntry{
		"alice": {{PublicKey: signer.PublicKey()}},
	}, token)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid SSH signature format")
}

func rawKeyToken(t *testing.T, username, signature string) string {
	t.Helper()

	token := &sshkey.Token{
		Version:   1,
		Username:  username,
		Timestamp: time.Now().UnixNano(),
		Nonce:     "nonce-1",
		Method:    "/test.Service/Method",
		Signature: signature,
	}
	data, err := json.Marshal(token)
	require.NoError(t, err)

	return "sshkey " + base64.StdEncoding.EncodeToString(data)
}
