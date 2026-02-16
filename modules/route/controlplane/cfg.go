package route

import (
	"time"

	"github.com/c2h5oh/datasize"
)

type Config struct {
	// InstanceID specifies which dataplane instance this module serves.
	InstanceID uint32 `yaml:"instance_id"`
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
	// NetlinkMonitor configures the kernel neighbour discovery via netlink.
	NetlinkMonitor NetlinkMonitorConfig `yaml:"netlink_monitor"`
}

// NetlinkMonitorConfig configures the kernel neighbour discovery via netlink.
type NetlinkMonitorConfig struct {
	// Disabled disables the netlink neighbour monitor entirely.
	//
	// When disabled, no kernel neighbour table is created and no netlink
	// subscription is started.
	Disabled bool `yaml:"disabled"`
	// TableName is the name of the kernel neighbour table.
	TableName string `yaml:"table_name"`
	// DefaultPriority is the default priority for kernel-learned
	// neighbour entries.
	DefaultPriority uint32 `yaml:"default_priority"`
}

func DefaultConfig() *Config {
	return &Config{
		MemoryPath:         "/dev/hugepages/yanet",
		MemoryRequirements: 16777216,
		Endpoint:           "[::1]:0",
		GatewayEndpoint:    "[::1]:8080",
		RibTTL:             time.Minute,
		LinkMap:            make(map[string]string),
		NetlinkMonitor: NetlinkMonitorConfig{
			TableName:       "kernel",
			DefaultPriority: 100,
		},
	}
}
