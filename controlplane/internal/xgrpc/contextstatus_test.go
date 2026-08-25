package xgrpc_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/internal/xgrpc"
)

// Test_ContextStatusInterceptor_Outcome verifies which failures are
// reported as the caller having walked away, and that the reason the
// handler gave survives the relabelling exactly once.
func Test_ContextStatusInterceptor_Outcome(t *testing.T) {
	handlerErr := status.Error(codes.Internal, "failed to update module config")

	tests := []struct {
		name        string
		context     func(t *testing.T) context.Context
		handlerErr  error
		wantCode    codes.Code
		wantMessage string
	}{
		{
			name:       "successful handler is untouched",
			context:    func(t *testing.T) context.Context { return t.Context() },
			handlerErr: nil,
			wantCode:   codes.OK,
		},
		{
			name: "cancelled context reports work its caller left behind",
			context: func(t *testing.T) context.Context {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx
			},
			handlerErr:  nil,
			wantCode:    codes.Canceled,
			wantMessage: "completed after its caller left",
		},
		{
			name:        "live context keeps the handler's own code",
			context:     func(t *testing.T) context.Context { return t.Context() },
			handlerErr:  handlerErr,
			wantCode:    codes.Internal,
			wantMessage: "failed to update module config",
		},
		{
			name: "cancelled context reports the caller leaving",
			context: func(t *testing.T) context.Context {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx
			},
			handlerErr:  handlerErr,
			wantCode:    codes.Canceled,
			wantMessage: "failed to update module config",
		},
		{
			name: "cancelled context relabels a plain handler error",
			context: func(t *testing.T) context.Context {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx
			},
			handlerErr:  errors.New("failed to update device"),
			wantCode:    codes.Canceled,
			wantMessage: "failed to update device",
		},
		{
			name: "cancelled context keeps a refusal the handler chose",
			context: func(t *testing.T) context.Context {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx
			},
			handlerErr:  status.Error(codes.InvalidArgument, "prefix is malformed"),
			wantCode:    codes.InvalidArgument,
			wantMessage: "prefix is malformed",
		},
		{
			name: "expired deadline reports the deadline",
			context: func(t *testing.T) context.Context {
				ctx, cancel := context.WithDeadline(t.Context(), time.Now())
				t.Cleanup(cancel)
				return ctx
			},
			handlerErr:  handlerErr,
			wantCode:    codes.DeadlineExceeded,
			wantMessage: "failed to update module config",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := func(ctx context.Context, req any) (any, error) {
				return "response", test.handlerErr
			}

			response, err := xgrpc.ContextStatusInterceptor()(
				test.context(t),
				nil,
				&grpc.UnaryServerInfo{},
				handler,
			)

			require.Equal(t, test.wantCode, status.Code(err))
			if test.wantCode == codes.OK {
				require.NoError(t, err)
				require.Equal(t, "response", response)
				return
			}
			require.Equal(t, test.wantMessage, status.Convert(err).Message())
			require.Nil(t, response)
		})
	}
}

// Test_AccessLogInterceptor_AbandonedCallLevel verifies that a call its
// caller walked away from is not logged at the level reserved for the
// service failing.
func Test_AccessLogInterceptor_AbandonedCallLevel(t *testing.T) {
	tests := []struct {
		name       string
		handlerErr error
		wantLevel  string
	}{
		{
			name:       "cancelled call is not a fault",
			handlerErr: status.Error(codes.Canceled, "gone"),
			wantLevel:  `"level":"info"`,
		},
		{
			name:       "expired deadline is not a fault",
			handlerErr: status.Error(codes.DeadlineExceeded, "too late"),
			wantLevel:  `"level":"info"`,
		},
		{
			name:       "genuine failure stays a fault",
			handlerErr: status.Error(codes.Internal, "shared memory exhausted"),
			wantLevel:  `"level":"error"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			buf := new(bytes.Buffer)
			logger := zap.New(zapcore.NewCore(
				zapcore.NewJSONEncoder(zapcore.EncoderConfig{
					MessageKey:  "msg",
					LevelKey:    "level",
					EncodeLevel: zapcore.LowercaseLevelEncoder,
				}),
				zapcore.AddSync(buf),
				zap.DebugLevel,
			))

			_, err := xgrpc.AccessLogInterceptor(logger)(
				t.Context(),
				nil,
				&grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"},
				func(ctx context.Context, req any) (any, error) {
					return nil, test.handlerErr
				},
			)
			require.Error(t, err)
			require.NoError(t, logger.Sync())
			require.Contains(t, buf.String(), test.wantLevel)
		})
	}
}
