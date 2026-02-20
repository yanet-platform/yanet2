package sshcert

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func TestParseCertificate_Valid(t *testing.T) {
	ca := generateCA(t)
	cert, _ := generateUserCert(
		t, ca, "alice", 1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	certB64 := encodeCert(t, cert)
	parsed, err := parseCertificate(certB64)
	require.NoError(t, err)
	assert.Equal(t, cert.Serial, parsed.Serial)
}

func TestParseCertificate_InvalidBase64(t *testing.T) {
	_, err := parseCertificate("!!!invalid!!!")
	require.ErrorIs(t, err, ErrInvalidCertificate)
}

func TestParseCertificate_NotACert(t *testing.T) {
	// Encode a plain public key, not a certificate.
	signer := generateECDSASigner(t)
	pubKeyBytes := signer.PublicKey().Marshal()

	_, err := parseCertificate(
		base64.StdEncoding.EncodeToString(pubKeyBytes),
	)
	require.ErrorIs(t, err, ErrInvalidCertificate)
}

func TestCheckKeyType_ECDSA(t *testing.T) {
	ca := generateCA(t)
	cert, _ := generateUserCert(
		t, ca, "alice", 1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	err := checkKeyType(cert)
	require.NoError(t, err)
}

func TestCheckKeyType_Ed25519_Rejected(t *testing.T) {
	ca := generateCA(t)

	// Generate ed25519 user key.
	_, edPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	edSigner, err := ssh.NewSignerFromKey(edPriv)
	require.NoError(t, err)

	cert := &ssh.Certificate{
		CertType:        ssh.UserCert,
		Key:             edSigner.PublicKey(),
		Serial:          1,
		KeyId:           "ed25519-user",
		ValidPrincipals: []string{"alice"},
		ValidAfter:      uint64(time.Now().Add(-1 * time.Hour).Unix()),
		ValidBefore:     uint64(time.Now().Add(24 * time.Hour).Unix()),
	}

	err = cert.SignCert(rand.Reader, ca)
	require.NoError(t, err)

	err = checkKeyType(cert)
	require.ErrorIs(t, err, ErrUnsupportedKeyType)
}

func TestCheckCertType_UserCert(t *testing.T) {
	ca := generateCA(t)
	cert, _ := generateUserCert(
		t, ca, "alice", 1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	err := checkCertType(cert)
	require.NoError(t, err)
}

func TestCheckCertType_HostCert_Rejected(t *testing.T) {
	ca := generateCA(t)
	cert, _ := generateHostCert(t, ca, "host.example.com", 1)

	err := checkCertType(cert)
	require.ErrorIs(t, err, ErrNotUserCert)
}

func TestCheckValidity_Valid(t *testing.T) {
	ca := generateCA(t)
	cert, _ := generateUserCert(
		t, ca, "alice", 1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	err := checkValidity(cert, time.Now())
	require.NoError(t, err)
}

func TestCheckValidity_Expired(t *testing.T) {
	ca := generateCA(t)
	cert, _ := generateUserCert(
		t, ca, "alice", 1,
		time.Now().Add(-48*time.Hour),
		time.Now().Add(-24*time.Hour),
	)

	err := checkValidity(cert, time.Now())
	require.ErrorIs(t, err, ErrCertExpired)
}

func TestCheckValidity_NotYetValid(t *testing.T) {
	ca := generateCA(t)
	cert, _ := generateUserCert(
		t, ca, "alice", 1,
		time.Now().Add(24*time.Hour),
		time.Now().Add(48*time.Hour),
	)

	err := checkValidity(cert, time.Now())
	require.ErrorIs(t, err, ErrCertNotYetValid)
}

func TestExtractPrincipal_Valid(t *testing.T) {
	ca := generateCA(t)
	cert, _ := generateUserCert(
		t, ca, "alice", 1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	username, err := extractPrincipal(cert)
	require.NoError(t, err)
	assert.Equal(t, "alice", username)
}

func TestExtractPrincipal_Empty(t *testing.T) {
	ca := generateCA(t)
	signer := generateECDSASigner(t)

	cert := &ssh.Certificate{
		CertType:        ssh.UserCert,
		Key:             signer.PublicKey(),
		Serial:          1,
		KeyId:           "empty-principals",
		ValidPrincipals: nil,
		ValidAfter:      uint64(time.Now().Add(-1 * time.Hour).Unix()),
		ValidBefore:     uint64(time.Now().Add(24 * time.Hour).Unix()),
	}

	err := cert.SignCert(rand.Reader, ca)
	require.NoError(t, err)

	_, err = extractPrincipal(cert)
	require.ErrorIs(t, err, ErrEmptyPrincipals)
}
