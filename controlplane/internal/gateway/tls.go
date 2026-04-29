package gateway

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

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
	// ServerName is the SNI / hostname used by in-process loopback
	// clients.
	//
	// Optional. Defaults to the host parsed from the dial endpoint.
	ServerName string `yaml:"server_name"`
}

// ServerCredentials loads the cert/key pair and returns gRPC server
// transport credentials.
func (m *TLSConfig) ServerCredentials() (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(m.CertFile.Unwrap(), m.KeyFile.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("failed to load gateway TLS keypair: %w", err)
	}

	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}), nil
}

// LoopbackClientCredentials returns gRPC client transport credentials
// that trust the gateway's own server certificate as the only CA.
//
// Used by self-dials inside the controlplane-director process.
//
// fallbackHost is used as ServerName when m.ServerName is empty.
func (m *TLSConfig) LoopbackClientCredentials(fallbackHost string) (credentials.TransportCredentials, error) {
	certFile := m.CertFile.Unwrap()
	pem, err := os.ReadFile(certFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read gateway TLS cert: %w", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("failed to parse PEM certificates from %q", certFile)
	}

	name := m.ServerName
	if name == "" {
		name = fallbackHost
	}

	return credentials.NewTLS(&tls.Config{
		RootCAs:    pool,
		ServerName: name,
		MinVersion: tls.VersionTLS12,
	}), nil
}

func transportCredentials(tlsCfg *TLSConfig, endpoint string) (credentials.TransportCredentials, error) {
	if tlsCfg == nil {
		return insecure.NewCredentials(), nil
	}
	return tlsCfg.LoopbackClientCredentials(hostFromEndpoint(endpoint))
}

func hostFromEndpoint(endpoint string) string {
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		return endpoint
	}

	return host
}
