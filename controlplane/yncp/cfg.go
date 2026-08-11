package yncp

import (
	"errors"

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
	Gateway *gateway.Config `json:"gateway" yaml:"gateway"`
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

// Validate rejects a document that clears the defaulted gateway block, be it
// a bare "gateway:" key or an explicit "gateway: null", either of which
// yaml.v3 decodes into a nil Gateway rather than leaving the default intact.
func (m *Config) Validate() error {
	if m.Gateway == nil {
		return errors.New("gateway: must not be empty or null")
	}

	return nil
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
