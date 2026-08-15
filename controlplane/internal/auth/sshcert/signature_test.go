package sshcert_test

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/sshcert"
)

func authenticateWithSignature(
	t *testing.T,
	ca ssh.Signer,
	cert *ssh.Certificate,
	signature string,
) error {
	t.Helper()

	authenticator := sshcert.NewAuthenticator(
		sshcert.NewCAStore([]sshcert.CAEntry{{PublicKey: ca.PublicKey()}}),
		sshcert.NewNopRevocationChecker(),
		sshcert.WithTimeWindow(time.Minute),
	)
	t.Cleanup(authenticator.Close)

	token := &sshcert.Token{
		Version:     1,
		Certificate: base64.StdEncoding.EncodeToString(cert.Marshal()),
		Timestamp:   time.Now().UnixNano(),
		Nonce:       "nonce-1",
		Method:      "/test.Service/Method",
		Signature:   signature,
	}
	encoded, err := json.Marshal(token)
	require.NoError(t, err)

	_, err = authenticator.Authenticate(
		t.Context(),
		"sshcert "+base64.StdEncoding.EncodeToString(encoded),
		&core.RequestInfo{FullMethod: "/test.Service/Method"},
	)
	return err
}

func TestVerifySignature_WrongKey(t *testing.T) {
	ca := generateTestCA(t)
	cert, _ := generateTestUserCert(
		t, ca, "alice", 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour),
	)
	otherSigner := generateTestECDSASigner(t)

	err := authenticateCertificate(t, ca, cert, otherSigner)
	require.Error(t, err)
	require.Contains(t, err.Error(), "signature verification failed")
}

func TestVerifySignature_InvalidBase64(t *testing.T) {
	ca := generateTestCA(t)
	cert, _ := generateTestUserCert(
		t, ca, "alice", 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour),
	)

	err := authenticateWithSignature(t, ca, cert, "!!!invalid!!!")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid base64 signature")
}

func TestVerifySignature_InvalidSSHFormat(t *testing.T) {
	ca := generateTestCA(t)
	cert, _ := generateTestUserCert(
		t, ca, "alice", 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour),
	)

	err := authenticateWithSignature(t, ca, cert, "dGVzdA==")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid SSH signature format")
}
