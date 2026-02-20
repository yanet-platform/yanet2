package sshkey

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// generateEd25519Signer creates a new Ed25519 SSH signer.
func generateEd25519Signer(t *testing.T) ssh.Signer {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)

	return signer
}

// generateRSASigner creates a new RSA SSH signer.
func generateRSASigner(t *testing.T) ssh.Signer {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)

	return signer
}

// generateECDSASigner creates a new ECDSA SSH signer.
func generateECDSASigner(t *testing.T) ssh.Signer {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)

	return signer
}

// signToken creates a valid signed token for testing.
func signToken(
	t *testing.T,
	signer ssh.Signer,
	username string,
	method string,
	timestamp int64,
	nonce string,
) string {
	t.Helper()

	token := &Token{
		Version:   tokenVersion,
		Username:  username,
		Timestamp: timestamp,
		Nonce:     nonce,
		Method:    method,
	}

	data := token.canonicalSignedData()
	sig, err := signer.Sign(rand.Reader, data)
	require.NoError(t, err)

	token.Signature = base64.StdEncoding.EncodeToString(ssh.Marshal(sig))

	jsonBytes, err := json.Marshal(token)
	require.NoError(t, err)

	return tokenPrefix + base64.StdEncoding.EncodeToString(jsonBytes)
}

// signData signs the given data with the signer and returns the
// base64-encoded SSH signature.
func signData(t *testing.T, signer ssh.Signer, data []byte) string {
	t.Helper()

	sig, err := signer.Sign(rand.Reader, data)
	require.NoError(t, err)

	return base64.StdEncoding.EncodeToString(ssh.Marshal(sig))
}

// requireGRPCError asserts that err is a gRPC status error with the
// expected code and message.
func requireGRPCError(
	t *testing.T,
	err error,
	code codes.Code,
	msg string,
) {
	t.Helper()

	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error, got %v", err)
	assert.Equal(t, code, st.Code())
	assert.Equal(t, msg, st.Message())
}
