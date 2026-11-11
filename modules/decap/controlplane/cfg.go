package decap

import (
	"github.com/c2h5oh/datasize"

	"github.com/yanet-platform/yanet2/controlplane/modulebase"
)

type Config struct {
	modulebase.BaseConfig `yaml:",inline"`
}

func DefaultConfig() *Config {
	return &Config{
		BaseConfig: modulebase.NewBaseConfig(16 * datasize.MB),
	}
}

// Default resets Config to DefaultConfig.
func (m *Config) Default() {
	*m = *DefaultConfig()
}
