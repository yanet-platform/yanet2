package fwstate

import (
	"time"

	"github.com/c2h5oh/datasize"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	fwstatemap "github.com/yanet-platform/yanet2/objects/fwstate/controlplane"
)

// Config represents FWState module configuration
type Config struct {
	ffi.AttachConfig `yaml:",inline"`

	// Endpoint is the gRPC endpoint address
	Endpoint xcfg.NonEmptyString `yaml:"endpoint"`

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
		AttachConfig:            ffi.DefaultAttachConfig(1024 * datasize.MB),
		Endpoint:                xcfg.MustNonEmptyString("[::1]:0"),
		StaleLayerSweepInterval: fwstatemap.DefaultStaleLayerSweepInterval,
	}
}

// Default resets Config to DefaultConfig.
func (m *Config) Default() {
	*m = *DefaultConfig()
}
