package sshcert

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestVerifySignature_Valid(t *testing.T) {
	ca := generateCA(t)
	cert, userSigner := generateUserCert(
		t,
		ca,
		"alice",
		1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	data := []byte("test data to sign")
	sigB64 := signData(t, userSigner, data)

	err := verifySignature(data, sigB64, cert)
	require.NoError(t, err)
}

func TestVerifySignature_WrongKey(t *testing.T) {
	ca := generateCA(t)
	cert, _ := generateUserCert(
		t,
		ca,
		"alice",
		1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	// Sign with a different key.
	otherSigner := generateECDSASigner(t)
	data := []byte("test data to sign")
	sigB64 := signData(t, otherSigner, data)

	err := verifySignature(data, sigB64, cert)
	require.ErrorIs(t, err, ErrSignatureVerificationFailed)
}

func TestVerifySignature_InvalidBase64(t *testing.T) {
	ca := generateCA(t)
	cert, _ := generateUserCert(
		t,
		ca,
		"alice",
		1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	err := verifySignature([]byte("data"), "!!!invalid!!!", cert)
	require.Error(t, err)
}

func TestVerifySignature_InvalidSSHFormat(t *testing.T) {
	ca := generateCA(t)
	cert, _ := generateUserCert(
		t,
		ca,
		"alice",
		1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	// Valid base64 but not an SSH signature.
	err := verifySignature([]byte("data"), "dGVzdA==", cert)
	require.Error(t, err)
}
