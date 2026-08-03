package unrdup

import (
	"github.com/c2h5oh/datasize"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

type Config struct {
	// InstanceID specifies which dataplane instance this module serves.
	InstanceID xcfg.Required[uint32] `yaml:"instance_id"`
	// MemoryPath is the shared-memory file used to talk to the dataplane.
	MemoryPath xcfg.NonEmptyString `yaml:"memory_path"`
	// MemoryRequirements is the memory needed for a single transaction.
	MemoryRequirements xcfg.NonZero[datasize.ByteSize] `yaml:"memory_requirements"`

	Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
}

func DefaultConfig() *Config {
	return &Config{
		MemoryPath:         xcfg.MustNonEmptyString("/dev/hugepages/yanet"),
		MemoryRequirements: xcfg.MustNonZero(32 * datasize.MB),
		Endpoint:           xcfg.MustNonEmptyString("[::1]:0"),
	}
}

// Default resets Config to DefaultConfig.
func (m *Config) Default() {
	*m = *DefaultConfig()
}
