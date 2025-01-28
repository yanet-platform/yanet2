package yncp

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/controlplane/internal/pkg/gateway"
	"github.com/yanet-platform/yanet2/controlplane/modules/route/pkg/route"
)

type Config struct {
	Logging LoggingConfig   `json:"logging" yaml:"logging"`
	Gateway *gateway.Config `json:"gateway" yaml:"gateway"`
	Modules ModulesConfig   `json:"modules" yaml:"modules"`
}

func LoadConfig(path string) (*Config, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(buf, cfg); err != nil {
		return nil, fmt.Errorf("failed to deserialize config: %w", err)
	}

	return cfg, nil
}

type LoggingConfig struct {
	Level string `yaml:"level"`
}

type ModulesConfig struct {
	Route *route.Config `yaml:"route"`
}
