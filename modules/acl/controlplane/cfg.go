package acl

import (
	"github.com/c2h5oh/datasize"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// Config represents ACL module configuration
type Config struct {
	ffi.AttachConfig `yaml:",inline"`

	// Endpoint is the gRPC endpoint address
	Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
}

// DefaultConfig returns default configuration
func DefaultConfig() *Config {
	return &Config{
		AttachConfig: ffi.DefaultAttachConfig(64 * datasize.MB),
		Endpoint:     xcfg.MustNonEmptyString("[::1]:0"),
	}
}

// Default resets Config to DefaultConfig.
func (m *Config) Default() {
	*m = *DefaultConfig()
}
