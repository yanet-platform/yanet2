package xgrpc

import (
	"context"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// ProtoLogValue is an interface for custom proto message serialization in logs.
//
// When a proto message implements this interface, the protoMarshaler will use
// the returned value instead of recursively serializing the message fields.
//
// The returned value can be:
//   - string: serialized as a string field
//   - zapcore.ObjectMarshaler: serialized as a nested object
//   - zapcore.ArrayMarshaler: serialized as an array
//   - any other type: serialized via zap.Any (reflection-based)
type ProtoLogValue interface {
	AsLogValue() any
}

// AccessLogInterceptor logs sanitized requests and unary RPC completion.
//
// Completion records identity, duration, handler status, and how the call
// arrived: transport (tls, plain or memory), peer address and user agent.
// Credential introspection failures omit parser details that may contain
// token fragments.
func AccessLogInterceptor(log *zap.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		now := time.Now()

		if message, ok := req.(proto.Message); ok {
			if log.Core().Enabled(zap.DebugLevel) {
				log.Debug("started gRPC execution",
					zap.String("method", info.FullMethod),
					zap.Object("request", &protoMarshaler{message: message}),
				)
			}
		} else {
			log.Debug("started gRPC execution",
				zap.String("method", info.FullMethod),
			)
		}

		resp, err := handler(ctx, req)
		logCompletion(log, ctx, info.FullMethod, now, err)

		return resp, err
	}
}

// StreamAccessLogInterceptor logs streaming RPC completion without inspecting
// message contents or changing the handler outcome.
//
// Completion carries the same identity, transport, peer and user agent
// fields as the unary access log.
func StreamAccessLogInterceptor(log *zap.Logger) grpc.StreamServerInterceptor {
	return func(
		server any,
		stream grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		now := time.Now()
		err := handler(server, stream)
		logCompletion(log, stream.Context(), info.FullMethod, now, err)
		return err
	}
}

func logCompletion(
	log *zap.Logger,
	ctx context.Context,
	method string,
	started time.Time,
	err error,
) {
	st, ok := status.FromError(err)
	if !ok {
		st = status.FromContextError(err)
	}

	fields := []zap.Field{
		zap.String("method", method),
		zap.String("status", st.Code().String()),
		zap.Duration("duration", time.Since(started)),
	}
	if principal := core.FromContext(ctx); principal != nil {
		fields = append(fields,
			zap.String("user", principal.User),
			zap.String("auth_method", principal.AuthMethod),
			zap.Bool("is_anonymous", principal.IsAnonymous),
		)
	}
	fields = append(fields, connectionFields(ctx)...)

	if err != nil {
		// Credential parsing errors may contain fragments of the supplied token.
		if method != ynpb.AuthService_IntrospectToken_FullMethodName {
			fields = append(fields, zap.Error(err))
		}
		log.Error("failed to execute gRPC", fields...)
	} else {
		log.Info("completed gRPC execution", fields...)
	}
}

// connectionFields describes how a call arrived, as received from the peer.
//
// A call over an in-process memory listener, a bufconn one in the gateway,
// is reported as memory, never plain, so plain entries are exactly the calls
// served on a plaintext gRPC listener. HTTP requests reach built-in services
// through such a listener and are logged as memory.
func connectionFields(ctx context.Context) []zap.Field {
	transport, address := "plain", ""
	if info, ok := peer.FromContext(ctx); ok && info.Addr != nil {
		address = info.Addr.String()
		switch {
		case isTLS(info.AuthInfo):
			transport = "tls"
		case info.Addr.Network() == "bufconn":
			transport = "memory"
		}
	}

	userAgent := ""
	if values := metadata.ValueFromIncomingContext(ctx, "user-agent"); len(values) > 0 {
		userAgent = values[0]
	}

	return []zap.Field{
		zap.String("transport", transport),
		zap.String("peer", address),
		zap.String("user_agent", userAgent),
	}
}

// isTLS reports whether the peer's authentication came from a TLS handshake.
func isTLS(authInfo credentials.AuthInfo) bool {
	_, ok := authInfo.(credentials.TLSInfo)
	return ok
}

// protoMarshaler wraps a proto.Message to implement zapcore.ObjectMarshaler.
type protoMarshaler struct {
	message proto.Message
}

// MarshalLogObject implements zapcore.ObjectMarshaler.
func (m *protoMarshaler) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	if v, ok := m.message.(ProtoLogValue); ok {
		logValue := v.AsLogValue()
		switch val := logValue.(type) {
		case zapcore.ObjectMarshaler:
			return val.MarshalLogObject(enc)
		case zapcore.ArrayMarshaler:
			return enc.AddArray("value", val)
		default:
			return enc.AddReflected("value", logValue)
		}
	}

	m.message.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		name := string(fd.Name())

		// Handle repeated scalar fields (lists of primitives)
		if fd.IsList() && fd.Kind() != protoreflect.MessageKind {
			list := v.List()
			_ = enc.AddArray(name, zapcore.ArrayMarshalerFunc(func(arr zapcore.ArrayEncoder) error {
				for i := 0; i < list.Len(); i++ {
					appendScalarValue(arr, fd, list.Get(i))
				}
				return nil
			}))
			return true
		}

		if fd.Kind() == protoreflect.MessageKind && !fd.IsList() && !fd.IsMap() {
			if nested := v.Message(); nested.IsValid() {
				encodeProtoField(enc, name, nested.Interface())
			}

			return true
		}

		if fd.IsList() && fd.Kind() == protoreflect.MessageKind {
			list := v.List()
			_ = enc.AddArray(name, zapcore.ArrayMarshalerFunc(func(arr zapcore.ArrayEncoder) error {
				for i := 0; i < list.Len(); i++ {
					appendProtoValue(arr, list.Get(i).Message().Interface())
				}
				return nil
			}))

			return true
		}

		if fd.IsMap() && fd.MapValue().Kind() == protoreflect.MessageKind {
			_ = enc.AddObject(name, zapcore.ObjectMarshalerFunc(func(obj zapcore.ObjectEncoder) error {
				v.Map().Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
					encodeProtoField(obj, k.String(), v.Message().Interface())
					return true
				})
				return nil
			}))

			return true
		}

		m.encodeScalar(enc, name, fd, v)
		return true
	})

	return nil
}

func encodeProtoField(enc zapcore.ObjectEncoder, name string, msg proto.Message) {
	if v, ok := msg.(ProtoLogValue); ok {
		zap.Any(name, v.AsLogValue()).AddTo(enc)
		return
	}
	_ = enc.AddObject(name, &protoMarshaler{message: msg})
}

func appendProtoValue(enc zapcore.ArrayEncoder, msg proto.Message) {
	if v, ok := msg.(ProtoLogValue); ok {
		_ = enc.AppendReflected(v.AsLogValue())
		return
	}
	_ = enc.AppendObject(&protoMarshaler{message: msg})
}

func (m *protoMarshaler) encodeScalar(
	enc zapcore.ObjectEncoder,
	name string,
	fd protoreflect.FieldDescriptor,
	v protoreflect.Value,
) {
	switch fd.Kind() {
	case protoreflect.BoolKind:
		enc.AddBool(name, v.Bool())
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		enc.AddInt32(name, int32(v.Int()))
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		enc.AddInt64(name, v.Int())
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		enc.AddUint32(name, uint32(v.Uint()))
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		enc.AddUint64(name, v.Uint())
	case protoreflect.FloatKind:
		enc.AddFloat32(name, float32(v.Float()))
	case protoreflect.DoubleKind:
		enc.AddFloat64(name, v.Float())
	case protoreflect.StringKind:
		enc.AddString(name, v.String())
	case protoreflect.BytesKind:
		enc.AddBinary(name, v.Bytes())
	case protoreflect.EnumKind:
		enc.AddString(name, string(fd.Enum().Values().ByNumber(v.Enum()).Name()))
	default:
		_ = enc.AddReflected(name, v.Interface())
	}
}

func appendScalarValue(arr zapcore.ArrayEncoder, fd protoreflect.FieldDescriptor, v protoreflect.Value) {
	switch fd.Kind() {
	case protoreflect.BoolKind:
		arr.AppendBool(v.Bool())
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		arr.AppendInt32(int32(v.Int()))
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		arr.AppendInt64(v.Int())
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		arr.AppendUint32(uint32(v.Uint()))
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		arr.AppendUint64(v.Uint())
	case protoreflect.FloatKind:
		arr.AppendFloat32(float32(v.Float()))
	case protoreflect.DoubleKind:
		arr.AppendFloat64(v.Float())
	case protoreflect.StringKind:
		arr.AppendString(v.String())
	case protoreflect.BytesKind:
		arr.AppendByteString(v.Bytes())
	case protoreflect.EnumKind:
		arr.AppendString(string(fd.Enum().Values().ByNumber(v.Enum()).Name()))
	default:
		_ = arr.AppendReflected(v.Interface())
	}
}
