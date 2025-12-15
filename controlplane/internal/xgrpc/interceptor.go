package xgrpc

import (
	"context"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
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

// AccessLogInterceptor returns a gRPC unary server interceptor that logs
// requests and responses.
//
// The interceptor logs:
// - Debug: method entry with sanitized request
// - Info: successful completion with duration and status
// - Error: failed calls with duration, status and error message
func AccessLogInterceptor(log *zap.SugaredLogger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		now := time.Now()

		if message, ok := req.(proto.Message); ok {
			if log.Level().Enabled(zap.DebugLevel) {
				log.Debugw("started gRPC execution",
					zap.String("method", info.FullMethod),
					zap.Object("request", &protoMarshaler{message: message}),
				)
			}
		} else {
			log.Debugw("started gRPC execution",
				zap.String("method", info.FullMethod),
			)
		}

		resp, err := handler(ctx, req)
		duration := time.Since(now)
		status, _ := status.FromError(err)

		if err != nil {
			log.Errorw("failed to execute gRPC",
				zap.String("method", info.FullMethod),
				zap.String("status", status.Code().String()),
				zap.Duration("duration", duration),
				zap.Error(err),
			)
		} else {
			log.Infow("completed gRPC execution",
				zap.String("method", info.FullMethod),
				zap.String("status", status.Code().String()),
				zap.Duration("duration", duration),
			)
		}

		return resp, err
	}
}

// protoMarshaler wraps a proto.Message to implement zapcore.ObjectMarshaler.
type protoMarshaler struct {
	message proto.Message
}

// MarshalLogObject implements zapcore.ObjectMarshaler.
func (m *protoMarshaler) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	m.message.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		name := string(fd.Name())

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
