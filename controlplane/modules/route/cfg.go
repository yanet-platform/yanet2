package route

import (
	"github.com/c2h5oh/datasize"
	"github.com/yanet-platform/yanet2/controlplane/modules/route/internal/discovery/bird"
)

type Config struct {
	// MemoryPathPrefix is the path to the shared-memory file that is used to
	// communicate with dataplane.
	//
	// NUMA index will be appended to the path.
	MemoryPathPrefix string `yaml:"memory_path_prefix"`
	// MemoryRequirements is the amount of memory that is required for a single
	// transaction.
	MemoryRequirements datasize.ByteSize `yaml:"memory_requirements"`
	Endpoint           string            `yaml:"endpoint"`
	GatewayEndpoint    string            `yaml:"gateway_endpoint"`
	// BirdExport configures the reader for the Bird Export Protocol feed.
	BirdExport *bird.Config `yaml:"bird_export"`
}

func DefaultConfig() *Config {
	return &Config{
		MemoryPathPrefix:   "/dev/hugepages/data-",
		MemoryRequirements: 16777216,
		Endpoint:           "[::1]:0",
		GatewayEndpoint:    "[::1]:8080",
		BirdExport:         bird.DefaultConfig(),
	}
}
