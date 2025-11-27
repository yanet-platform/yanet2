package route

import (
	"time"

	"github.com/c2h5oh/datasize"
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
	RibTTL             time.Duration     `yaml:"rib_ttl"`
	LinkMap            map[string]string `yaml:"link_map"`
}

func DefaultConfig() *Config {
	return &Config{
		MemoryPath:         "/dev/hugepages/yanet",
		MemoryRequirements: 16777216,
		Endpoint:           "[::1]:0",
		GatewayEndpoint:    "[::1]:8080",
		RibTTL:             time.Minute,
		LinkMap:            make(map[string]string),
	}
}

type instanceKey struct {
	name              string
	dataplaneInstance uint32
}
