package config

import (
	"fmt"
	"os"

	"github.com/yanet-platform/yanet2/common/go/logging"
	"go.uber.org/zap/zapcore"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Logging         logging.Config `yaml:"logging"`
	Port            int            `yaml:"port"`
	ModulesEndpoint string         `yaml:"endpoint"`
	Format          string         `yaml:"format"`
	Modules         []string       `yaml:"modules"`
}

func DefaultConfig() *Config {
	return &Config{
		Logging: logging.Config{
			Level: zapcore.InfoLevel,
		},
		Port:            8080,
		ModulesEndpoint: "[::1]:8080",
		Format:          "prometheus",
		Modules:         []string{},
	}
}

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

func MustLoad(path string) *Config {
	cfg, err := LoadConfig(path)
	if err != nil {
		panic(fmt.Sprintf("failed to load config: %v", err))
	}
	return cfg
}
