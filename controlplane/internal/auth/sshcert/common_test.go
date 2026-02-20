package sshcert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stripe/krl"
	"golang.org/x/crypto/ssh"
)

// generateECDSASigner creates a new ECDSA P-256 SSH signer.
func generateECDSASigner(t *testing.T) ssh.Signer {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)

	return signer
}

// generateCA creates a new ECDSA P-256 CA signer.
func generateCA(t *testing.T) ssh.Signer {
	t.Helper()

	return generateECDSASigner(t)
}

// generateUserCert creates a signed SSH user certificate.
func generateUserCert(
	t *testing.T,
	caSigner ssh.Signer,
	username string,
	serial uint64,
	validAfter time.Time,
	validBefore time.Time,
) (*ssh.Certificate, ssh.Signer) {
	t.Helper()

	userSigner := generateECDSASigner(t)

	cert := &ssh.Certificate{
		CertType:        ssh.UserCert,
		Key:             userSigner.PublicKey(),
		Serial:          serial,
		KeyId:           username + "-cert",
		ValidPrincipals: []string{username},
		ValidAfter:      uint64(validAfter.Unix()),
		ValidBefore:     uint64(validBefore.Unix()),
		Permissions: ssh.Permissions{
			Extensions: map[string]string{
				"permit-pty": "",
			},
		},
	}

	err := cert.SignCert(rand.Reader, caSigner)
	require.NoError(t, err)

	return cert, userSigner
}

// generateHostCert creates a signed SSH host certificate.
func generateHostCert(
	t *testing.T,
	caSigner ssh.Signer,
	hostname string,
	serial uint64,
) (*ssh.Certificate, ssh.Signer) {
	t.Helper()

	userSigner := generateECDSASigner(t)

	cert := &ssh.Certificate{
		CertType:        ssh.HostCert,
		Key:             userSigner.PublicKey(),
		Serial:          serial,
		KeyId:           hostname + "-cert",
		ValidPrincipals: []string{hostname},
		ValidAfter:      uint64(time.Now().Add(-1 * time.Hour).Unix()),
		ValidBefore:     uint64(time.Now().Add(24 * time.Hour).Unix()),
	}

	err := cert.SignCert(rand.Reader, caSigner)
	require.NoError(t, err)

	return cert, userSigner
}

// encodeCert encodes an SSH certificate to base64 wire format.
func encodeCert(t *testing.T, cert *ssh.Certificate) string {
	t.Helper()

	return base64.StdEncoding.EncodeToString(cert.Marshal())
}

// signCertToken creates a valid signed sshcert token for testing.
func signCertToken(
	t *testing.T,
	userSigner ssh.Signer,
	cert *ssh.Certificate,
	method string,
	timestamp int64,
	nonce string,
) string {
	t.Helper()

	certB64 := encodeCert(t, cert)

	token := &Token{
		Version:     tokenVersion,
		Certificate: certB64,
		Timestamp:   timestamp,
		Nonce:       nonce,
		Method:      method,
	}

	data := token.canonicalSignedData()
	sig, err := userSigner.Sign(rand.Reader, data)
	require.NoError(t, err)

	token.Signature = base64.StdEncoding.EncodeToString(
		ssh.Marshal(sig),
	)

	jsonBytes, err := json.Marshal(token)
	require.NoError(t, err)

	return tokenPrefix + base64.StdEncoding.EncodeToString(jsonBytes)
}

// signData signs the given data with the signer and returns the
// base64-encoded SSH signature.
func signData(
	t *testing.T,
	signer ssh.Signer,
	data []byte,
) string {
	t.Helper()

	sig, err := signer.Sign(rand.Reader, data)
	require.NoError(t, err)

	return base64.StdEncoding.EncodeToString(ssh.Marshal(sig))
}

// buildKRL creates a KRL binary with the given revoked serials
// for the given CA.
func buildKRL(
	t *testing.T,
	caSigner ssh.Signer,
	revokedSerials []uint64,
) []byte {
	t.Helper()

	serialList := krl.KRLCertificateSerialList(revokedSerials)

	k := &krl.KRL{
		Sections: []krl.KRLSection{
			&krl.KRLCertificateSection{
				CA: caSigner.PublicKey(),
				Sections: []krl.KRLCertificateSubsection{
					&serialList,
				},
			},
		},
	}

	data, err := k.Marshal(rand.Reader)
	require.NoError(t, err)

	return data
}
