package xgrpc

import (
	"bytes"
	"context"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
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
			)).Sugar()

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
