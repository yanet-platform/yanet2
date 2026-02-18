package sshcert

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseToken(t *testing.T) {
	ca := generateCA(t)
	cert, userSigner := generateUserCert(
		t,
		ca,
		"alice",
		1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	now := time.Now()
	raw := signCertToken(
		t,
		userSigner,
		cert,
		"/test.Service/Method",
		now.UnixNano(),
		"nonce-1",
	)

	token, err := parseToken(raw)
	require.NoError(t, err)
	assert.Equal(t, tokenVersion, token.Version)
	assert.NotEmpty(t, token.Certificate)
	assert.Equal(t, "/test.Service/Method", token.Method)
	assert.Equal(t, "nonce-1", token.Nonce)
	assert.NotEmpty(t, token.Signature)
}

func TestParseToken_WrongPrefix(t *testing.T) {
	_, err := parseToken("sshkey dGVzdA==")
	require.ErrorIs(t, err, ErrInvalidTokenPrefix)
}

func TestParseToken_InvalidBase64(t *testing.T) {
	_, err := parseToken("sshcert !!!invalid-base64!!!")
	require.Error(t, err)
}

func TestParseToken_InvalidJSON(t *testing.T) {
	raw := "sshcert " + base64.StdEncoding.EncodeToString(
		[]byte("not-json"),
	)
	_, err := parseToken(raw)
	require.Error(t, err)
}

func TestParseToken_UnsupportedVersion(t *testing.T) {
	payload := `{"version":99,"certificate":"c","timestamp":1,"nonce":"n","method":"/m","signature":"sig"}`
	raw := "sshcert " + base64.StdEncoding.EncodeToString(
		[]byte(payload),
	)

	_, err := parseToken(raw)
	require.ErrorIs(t, err, ErrUnsupportedVersion)
}

func TestParseToken_EmptyCertificate(t *testing.T) {
	payload := `{"version":1,"certificate":"","timestamp":1,"nonce":"n","method":"/m","signature":"sig"}`
	raw := "sshcert " + base64.StdEncoding.EncodeToString(
		[]byte(payload),
	)

	_, err := parseToken(raw)
	require.ErrorIs(t, err, ErrEmptyCertificate)
}

func TestParseToken_EmptyNonce(t *testing.T) {
	payload := `{"version":1,"certificate":"c","timestamp":1,"nonce":"","method":"/m","signature":"sig"}`
	raw := "sshcert " + base64.StdEncoding.EncodeToString(
		[]byte(payload),
	)

	_, err := parseToken(raw)
	require.ErrorIs(t, err, ErrEmptyNonce)
}

func TestParseToken_EmptyMethod(t *testing.T) {
	payload := `{"version":1,"certificate":"c","timestamp":1,"nonce":"n","method":"","signature":"sig"}`
	raw := "sshcert " + base64.StdEncoding.EncodeToString(
		[]byte(payload),
	)

	_, err := parseToken(raw)
	require.ErrorIs(t, err, ErrEmptyMethod)
}

func TestParseToken_EmptySignature(t *testing.T) {
	payload := `{"version":1,"certificate":"c","timestamp":1,"nonce":"n","method":"/m","signature":""}`
	raw := "sshcert " + base64.StdEncoding.EncodeToString(
		[]byte(payload),
	)

	_, err := parseToken(raw)
	require.ErrorIs(t, err, ErrEmptySignature)
}

func TestToken_CanonicalSignedData(t *testing.T) {
	token := &Token{
		Version:     1,
		Certificate: "cert-data",
		Timestamp:   1234567890,
		Nonce:       "test-nonce",
		Method:      "/test.Service/Method",
	}

	expected := "version=1\ncertificate=cert-data\n" +
		"timestamp=1234567890\nnonce=test-nonce\n" +
		"method=/test.Service/Method"
	assert.Equal(t, expected, string(token.canonicalSignedData()))
}
