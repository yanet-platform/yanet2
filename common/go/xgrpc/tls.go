package xgrpc

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// ClientTLSConfig is the TLS material a gRPC client uses towards a server.
//
// A nil config dials in plaintext. A present config, even an empty one,
// dials with TLS: the server certificate is verified against the system
// roots unless a CA bundle replaces them, and a client certificate is
// presented only when both of its files are given. Every file is PEM.
type ClientTLSConfig struct {
	// CAFile is the PEM bundle that replaces the system roots for server
	// verification.
	CAFile string `yaml:"ca_file"`
	// CertFile is the PEM client certificate chain for mutual TLS.
	CertFile string `yaml:"cert_file"`
	// KeyFile is the PEM private key matching the client certificate.
	KeyFile string `yaml:"key_file"`
	// ServerName is the host the server certificate is verified against
	// and sent as SNI.
	//
	// Optional. Defaults to the host of the dialed endpoint.
	ServerName string `yaml:"server_name"`
}

// Validate rejects a client certificate given without its key and a key
// given without its certificate.
func (m *ClientTLSConfig) Validate() error {
	if (m.CertFile == "") != (m.KeyFile == "") {
		return errors.New("cert_file and key_file must be set together")
	}

	return nil
}

// ClientCredentials builds the transport credentials cfg describes:
// plaintext for nil, TLS otherwise.
func ClientCredentials(cfg *ClientTLSConfig) (credentials.TransportCredentials, error) {
	if cfg == nil {
		return insecure.NewCredentials(), nil
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: cfg.ServerName,
	}

	if cfg.CAFile != "" {
		bundle, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA bundle: %w", err)
		}

		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(bundle) {
			return nil, fmt.Errorf("no PEM certificates found in CA bundle %q", cfg.CAFile)
		}
		tlsConfig.RootCAs = pool
	}

	if cfg.CertFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client TLS keypair: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	return credentials.NewTLS(tlsConfig), nil
}
