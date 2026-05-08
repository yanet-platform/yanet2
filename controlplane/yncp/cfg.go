package yncp

import (
	"gopkg.in/yaml.v3"

	"go.uber.org/zap/zapcore"

	"github.com/yanet-platform/yanet2/common/go/logging"
	"github.com/yanet-platform/yanet2/controlplane/bundle"
	"github.com/yanet-platform/yanet2/controlplane/gateway"
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
	Modules bundle.ModulesConfig `json:"modules" yaml:"modules"`
	// Devices configuration.
	Devices bundle.DevicesConfig `json:"devices" yaml:"devices"`
}

func (m *Config) Default() {
	*m = *DefaultConfig()
}

func DefaultConfig() *Config {
	return &Config{
		Logging: logging.Config{
			Level: zapcore.InfoLevel,
		},
		MemoryPath: "/dev/hugepages/yanet",
		Gateway:    gateway.DefaultConfig(),
		Modules:    bundle.DefaultModulesConfig(),
		Devices:    bundle.DefaultDevicesConfig(),
	}
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
