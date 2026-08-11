package builtin

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// Logging is an in-process gRPC service that exposes logging configuration
// at runtime.
type Logging struct {
	ynpb.UnimplementedLoggingServer

	atom     *zap.AtomicLevel
	observer func(zapcore.Level)
	log      *zap.Logger
}

// LoggingOption configures the Logging constructor.
type LoggingOption func(*loggingOptions)

type loggingOptions struct {
	Observer func(zapcore.Level)
	Log      *zap.Logger
}

func newLoggingOptions() *loggingOptions {
	return &loggingOptions{
		Log: zap.NewNop(),
	}
}

// WithLoggingLog sets the logger for the logging service.
func WithLoggingLog(log *zap.Logger) LoggingOption {
	return func(o *loggingOptions) {
		o.Log = log
	}
}

// WithLoggingLevelObserver registers a function called with the new level
// every time UpdateLevel changes it, in addition to the atomic Go level.
//
// This is how a runtime level change also reaches lib/logging's C gate,
// without this package importing the cgo-based clog package.
func WithLoggingLevelObserver(observer func(zapcore.Level)) LoggingOption {
	return func(o *loggingOptions) {
		o.Observer = observer
	}
}

// NewLogging creates a new Logging service.
func NewLogging(level *zap.AtomicLevel, options ...LoggingOption) *Logging {
	opts := newLoggingOptions()
	for _, o := range options {
		o(opts)
	}

	return &Logging{
		atom:     level,
		observer: opts.Observer,
		log:      opts.Log,
	}
}

// Name returns the service name.
func (m *Logging) Name() string { return "logging" }

// Endpoint returns empty string indicating in-process service.
func (m *Logging) Endpoint() string { return "" }

// ServicesNames returns the gRPC service names served by this service.
func (m *Logging) ServicesNames() []string { return []string{"controlplane.ynpb.v1.Logging"} }

// RegisterService registers the service on the given gRPC server.
func (m *Logging) RegisterService(server *grpc.Server) {
	ynpb.RegisterLoggingServer(server, m)
}

// UpdateLevel updates the minimum logging level.
func (m *Logging) UpdateLevel(
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

	// The observer drives an external gate that must never be stricter
	// than zap, even momentarily, or a line it drops never reaches zap
	// to be re-filtered. zapcore.Level orders from most to least verbose,
	// so widening the level (a numerically smaller value) applies the
	// observer before the atomic level. Narrowing keeps the old order,
	// since a temporarily loose gate only costs some extra formatting.
	if level < m.atom.Level() {
		if m.observer != nil {
			m.observer(level)
		}
		m.atom.SetLevel(level)
	} else {
		m.atom.SetLevel(level)
		if m.observer != nil {
			m.observer(level)
		}
	}
	m.log.Info("updated log level", zap.Stringer("level", level))

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
