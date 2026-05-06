package config

import (
	"fmt"
	"os"

	"github.com/yanet-platform/yanet2/common/go/logging"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

type Config struct {
	Port          int            `yaml:"port"`
	ModulesAdress string         `yaml:"endpoint"`
	Format        string         `yaml:"format"`
	Modules       []string       `yaml:"modules"`
	Logging       logging.Config `yaml:"logging"`
}

func DefaultConfig() *Config {
	return &Config{
		Port:          8080,
		ModulesAdress: "[::1]:8080",
		Format:        "prometheus",
		Logging: logging.Config{
			Level: 0,
		},
	}
}

func Load(path string) (*Config, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := DefaultConfig()
	if err := xcfg.Decode(buf, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return cfg, nil
}

func MustLoad(path string) *Config {
	cfg, err := Load(path)
	if err != nil {
		panic(fmt.Sprintf("failed to load config: %v", err))
	}
	return cfg
}
