package operator

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap/zapcore"

	"github.com/yanet-platform/yanet2/common/go/logging"
	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
)

const (
	DefaultNetplanPath             = "/etc/netplan/00-interfaces.yaml"
	DefaultNeighbourTable          = "netlink-dataplane-default"
	DefaultNeighbourPriority       = 100
	DefaultNeighbourPublishTimeout = 5 * time.Second
)

// Config describes immutable interface configuration and one neighbour source.
type Config struct {
	Logging                 logging.Config                  `yaml:"logging"`
	Server                  commonoperator.GRPCServerConfig `yaml:"server"`
	Gateways                []commonoperator.GatewayConfig  `yaml:"gateways"`
	Register                commonoperator.RegisterConfig   `yaml:"register"`
	Reconcile               commonoperator.ReconcileConfig  `yaml:"reconcile"`
	NetplanPath             xcfg.NonEmptyString             `yaml:"netplan_path"`
	LinkMap                 map[string]string               `yaml:"link_map"`
	NeighbourTable          string                          `yaml:"neighbour_table"`
	NeighbourPriority       uint32                          `yaml:"neighbour_priority"`
	NeighbourPublishTimeout time.Duration                   `yaml:"neighbour_publish_timeout"`
}

// Default resets the receiver before YAML decoding.
func (m *Config) Default() { *m = *DefaultConfig() }

// LoggingConfig exposes configuration to the common command lifecycle.
func (m *Config) LoggingConfig() *logging.Config { return &m.Logging }

// PublicationConfig returns the single-table transport contract.
func (m *Config) PublicationConfig() neighbour.PublicationConfig {
	return neighbour.PublicationConfig{TableName: m.NeighbourTable, DefaultPriority: m.NeighbourPriority, Timeout: m.NeighbourPublishTimeout}
}

// Validate rejects invalid transport and scheduling configuration at startup.
func (m *Config) Validate() error {
	if len(m.Gateways) == 0 {
		return errors.New("at least one gateway must be configured")
	}
	if err := validateListenEndpoint(m.Server.Endpoint.Unwrap()); err != nil {
		return fmt.Errorf("invalid server endpoint: %w", err)
	}
	if err := m.Server.Validate(); err != nil {
		return fmt.Errorf("invalid server configuration: %w", err)
	}
	for _, value := range []struct {
		Name     string
		Duration time.Duration
	}{
		{"register.interval", m.Register.Interval.Unwrap()},
		{"reconcile.interval", m.Reconcile.Interval.Unwrap()},
		{"reconcile.initial_backoff", m.Reconcile.InitialBackoff.Unwrap()},
		{"reconcile.max_backoff", m.Reconcile.MaxBackoff.Unwrap()},
	} {
		if value.Duration <= 0 {
			return fmt.Errorf("%s must be positive", value.Name)
		}
	}
	if err := m.Reconcile.Validate(); err != nil {
		return err
	}
	if m.NetplanPath.Unwrap() == "" {
		return errors.New("netplan_path must be nonempty")
	}
	if err := m.PublicationConfig().Validate(); err != nil {
		return err
	}
	names := map[string]bool{}
	for _, gateway := range m.Gateways {
		if strings.TrimSpace(gateway.Name) == "" {
			return errors.New("gateway name must be nonempty")
		}
		if names[gateway.Name] {
			return fmt.Errorf("duplicate gateway name %q", gateway.Name)
		}
		names[gateway.Name] = true
		if err := validateGRPCTarget(gateway.Endpoint.Unwrap()); err != nil {
			return fmt.Errorf("gateway %q: invalid endpoint: %w", gateway.Name, err)
		}
		if gateway.TLS != nil {
			if err := gateway.TLS.Validate(); err != nil {
				return fmt.Errorf("gateway %q: %w", gateway.Name, err)
			}
		}
	}
	for name := range m.LinkMap {
		if err := netplan.ValidateInterfaceName(name); err != nil {
			return fmt.Errorf("link_map %q: %w", name, err)
		}
	}
	return nil
}

func validateListenEndpoint(endpoint string) error {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return err
	}
	if strings.TrimSpace(host) != host {
		return errors.New("host contains surrounding whitespace")
	}
	if port == "0" {
		return nil
	}
	portNumber, err := net.LookupPort("tcp", port)
	if err != nil || portNumber <= 0 {
		return fmt.Errorf("invalid port %q", port)
	}
	return nil
}

func validateGRPCTarget(target string) error {
	if strings.TrimSpace(target) != target || target == "" {
		return errors.New("target is empty or contains surrounding whitespace")
	}
	if _, port, err := net.SplitHostPort(target); err == nil {
		portNumber, lookupErr := net.LookupPort("tcp", port)
		if lookupErr != nil || portNumber <= 0 {
			return fmt.Errorf("invalid port %q", port)
		}
		return nil
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return err
	}
	if parsed.Scheme != "" && parsed.Host == "" && parsed.Path == "" && parsed.Opaque == "" {
		return errors.New("target URI has no endpoint")
	}
	return nil
}

// DefaultConfig supplies bounded scheduling and a stable neighbour-table name.
func DefaultConfig() *Config {
	return &Config{
		Logging:  logging.Config{Level: zapcore.InfoLevel},
		Server:   commonoperator.GRPCServerConfig{Endpoint: xcfg.MustNonEmptyString("[::1]:0")},
		Register: commonoperator.RegisterConfig{Interval: xcfg.MustNonZero(commonoperator.DefaultRegisterInterval)},
		Reconcile: commonoperator.ReconcileConfig{
			Interval:       xcfg.MustNonZero(commonoperator.DefaultReconcileInterval),
			InitialBackoff: xcfg.MustNonZero(commonoperator.DefaultReconcileInitialBackoff),
			MaxBackoff:     xcfg.MustNonZero(commonoperator.DefaultReconcileMaxBackoff),
		},
		NetplanPath:             xcfg.MustNonEmptyString(DefaultNetplanPath),
		LinkMap:                 map[string]string{},
		NeighbourTable:          DefaultNeighbourTable,
		NeighbourPriority:       DefaultNeighbourPriority,
		NeighbourPublishTimeout: DefaultNeighbourPublishTimeout,
	}
}
