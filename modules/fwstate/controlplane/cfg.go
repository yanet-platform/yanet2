package fwstate

import (
	"github.com/c2h5oh/datasize"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

// Config represents FWState module configuration
type Config struct {
	// InstanceID specifies which dataplane instance this module serves.
	//
	// Required: a listed module must set it explicitly, even to 0.
	InstanceID xcfg.Required[uint32] `yaml:"instance_id"`

	// MemoryPath is the path to the shared memory file
	MemoryPath xcfg.NonEmptyString `yaml:"memory_path"`

	// MemoryRequirements specifies memory requirements for the module
	MemoryRequirements xcfg.NonZero[datasize.ByteSize] `yaml:"memory_requirements"`

	// Endpoint is the gRPC endpoint address
	Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
}

// DefaultConfig returns default configuration
func DefaultConfig() *Config {
	return &Config{
		MemoryPath: xcfg.MustNonEmptyString("/dev/hugepages/yanet"),
		// The fwstate-map objects linked by acl and fwstate configs
		// allocate in this module's agent zone, so the default must
		// hold at least the default map dimensions: a zero-sizing
		// CreateMap picks a 1,048,576-entry index, and one such layer
		// across both families needs well over 100 MB before module
		// configs and allocator overhead.
		MemoryRequirements: xcfg.MustNonZero(1024 * datasize.MB),
		Endpoint:           xcfg.MustNonEmptyString("[::1]:0"),
	}
}

// Default resets Config to DefaultConfig.
func (m *Config) Default() {
	*m = *DefaultConfig()
}
