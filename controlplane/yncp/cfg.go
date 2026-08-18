package yncp

import (
	"go.uber.org/zap/zapcore"

	"github.com/yanet-platform/yanet2/common/go/logging"
	"github.com/yanet-platform/yanet2/controlplane/bundle"
	"github.com/yanet-platform/yanet2/controlplane/gateway"
)

// Config is the control plane configuration.
type Config struct {
	// Logging configuration.
	Logging logging.Config `json:"logging" yaml:"logging"`
	// MemoryPath is the path to the shared-memory file that is used to
	// communicate with dataplane.
	MemoryPath string `yaml:"memory_path"`
	// Gateway configuration.
	Gateway gateway.Config `json:"gateway" yaml:"gateway"`
	// Modules configuration. A module absent from the document is not
	// started.
	Modules bundle.ModulesConfig `json:"modules" yaml:"modules"`
	// Devices configuration. A device absent from the document is not
	// started.
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
	}
}
