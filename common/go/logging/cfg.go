package logging

import "go.uber.org/zap/zapcore"

// Config is the configuration for the logging subsystem.
type Config struct {
	// Level is the logging level.
	Level zapcore.Level `yaml:"level"`
}
