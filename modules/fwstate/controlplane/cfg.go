package fwstate

import (
	"time"

	"github.com/c2h5oh/datasize"

	"github.com/yanet-platform/yanet2/controlplane/modulebase"

	fwstatemap "github.com/yanet-platform/yanet2/objects/fwstate/controlplane"
)

// Config represents FWState module configuration
type Config struct {
	modulebase.BaseConfig `yaml:",inline"`

	// StaleLayerSweepInterval is the period between stale-layer sweeps.
	StaleLayerSweepInterval time.Duration `yaml:"stale_layer_sweep_interval"`
}

// DefaultConfig returns default configuration
func DefaultConfig() *Config {
	return &Config{
		// The fwstate-map objects linked by acl and fwstate configs
		// allocate in this module's agent zone, so the default must
		// hold at least the default map dimensions: a zero-sizing
		// CreateMap picks a 1,048,576-entry index, and one such layer
		// across both families needs well over 100 MB before module
		// configs and allocator overhead.
		BaseConfig:              modulebase.NewBaseConfig(1024 * datasize.MB),
		StaleLayerSweepInterval: fwstatemap.DefaultStaleLayerSweepInterval,
	}
}

// Default resets Config to DefaultConfig.
func (m *Config) Default() {
	*m = *DefaultConfig()
}
