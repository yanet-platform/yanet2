package route_mpls

import (
	"github.com/c2h5oh/datasize"

	"github.com/yanet-platform/yanet2/controlplane/modulebase"
)

// Config holds the configuration for the route-mpls control-plane module.
type Config struct {
	modulebase.BaseConfig `yaml:",inline"`
}

// DefaultConfig returns a Config populated with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		BaseConfig: modulebase.NewBaseConfig(16 * datasize.MB),
	}
}

// Default resets Config to DefaultConfig.
func (m *Config) Default() {
	*m = *DefaultConfig()
}
