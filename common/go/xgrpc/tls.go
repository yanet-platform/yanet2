package xgrpc

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/yanet-platform/yanet2/common/go/bytesource"
)

// ClientTLSConfig is the TLS material a gRPC client uses towards a server.
//
// A nil config dials in plaintext. A present config, even an empty one,
// dials with TLS: the server certificate is verified against the system
// roots unless a CA bundle replaces them, and a client certificate is
// presented only when a key pair is given. Every file is PEM.
type ClientTLSConfig struct {
	// CAFile is the PEM bundle that replaces the system roots for server
	// verification.
	CAFile string `yaml:"ca_file"`
	// KeyPairs lists the client certificates for mutual TLS, each with its
	// private key.
	//
	// Optional. A handshake presents the first pair that is valid and that
	// the server accepts, else none. Renewed files are picked up without a
	// restart.
	KeyPairs []KeyPairConfig `yaml:"key_pairs"`
	// ServerName is the host the server certificate is verified against
	// and sent as SNI.
	//
	// Optional. Defaults to the host of the dialed endpoint.
	ServerName string `yaml:"server_name"`
}

// ClientCredentials builds the transport credentials a client TLS config
// describes: plaintext for none, TLS otherwise.
//
// Key pairs are presented through a certificate store, which the options
// configure, so a renewed pair is picked up without a restart at the first
// handshake after the refresh interval. No session cache is used: every
// connection makes a full handshake and presents the pair current at that
// moment.
func ClientCredentials(config *ClientTLSConfig, options ...CertificateStoreOption) (credentials.TransportCredentials, error) {
	if config == nil {
		return insecure.NewCredentials(), nil
	}

	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: config.ServerName,
	}

	if config.CAFile != "" {
		bundle, err := bytesource.NewFileLoader(config.CAFile).Load()
		if err != nil {
			return nil, fmt.Errorf("failed to read CA bundle: %w", err)
		}

		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(bundle) {
			return nil, fmt.Errorf("no PEM certificates found in CA bundle %q", config.CAFile)
		}
		tlsConfig.RootCAs = pool
	}

	if len(config.KeyPairs) > 0 {
		loaders := make([]KeyPairLoader, 0, len(config.KeyPairs))
		for idx, entry := range config.KeyPairs {
			loader, err := NewKeyPairLoader(entry)
			if err != nil {
				return nil, fmt.Errorf("key_pairs[%d]: %w", idx, err)
			}
			loaders = append(loaders, loader)
		}

		store, err := NewCertificateStore(loaders, options...)
		if err != nil {
			return nil, fmt.Errorf("failed to load client TLS key pairs: %w", err)
		}
		tlsConfig.GetClientCertificate = store.GetClientCertificate
	}

	return credentials.NewTLS(tlsConfig), nil
}
