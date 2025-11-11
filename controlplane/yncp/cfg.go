package yncp

import (
	"fmt"
	"os"

	"go.uber.org/zap/zapcore"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/common/go/logging"
	"github.com/yanet-platform/yanet2/controlplane/internal/gateway"

	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
	balancer "github.com/yanet-platform/yanet2/modules/balancer/controlplane"
	decap "github.com/yanet-platform/yanet2/modules/decap/controlplane"
	dscp "github.com/yanet-platform/yanet2/modules/dscp/controlplane"
	forward "github.com/yanet-platform/yanet2/modules/forward/controlplane"
	nat64 "github.com/yanet-platform/yanet2/modules/nat64/controlplane"
	pdump "github.com/yanet-platform/yanet2/modules/pdump/controlplane"
	route "github.com/yanet-platform/yanet2/modules/route/controlplane"

	plain "github.com/yanet-platform/yanet2/devices/plain/controlplane"
	vlan "github.com/yanet-platform/yanet2/devices/vlan/controlplane"
)

type Config config
type config struct {
	// Logging configuration.
	Logging logging.Config `json:"logging" yaml:"logging"`
	// MemoryPath is the path to the shared-memory file that is used to
	// communicate with dataplane.
	MemoryPath string `yaml:"memory_path"`
	// Gateway configuration.
	Gateway *gateway.Config `json:"gateway" yaml:"gateway"`
	// Modules configuration.
	Modules ModulesConfig `json:"modules" yaml:"modules"`
	// Devices configuration.
	Devices DevicesConfig `json:"devices" yaml:"devices"`
}

func DefaultConfig() *Config {
	return &Config{
		Logging: logging.Config{
			Level: zapcore.InfoLevel,
		},
		MemoryPath: "/dev/hugepages/yanet",
		Gateway:    gateway.DefaultConfig(),
		Modules: ModulesConfig{
			Route:    route.DefaultConfig(),
			Decap:    decap.DefaultConfig(),
			DSCP:     dscp.DefaultConfig(),
			Forward:  forward.DefaultConfig(),
			NAT64:    nat64.DefaultConfig(),
			Pdump:    pdump.DefaultConfig(),
			Balancer: balancer.DefaultConfig(),
			ACL:      acl.DefaultConfig(),
		},
		Devices: DevicesConfig{
			Plain: plain.DefaultConfig(),
			Vlan:  vlan.DefaultConfig(),
		},
	}
}

// LoadConfig loads the configuration from the given path.
func LoadConfig(path string) (*Config, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(buf, cfg); err != nil {
		return nil, fmt.Errorf("failed to deserialize config: %w", err)
	}

	return cfg, nil
}

// ModulesConfig describes built-in modules.
type ModulesConfig struct {
	// Route is the configuration for the route module.
	Route *route.Config `yaml:"route"`

	// Decap is the configuration for the decap module.
	Decap *decap.Config `yaml:"decap"`

	// DSCP is the configuration for the dscp module.
	DSCP *dscp.Config `yaml:"dscp"`

	// Forward is the configuration for the forward module.
	Forward *forward.Config `yaml:"forward"`

	// NAT64 is the configuration for the NAT64 module.
	NAT64 *nat64.Config `yaml:"nat64"`

	// Pdump is the configuration for the packet dump module.
	Pdump *pdump.Config `yaml:"pdump"`

	// Balancer is the configuration for the balancer module.
	Balancer *balancer.Config `yaml:"balancer"`

	// ACL is the configuration for the acl module.
	ACL *acl.Config `yaml:"acl"`
}

type DevicesConfig struct {
	// Plain is the configuration for the plain device.
	Plain *plain.Config `yaml:"plain"`
	// Vlan is the configuration for the plain device.
	Vlan *vlan.Config `yaml:"vlan"`
}

// UnmarshalYAML serves as a proxy for validation.
//
// To avoid infinite recursion, the validating wrapper casts itself to the
// private config struct. This allows the decoder to operate on it using the
// default behavior for handling Go structs without an unmarshal method.
func (m *Config) UnmarshalYAML(value *yaml.Node) error {
	err := value.Decode((*config)(m))
	if err != nil {
		return err
	}
	return m.Validate()
}

// Validate validates the control plane configuration.
func (m *Config) Validate() error {
	err := m.Modules.Validate()
	if err != nil {
		return err
	}
	return m.Devices.Validate()
}

func (m *ModulesConfig) Validate() error {
	if m.Route == nil {
		return fmt.Errorf("route module is not configured")
	}
	if m.Decap == nil {
		return fmt.Errorf("decap module is not configured")
	}
	if m.DSCP == nil {
		return fmt.Errorf("dscp module is not configured")
	}
	if m.Forward == nil {
		return fmt.Errorf("forward module is not configured")
	}
	if m.NAT64 == nil {
		return fmt.Errorf("nat64 module is not configured")
	}
	if m.Balancer == nil {
		return fmt.Errorf("balancer module is not configured")
	}
	return nil
}

func (m *DevicesConfig) Validate() error {
	if m.Plain == nil {
		return fmt.Errorf("plain device is not configured")
	}
	if m.Vlan == nil {
		return fmt.Errorf("vlan device is not configured")
	}
	return nil
}
