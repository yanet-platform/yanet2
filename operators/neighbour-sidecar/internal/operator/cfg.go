package operator

import (
	"errors"
	"time"

	"go.uber.org/zap/zapcore"

	"github.com/yanet-platform/yanet2/common/go/logging"
	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/operators/route/neigh"
)

// Config selects the neighbour source and transports to one route operator.
type Config struct {
	Logging         logging.Config              `yaml:"logging"`
	Server          operator.GRPCServerConfig   `yaml:"server"`
	Gateways        []operator.GatewayConfig    `yaml:"gateways"`
	Reconcile       operator.ReconcileConfig    `yaml:"reconcile"`
	TableName       xcfg.NonEmptyString         `yaml:"table_name"`
	DefaultPriority uint32                      `yaml:"default_priority"`
	LinkMap         map[string]string           `yaml:"link_map,omitempty"`
	UpdateInterval  xcfg.NonZero[time.Duration] `yaml:"update_interval"`
	PublishTimeout  xcfg.NonZero[time.Duration] `yaml:"publish_timeout"`
}

// Default supplies discovery cadence and retry settings before YAML decoding.
func (m *Config) Default() {
	*m = Config{
		Logging: logging.Config{Level: zapcore.InfoLevel},
		Server: operator.GRPCServerConfig{
			Endpoint: xcfg.MustNonEmptyString("[::]:9903"),
		},
		Reconcile: operator.ReconcileConfig{
			Interval:       xcfg.MustNonZero(operator.DefaultReconcileInterval),
			InitialBackoff: xcfg.MustNonZero(operator.DefaultReconcileInitialBackoff),
			MaxBackoff:     xcfg.MustNonZero(operator.DefaultReconcileMaxBackoff),
		},
		TableName:       xcfg.MustNonEmptyString("neighbour-sidecar"),
		DefaultPriority: 100,
		LinkMap:         map[string]string{},
		UpdateInterval:  xcfg.MustNonZero(neigh.DefaultUpdateInterval),
		PublishTimeout:  xcfg.MustNonZero(5 * time.Second),
	}
}

// LoggingConfig exposes logging to the common command lifecycle.
func (m *Config) LoggingConfig() *logging.Config {
	return &m.Logging
}

// GatewayConfigs exposes named transports to the deployment override contract.
func (m *Config) GatewayConfigs() *[]operator.GatewayConfig {
	return &m.Gateways
}

// Validate requires a destination and rejects negative scheduling intervals.
func (m *Config) Validate() error {
	if len(m.Gateways) == 0 {
		return errors.New("at least one gateway must be configured")
	}
	for _, interval := range []time.Duration{
		m.UpdateInterval.Unwrap(),
		m.PublishTimeout.Unwrap(),
		m.Reconcile.Interval.Unwrap(),
		m.Reconcile.InitialBackoff.Unwrap(),
		m.Reconcile.MaxBackoff.Unwrap(),
	} {
		if interval < 0 {
			return errors.New("update, publish and reconcile intervals must be positive")
		}
	}
	return m.Reconcile.Validate()
}
