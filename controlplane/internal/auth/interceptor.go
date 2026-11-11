package auth

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc"
)

// UnaryServerInterceptor returns a gRPC unary server interceptor that performs
// authentication and authorization.
//
// A call that carries an admission runs as the admission's principal,
// authorized again for the method; any other call is checked on the
// credential it presents.
func UnaryServerInterceptor(
	manager *Manager,
	log *zap.Logger,
) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx, err := admitIncoming(ctx, manager, log, info.FullMethod)
		if err != nil {
			return nil, err
		}

		return handler(ctx, req)
	}
}

// StreamServerInterceptor returns a gRPC stream server interceptor that performs authentication and authorization.
//
// It admits a call as UnaryServerInterceptor does.
func StreamServerInterceptor(manager *Manager, log *zap.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, err := admitIncoming(ss.Context(), manager, log, info.FullMethod)
		if err != nil {
			return err
		}

		wrapped := &wrappedServerStream{
			ServerStream: ss,
			ctx:          ctx,
		}

		return handler(srv, wrapped)
	}
}

// wrappedServerStream wraps grpc.ServerStream to override the Context()
// method.
//
// This allows us to inject the Principal into the stream context.
type wrappedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

// Context returns the wrapped context with Principal.
func (m *wrappedServerStream) Context() context.Context {
	return m.ctx
}
