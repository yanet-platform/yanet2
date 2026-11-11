package route

import (
	"github.com/c2h5oh/datasize"

	"github.com/yanet-platform/yanet2/controlplane/modulebase"
)

// Config is the route module shim configuration.
type Config struct {
	modulebase.BaseConfig `yaml:",inline"`

	// DisableNexthopCounters turns off the per-nexthop dataplane counters.
	//
	// Negative polarity so the zero value keeps counters on: an operator
	// who never sets this field gets the safer, more observable default
	// without DefaultConfig needing an explicit entry for it.
	DisableNexthopCounters bool `yaml:"disable_nexthop_counters"`
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
