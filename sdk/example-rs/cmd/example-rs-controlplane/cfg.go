package main

import (
	"go.uber.org/zap/zapcore"

	"github.com/yanet-platform/yanet2/common/go/logging"
	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	example "github.com/yanet-platform/yanet2/sdk/example-rs/controlplane"
)

// Config is the daemon's YAML configuration.
type Config struct {
	// Module carries the shared-memory attachment parameters passed to
	// ffi.Attach.
	Module example.Config `yaml:"module"`

	// Server is where the daemon serves the module's gRPC service.
	Server operator.GRPCServerConfig `yaml:"server"`

	// Register paces the gateway registration heartbeat.
	Register operator.RegisterConfig `yaml:"register"`

	// Gateways receive the registration heartbeat advertising Server.
	// An empty list leaves the daemon serving its endpoint directly,
	// reachable without the gateway.
	Gateways []operator.GatewayConfig `yaml:"gateways"`

	Logging logging.Config `yaml:"logging"`
}

// DefaultConfig returns default configuration.
func DefaultConfig() *Config {
	return &Config{
		Module: *example.DefaultConfig(),
		Server: operator.GRPCServerConfig{
			Endpoint: xcfg.MustNonEmptyString("[::1]:8092"),
		},
		Register: operator.RegisterConfig{
			Interval: xcfg.MustNonZero(operator.DefaultRegisterInterval),
		},
		Gateways: []operator.GatewayConfig{},
		Logging: logging.Config{
			Level: zapcore.InfoLevel,
		},
	}
}

// Default resets Config to DefaultConfig.
func (m *Config) Default() {
	*m = *DefaultConfig()
}

// LoggingConfig exposes the logging configuration to the generic operator
// CLI helper.
func (m *Config) LoggingConfig() *logging.Config {
	return &m.Logging
}
