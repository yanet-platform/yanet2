package xgrpc

import (
	"context"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	commonpb "github.com/yanet-platform/yanet2/common/proto"
)

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
					zap.Any("request", sanitizeMessage(message)),
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

// sanitizeMessage creates a sanitized copy of the proto message for logging.
// Fields marked with skip_logging=true will have their values replaced with
// "<skipped>".
func sanitizeMessage(msg proto.Message) map[string]any {
	result := map[string]any{}

	msgReflect := msg.ProtoReflect()
	msgReflect.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		fieldName := string(fd.Name())

		// Check if field has skip_logging option.
		if shouldSkipLogging(fd) {
			result[fieldName] = "<skipped>"
			return true
		}

		// Handle nested messages recursively.
		if fd.Kind() == protoreflect.MessageKind && !fd.IsList() && !fd.IsMap() {
			if nestedMsg := v.Message(); nestedMsg.IsValid() {
				result[fieldName] = sanitizeMessage(nestedMsg.Interface())
			}
			return true
		}

		// Handle repeated message fields.
		if fd.IsList() && fd.Kind() == protoreflect.MessageKind {
			list := v.List()
			items := make([]any, list.Len())
			for i := 0; i < list.Len(); i++ {
				items[i] = sanitizeMessage(list.Get(i).Message().Interface())
			}
			result[fieldName] = items
			return true
		}

		// Handle map fields with message values.
		if fd.IsMap() && fd.MapValue().Kind() == protoreflect.MessageKind {
			mapVal := v.Map()
			mapResult := map[string]any{}
			mapVal.Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
				mapResult[k.String()] = sanitizeMessage(v.Message().Interface())
				return true
			})
			result[fieldName] = mapResult
			return true
		}

		// For other fields, use the interface value.
		result[fieldName] = v.Interface()
		return true
	})

	return result
}

// shouldSkipLogging checks if a field has the skip_logging option set to true.
func shouldSkipLogging(fd protoreflect.FieldDescriptor) bool {
	opts := fd.Options()
	if opts == nil {
		return false
	}

	// Get the skip_logging extension value.
	ext := proto.GetExtension(opts, commonpb.E_SkipLogging)
	if skip, ok := ext.(bool); ok {
		return skip
	}

	return false
}
