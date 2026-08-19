package sshcert_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stripe/krl"
	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/sshcert"
)

// decodeConfigNode parses text as the "config: ..." wrapper production uses,
// and returns the inner node exactly as the authenticator entry's nested
// configuration node in the control-plane auth configuration would.
func decodeConfigNode(t *testing.T, text string) *yaml.Node {
	t.Helper()

	var wrapper struct {
		Config yaml.Node `yaml:"config"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(text), &wrapper))

	return &wrapper.Config
}

// generateECDSASigner creates a new ECDSA P-256 SSH signer, the only key
// type sshcert supports.
func generateECDSASigner(t *testing.T) ssh.Signer {
	t.Helper()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	signer, err := ssh.NewSignerFromKey(privateKey)
	require.NoError(t, err)

	return signer
}

// generateUserCert creates a user certificate for username, signed by
// caSigner, and returns the certificate along with the signer for its
// subject key.
func generateUserCert(t *testing.T, caSigner ssh.Signer, username string, serial uint64) (*ssh.Certificate, ssh.Signer) {
	t.Helper()

	userSigner := generateECDSASigner(t)

	cert := &ssh.Certificate{
		CertType:        ssh.UserCert,
		Key:             userSigner.PublicKey(),
		Serial:          serial,
		KeyId:           username + "-cert",
		ValidPrincipals: []string{username},
		ValidAfter:      uint64(time.Now().Add(-1 * time.Hour).Unix()),
		ValidBefore:     uint64(time.Now().Add(24 * time.Hour).Unix()),
		Permissions: ssh.Permissions{
			Extensions: map[string]string{"permit-pty": ""},
		},
	}

	require.NoError(t, cert.SignCert(rand.Reader, caSigner))

	return cert, userSigner
}

// writeCAFile writes an authorized_keys-format CA file containing the
// public keys of signers.
func writeCAFile(t *testing.T, signers ...ssh.Signer) string {
	t.Helper()

	var content bytes.Buffer
	for _, signer := range signers {
		content.Write(ssh.MarshalAuthorizedKey(signer.PublicKey()))
	}

	path := filepath.Join(t.TempDir(), "ca.pub")
	require.NoError(t, os.WriteFile(path, content.Bytes(), 0o600))

	return path
}

// buildKRL creates a KRL binary revoking revokedSerials for the given CA.
func buildKRL(t *testing.T, caSigner ssh.Signer, revokedSerials []uint64) []byte {
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

// writeKRLFile writes data revoking revokedSerials for caSigner to a temp
// file and returns its path.
func writeKRLFile(t *testing.T, caSigner ssh.Signer, revokedSerials []uint64) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "revoked.krl")
	require.NoError(t, os.WriteFile(path, buildKRL(t, caSigner, revokedSerials), 0o600))

	return path
}

// signCertToken builds a "sshcert <base64-json>" token binding cert, signed
// by userSigner (the certificate's subject key).
func signCertToken(
	t *testing.T,
	userSigner ssh.Signer,
	cert *ssh.Certificate,
	method string,
	timestamp int64,
	nonce string,
) string {
	t.Helper()

	token := &sshcert.Token{
		Version:     1,
		Certificate: base64.StdEncoding.EncodeToString(cert.Marshal()),
		Timestamp:   timestamp,
		Nonce:       nonce,
		Method:      method,
	}

	data := fmt.Appendf(nil,
		"version=%d\ncertificate=%s\ntimestamp=%d\nnonce=%s\nmethod=%s",
		token.Version, token.Certificate, token.Timestamp, token.Nonce, token.Method,
	)

	signature, err := userSigner.Sign(rand.Reader, data)
	require.NoError(t, err)
	token.Signature = base64.StdEncoding.EncodeToString(ssh.Marshal(signature))

	jsonBytes, err := json.Marshal(token)
	require.NoError(t, err)

	return "sshcert " + base64.StdEncoding.EncodeToString(jsonBytes)
}

// newAuthenticator builds an sshcert.Authenticator from rawConfig and
// registers its Close for cleanup so the background refresh goroutine does
// not leak past the test.
func newAuthenticator(t *testing.T, rawConfig *yaml.Node) core.Authenticator {
	t.Helper()

	authenticator, err := sshcert.NewFromConfig(rawConfig)
	require.NoError(t, err)
	t.Cleanup(func() { authenticator.(*sshcert.Authenticator).Close() })

	return authenticator
}

// TestNewFromConfigCASources verifies that ca_sources is really wired into
// the composed CA store: a cert signed by either configured CA
// authenticates, while a cert signed by a third, unconfigured CA is
// rejected.
func TestNewFromConfigCASources(t *testing.T) {
	caOne := generateECDSASigner(t)
	caTwo := generateECDSASigner(t)
	caThree := generateECDSASigner(t)

	rawConfig := decodeConfigNode(t, "config:\n  ca_sources:\n    - "+
		writeCAFile(t, caOne)+"\n    - "+writeCAFile(t, caTwo)+"\n")

	authenticator := newAuthenticator(t, rawConfig)

	method := "/test.Service/Method"
	requestInfo := &core.RequestInfo{FullMethod: method}

	tests := []struct {
		name string
		ca   ssh.Signer
	}{
		{name: "caOne", ca: caOne},
		{name: "caTwo", ca: caTwo},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			cert, userSigner := generateUserCert(t, testCase.ca, "alice", 1)
			token := signCertToken(t, userSigner, cert, method, time.Now().UnixNano(), "nonce-1")

			authInfo, err := authenticator.Authenticate(t.Context(), token, requestInfo)
			require.NoError(t, err)
			require.Equal(t, &core.AuthInfo{
				Subject:    core.NewLocalSubject("alice"),
				AuthMethod: "sshcert",
			}, authInfo)
		})
	}

	t.Run("unconfigured CA rejected", func(t *testing.T) {
		cert, userSigner := generateUserCert(t, caThree, "alice", 2)
		token := signCertToken(t, userSigner, cert, method, time.Now().UnixNano(), "nonce-1")

		_, err := authenticator.Authenticate(t.Context(), token, requestInfo)
		require.Error(t, err)
		require.Contains(t, err.Error(), "CA verification failed")
	})
}

// TestNewFromConfigKRLSource verifies that krl_source is really wired into
// the revocation checker: the same certificate is rejected once its serial
// is present in a configured KRL, and accepted when krl_source is omitted.
func TestNewFromConfigKRLSource(t *testing.T) {
	ca := generateECDSASigner(t)
	caPath := writeCAFile(t, ca)
	cert, userSigner := generateUserCert(t, ca, "alice", 42)

	method := "/test.Service/Method"
	requestInfo := &core.RequestInfo{FullMethod: method}

	t.Run("krl_source revokes the certificate", func(t *testing.T) {
		krlPath := writeKRLFile(t, ca, []uint64{42})
		rawConfig := decodeConfigNode(t, "config:\n  ca_sources:\n    - "+caPath+
			"\n  krl_source: "+krlPath+"\n")

		authenticator := newAuthenticator(t, rawConfig)
		token := signCertToken(t, userSigner, cert, method, time.Now().UnixNano(), "nonce-1")

		_, err := authenticator.Authenticate(t.Context(), token, requestInfo)
		require.Error(t, err)
		require.Contains(t, err.Error(), "certificate revocation check failed")
	})

	t.Run("omitted krl_source authenticates", func(t *testing.T) {
		rawConfig := decodeConfigNode(t, "config:\n  ca_sources:\n    - "+caPath+"\n")

		authenticator := newAuthenticator(t, rawConfig)
		token := signCertToken(t, userSigner, cert, method, time.Now().UnixNano(), "nonce-1")

		authInfo, err := authenticator.Authenticate(t.Context(), token, requestInfo)
		require.NoError(t, err)
		require.Equal(t, &core.AuthInfo{
			Subject:    core.NewLocalSubject("alice"),
			AuthMethod: "sshcert",
		}, authInfo)
	})
}

// TestNewFromConfigErrors verifies that malformed or incomplete
// configurations are rejected before an authenticator is built.
func TestNewFromConfigErrors(t *testing.T) {
	ca := generateECDSASigner(t)
	caPath := writeCAFile(t, ca)

	emptyCAPath := filepath.Join(t.TempDir(), "empty.pub")
	require.NoError(t, os.WriteFile(emptyCAPath, []byte("# no keys here\n"), 0o600))

	tests := []struct {
		name string
		text string
	}{
		{
			name: "empty ca_sources",
			text: "config: {}\n",
		},
		{
			name: "nonexistent ca_sources entry",
			text: "config:\n  ca_sources:\n    - /nonexistent/ca.pub\n",
		},
		{
			name: "CA file with no valid keys",
			text: "config:\n  ca_sources:\n    - " + emptyCAPath + "\n",
		},
		{
			name: "nonexistent krl_source",
			text: "config:\n  ca_sources:\n    - " + caPath +
				"\n  krl_source: /nonexistent/revoked.krl\n",
		},
		{
			name: "scalar config node",
			text: "config: \"oops\"\n",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			rawConfig := decodeConfigNode(t, testCase.text)

			_, err := sshcert.NewFromConfig(rawConfig)
			require.Error(t, err)
		})
	}
}
