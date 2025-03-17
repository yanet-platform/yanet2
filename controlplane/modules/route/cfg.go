package route

import (
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/yanet-platform/yanet2/controlplane/modules/route/internal/discovery/bird"
)

type Config struct {
	// MemoryPath is the path to the shared-memory file that is used to
	// communicate with dataplane.
	MemoryPath string `yaml:"memory_path"`
	// MemoryRequirements is the amount of memory that is required for a single
	// transaction.
	MemoryRequirements datasize.ByteSize `yaml:"memory_requirements"`
	Endpoint           string            `yaml:"endpoint"`
	GatewayEndpoint    string            `yaml:"gateway_endpoint"`
	// RIBFlushPeriod is the time interval between periodic route updates synchronization.
	RIBFlushPeriod time.Duration `yaml:"rib_flush_period"`
	// BirdExport configures the reader for the Bird Export Protocol feed.
	BirdExport *bird.Config `yaml:"bird_export"`
}

func DefaultConfig() *Config {
	return &Config{
		MemoryPath:         "/dev/hugepages/yanet",
		MemoryRequirements: 16777216,
		Endpoint:           "[::1]:0",
		GatewayEndpoint:    "[::1]:8080",
		RIBFlushPeriod:     time.Second,
		BirdExport:         bird.DefaultConfig(),
	}
}
