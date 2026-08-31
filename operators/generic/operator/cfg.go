package operator

import (
	"errors"
	"fmt"

	"go.uber.org/zap/zapcore"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/common/go/logging"
	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/common/go/xproto"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// Config is the top-level YAML configuration for one yanet-generic-operator
// instance.
type Config struct {
	// Name is the instance name. The operator reports readiness under it,
	// so it must match the name the announcer addresses.
	Name      xcfg.NonEmptyString       `yaml:"name"`
	Logging   logging.Config            `yaml:"logging"`
	Server    operator.GRPCServerConfig `yaml:"server"`
	Gateways  []operator.GatewayConfig  `yaml:"gateways"`
	Register  operator.RegisterConfig   `yaml:"register"`
	Reconcile operator.ReconcileConfig  `yaml:"reconcile"`
	Targets   []TargetConfig            `yaml:"targets"`
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
	if len(m.Targets) == 0 {
		return errors.New("at least one target must be configured")
	}

	gatewayNames := map[string]struct{}{}
	for idx, gw := range m.Gateways {
		if _, dup := gatewayNames[gw.Name]; dup {
			return fmt.Errorf("duplicate gateway name %q at index %d", gw.Name, idx)
		}
		gatewayNames[gw.Name] = struct{}{}
	}

	return nil
}

// DefaultConfig returns a Config populated with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		Logging: logging.Config{
			Level: zapcore.InfoLevel,
		},
		Server: operator.GRPCServerConfig{
			Endpoint: xcfg.MustNonEmptyString("[::1]:0"),
		},
		Reconcile: operator.ReconcileConfig{
			Interval:       xcfg.MustNonZero(operator.DefaultReconcileInterval),
			InitialBackoff: xcfg.MustNonZero(operator.DefaultReconcileInitialBackoff),
			MaxBackoff:     xcfg.MustNonZero(operator.DefaultReconcileMaxBackoff),
		},
		Register: operator.RegisterConfig{
			Interval: xcfg.MustNonZero(operator.DefaultRegisterInterval),
		},
		Targets: []TargetConfig{},
	}
}

// TargetConfig describes one module config this instance pushes and the
// function that references it.
type TargetConfig struct {
	// Name labels the target in logs and errors.
	Name string `yaml:"name"`
	// Method is the unary gRPC method that replaces the module config,
	// spelled as "package.Service/Method".
	Method xcfg.NonEmptyString `yaml:"method"`
	// File is the path to the module config for this target: the method's
	// request in YAML, sent as is.
	File xcfg.NonEmptyString `yaml:"file"`
	// Function is published after the config, a whole ynpb.Function in the
	// message's YAML form. May be omitted when the target owns none.
	Function FunctionConfig `yaml:"function"`
	// IgnorePdump skips function updates when the existing chain already
	// matches once every pdump:* module is filtered out.
	//
	// Defaults to true when the field is omitted from the YAML input.
	IgnorePdump bool `yaml:"ignore_pdump"`
}

// UnmarshalYAML implements yaml.Unmarshaler so that IgnorePdump defaults
// to true when the field is absent from the YAML input.
func (m *TargetConfig) UnmarshalYAML(value *yaml.Node) error {
	type plain TargetConfig
	p := plain{IgnorePdump: true}
	if err := value.Decode(&p); err != nil {
		return err
	}
	*m = TargetConfig(p)
	return nil
}

// FunctionConfig carries a whole ynpb.Function spelled in the message's
// YAML form.
type FunctionConfig struct {
	function *ynpb.Function
}

// Unwrap returns the decoded function, nil when the config spelled none.
func (m *FunctionConfig) Unwrap() *ynpb.Function {
	return m.function
}

// UnmarshalYAML decodes the node through xproto, so unknown keys are
// rejected and enums accept their declared names.
func (m *FunctionConfig) UnmarshalYAML(value *yaml.Node) error {
	data, err := yaml.Marshal(value)
	if err != nil {
		return err
	}
	function := &ynpb.Function{}
	if err := xproto.Unmarshal(data, function); err != nil {
		return err
	}
	m.function = function
	return nil
}
