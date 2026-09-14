package operator

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap/zapcore"

	"github.com/yanet-platform/yanet2/common/go/logging"
	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/operators/route/neigh"
)

// Config selects the neighbour source and transports to one route operator.
type Config struct {
	Logging         logging.Config           `yaml:"logging"`
	Gateways        []operator.GatewayConfig `yaml:"gateways"`
	Reconcile       operator.ReconcileConfig `yaml:"reconcile"`
	TableName       string                   `yaml:"table_name"`
	DefaultPriority uint32                   `yaml:"default_priority"`
	LinkMap         map[string]string        `yaml:"link_map"`
	UpdateInterval  time.Duration            `yaml:"update_interval"`
	PublishTimeout  time.Duration            `yaml:"publish_timeout"`
}

// Default supplies discovery cadence and retry settings before YAML decoding.
func (m *Config) Default() {
	*m = Config{
		Logging: logging.Config{Level: zapcore.InfoLevel},
		Reconcile: operator.ReconcileConfig{
			Interval:       xcfg.MustNonZero(operator.DefaultReconcileInterval),
			InitialBackoff: xcfg.MustNonZero(operator.DefaultReconcileInitialBackoff),
			MaxBackoff:     xcfg.MustNonZero(operator.DefaultReconcileMaxBackoff),
		},
		TableName:       "neighbour-sidecar",
		DefaultPriority: 100,
		LinkMap:         map[string]string{},
		UpdateInterval:  neigh.DefaultUpdateInterval,
		PublishTimeout:  5 * time.Second,
	}
}

// LoggingConfig exposes logging to the common command lifecycle.
func (m *Config) LoggingConfig() *logging.Config {
	return &m.Logging
}

// Validate rejects unusable destinations and nonpositive scheduling intervals.
func (m *Config) Validate() error {
	if len(m.Gateways) == 0 {
		return errors.New("at least one gateway must be configured")
	}
	if strings.TrimSpace(m.TableName) == "" {
		return errors.New("table_name must be nonempty")
	}
	for _, gateway := range m.Gateways {
		if strings.TrimSpace(gateway.Endpoint.Unwrap()) == "" {
			return fmt.Errorf("gateway %q endpoint must be nonempty", gateway.Name)
		}
	}
	for _, interval := range []time.Duration{
		m.UpdateInterval,
		m.PublishTimeout,
		m.Reconcile.Interval.Unwrap(),
		m.Reconcile.InitialBackoff.Unwrap(),
		m.Reconcile.MaxBackoff.Unwrap(),
	} {
		if interval <= 0 {
			return errors.New("update, publish and reconcile intervals must be positive")
		}
	}
	return m.Reconcile.Validate()
}
