package operator

import (
	"errors"
	"fmt"

	"go.uber.org/zap/zapcore"

	"github.com/yanet-platform/yanet2/common/go/logging"
	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

// Config is the top-level YAML configuration for yanet-forward-operator.
type Config struct {
	Logging   logging.Config             `yaml:"logging"`
	Server    *operator.GRPCServerConfig `yaml:"server"`
	Gateways  []operator.GatewayConfig   `yaml:"gateways"`
	Register  operator.RegisterConfig    `yaml:"register"`
	Reconcile operator.ReconcileConfig   `yaml:"reconcile"`
	Functions []FunctionConfig           `yaml:"functions"`
}

// Default resets the config to built-in defaults.
func (m *Config) Default() {
	*m = *DefaultConfig()
}

// LoggingConfig exposes the embedded logging configuration to the
// generic operator CLI helper.
func (m *Config) LoggingConfig() *logging.Config {
	return &m.Logging
}

// Validate checks that the config is structurally sound.
func (m *Config) Validate() error {
	if len(m.Gateways) == 0 {
		return errors.New("at least one gateway must be configured")
	}
	if len(m.Functions) == 0 {
		return errors.New("at least one function must be configured")
	}

	gatewayNames := map[string]struct{}{}
	for idx, gw := range m.Gateways {
		if _, dup := gatewayNames[gw.Name]; dup {
			return fmt.Errorf("duplicate gateway name %q at index %d", gw.Name, idx)
		}
		gatewayNames[gw.Name] = struct{}{}
	}

	names := map[string]struct{}{}
	modules := map[string]struct{}{}
	for idx, fn := range m.Functions {
		name := fn.Name.Unwrap()
		if _, dup := names[name]; dup {
			return fmt.Errorf("duplicate function name %q at index %d", name, idx)
		}
		names[name] = struct{}{}

		mod := fn.Module.Unwrap()
		if _, dup := modules[mod]; dup {
			return fmt.Errorf("duplicate module name %q at index %d", mod, idx)
		}
		modules[mod] = struct{}{}
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
			Endpoint: xcfg.MustNonEmptyString("[::1]:50003"),
		},
		Reconcile: operator.ReconcileConfig{
			Interval:       xcfg.MustNonZero(operator.DefaultReconcileInterval),
			InitialBackoff: xcfg.MustNonZero(operator.DefaultReconcileInitialBackoff),
			MaxBackoff:     xcfg.MustNonZero(operator.DefaultReconcileMaxBackoff),
		},
		Register: operator.RegisterConfig{
			Interval: xcfg.MustNonZero(operator.DefaultRegisterInterval),
		},
		Functions: []FunctionConfig{},
	}
}

// FunctionConfig describes one gateway-side network function managed by
// this operator.
type FunctionConfig struct {
	// Name is the function identifier (e.g. "fn:forward-vlan-phy").
	Name xcfg.NonEmptyString `yaml:"name"`
	// Chain is the chain name (e.g. "default").
	Chain xcfg.NonEmptyString `yaml:"chain"`
	// Weight is the chain weight inside the function.
	Weight uint64 `yaml:"weight"`
	// Module is the forward module config name this function targets
	// (e.g. "vlan-phy").
	Module xcfg.NonEmptyString `yaml:"module"`
	// RulesFile is the path to the forward rules YAML file for this
	// function.
	RulesFile xcfg.NonEmptyString `yaml:"rules_file"`
	// IgnorePdump skips function updates when the existing chain already
	// matches Modules once every pdump:* module is filtered out.
	IgnorePdump bool `yaml:"ignore_pdump"`
}
