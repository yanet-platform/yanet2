package sshkey

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseToken(t *testing.T) {
	signer := generateEd25519Signer(t)
	now := time.Now()

	raw := signToken(t, signer, "alice", "/test.Service/Method", now.UnixNano(), "nonce-1")

	token, err := parseToken(raw)
	require.NoError(t, err)
	assert.Equal(t, tokenVersion, token.Version)
	assert.Equal(t, "alice", token.Username)
	assert.Equal(t, "/test.Service/Method", token.Method)
	assert.Equal(t, "nonce-1", token.Nonce)
	assert.NotEmpty(t, token.Signature)
}

func TestParseToken_WrongPrefix(t *testing.T) {
	_, err := parseToken("basic dGVzdA==")
	require.ErrorIs(t, err, ErrInvalidTokenPrefix)
}

func TestParseToken_InvalidBase64(t *testing.T) {
	_, err := parseToken("sshkey !!!invalid-base64!!!")
	require.Error(t, err)
}

func TestParseToken_InvalidJSON(t *testing.T) {
	raw := "sshkey " + base64.StdEncoding.EncodeToString([]byte("not-json"))
	_, err := parseToken(raw)
	require.Error(t, err)
}

func TestParseToken_UnsupportedVersion(t *testing.T) {
	payload := `{"version":99,"username":"alice","timestamp":1,"nonce":"n","method":"/m","signature":"sig"}`
	raw := "sshkey " + base64.StdEncoding.EncodeToString([]byte(payload))

	_, err := parseToken(raw)
	require.ErrorIs(t, err, ErrUnsupportedVersion)
}

func TestParseToken_EmptyUsername(t *testing.T) {
	payload := `{"version":1,"username":"","timestamp":1,"nonce":"n","method":"/m","signature":"sig"}`
	raw := "sshkey " + base64.StdEncoding.EncodeToString([]byte(payload))

	_, err := parseToken(raw)
	require.ErrorIs(t, err, ErrEmptyUsername)
}

func TestParseToken_EmptyNonce(t *testing.T) {
	payload := `{"version":1,"username":"alice","timestamp":1,"nonce":"","method":"/m","signature":"sig"}`
	raw := "sshkey " + base64.StdEncoding.EncodeToString([]byte(payload))

	_, err := parseToken(raw)
	require.ErrorIs(t, err, ErrEmptyNonce)
}

func TestParseToken_EmptyMethod(t *testing.T) {
	payload := `{"version":1,"username":"alice","timestamp":1,"nonce":"n","method":"","signature":"sig"}`
	raw := "sshkey " + base64.StdEncoding.EncodeToString([]byte(payload))

	_, err := parseToken(raw)
	require.ErrorIs(t, err, ErrEmptyMethod)
}

func TestParseToken_EmptySignature(t *testing.T) {
	payload := `{"version":1,"username":"alice","timestamp":1,"nonce":"n","method":"/m","signature":""}`
	raw := "sshkey " + base64.StdEncoding.EncodeToString([]byte(payload))

	_, err := parseToken(raw)
	require.ErrorIs(t, err, ErrEmptySignature)
}

func TestToken_CanonicalSignedData(t *testing.T) {
	token := &Token{
		Version:   1,
		Username:  "alice",
		Timestamp: 1234567890,
		Nonce:     "test-nonce",
		Method:    "/test.Service/Method",
	}

	expected := "version=1\nusername=alice\ntimestamp=1234567890\nnonce=test-nonce\nmethod=/test.Service/Method"
	assert.Equal(t, expected, string(token.canonicalSignedData()))
}
