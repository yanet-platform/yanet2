package gateway

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/ynpb"
)

// LoggingService is a service that exposes logging configuration at runtime.
type LoggingService struct {
	ynpb.UnimplementedLoggingServer

	atom *zap.AtomicLevel
	log  *zap.SugaredLogger
}

// NewLoggingService creates a new LoggingService.
func NewLoggingService(atom *zap.AtomicLevel, log *zap.SugaredLogger) *LoggingService {
	return &LoggingService{
		atom: atom,
		log:  log,
	}
}

// UpdateLevel updates the minimum logging level.
func (m *LoggingService) UpdateLevel(
	ctx context.Context,
	req *ynpb.UpdateLevelRequest,
) (*ynpb.UpdateLevelResponse, error) {
	if m.atom == nil {
		return nil, status.Errorf(codes.Unimplemented, "service doesn't support setting log level dynamically")
	}

	level, err := convertLevel(req.GetLevel())
	if err != nil {
		return nil, fmt.Errorf("failed to convert logging level: %w", err)
	}

	m.atom.SetLevel(level)
	m.log.Infof("updated log level to %q", level)

	return &ynpb.UpdateLevelResponse{}, nil
}

func convertLevel(v ynpb.LogLevel) (zapcore.Level, error) {
	switch v {
	case ynpb.LogLevel_DEBUG:
		return zapcore.DebugLevel, nil
	case ynpb.LogLevel_INFO:
		return zapcore.InfoLevel, nil
	case ynpb.LogLevel_WARN:
		return zapcore.WarnLevel, nil
	case ynpb.LogLevel_ERROR:
		return zapcore.ErrorLevel, nil
	default:
		return zapcore.InvalidLevel, fmt.Errorf("unexpected value: %v", v)
	}
}
