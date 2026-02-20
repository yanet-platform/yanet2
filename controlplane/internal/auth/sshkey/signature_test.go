package sshkey

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func TestVerifySignature_Ed25519(t *testing.T) {
	signer := generateEd25519Signer(t)
	data := []byte("test data to sign")

	sigB64 := signData(t, signer, data)
	require.NoError(t, verifySignature(data, sigB64, []ssh.PublicKey{signer.PublicKey()}))
}

func TestVerifySignature_RSA(t *testing.T) {
	signer := generateRSASigner(t)
	data := []byte("test data to sign")

	sigB64 := signData(t, signer, data)
	require.NoError(t, verifySignature(data, sigB64, []ssh.PublicKey{signer.PublicKey()}))
}

func TestVerifySignature_ECDSA(t *testing.T) {
	signer := generateECDSASigner(t)
	data := []byte("test data to sign")

	sigB64 := signData(t, signer, data)
	require.NoError(t, verifySignature(data, sigB64, []ssh.PublicKey{signer.PublicKey()}))
}

func TestVerifySignature_WrongKey(t *testing.T) {
	signerEd25519 := generateEd25519Signer(t)
	signerRSA := generateRSASigner(t)
	data := []byte("test data to sign")

	sigB64 := signData(t, signerEd25519, data)
	err := verifySignature(data, sigB64, []ssh.PublicKey{signerRSA.PublicKey()})
	require.ErrorIs(t, err, ErrSignatureVerificationFailed)
}

func TestVerifySignature_MultipleKeysMatchesSecond(t *testing.T) {
	signerEd25519 := generateEd25519Signer(t)
	signerRSA := generateRSASigner(t)
	data := []byte("test data to sign")

	sigB64 := signData(t, signerRSA, data)
	require.NoError(t, verifySignature(data, sigB64, []ssh.PublicKey{
		signerEd25519.PublicKey(),
		signerRSA.PublicKey(),
	}))
}

func TestVerifySignature_NoKeys(t *testing.T) {
	signer := generateEd25519Signer(t)
	data := []byte("test data to sign")

	sigB64 := signData(t, signer, data)
	err := verifySignature(data, sigB64, []ssh.PublicKey{})
	require.ErrorIs(t, err, ErrSignatureVerificationFailed)
}

func TestVerifySignature_InvalidBase64(t *testing.T) {
	signer := generateEd25519Signer(t)
	data := []byte("test data to sign")

	err := verifySignature(data, "!!!invalid!!!", []ssh.PublicKey{signer.PublicKey()})
	require.Error(t, err)
}

func TestVerifySignature_InvalidSSHSignatureFormat(t *testing.T) {
	signer := generateEd25519Signer(t)
	data := []byte("test data to sign")

	sigB64 := base64.StdEncoding.EncodeToString([]byte("not-a-valid-ssh-sig"))
	err := verifySignature(data, sigB64, []ssh.PublicKey{signer.PublicKey()})
	require.Error(t, err)
}
