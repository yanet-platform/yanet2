package yncp

import (
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// InitLogging initializes the logging subsystem.
func InitLogging(cfg *LoggingConfig) (*zap.SugaredLogger, error) {
	logLevel, err := zapcore.ParseLevel(cfg.Level)
	if err != nil {
		return nil, fmt.Errorf("failed to parse logging level: %w", err)
	}

	logCfg := zap.NewDevelopmentConfig()
	logCfg.Development = false
	logCfg.Level.SetLevel(logLevel)

	logger, err := logCfg.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize logger: %w", err)
	}

	return logger.Sugar(), nil
}
