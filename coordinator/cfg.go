package coordinator

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/coordinator/internal/stage"
)

// Config represents the main configuration structure for the coordinator.
type Config struct {
	// Coordinator configuration.
	Coordinator CoordinatorConfig `yaml:"coordinator"`
	// Gateway configuration.
	Gateway GatewayConfig `yaml:"gateway"`
	// Multi-stage configuration.
	Stages []stage.Config `yaml:"stages"`
}

// LoadConfig loads configuration from a YAML file at the specified path.
func LoadConfig(path string) (*Config, error) {
	// Read file content.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Start with default configuration.
	cfg := DefaultConfig()

	// Unmarshal YAML into config structure.
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse YAML configuration: %w", err)
	}

	return cfg, nil
}

// RequiredModules returns a set of module names from the configuration.
func (m *Config) RequiredModules() map[string]struct{} {
	modules := map[string]struct{}{}

	for _, stage := range m.Stages {
		for _, instanceConfig := range stage.Instances {
			for name := range instanceConfig.Modules {
				modules[name] = struct{}{}
			}
		}
	}

	return modules
}

// CoordinatorConfig contains settings for the coordinator itself.
type CoordinatorConfig struct {
	// Endpoint is the coordinator gRPC endpoint for external module
	// registration.
	Endpoint string `yaml:"endpoint"`
}

// GatewayConfig contains settings for connecting to the Gateway API.
type GatewayConfig struct {
	// Endpoint is the Gateway API endpoint.
	Endpoint string `yaml:"endpoint"`
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	return &Config{
		Coordinator: CoordinatorConfig{
			Endpoint: "[::1]:50052",
		},
		Gateway: GatewayConfig{
			Endpoint: "[::1]:8080",
		},
		Stages: []stage.Config{},
	}
}
