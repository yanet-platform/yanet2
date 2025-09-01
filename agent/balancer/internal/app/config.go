package app

import (
	"fmt"
	"os"

	"github.com/yanet-platform/yanet2/agent/balancer/internal/controlplane"
	"gopkg.in/yaml.v3"
)

type Config struct {
	ServicesPath string `yaml:"path"`

	ControlPlane controlplane.Config
}

func LoadConfig(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()
	cfg := new(Config)

	err = yaml.NewDecoder(f).Decode(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to decode config: %w", err)
	}
	return cfg, nil
}
