package yncp

import (
	"fmt"

	"go.uber.org/zap"
)

// InitLogging initializes the logging subsystem.
func InitLogging(cfg *LoggingConfig) (*zap.SugaredLogger, zap.AtomicLevel, error) {
	config := zap.NewDevelopmentConfig()
	config.Development = false
	config.Level.SetLevel(cfg.Level)

	logger, err := config.Build()
	if err != nil {
		return nil, zap.AtomicLevel{}, fmt.Errorf("failed to initialize logger: %w", err)
	}

	return logger.Sugar(), config.Level, nil
}
