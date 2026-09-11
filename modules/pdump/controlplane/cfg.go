package pdump

import (
	"github.com/c2h5oh/datasize"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

type Config struct {
	ffi.AttachConfig `yaml:",inline"`

	Endpoint  xcfg.NonEmptyString `yaml:"endpoint"`
	DebugEBPF bool                `yaml:"debug_ebpf"`
}

func DefaultConfig() *Config {
	return &Config{
		AttachConfig: ffi.DefaultAttachConfig(16 * datasize.MB),
		Endpoint:     xcfg.MustNonEmptyString("[::1]:0"),
		DebugEBPF:    false,
	}
}

// Default resets Config to DefaultConfig.
func (m *Config) Default() {
	*m = *DefaultConfig()
}
