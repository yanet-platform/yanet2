package gateway

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"

	"google.golang.org/grpc/credentials"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

// TLSConfig holds the certificate a gateway TLS listener serves.
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
