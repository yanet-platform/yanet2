package route_mpls

import (
	"github.com/c2h5oh/datasize"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

// Config holds the configuration for the route-mpls control-plane module.
type Config struct {
	// InstanceID specifies which dataplane instance this module serves.
	InstanceID uint32 `yaml:"instance_id"`
	// MemoryPath is the path to the shared-memory file used to communicate
	// with the dataplane.
	MemoryPath xcfg.NonEmptyString `yaml:"memory_path"`
	// MemoryRequirements is the amount of shared memory required for a single
	// transaction.
	MemoryRequirements xcfg.NonZero[datasize.ByteSize] `yaml:"memory_requirements"`
	// Endpoint is the gRPC listen address for this module.
	Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
}

// DefaultConfig returns a Config populated with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		MemoryPath:         xcfg.MustNonEmptyString("/dev/hugepages/yanet"),
		MemoryRequirements: xcfg.MustNonZero(16 * datasize.MB),
		Endpoint:           xcfg.MustNonEmptyString("[::1]:0"),
	}
}
