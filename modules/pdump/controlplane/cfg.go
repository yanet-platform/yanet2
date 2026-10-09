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
		// Ring objects allocate in this agent's arena; pdump keeps no
		// separate reserve for them.
		//
		// A 1 MiB ring on a 16-worker dataplane takes about 16 MiB, so
		// this default holds several such rings with headroom. Configs
		// can share a ring without multiplying its cost. Raise this to
		// fit more or larger rings.
		AttachConfig: ffi.DefaultAttachConfig(128 * datasize.MB),
		Endpoint:     xcfg.MustNonEmptyString("[::1]:0"),
		DebugEBPF:    false,
	}
}

// Default resets Config to DefaultConfig.
func (m *Config) Default() {
	*m = *DefaultConfig()
}
