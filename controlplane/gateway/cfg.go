package gateway

import (
	"fmt"
	"time"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth"
)

// defaultRegistryTTL is a generous multiple of the period on which external
// backends re-register with the gateway.
//
// An operator may configure a longer register.interval in its own YAML, in
// which case the gateway TTL must be raised to match, or that operator will
// flap in and out of the registry.
const defaultRegistryTTL = 5 * time.Minute

// defaultRegistrySweepInterval is the default period between eviction sweeps.
const defaultRegistrySweepInterval = 30 * time.Second

// Config is the configuration for the gateway.
type Config struct {
	// InstanceID specifies which dataplane instance this gateway serves.
	//
	// Required: it must be set explicitly, even to 0.
	InstanceID xcfg.Required[uint32] `yaml:"instance_id"`
	// Server is the configuration for the gateway server.
	Server ServerConfig `yaml:"server"`
	// Auth is the configuration for authentication and authorization.
	Auth auth.Config `yaml:"auth"`
	// Registry is the configuration for stale backend eviction.
	Registry RegistryConfig `yaml:"registry"`
}

// ServerConfig is the configuration for the gateway server.
type ServerConfig struct {
	// Endpoint is the endpoint for the gateway server to be exposed on.
	Endpoint string `yaml:"endpoint"`
	// HTTPEndpoint is the endpoint for the HTTP server that provides
	// access to gRPC services for web clients.
	HTTPEndpoint string `yaml:"http_endpoint"`
	// TLS configures TLS for both gRPC and HTTP listeners. When nil,
	// both listen in plaintext.
	TLS *TLSConfig `yaml:"tls,omitempty"`
}

// RegistryConfig is the configuration for stale backend eviction.
type RegistryConfig struct {
	// PreserveStaleBackends keeps backends that stopped refreshing their
	// registration instead of evicting them.
	//
	// An entry, once registered, stays in the registry until the gateway
	// stops, even after its backend is gone. Set it when a deployment would
	// rather serve a request into a backend that may be gone than have it
	// vanish from the registry.
	PreserveStaleBackends bool `yaml:"preserve_stale_backends"`
	// TTL bounds how long an external backend may go without a
	// registration refresh before it is evicted.
	TTL xcfg.NonZero[time.Duration] `yaml:"ttl"`
	// SweepInterval is the period between eviction sweeps.
	SweepInterval xcfg.NonZero[time.Duration] `yaml:"sweep_interval"`
}

// Validate validates the registry config.
func (m *RegistryConfig) Validate() error {
	if m.TTL.Unwrap() <= 0 {
		return fmt.Errorf("ttl (%s) must be positive", m.TTL.Unwrap())
	}
	if m.SweepInterval.Unwrap() <= 0 {
		return fmt.Errorf("sweep_interval (%s) must be positive", m.SweepInterval.Unwrap())
	}

	return nil
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Endpoint: "[::1]:8080",
		},
		Auth: auth.DefaultConfig(),
		Registry: RegistryConfig{
			TTL:           xcfg.MustNonZero(defaultRegistryTTL),
			SweepInterval: xcfg.MustNonZero(defaultRegistrySweepInterval),
		},
	}
}
