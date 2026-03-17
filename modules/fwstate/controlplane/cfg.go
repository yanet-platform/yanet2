package fwstate

import (
	"github.com/c2h5oh/datasize"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

// Config represents FWState module configuration
type Config struct {
	// InstanceID specifies which dataplane instance this module serves.
	InstanceID uint32 `yaml:"instance_id"`

	// MemoryPath is the path to the shared memory file
	MemoryPath xcfg.NonEmptyString `yaml:"memory_path"`

	// MemoryRequirements specifies memory requirements for the module
	MemoryRequirements xcfg.NonZero[datasize.ByteSize] `yaml:"memory_requirements"`

	// Endpoint is the gRPC endpoint address
	Endpoint xcfg.NonEmptyString `yaml:"endpoint"`

	// GatewayEndpoint is the address of the gateway service
	GatewayEndpoint xcfg.NonEmptyString `yaml:"gateway_endpoint"`
}

// DefaultConfig returns default configuration
func DefaultConfig() *Config {
	return &Config{
		MemoryPath:         xcfg.MustNonEmptyString("/dev/hugepages/yanet"),
		MemoryRequirements: xcfg.MustNonZero(64 * datasize.MB),
		Endpoint:           xcfg.MustNonEmptyString("[::1]:0"),
		GatewayEndpoint:    xcfg.MustNonEmptyString("[::1]:8080"),
	}
}
