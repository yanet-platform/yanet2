package gateway

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"

	"google.golang.org/grpc/credentials"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

// TLSConfig holds the gateway server TLS material.
//
// Both files must be PEM-encoded.
type TLSConfig struct {
	// CertFile is the path to the PEM-encoded server certificate.
	CertFile xcfg.NonEmptyString `yaml:"cert_file"`
	// KeyFile is the path to the PEM-encoded server private key.
	KeyFile xcfg.NonEmptyString `yaml:"key_file"`
}

// ServerCredentials loads the cert/key pair and returns gRPC server
// transport credentials.
//
// With clientCAs set the listener asks every client for a certificate and
// verifies the one it gets against the pool clientCAs returns for that
// handshake, so a reloaded pool applies to new connections without a
// restart. A client without a certificate is still accepted and
// authenticates by other means.
func (m *TLSConfig) ServerCredentials(clientCAs func() *x509.CertPool) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(m.CertFile.Unwrap(), m.KeyFile.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("failed to load gateway TLS keypair: %w", err)
	}

	config := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	if clientCAs != nil {
		config.ClientAuth = tls.VerifyClientCertIfGiven
		config.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) {
			perClient := config.Clone()
			perClient.ClientCAs = clientCAs()

			return perClient, nil
		}
	}

	return credentials.NewTLS(config), nil
}

// LoopbackCredentials returns client credentials that accept exactly the
// certificate this config serves, for the hop the gateway makes into its
// own server without leaving the process.
//
// Server credentials apply to every listener of a gRPC server, the
// in-memory one included, so the loopback has to complete a TLS handshake.
// Pinning replaces name and chain verification: the certificate need not
// name any host for the loopback and no CA is consulted.
func (m *TLSConfig) LoopbackCredentials() (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(m.CertFile.Unwrap(), m.KeyFile.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("failed to load gateway TLS keypair: %w", err)
	}

	pinned := cert.Certificate[0]

	return credentials.NewTLS(&tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 || !bytes.Equal(rawCerts[0], pinned) {
				return errors.New("loopback peer did not present the gateway's own certificate")
			}
			return nil
		},
	}), nil
}
