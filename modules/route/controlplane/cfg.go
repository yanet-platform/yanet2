package route

import (
	"github.com/c2h5oh/datasize"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// Config is the route module shim configuration.
type Config struct {
	ffi.AttachConfig `yaml:",inline"`

	// Endpoint is the gRPC endpoint of the route module shim.
	Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
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
		AttachConfig: ffi.DefaultAttachConfig(16 * datasize.MB),
		Endpoint:     xcfg.MustNonEmptyString("[::1]:0"),
	}
}

// Default resets Config to DefaultConfig.
func (m *Config) Default() {
	*m = *DefaultConfig()
}
