package logging

import (
	"fmt"
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/term"
)

// Init initializes the logging subsystem.
func Init(cfg *Config) (*zap.SugaredLogger, zap.AtomicLevel, error) {
	encoderConfig := zap.NewDevelopmentEncoderConfig()

	if term.IsTerminal(int(os.Stderr.Fd())) {
		encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	} else {
		encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	}

	config := zap.Config{
		Level:            zap.NewAtomicLevelAt(cfg.Level),
		Development:      false,
		Encoding:         "console",
		EncoderConfig:    encoderConfig,
		OutputPaths:      []string{"stderr"},
		ErrorOutputPaths: []string{"stderr"},
	}

	logger, err := config.Build()
	if err != nil {
		return nil, zap.AtomicLevel{}, fmt.Errorf("failed to initialize logger: %w", err)
	}

	return logger.Sugar(), config.Level, nil
}
