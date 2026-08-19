package sshcert_test

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/sshcert"
)

func authenticateRawToken(t *testing.T, raw string) error {
	t.Helper()

	authenticator := sshcert.NewAuthenticator(
		sshcert.NewCAStore(nil),
		sshcert.NewNopRevocationChecker(),
		sshcert.WithTimeWindow(time.Minute),
	)
	t.Cleanup(authenticator.Close)

	_, err := authenticator.Authenticate(
		t.Context(),
		raw,
		&core.RequestInfo{FullMethod: "/test.Service/Method"},
	)
	return err
}

func rawToken(t *testing.T, payload string) string {
	t.Helper()

	return "sshcert " + base64.StdEncoding.EncodeToString([]byte(payload))
}

func TestParseToken(t *testing.T) {
	ca := generateTestCA(t)
	cert, userSigner := generateTestUserCert(
		t, ca, "alice", 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour),
	)
	raw := signTestCertToken(
		t, userSigner, cert, "/test.Service/Method",
		time.Now().UnixNano(), "nonce-1",
	)

	authenticator := sshcert.NewAuthenticator(
		sshcert.NewCAStore([]sshcert.CAEntry{{PublicKey: ca.PublicKey()}}),
		sshcert.NewNopRevocationChecker(),
		sshcert.WithTimeWindow(time.Minute),
	)
	t.Cleanup(authenticator.Close)

	authInfo, err := authenticator.Authenticate(
		t.Context(), raw,
		&core.RequestInfo{FullMethod: "/test.Service/Method"},
	)
	require.NoError(t, err)
	assert.Equal(t, core.NewLocalSubject("alice"), authInfo.Subject)
}

func TestParseToken_WrongPrefix(t *testing.T) {
	err := authenticateRawToken(t, "sshkey dGVzdA==")
	require.Error(t, err)
	require.Contains(t, err.Error(), sshcert.ErrInvalidTokenPrefix.Error())
}

func TestParseToken_InvalidBase64(t *testing.T) {
	err := authenticateRawToken(t, "sshcert !!!invalid-base64!!!")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid base64 encoding")
}

func TestParseToken_InvalidJSON(t *testing.T) {
	err := authenticateRawToken(t, rawToken(t, "not-json"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid JSON payload")
}

func TestParseToken_UnsupportedVersion(t *testing.T) {
	payload := `{"version":99,"certificate":"c","timestamp":1,"nonce":"n","method":"/m","signature":"sig"}`
	err := authenticateRawToken(t, rawToken(t, payload))
	require.Error(t, err)
	require.Contains(t, err.Error(), sshcert.ErrUnsupportedVersion.Error())
}

func TestParseToken_EmptyCertificate(t *testing.T) {
	payload := `{"version":1,"certificate":"","timestamp":1,"nonce":"n","method":"/m","signature":"sig"}`
	err := authenticateRawToken(t, rawToken(t, payload))
	require.Error(t, err)
	require.Contains(t, err.Error(), sshcert.ErrEmptyCertificate.Error())
}

func TestParseToken_EmptyNonce(t *testing.T) {
	payload := `{"version":1,"certificate":"c","timestamp":1,"nonce":"","method":"/m","signature":"sig"}`
	err := authenticateRawToken(t, rawToken(t, payload))
	require.Error(t, err)
	require.Contains(t, err.Error(), sshcert.ErrEmptyNonce.Error())
}

func TestParseToken_EmptyMethod(t *testing.T) {
	payload := `{"version":1,"certificate":"c","timestamp":1,"nonce":"n","method":"","signature":"sig"}`
	err := authenticateRawToken(t, rawToken(t, payload))
	require.Error(t, err)
	require.Contains(t, err.Error(), sshcert.ErrEmptyMethod.Error())
}

func TestParseToken_EmptySignature(t *testing.T) {
	payload := `{"version":1,"certificate":"c","timestamp":1,"nonce":"n","method":"/m","signature":""}`
	err := authenticateRawToken(t, rawToken(t, payload))
	require.Error(t, err)
	require.Contains(t, err.Error(), sshcert.ErrEmptySignature.Error())
}

func TestToken_CanonicalSignedDataAuthenticates(t *testing.T) {
	ca := generateTestCA(t)
	cert, userSigner := generateTestUserCert(
		t, ca, "alice", 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour),
	)
	raw := signTestCertToken(
		t, userSigner, cert, "/test.Service/Method",
		time.Now().UnixNano(), "nonce-1",
	)

	authenticator := sshcert.NewAuthenticator(
		sshcert.NewCAStore([]sshcert.CAEntry{{PublicKey: ca.PublicKey()}}),
		sshcert.NewNopRevocationChecker(),
		sshcert.WithTimeWindow(time.Minute),
	)
	t.Cleanup(authenticator.Close)

	_, err := authenticator.Authenticate(
		t.Context(), raw,
		&core.RequestInfo{FullMethod: "/test.Service/Method"},
	)
	require.NoError(t, err)
}

func TestToken_CanonicalSignedData(t *testing.T) {
	token := &sshcert.Token{
		Version:     1,
		Certificate: "cert-data",
		Timestamp:   1234567890,
		Nonce:       "test-nonce",
		Method:      "/test.Service/Method",
	}

	expected := "version=1\ncertificate=cert-data\n" +
		"timestamp=1234567890\nnonce=test-nonce\n" +
		"method=/test.Service/Method"
	assert.Equal(t, expected, string(token.CanonicalSignedData()))
}
