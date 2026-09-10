package x509

import (
	"time"
)

// Config configures client certificate authentication.
type Config struct {
	// CASources lists the PEM bundles of the authorities client certificates
	// are verified against, each a file path or an HTTP(S) URL.
	//
	// Bundles are merged. A source ending in ".zst" is decompressed.
	CASources []string `yaml:"ca_sources"`
	// CRLSources lists the certificate revocation lists to consult, each a
	// file path or an HTTP(S) URL holding one DER or PEM encoded list.
	//
	// A list is accepted only when one of the authorities signed it.
	CRLSources []string `yaml:"crl_sources"`
	// RefreshInterval is the period at which the authorities and revocation
	// lists are reloaded from their sources.
	//
	// Default: 5m.
	RefreshInterval time.Duration `yaml:"refresh_interval"`
}
