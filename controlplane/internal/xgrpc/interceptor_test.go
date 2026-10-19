package xgrpc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

type testProtoLogValue struct {
	wrapperspb.StringValue
	logValue any
}

func (m *testProtoLogValue) AsLogValue() any {
	return m.logValue
}

var (
	_ proto.Message = (*testProtoLogValue)(nil)
	_ ProtoLogValue = (*testProtoLogValue)(nil)
)

func TestAccessLogInterceptor_ProtoLogValue(t *testing.T) {
	tests := []struct {
		name        string
		req         any
		wantContain string
	}{
		{
			name: "ObjectMarshalerFunc",
			req: &testProtoLogValue{
				logValue: zapcore.ObjectMarshalerFunc(func(enc zapcore.ObjectEncoder) error {
					enc.AddString("name", "test-config")
					enc.AddString("rules", "<redacted>")
					enc.AddInt("rules_count", 1000)
					return nil
				}),
			},
			wantContain: `"rules_count":1000`,
		},
		{
			name: "string value",
			req: &testProtoLogValue{
				logValue: "192.168.1.0/24",
			},
			wantContain: `"value":"192.168.1.0/24"`,
		},
		{
			name:        "regular proto.Message",
			req:         wrapperspb.String("hello"),
			wantContain: `"value":"hello"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

			interceptor := AccessLogInterceptor(logger)
			info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}

			_, _ = interceptor(context.Background(), tt.req, info, func(ctx context.Context, req any) (any, error) {
				return nil, nil
			})
			_ = logger.Sync()

			if !bytes.Contains(buf.Bytes(), []byte(tt.wantContain)) {
				t.Errorf("log output = %q, want to contain %q", buf.String(), tt.wantContain)
			}
		})
	}
}

// Test_StreamAccessLogInterceptor_LogsIdentityAndOutcome verifies that stream
// completion records verified identity and the handler's gRPC status.
func Test_StreamAccessLogInterceptor_LogsIdentityAndOutcome(t *testing.T) {
	verifiedPrincipal := &core.Principal{User: "alice", AuthMethod: "sshcert"}
	anonymousPrincipal := &core.Principal{
		User:        "anonymous",
		AuthMethod:  "none",
		IsAnonymous: true,
	}
	cases := []struct {
		name         string
		principal    *core.Principal
		handlerError error
		statusCode   codes.Code
	}{
		{
			name:       "verified identity succeeds",
			principal:  verifiedPrincipal,
			statusCode: codes.OK,
		},
		{
			name:       "anonymous identity succeeds",
			principal:  anonymousPrincipal,
			statusCode: codes.OK,
		},
		{
			name:         "grpc failure preserves status",
			principal:    verifiedPrincipal,
			handlerError: status.Error(codes.PermissionDenied, "backend denied access"),
			statusCode:   codes.PermissionDenied,
		},
		{
			name:         "ordinary failure remains unknown",
			principal:    verifiedPrincipal,
			handlerError: errors.New("backend failed"),
			statusCode:   codes.Unknown,
		},
		{
			name:         "cancellation records canceled",
			principal:    anonymousPrincipal,
			handlerError: context.Canceled,
			statusCode:   codes.Canceled,
		},
		{
			name:         "wrapped cancellation records canceled",
			principal:    verifiedPrincipal,
			handlerError: fmt.Errorf("stream interrupted: %w", context.Canceled),
			statusCode:   codes.Canceled,
		},
		{
			name:         "deadline records deadline exceeded",
			principal:    verifiedPrincipal,
			handlerError: context.DeadlineExceeded,
			statusCode:   codes.DeadlineExceeded,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logCore, entries := observer.New(zap.DebugLevel)
			ctx := metadata.NewIncomingContext(t.Context(), metadata.Pairs(
				"authorization", "Bearer never-log-this-token",
				"user", "unverified-user",
			))
			ctx = core.WithPrincipal(ctx, tc.principal)
			stream := &accessLogServerStream{ContextValue: ctx}
			server := &struct{}{}
			info := &grpc.StreamServerInfo{FullMethod: "/test.Service/Stream"}
			err := StreamAccessLogInterceptor(zap.New(logCore))(
				server, stream, info,
				func(actualServer any, actualStream grpc.ServerStream) error {
					require.Same(t, server, actualServer)
					require.Same(t, stream, actualStream)
					require.Same(t, ctx, actualStream.Context())
					return tc.handlerError
				},
			)
			require.True(t, err == tc.handlerError, "handler error was replaced")
			require.Equal(t, 1, entries.Len())
			entry := entries.All()[0]
			fields := entry.ContextMap()
			require.Equal(t, info.FullMethod, fields["method"])
			require.Equal(t, tc.statusCode.String(), fields["status"])
			require.Equal(t, tc.principal.User, fields["user"])
			require.Equal(t, tc.principal.AuthMethod, fields["auth_method"])
			require.Equal(t, tc.principal.IsAnonymous, fields["is_anonymous"])
			require.IsType(t, time.Duration(0), fields["duration"])
			require.Greater(t, fields["duration"].(time.Duration), time.Duration(0))
			require.NotContains(t, fmt.Sprint(fields), "never-log-this-token")
			require.NotContains(t, fmt.Sprint(fields), "unverified-user")
			if tc.handlerError == nil {
				require.Equal(t, zap.InfoLevel, entry.Level)
			} else {
				require.Equal(t, zap.ErrorLevel, entry.Level)
			}
		})
	}
}

// Test_StreamAccessLogInterceptor_PreservesCancellation verifies that observing
// a canceled handler keeps the original context and cancellation result intact.
func Test_StreamAccessLogInterceptor_PreservesCancellation(t *testing.T) {
	logCore, entries := observer.New(zap.InfoLevel)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream := &accessLogServerStream{ContextValue: ctx}
	info := &grpc.StreamServerInfo{FullMethod: "/test.Service/Stream"}
	err := StreamAccessLogInterceptor(zap.New(logCore))(
		nil, stream, info,
		func(server any, actualStream grpc.ServerStream) error {
			cancel()
			return actualStream.Context().Err()
		},
	)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.Equal(t, codes.Canceled.String(), entries.All()[0].ContextMap()["status"])
}

// Test_AccessLogInterceptor_PreservesResponseAndCancellation verifies that
// completion logging leaves the handler response and canceled result intact.
func Test_AccessLogInterceptor_PreservesResponseAndCancellation(t *testing.T) {
	logCore, entries := observer.New(zap.InfoLevel)
	principal := &core.Principal{User: "alice", AuthMethod: "sshcert"}
	ctx := core.WithPrincipal(t.Context(), principal)
	response := wrapperspb.String("partial response")
	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
	actualResponse, err := AccessLogInterceptor(zap.New(logCore))(
		ctx, wrapperspb.String("request"), info,
		func(handlerContext context.Context, request any) (any, error) {
			require.Same(t, ctx, handlerContext)
			return response, context.Canceled
		},
	)
	require.Same(t, response, actualResponse)
	require.ErrorIs(t, err, context.Canceled)
	fields := entries.All()[0].ContextMap()
	require.Equal(t, codes.Canceled.String(), fields["status"])
	require.Equal(t, principal.User, fields["user"])
	require.Equal(t, principal.AuthMethod, fields["auth_method"])
	require.Equal(t, false, fields["is_anonymous"])
}

// Test_AccessLogInterceptor_RedactsIntrospectionToken verifies that debug
// request serialization cannot expose an introspected credential.
func Test_AccessLogInterceptor_RedactsIntrospectionToken(t *testing.T) {
	logCore, entries := observer.New(zap.DebugLevel)
	request := &ynpb.IntrospectTokenRequest{Token: "never-log-this-token"}
	info := &grpc.UnaryServerInfo{
		FullMethod: ynpb.AuthService_IntrospectToken_FullMethodName,
	}
	_, err := AccessLogInterceptor(zap.New(logCore))(
		t.Context(), request, info,
		func(ctx context.Context, actualRequest any) (any, error) {
			require.Same(t, request, actualRequest)
			return nil, nil
		},
	)
	require.NoError(t, err)
	for _, entry := range entries.All() {
		require.NotContains(t, fmt.Sprint(entry.ContextMap()), request.GetToken())
	}
	require.Equal(t, 1, entries.FilterMessage("started gRPC execution").Len())
}

// accessLogServerStream supplies a context without altering stream behavior.
type accessLogServerStream struct {
	grpc.ServerStream
	ContextValue context.Context
}

func (m *accessLogServerStream) Context() context.Context {
	return m.ContextValue
}
