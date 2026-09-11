package operator

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap/zapcore"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/common/go/logging"
	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/desired"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/native"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
)

const (
	DefaultNetplanPath             = "/etc/netplan/00-interfaces.yaml"
	DefaultNeighbourTable          = "netlink-dataplane-default"
	DefaultNeighbourPriority       = 100
	DefaultNeighbourPublishTimeout = 5 * time.Second
	DefaultNeighbourUpdateInterval = 5 * time.Minute
)

// Config describes immutable interface configuration and one neighbour source.
type Config struct {
	Logging                 logging.Config                  `yaml:"logging"`
	Server                  commonoperator.GRPCServerConfig `yaml:"server"`
	Gateways                []commonoperator.GatewayConfig  `yaml:"gateways"`
	Register                commonoperator.RegisterConfig   `yaml:"register"`
	Reconcile               commonoperator.ReconcileConfig  `yaml:"reconcile"`
	Source                  string                          `yaml:"source"`
	NetplanPath             *string                         `yaml:"netplan_path"`
	Native                  *native.Config                  `yaml:"native"`
	LinkMap                 map[string]string               `yaml:"link_map"`
	NeighbourTable          string                          `yaml:"neighbour_table"`
	NeighbourPriority       uint32                          `yaml:"neighbour_priority"`
	NeighbourPublishTimeout time.Duration                   `yaml:"neighbour_publish_timeout"`
}

// Default resets the receiver before YAML decoding.
func (m *Config) Default() { *m = *DefaultConfig() }

// UnmarshalYAML preserves defaults while rejecting an explicitly null native
// block, which cannot be treated as an omitted alternative source.
func (m *Config) UnmarshalYAML(node *yaml.Node) error {
	var fields map[string]yaml.Node
	if err := node.Decode(&fields); err != nil {
		return err
	}
	if native, present := fields["native"]; present && native.ShortTag() == "!!null" {
		return errors.New("native configuration mapping is required")
	}
	type config Config
	decoded := config(*m)
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	*m = Config(decoded)
	return nil
}

// LoggingConfig exposes configuration to the common command lifecycle.
func (m *Config) LoggingConfig() *logging.Config { return &m.Logging }

// PublicationConfig returns the single-table transport contract.
func (m *Config) PublicationConfig() neighbour.PublicationConfig {
	return neighbour.PublicationConfig{TableName: m.NeighbourTable, DefaultPriority: m.NeighbourPriority, Timeout: m.NeighbourPublishTimeout}
}

// Validate rejects invalid publication and scheduling configuration at startup.
func (m *Config) Validate() error {
	if err := m.ValidateSource(); err != nil {
		return err
	}
	if len(m.Gateways) == 0 {
		return errors.New("at least one gateway must be configured")
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
		if gateway.TLS != nil {
			if err := gateway.TLS.Validate(); err != nil {
				return fmt.Errorf("gateway %q: %w", gateway.Name, err)
			}
		}
	}
	for name := range m.LinkMap {
		if err := desired.ValidateInterfaceName(name); err != nil {
			return fmt.Errorf("link_map %q: %w", name, err)
		}
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
			Interval:       xcfg.MustNonZero(DefaultNeighbourUpdateInterval),
			InitialBackoff: xcfg.MustNonZero(commonoperator.DefaultReconcileInitialBackoff),
			MaxBackoff:     xcfg.MustNonZero(commonoperator.DefaultReconcileMaxBackoff),
		},
		LinkMap:                 map[string]string{},
		NeighbourTable:          DefaultNeighbourTable,
		NeighbourPriority:       DefaultNeighbourPriority,
		NeighbourPublishTimeout: DefaultNeighbourPublishTimeout,
	}
}

// ValidateSource checks the required selector and exclusive source inputs
// without opening files or runtime resources.
func (m *Config) ValidateSource() error {
	switch m.Source {
	case "netplan":
		if m.Native != nil {
			return errors.New("native is not supported with source netplan")
		}
		if m.NetplanPath != nil && *m.NetplanPath == "" {
			return errors.New("netplan_path must be nonempty")
		}
	case "native":
		if m.Native == nil {
			return errors.New("native configuration mapping is required")
		}
		if m.NetplanPath != nil {
			return errors.New("netplan_path is not supported with source native")
		}
	default:
		return fmt.Errorf("source must be netplan or native, got %q", m.Source)
	}
	return nil
}
