package nat64

import (
	"fmt"
	"net"

	"github.com/c2h5oh/datasize"
)

// Config represents NAT64 module configuration
type Config struct {
	// MemoryPath is the path to the shared memory file
	MemoryPath string `yaml:"memory_path"`

	// MemoryRequirements specifies memory requirements for the module
	MemoryRequirements datasize.ByteSize `yaml:"memory_requirements"`

	// Endpoint is the gRPC endpoint address
	Endpoint string `yaml:"endpoint"`

	// GatewayEndpoint is the address of the gateway service
	GatewayEndpoint string `yaml:"gateway_endpoint"`
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.MemoryPath == "" {
		return fmt.Errorf("memory path is required")
	}

	if c.MemoryRequirements == 0 {
		return fmt.Errorf("memory requirements must be greater than 0")
	}

	if c.Endpoint == "" {
		return fmt.Errorf("endpoint is required")
	}

	if _, err := net.ResolveTCPAddr("tcp", c.Endpoint); err != nil {
		return fmt.Errorf("invalid endpoint address: %w", err)
	}

	if c.GatewayEndpoint == "" {
		return fmt.Errorf("gateway endpoint is required")
	}

	if _, err := net.ResolveTCPAddr("tcp", c.GatewayEndpoint); err != nil {
		return fmt.Errorf("invalid gateway endpoint address: %w", err)
	}

	return nil
}

// DefaultConfig returns default configuration
func DefaultConfig() *Config {
	return &Config{
		MemoryPath:         "/dev/hugepages/yanet",
		MemoryRequirements: 16 * datasize.MB,
		Endpoint:           "[::1]:0",
		GatewayEndpoint:    "[::1]:8080",
	}
}

// instanceKey uniquely identifies a module instance on a dataplane instance
type instanceKey struct {
	name              string
	dataplaneInstance uint32
}
