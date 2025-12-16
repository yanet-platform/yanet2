package gateway

// Config is the configuration for the gateway.
type Config struct {
	// InstanceID specifies which dataplane instance this gateway serves.
	InstanceID uint32 `yaml:"instance_id"`
	// Server is the configuration for the gateway server.
	Server ServerConfig `yaml:"server"`
}

// ServerConfig is the configuration for the gateway server.
type ServerConfig struct {
	// Endpoint is the endpoint for the gateway server to be exposed on.
	Endpoint string `yaml:"endpoint"`
	// HTTPEndpoint is the endpoint for the HTTP server that provides
	// access to gRPC services for web clients.
	HTTPEndpoint string `yaml:"http_endpoint"`
}

func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Endpoint: "[::1]:8080",
		},
	}
}
