package route

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
}
