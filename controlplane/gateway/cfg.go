package gateway

import (
	"cmp"
	"fmt"
	"net/http"
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

// defaultEndpoint is the gRPC endpoint served when the config names neither
// the deprecated endpoint nor a gRPC section.
const defaultEndpoint = "[::1]:8080"

// deprecatedKeysIssue is the issue that removes the deprecated server keys.
const deprecatedKeysIssue = "https://github.com/yanet-platform/yanet2/issues/2983"

// minConnectionAge is the shortest bound on gRPC TLS connection age; a
// shorter one would keep clients reconnecting almost without pause.
const minConnectionAge = time.Second

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
//
// Each protocol listens where its section says. Without a section it
// listens where the deprecated keys say, which serve TLS only while the
// deprecated TLS material is set.
type ServerConfig struct {
	// GRPC configures the gRPC listeners and replaces the deprecated
	// endpoint and TLS keys for gRPC.
	GRPC *ListenerConfig `yaml:"grpc,omitempty"`
	// HTTP configures the listeners of the HTTP server that provides
	// access to gRPC services for web clients.
	//
	// It replaces the deprecated HTTP endpoint and TLS keys for HTTP. No
	// HTTP server runs without it or the deprecated HTTP endpoint.
	HTTP *ListenerConfig `yaml:"http,omitempty"`
	// Endpoint is the gRPC endpoint, "[::1]:8080" when neither it nor the
	// gRPC section is given.
	//
	// Deprecated: use the gRPC section; issue #2983 removes it.
	Endpoint string `yaml:"endpoint"`
	// HTTPEndpoint is the HTTP endpoint.
	//
	// Deprecated: use the HTTP section; issue #2983 removes it.
	HTTPEndpoint string `yaml:"http_endpoint"`
	// TLS turns the deprecated gRPC and HTTP endpoints into TLS listeners.
	//
	// Deprecated: use the TLS listener of each section; issue #2983 removes
	// it.
	TLS *TLSConfig `yaml:"tls,omitempty"`
}

// ListenerConfig configures where one protocol listens: an optional
// plaintext endpoint and an optional TLS listener, side by side.
type ListenerConfig struct {
	// Endpoint is the plaintext listener's address; empty means none.
	Endpoint string `yaml:"endpoint"`
	// TLS configures the TLS listener; nil means none.
	TLS *TLSListenerConfig `yaml:"tls,omitempty"`
	// TrustedOrigins are the web origins, each "scheme://host[:port]",
	// whose browser pages may call the HTTP server cross-origin, such as a
	// web UI served apart from the gateway.
	//
	// A cross-origin browser request from any other origin that is not a
	// GET, HEAD or OPTIONS is rejected with 403, and only a trusted origin
	// gets CORS headers. Clients that are not browsers send no origin and
	// are unaffected. Only server.http takes it.
	TrustedOrigins []string `yaml:"trusted_origins,omitempty"`
}

// Validate rejects a trusted origin that is not a bare
// "scheme://host[:port]".
func (m *ListenerConfig) Validate() error {
	_, err := newCrossOriginProtection(m.TrustedOrigins)
	return err
}

// newCrossOriginProtection returns the protection that rejects cross-origin
// browser requests from every origin but the trusted ones.
func newCrossOriginProtection(origins []string) (*http.CrossOriginProtection, error) {
	protection := http.NewCrossOriginProtection()
	for _, origin := range origins {
		if err := protection.AddTrustedOrigin(origin); err != nil {
			return nil, fmt.Errorf("trusted_origins: %w", err)
		}
	}

	return protection, nil
}

// TLSListenerConfig configures a listener that serves TLS.
type TLSListenerConfig struct {
	// Endpoint is the listener's address.
	Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
	// TLSConfig holds the certificates the listener serves.
	TLSConfig `yaml:",inline"`
	// MaxConnectionAge bounds how long a gRPC TLS connection lives before
	// the gateway asks its client to reconnect, so a renewed client
	// certificate reaches every connection.
	//
	// Optional; zero leaves connections open for as long as their clients
	// keep them, and any other value is at least 1s. Each connection's age
	// varies by up to 10% either way, calls in flight complete without a
	// deadline, and the client reconnects on its next call. Only
	// server.grpc.tls takes it.
	MaxConnectionAge time.Duration `yaml:"max_connection_age"`
}

// listenerPlan is where the gateway listens, with the deprecated keys
// mapped onto the sections they alias.
type listenerPlan struct {
	// GRPC is where gRPC is served.
	GRPC ListenerConfig
	// HTTP is where HTTP is served; zero when no HTTP server runs.
	HTTP ListenerConfig
	// Mapped names the deprecated keys the plan serves.
	Mapped []string
	// Ignored names the deprecated keys given beside the sections that
	// replace them.
	Ignored []string
}

// newListenerPlan maps the server config onto one listener plan, so the
// sections and the deprecated keys are served by one path.
//
// A present section replaces the deprecated keys of its protocol. Without
// one, a deprecated endpoint serves TLS while the deprecated TLS material is
// set, and gRPC falls back to the default endpoint.
func newListenerPlan(server *ServerConfig) listenerPlan {
	plan := listenerPlan{}

	note := func(key string, given, replaced bool) {
		switch {
		case !given:
		case replaced:
			plan.Ignored = append(plan.Ignored, key)
		default:
			plan.Mapped = append(plan.Mapped, key)
		}
	}
	note("server.endpoint", server.Endpoint != "", server.GRPC != nil)
	note("server.http_endpoint", server.HTTPEndpoint != "", server.HTTP != nil)
	// The deprecated TLS material serves every protocol still listening on
	// its deprecated endpoint, so it is ignored only when none is.
	tlsReplaced := server.GRPC != nil && (server.HTTP != nil || server.HTTPEndpoint == "")
	note("server.tls", server.TLS != nil, tlsReplaced)

	deprecated := func(endpoint string) ListenerConfig {
		if server.TLS == nil {
			return ListenerConfig{Endpoint: endpoint}
		}

		return ListenerConfig{TLS: &TLSListenerConfig{
			Endpoint:  xcfg.MustNonEmptyString(endpoint),
			TLSConfig: *server.TLS,
		}}
	}

	if server.GRPC != nil {
		plan.GRPC = *server.GRPC
	} else {
		plan.GRPC = deprecated(cmp.Or(server.Endpoint, defaultEndpoint))
	}
	switch {
	case server.HTTP != nil:
		plan.HTTP = *server.HTTP
	case server.HTTPEndpoint != "":
		plan.HTTP = deprecated(server.HTTPEndpoint)
	}

	return plan
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

// Default resets the config to built-in defaults.
func (m *Config) Default() {
	*m = DefaultConfig()
}

// DefaultConfig returns a Config with sensible defaults.
//
// It names no listener: the gRPC default endpoint applies only while the
// config names neither the deprecated endpoint nor a gRPC section.
func DefaultConfig() Config {
	return Config{
		Auth: auth.DefaultConfig(),
		Registry: RegistryConfig{
			TTL:           xcfg.MustNonZero(defaultRegistryTTL),
			SweepInterval: xcfg.MustNonZero(defaultRegistrySweepInterval),
		},
	}
}
