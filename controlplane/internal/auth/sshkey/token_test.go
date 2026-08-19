package sshkey_test

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/sshkey"
)

func TestParseToken(t *testing.T) {
	signer := generateTestEd25519Signer(t)
	raw := signTestToken(
		t, signer, "alice", "/test.Service/Method",
		time.Now().UnixNano(), "nonce-1",
	)
	authenticator := sshkey.NewAuthenticator(
		sshkey.NewKeyStore(map[string][]sshkey.KeyEntry{
			"alice": {{PublicKey: signer.PublicKey()}},
		}),
	)

	authInfo, err := authenticator.Authenticate(
		t.Context(), raw,
		&core.RequestInfo{FullMethod: "/test.Service/Method"},
	)
	require.NoError(t, err)
	assert.Equal(t, core.NewLocalSubject("alice"), authInfo.Subject)
}

func authenticateRawKeyToken(t *testing.T, raw string) error {
	t.Helper()

	authenticator := sshkey.NewAuthenticator(sshkey.NewKeyStore(map[string][]sshkey.KeyEntry{}))
	_, err := authenticator.Authenticate(
		t.Context(), raw,
		&core.RequestInfo{FullMethod: "/test.Service/Method"},
	)
	return err
}

func rawKeyTokenPayload(payload string) string {
	return "sshkey " + base64.StdEncoding.EncodeToString([]byte(payload))
}

func TestParseToken_WrongPrefix(t *testing.T) {
	err := authenticateRawKeyToken(t, "basic dGVzdA==")
	require.Error(t, err)
	require.Contains(t, err.Error(), sshkey.ErrInvalidTokenPrefix.Error())
}

func TestParseToken_InvalidBase64(t *testing.T) {
	err := authenticateRawKeyToken(t, "sshkey !!!invalid-base64!!!")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid base64 encoding")
}

func TestParseToken_InvalidJSON(t *testing.T) {
	err := authenticateRawKeyToken(t, rawKeyTokenPayload("not-json"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid JSON payload")
}

func TestParseToken_UnsupportedVersion(t *testing.T) {
	payload := `{"version":99,"username":"alice","timestamp":1,"nonce":"n","method":"/m","signature":"sig"}`
	err := authenticateRawKeyToken(t, rawKeyTokenPayload(payload))
	require.Error(t, err)
	require.Contains(t, err.Error(), sshkey.ErrUnsupportedVersion.Error())
}

func TestParseToken_EmptyUsername(t *testing.T) {
	payload := `{"version":1,"username":"","timestamp":1,"nonce":"n","method":"/m","signature":"sig"}`
	err := authenticateRawKeyToken(t, rawKeyTokenPayload(payload))
	require.Error(t, err)
	require.Contains(t, err.Error(), sshkey.ErrEmptyUsername.Error())
}

func TestParseToken_EmptyNonce(t *testing.T) {
	payload := `{"version":1,"username":"alice","timestamp":1,"nonce":"","method":"/m","signature":"sig"}`
	err := authenticateRawKeyToken(t, rawKeyTokenPayload(payload))
	require.Error(t, err)
	require.Contains(t, err.Error(), sshkey.ErrEmptyNonce.Error())
}

func TestParseToken_EmptyMethod(t *testing.T) {
	payload := `{"version":1,"username":"alice","timestamp":1,"nonce":"n","method":"","signature":"sig"}`
	err := authenticateRawKeyToken(t, rawKeyTokenPayload(payload))
	require.Error(t, err)
	require.Contains(t, err.Error(), sshkey.ErrEmptyMethod.Error())
}

func TestParseToken_EmptySignature(t *testing.T) {
	payload := `{"version":1,"username":"alice","timestamp":1,"nonce":"n","method":"/m","signature":""}`
	err := authenticateRawKeyToken(t, rawKeyTokenPayload(payload))
	require.Error(t, err)
	require.Contains(t, err.Error(), sshkey.ErrEmptySignature.Error())
}

func TestToken_CanonicalSignedData(t *testing.T) {
	token := &sshkey.Token{
		Version:   1,
		Username:  "alice",
		Timestamp: 1234567890,
		Nonce:     "test-nonce",
		Method:    "/test.Service/Method",
	}

	expected := "version=1\nusername=alice\ntimestamp=1234567890\n" +
		"nonce=test-nonce\nmethod=/test.Service/Method"
	assert.Equal(t, expected, string(token.CanonicalSignedData()))
}
