package gateway

import (
	"crypto/tls"
	"crypto/x509"
	"errors"

	"google.golang.org/grpc/credentials"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	commonxgrpc "github.com/yanet-platform/yanet2/common/go/xgrpc"
)

const (
	// metricTLSCertificateNotAfter is the unix time the certificate a TLS
	// listener serves for one key pair entry expires at.
	metricTLSCertificateNotAfter = "gateway_tls_certificate_not_after_seconds"
	// labelListener names the TLS listener a metric is about: grpcs or
	// https.
	labelListener = "listener"
	// labelSource names the key pair entry a metric is about.
	labelSource = "source"
)

// TLSConfig holds the certificates a gateway TLS listener serves.
type TLSConfig struct {
	// KeyPairs lists the certificates the listener serves, each with its
	// private key.
	//
	// Required, at least one. A handshake gets the first pair that is valid
	// and that the client supports, else the first pair. Renewed files are
	// picked up without a restart.
	KeyPairs []commonxgrpc.KeyPairConfig `yaml:"key_pairs"`
}

// Validate requires at least one key pair.
func (m *TLSConfig) Validate() error {
	if len(m.KeyPairs) == 0 {
		return errors.New("key_pairs needs at least one entry")
	}

	return nil
}

// newCertificateStore loads the key pairs of a TLS listener into a store
// that picks up renewed files without a restart.
//
// Loading fails on an unreadable or mismatched pair, so a misconfigured
// listener fails startup.
func newCertificateStore(
	config *TLSConfig,
	options ...commonxgrpc.CertificateStoreOption,
) (*commonxgrpc.CertificateStore, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	return commonxgrpc.NewCertificateStoreFromConfig(config.KeyPairs, options...)
}

// serverCredentials returns gRPC server transport credentials serving the
// certificates of the store.
//
// Session tickets are off, since a resumed session carries over the client
// certificate of the handshake it resumes. Given a source of client
// authorities, the listener asks every client for a certificate and
// verifies the one it gets against the pool the source returns for that
// handshake, so a reloaded pool applies to new connections without a
// restart. A client without a certificate is still accepted and
// authenticates by other means.
func serverCredentials(
	certificates *commonxgrpc.CertificateStore,
	clientCAs func() *x509.CertPool,
) credentials.TransportCredentials {
	config := &tls.Config{
		GetCertificate:         certificates.GetCertificate,
		MinVersion:             tls.VersionTLS12,
		SessionTicketsDisabled: true,
	}
	if clientCAs != nil {
		config.ClientAuth = tls.VerifyClientCertIfGiven
		config.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) {
			perClient := config.Clone()
			perClient.ClientCAs = clientCAs()

			return perClient, nil
		}
	}

	return credentials.NewTLS(config)
}

// listenerCertificates is the certificate store one TLS listener serves
// from.
type listenerCertificates struct {
	// Listener names the listener: grpcs or https.
	Listener string
	// Store is the store the listener serves from.
	Store *commonxgrpc.CertificateStore
}

// servedCertificates reports when the certificates the gateway TLS
// listeners serve expire, one series per key pair entry.
type servedCertificates []listenerCertificates

// Collect reports the expiry of every served certificate, refreshing the
// entries that are due first.
func (m servedCertificates) Collect() []*commonpb.Metric {
	var out []*commonpb.Metric
	for _, listener := range m {
		for _, leaf := range listener.Store.Leaves() {
			out = append(out, commonpb.NewMetricGauge(
				metricTLSCertificateNotAfter,
				float64(leaf.Leaf.NotAfter.Unix()),
				commonpb.NewLabel(labelListener, listener.Listener),
				commonpb.NewLabel(labelSource, leaf.Source),
			))
		}
	}

	return out
}
