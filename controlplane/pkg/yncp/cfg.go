package yncp

import (
	"fmt"
	"os"

	"go.uber.org/zap/zapcore"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/controlplane/internal/gateway"
	"github.com/yanet-platform/yanet2/controlplane/modules/route"
)

type Config struct {
	// Logging configuration.
	Logging LoggingConfig `json:"logging" yaml:"logging"`
	// MemoryPath is the path to the shared-memory file that is used to
	// communicate with dataplane.
	MemoryPath string `yaml:"memory_path"`
	// Gateway configuration.
	Gateway *gateway.Config `json:"gateway" yaml:"gateway"`
	// Modules configuration.
	Modules ModulesConfig `json:"modules" yaml:"modules"`
}

func DefaultConfig() *Config {
	return &Config{
		Logging: LoggingConfig{
			Level: zapcore.InfoLevel,
		},
		MemoryPath: "/dev/hugepages/yanet",
		Gateway:    gateway.DefaultConfig(),
		Modules: ModulesConfig{
			Route: route.DefaultConfig(),
		},
	}
}

// LoadConfig loads the configuration from the given path.
func LoadConfig(path string) (*Config, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(buf, cfg); err != nil {
		return nil, fmt.Errorf("failed to deserialize config: %w", err)
	}

	return cfg, nil
}

// LoggingConfig is the configuration for the logging subsystem.
type LoggingConfig struct {
	// Level is the logging level.
	Level zapcore.Level `yaml:"level"`
}

// ModulesConfig describes built-in modules.
type ModulesConfig struct {
	// Route is the configuration for the route module.
	Route *route.Config `yaml:"route"`
}
