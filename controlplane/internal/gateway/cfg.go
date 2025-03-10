package gateway

// Config is the configuration for the gateway.
type Config struct {
	// Server is the configuration for the gateway server.
	Server ServerConfig `yaml:"server"`
}

// ServerConfig is the configuration for the gateway server.
type ServerConfig struct {
	// Endpoint is the endpoint for the gateway server to be exposed on.
	Endpoint string `yaml:"endpoint"`
}

func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Endpoint: "[::1]:8080",
		},
	}
}
