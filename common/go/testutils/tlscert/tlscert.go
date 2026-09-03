// Package tlscert issues throwaway certificates for tests that need a TLS
// server or client behind a private CA.
package tlscert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// CA is a self-signed certificate authority whose bundle lives in the test's
// temporary directory. One CA may issue from parallel subtests.
type CA struct {
	certificate *x509.Certificate
	key         *ecdsa.PrivateKey
	pool        *x509.CertPool
	bundleFile  string
	dir         string
	issued      atomic.Int64
}

// Keypair is a certificate issued by a CA together with the PEM files it was
// written to.
type Keypair struct {
	// Certificate is the issued certificate with its private key.
	Certificate tls.Certificate
	// CertFile is the PEM file holding the certificate.
	CertFile string
	// KeyFile is the PEM file holding the private key.
	KeyFile string
}

// NewCA generates a CA valid for an hour and writes its PEM bundle to disk.
func NewCA(t *testing.T) *CA {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber:          newSerial(t),
		Subject:               pkix.Name{CommonName: "test CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	certificate, err := x509.ParseCertificate(der)
	require.NoError(t, err)

	pool := x509.NewCertPool()
	pool.AddCert(certificate)

	dir := t.TempDir()
	bundleFile := filepath.Join(dir, "ca.pem")
	writePEM(t, bundleFile, "CERTIFICATE", der)

	return &CA{
		certificate: certificate,
		key:         key,
		pool:        pool,
		bundleFile:  bundleFile,
		dir:         dir,
	}
}

// BundleFile returns the path of the PEM bundle holding the CA certificate.
func (m *CA) BundleFile() string {
	return m.bundleFile
}

// Pool returns a pool trusting only this CA.
func (m *CA) Pool() *x509.CertPool {
	return m.pool
}

// IssueServer issues a server certificate for the given hosts, each a DNS
// name or an IP address literal.
func (m *CA) IssueServer(t *testing.T, hosts ...string) Keypair {
	t.Helper()

	template := &x509.Certificate{
		SerialNumber: newSerial(t),
		Subject:      pkix.Name{CommonName: "test server"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
			continue
		}
		template.DNSNames = append(template.DNSNames, host)
	}

	return m.issue(t, "server", template)
}

// IssueClient issues a client certificate with the given common name.
func (m *CA) IssueClient(t *testing.T, commonName string) Keypair {
	t.Helper()

	template := &x509.Certificate{
		SerialNumber: newSerial(t),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	return m.issue(t, "client", template)
}

// issue signs template with the CA and writes the pair under a unique name.
func (m *CA) issue(t *testing.T, kind string, template *x509.Certificate) Keypair {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	der, err := x509.CreateCertificate(rand.Reader, template, m.certificate, &key.PublicKey, m.key)
	require.NoError(t, err)

	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)

	issued := m.issued.Add(1)
	certFile := filepath.Join(m.dir, fmt.Sprintf("%s-%d.pem", kind, issued))
	keyFile := filepath.Join(m.dir, fmt.Sprintf("%s-%d.key", kind, issued))
	writePEM(t, certFile, "CERTIFICATE", der)
	writePEM(t, keyFile, "PRIVATE KEY", keyDER)

	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	require.NoError(t, err)

	return Keypair{
		Certificate: certificate,
		CertFile:    certFile,
		KeyFile:     keyFile,
	}
}

// newSerial draws a 62-bit random serial, making a collision between
// certificates issued by one CA improbable rather than impossible.
func newSerial(t *testing.T) *big.Int {
	t.Helper()

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	require.NoError(t, err)

	return serial
}

// writePEM writes one DER block to path in PEM armor.
func writePEM(t *testing.T, path string, blockType string, der []byte) {
	t.Helper()

	block := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	require.NoError(t, os.WriteFile(path, block, 0o600))
}
