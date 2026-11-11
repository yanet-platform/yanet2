package plain

import (
	"github.com/c2h5oh/datasize"

	"github.com/yanet-platform/yanet2/controlplane/modulebase"
)

// Config represents plain device configuration
type Config struct {
	modulebase.BaseConfig `yaml:",inline"`
}

// DefaultConfig returns default configuration
func DefaultConfig() *Config {
	return &Config{
		BaseConfig: modulebase.NewBaseConfig(16 * datasize.MB),
	}
}

// Default resets Config to DefaultConfig.
func (m *Config) Default() {
	*m = *DefaultConfig()
}
