package operator

import (
	"errors"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap/zapcore"

	"github.com/yanet-platform/yanet2/common/go/logging"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

const (
	DefaultReconcileInterval       = 30 * time.Second
	DefaultReconcileInitialBackoff = 500 * time.Millisecond
	DefaultReconcileMaxBackoff     = 30 * time.Second
	DefaultRegisterInterval        = 30 * time.Second
)

// Config is the top-level YAML configuration for yanet-pipeline-operator.
type Config struct {
	Logging   logging.Config    `yaml:"logging"`
	Server    *GRPCServerConfig `yaml:"server"`
	Gateways  []GatewayConfig   `yaml:"gateways"`
	Register  RegisterConfig    `yaml:"register"`
	Reconcile ReconcileConfig   `yaml:"reconcile"`
	Stages    []StageConfig     `yaml:"stages"`
}

// GatewayConfig holds the name and gRPC endpoint of a single Gateway.
type GatewayConfig struct {
	// Name is a human-readable label used in logs and status reports.
	Name string `yaml:"name"`
	// Endpoint is the gRPC address of the Gateway.
	Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
}

// RegisterConfig holds the gateway registration heartbeat parameter.
type RegisterConfig struct {
	// Interval sets heartbeat period between registration refreshes.
	Interval xcfg.NonZero[time.Duration] `yaml:"interval"`
}

// ReconcileConfig holds timing parameters for the reconcile loop.
type ReconcileConfig struct {
	// Interval is the steady-state period between successful reconcile
	// passes.
	Interval xcfg.NonZero[time.Duration] `yaml:"interval"`
	// InitialBackoff is the first sleep after a failed pass; grows
	// exponentially.
	InitialBackoff xcfg.NonZero[time.Duration] `yaml:"initial_backoff"`
	// MaxBackoff caps the exponential backoff sleep.
	MaxBackoff xcfg.NonZero[time.Duration] `yaml:"max_backoff"`
}

func (m *ReconcileConfig) Validate() error {
	if m.MaxBackoff.Unwrap() < m.InitialBackoff.Unwrap() {
		return fmt.Errorf(
			"max_backoff (%s) must be >= initial_backoff (%s)",
			m.MaxBackoff.Unwrap(),
			m.InitialBackoff.Unwrap(),
		)
	}

	return nil
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
		Server: &GRPCServerConfig{
			Endpoint: xcfg.MustNonEmptyString("localhost:50001"),
		},
		Reconcile: ReconcileConfig{
			Interval:       xcfg.MustNonZero(DefaultReconcileInterval),
			InitialBackoff: xcfg.MustNonZero(DefaultReconcileInitialBackoff),
			MaxBackoff:     xcfg.MustNonZero(DefaultReconcileMaxBackoff),
		},
		Register: RegisterConfig{
			Interval: xcfg.MustNonZero(DefaultRegisterInterval),
		},
	}
}

// LoadConfig reads a YAML file from path and returns the parsed Config.
//
// Default values are applied before unmarshalling so any absent field
// retains its default. Validation is driven by xcfg.Decode, which calls
// Validate() on every field whose type implements it.
func LoadConfig(path string) (*Config, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := DefaultConfig()
	if err := xcfg.Decode(buf, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return cfg, nil
}
