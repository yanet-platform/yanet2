package operator

import (
	"errors"

	"go.uber.org/zap/zapcore"

	"github.com/yanet-platform/yanet2/common/go/logging"
	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

// Config is the top-level YAML configuration for yanet-pipeline-operator.
type Config struct {
	Logging   logging.Config             `yaml:"logging"`
	Server    *operator.GRPCServerConfig `yaml:"server"`
	Gateways  []operator.GatewayConfig   `yaml:"gateways"`
	Register  operator.RegisterConfig    `yaml:"register"`
	Reconcile operator.ReconcileConfig   `yaml:"reconcile"`
	Stages    []StageConfig              `yaml:"stages"`
}

func (m *Config) Default() {
	*m = *DefaultConfig()
}

func (m *Config) Validate() error {
	if len(m.Gateways) == 0 {
		return errors.New("at least one gateway must be configured")
	}

	return nil
}

// DefaultConfig returns a Config populated with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		Logging: logging.Config{
			Level: zapcore.InfoLevel,
		},
		Server: &operator.GRPCServerConfig{
			Endpoint: xcfg.MustNonEmptyString("localhost:50001"),
		},
		Reconcile: operator.ReconcileConfig{
			Interval:       xcfg.MustNonZero(operator.DefaultReconcileInterval),
			InitialBackoff: xcfg.MustNonZero(operator.DefaultReconcileInitialBackoff),
			MaxBackoff:     xcfg.MustNonZero(operator.DefaultReconcileMaxBackoff),
		},
		Register: operator.RegisterConfig{
			Interval: xcfg.MustNonZero(operator.DefaultRegisterInterval),
		},
	}
}
