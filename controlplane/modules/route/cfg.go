package route

import (
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
	MemoryRequirements uint   `yaml:"memory_requirements"`
	Endpoint           string `yaml:"endpoint"`
	GatewayEndpoint    string `yaml:"gateway_endpoint"`
	// BirdExport configures the reader for the Bird Export Protocol feed.
	BirdExport *bird.Config `yaml:"bird_export"`
}

func DefaultConfig() *Config {
	return &Config{
		// FIXME: a reasonable default value
		MemoryPathPrefix:   "",
		MemoryRequirements: 0,
		Endpoint:           "",
		GatewayEndpoint:    "",
		BirdExport:         bird.DefaultConfig(),
	}
}
