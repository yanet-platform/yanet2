package operator

import (
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap/zapcore"
	"gopkg.in/yaml.v3"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/logging"
	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// Config is the top-level YAML configuration for one yanet-generic-operator
// instance.
//
// The configs section spells the module configs the instance pushes, the
// functions section the gateway functions published after them.
type Config struct {
	// Name is the instance name. The operator reports readiness under it,
	// so it must match the name the announcer addresses.
	Name      xcfg.NonEmptyString       `yaml:"name"`
	Logging   logging.Config            `yaml:"logging"`
	Server    operator.GRPCServerConfig `yaml:"server"`
	Gateways  []operator.GatewayConfig  `yaml:"gateways"`
	Register  operator.RegisterConfig   `yaml:"register"`
	Reconcile operator.ReconcileConfig  `yaml:"reconcile"`
	// Configs are the module configs this instance pushes.
	Configs []ModuleConfig `yaml:"configs"`
	// Functions are the gateway functions published after the configs.
	Functions []FunctionConfig `yaml:"functions"`
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
	if len(m.Configs) == 0 {
		return errors.New("at least one module config must be configured")
	}

	gatewayNames := map[string]struct{}{}
	for idx, gw := range m.Gateways {
		if _, dup := gatewayNames[gw.Name]; dup {
			return fmt.Errorf("duplicate gateway name %q at index %d", gw.Name, idx)
		}
		gatewayNames[gw.Name] = struct{}{}
	}

	configKeys := map[string]struct{}{}
	for idx, config := range m.Configs {
		name := config.Name.Unwrap()
		// The transport accepts the method with and without a leading
		// slash, so the duplicate key must not tell the spellings apart.
		key := strings.TrimPrefix(config.Method.Unwrap(), "/") + " " + name
		if _, dup := configKeys[key]; dup {
			return fmt.Errorf("configs[%d]: config %q is pushed twice", idx, name)
		}
		configKeys[key] = struct{}{}
	}

	functionNames := map[string]struct{}{}
	for idx, function := range m.Functions {
		name := function.Name.Unwrap()
		if _, dup := functionNames[name]; dup {
			return fmt.Errorf("functions[%d]: function %q is declared twice", idx, name)
		}
		functionNames[name] = struct{}{}
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
		Configs:   []ModuleConfig{},
		Functions: []FunctionConfig{},
	}
}

// ModuleConfig describes one module config this instance pushes.
type ModuleConfig struct {
	// Name is the module config name.
	//
	// It is filled into the request when the file omits its own name, so a
	// payload file does not have to repeat it.
	Name xcfg.NonEmptyString `yaml:"name"`
	// Method is the unary gRPC method that replaces the module config,
	// spelled as "package.Service/Method".
	//
	// The reconcile loop repeats the call, so the method must replace
	// whole state, not accumulate it.
	Method xcfg.NonEmptyString `yaml:"method"`
	// File is the path to the module config file, the method's request in
	// YAML.
	File xcfg.NonEmptyString `yaml:"file"`
}

// FunctionConfig is one gateway function this instance publishes after
// the configs.
type FunctionConfig struct {
	// Name is the function identifier (e.g. "fn:decap").
	Name xcfg.NonEmptyString `yaml:"name"`
	// Chains are the function's weighted processing chains.
	Chains []FunctionChainConfig `yaml:"chains"`
	// IgnorePdump skips function updates when the existing chain already
	// matches once every pdump:* module is filtered out.
	//
	// Defaults to true when the field is omitted from the YAML input.
	IgnorePdump bool `yaml:"ignore_pdump"`
}

// UnmarshalYAML implements yaml.Unmarshaler so that IgnorePdump defaults
// to true when the field is absent from the YAML input.
func (m *FunctionConfig) UnmarshalYAML(value *yaml.Node) error {
	type plain FunctionConfig
	p := plain{IgnorePdump: true}
	if err := value.Decode(&p); err != nil {
		return err
	}
	*m = FunctionConfig(p)
	return nil
}

// AsFunction converts the spelled function to its proto form.
func (m *FunctionConfig) AsFunction() *ynpb.Function {
	chains := make([]*ynpb.FunctionChain, 0, len(m.Chains))
	for _, chain := range m.Chains {
		modules := make([]*commonpb.ModuleId, 0, len(chain.Chain.Modules))
		for _, module := range chain.Chain.Modules {
			modules = append(modules, &commonpb.ModuleId{
				Type: module.Type.Unwrap(),
				Name: module.Name.Unwrap(),
			})
		}
		chains = append(chains, &ynpb.FunctionChain{
			Chain: &ynpb.Chain{
				Name:    chain.Chain.Name.Unwrap(),
				Modules: modules,
			},
			Weight: chain.Weight.Unwrap(),
		})
	}
	return &ynpb.Function{
		Id:     &commonpb.FunctionId{Name: m.Name.Unwrap()},
		Chains: chains,
	}
}

// FunctionChainConfig pairs a chain with its load-balancing weight.
type FunctionChainConfig struct {
	// Chain is the module sequence the weight applies to.
	Chain ChainConfig `yaml:"chain"`
	// Weight must be spelled explicitly, an explicit 0 disables the chain.
	Weight xcfg.Required[uint64] `yaml:"weight"`
}

// ChainConfig is an ordered module sequence.
type ChainConfig struct {
	// Name is the chain name (e.g. "default").
	Name xcfg.NonEmptyString `yaml:"name"`
	// Modules are the module configs packets traverse, in order.
	Modules []ModuleIdConfig `yaml:"modules"`
}

// ModuleIdConfig references one module config by type and name.
type ModuleIdConfig struct {
	// Type is the module type (e.g. "decap").
	Type xcfg.NonEmptyString `yaml:"type"`
	// Name is the module config name (e.g. "decap0").
	Name xcfg.NonEmptyString `yaml:"name"`
}
