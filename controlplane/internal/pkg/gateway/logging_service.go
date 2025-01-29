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

type LoggingService struct {
	ynpb.UnimplementedLoggingServer

	atom *zap.AtomicLevel
	log  *zap.SugaredLogger
}

func NewLoggingService(atom *zap.AtomicLevel, log *zap.SugaredLogger) *LoggingService {
	return &LoggingService{
		atom: atom,
		log:  log,
	}
}

func (m *LoggingService) UpdateLevel(
	ctx context.Context,
	req *ynpb.UpdateLevelRequest,
) (*ynpb.UpdateLevelResponse, error) {
	if m.atom == nil {
		return nil, status.Errorf(codes.Unimplemented, "service doesn't support setting log level dynamically")
	}

	level, err := zapcore.ParseLevel(req.GetLevel())
	if err != nil {
		return nil, fmt.Errorf("failed to parse level: %w", err)
	}

	m.atom.SetLevel(level)
	m.log.Infof("successfully updated log level to %q", level)

	return &ynpb.UpdateLevelResponse{}, nil
}
