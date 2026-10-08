package example_rs

import (
	"github.com/c2h5oh/datasize"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// Config represents the example module's shared-memory attachment
// configuration.
//
// Unlike an in-tree module, an out-of-tree module owns its daemon: the
// gRPC endpoint and gateway registration live in the daemon's
// configuration (cmd/example-rs-controlplane), not here.
type Config struct {
	ffi.AttachConfig `yaml:",inline"`
}

// DefaultConfig returns default configuration.
func DefaultConfig() *Config {
	return &Config{
		AttachConfig: ffi.DefaultAttachConfig(4 * datasize.MB),
	}
}

// Default resets Config to DefaultConfig.
func (m *Config) Default() {
	*m = *DefaultConfig()
}
