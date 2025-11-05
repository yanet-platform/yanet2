package balancer

import "github.com/c2h5oh/datasize"

// Config for the balancer service
type Config struct {
	// MemoryPath is the path to the shared-memory file that is used to
	// communicate with dataplane.
	MemoryPath string `yaml:"memory_path"`

	// MemoryRequirements is the amount of memory that is required for a single
	// agent
	MemoryRequirements datasize.ByteSize `yaml:"memory_requirements"`

	Endpoint        string `yaml:"endpoint"`
	GatewayEndpoint string `yaml:"gateway_endpoint"`
}

func DefaultConfig() *Config {
	return &Config{
		MemoryPath:         "/dev/hugepages/yanet",
		MemoryRequirements: 256 * datasize.MB,
		Endpoint:           "[::1]:0",
		GatewayEndpoint:    "[::1]:8080",
	}
}
