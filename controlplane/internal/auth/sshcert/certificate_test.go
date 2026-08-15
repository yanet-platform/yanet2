package sshcert_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/sshcert"
)

func authenticateCertificate(
	t *testing.T,
	ca ssh.Signer,
	cert *ssh.Certificate,
	userSigner ssh.Signer,
) error {
	t.Helper()

	authenticator := sshcert.NewAuthenticator(
		sshcert.NewCAStore([]sshcert.CAEntry{{PublicKey: ca.PublicKey()}}),
		sshcert.NewNopRevocationChecker(),
		sshcert.WithTimeWindow(time.Minute),
	)
	t.Cleanup(authenticator.Close)

	token := signTestCertToken(
		t,
		userSigner,
		cert,
		"/test.Service/Method",
		time.Now().UnixNano(),
		"nonce-1",
	)
	_, err := authenticator.Authenticate(
		t.Context(),
		token,
		&core.RequestInfo{FullMethod: "/test.Service/Method"},
	)
	return err
}

func certificateToken(t *testing.T, certificate string) string {
	t.Helper()

	token := &sshcert.Token{
		Version:     1,
		Certificate: certificate,
		Timestamp:   time.Now().UnixNano(),
		Nonce:       "nonce-1",
		Method:      "/test.Service/Method",
		Signature:   "sig",
	}
	encoded, err := json.Marshal(token)
	require.NoError(t, err)

	return "sshcert " + base64.StdEncoding.EncodeToString(encoded)
}

func TestParseCertificate_Valid(t *testing.T) {
	ca := generateTestCA(t)
	cert, userSigner := generateTestUserCert(
		t, ca, "alice", 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour),
	)

	require.NoError(t, authenticateCertificate(t, ca, cert, userSigner))
}

func TestParseCertificate_InvalidBase64(t *testing.T) {
	ca := generateTestCA(t)
	authenticator := sshcert.NewAuthenticator(
		sshcert.NewCAStore([]sshcert.CAEntry{{PublicKey: ca.PublicKey()}}),
		sshcert.NewNopRevocationChecker(),
	)
	t.Cleanup(authenticator.Close)

	_, err := authenticator.Authenticate(
		t.Context(),
		certificateToken(t, "!!!invalid!!!"),
		&core.RequestInfo{FullMethod: "/test.Service/Method"},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid certificate: invalid base64")
}

func TestParseCertificate_NotACert(t *testing.T) {
	signer := generateTestECDSASigner(t)
	certificate := base64.StdEncoding.EncodeToString(signer.PublicKey().Marshal())

	ca := generateTestCA(t)
	authenticator := sshcert.NewAuthenticator(
		sshcert.NewCAStore([]sshcert.CAEntry{{PublicKey: ca.PublicKey()}}),
		sshcert.NewNopRevocationChecker(),
	)
	t.Cleanup(authenticator.Close)

	_, err := authenticator.Authenticate(
		t.Context(),
		certificateToken(t, certificate),
		&core.RequestInfo{FullMethod: "/test.Service/Method"},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid certificate: not a certificate")
}

func TestCheckKeyType_ECDSA(t *testing.T) {
	ca := generateTestCA(t)
	cert, userSigner := generateTestUserCert(
		t, ca, "alice", 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour),
	)

	require.NoError(t, authenticateCertificate(t, ca, cert, userSigner))
}

func TestCheckKeyType_Ed25519_Rejected(t *testing.T) {
	ca := generateTestCA(t)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	edSigner, err := ssh.NewSignerFromKey(privateKey)
	require.NoError(t, err)

	cert := &ssh.Certificate{
		CertType:        ssh.UserCert,
		Key:             edSigner.PublicKey(),
		Serial:          1,
		KeyId:           "ed25519-user",
		ValidPrincipals: []string{"alice"},
		ValidAfter:      uint64(time.Now().Add(-time.Hour).Unix()),
		ValidBefore:     uint64(time.Now().Add(time.Hour).Unix()),
	}
	require.NoError(t, cert.SignCert(rand.Reader, ca))

	err = authenticateCertificate(t, ca, cert, edSigner)
	require.Error(t, err)
	require.Contains(t, err.Error(), "certificate key type check failed: unsupported key type")
}

func TestCheckCertType_UserCert(t *testing.T) {
	ca := generateTestCA(t)
	cert, userSigner := generateTestUserCert(
		t, ca, "alice", 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour),
	)

	require.NoError(t, authenticateCertificate(t, ca, cert, userSigner))
}

func TestCheckCertType_HostCert_Rejected(t *testing.T) {
	ca := generateTestCA(t)
	cert, userSigner := generateTestHostCert(t, ca, "host.example.com", 1)

	err := authenticateCertificate(t, ca, cert, userSigner)
	require.Error(t, err)
	require.Contains(t, err.Error(), "certificate type check failed: not a user certificate")
}

func TestCheckValidity_Valid(t *testing.T) {
	ca := generateTestCA(t)
	cert, userSigner := generateTestUserCert(
		t, ca, "alice", 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour),
	)

	require.NoError(t, authenticateCertificate(t, ca, cert, userSigner))
}

func TestCheckValidity_Expired(t *testing.T) {
	ca := generateTestCA(t)
	cert, userSigner := generateTestUserCert(
		t, ca, "alice", 1,
		time.Now().Add(-48*time.Hour), time.Now().Add(-24*time.Hour),
	)

	err := authenticateCertificate(t, ca, cert, userSigner)
	require.Error(t, err)
	require.Contains(t, err.Error(), "certificate validity check failed: certificate expired")
}

func TestCheckValidity_NotYetValid(t *testing.T) {
	ca := generateTestCA(t)
	cert, userSigner := generateTestUserCert(
		t, ca, "alice", 1,
		time.Now().Add(24*time.Hour), time.Now().Add(48*time.Hour),
	)

	err := authenticateCertificate(t, ca, cert, userSigner)
	require.Error(t, err)
	require.Contains(t, err.Error(), "certificate validity check failed: certificate not yet valid")
}

func TestExtractPrincipal_Valid(t *testing.T) {
	ca := generateTestCA(t)
	cert, userSigner := generateTestUserCert(
		t, ca, "alice", 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour),
	)

	require.NoError(t, authenticateCertificate(t, ca, cert, userSigner))
}

func TestExtractPrincipal_Empty(t *testing.T) {
	ca := generateTestCA(t)
	signer := generateTestECDSASigner(t)

	cert := &ssh.Certificate{
		CertType:        ssh.UserCert,
		Key:             signer.PublicKey(),
		Serial:          1,
		KeyId:           "empty-principals",
		ValidAfter:      uint64(time.Now().Add(-time.Hour).Unix()),
		ValidBefore:     uint64(time.Now().Add(time.Hour).Unix()),
		ValidPrincipals: nil,
	}
	require.NoError(t, cert.SignCert(rand.Reader, ca))

	err := authenticateCertificate(t, ca, cert, signer)
	require.Error(t, err)
	require.Contains(t, err.Error(), "CA verification failed: certificate has no principals")
}
