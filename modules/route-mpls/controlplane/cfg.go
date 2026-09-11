package route_mpls

import (
	"github.com/c2h5oh/datasize"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// Config holds the configuration for the route-mpls control-plane module.
type Config struct {
	ffi.AttachConfig `yaml:",inline"`

	// Endpoint is the gRPC listen address for this module.
	Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
}

// DefaultConfig returns a Config populated with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		AttachConfig: ffi.DefaultAttachConfig(16 * datasize.MB),
		Endpoint:     xcfg.MustNonEmptyString("[::1]:0"),
	}
}

// Default resets Config to DefaultConfig.
func (m *Config) Default() {
	*m = *DefaultConfig()
}
