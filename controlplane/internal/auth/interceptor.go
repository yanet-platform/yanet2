package auth

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
)

// UnaryServerInterceptor returns a gRPC unary server interceptor that performs
// authentication and authorization.
func UnaryServerInterceptor(
	manager *Manager,
	log *zap.Logger,
) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		token := ExtractToken(ctx)

		requestInfo := &core.RequestInfo{
			FullMethod: info.FullMethod,
		}

		// Authenticate the request.
		principal, err := manager.Authenticate(ctx, token, requestInfo)
		if err != nil {
			log.Warn("authentication failed",
				zap.String("method", info.FullMethod),
				zap.Error(err),
			)
			return nil, status.Errorf(codes.Unauthenticated, "authentication failed: %v", err)
		}

		// Authorize the request.
		if err := manager.Authorize(ctx, principal, info.FullMethod); err != nil {
			log.Warn("authorization failed",
				zap.String("method", info.FullMethod),
				zap.String("user", principal.User),
				zap.Error(err),
			)
			return nil, status.Errorf(codes.PermissionDenied, "authorization failed: %v", err)
		}

		return handler(core.WithPrincipal(ctx, principal), req)
	}
}

// StreamServerInterceptor returns a gRPC stream server interceptor that performs authentication and authorization.
func StreamServerInterceptor(manager *Manager, log *zap.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()

		token := ExtractToken(ctx)

		requestInfo := &core.RequestInfo{
			FullMethod: info.FullMethod,
		}

		// Authenticate the request.
		principal, err := manager.Authenticate(ctx, token, requestInfo)
		if err != nil {
			log.Warn("authentication failed",
				zap.String("method", info.FullMethod),
				zap.Error(err),
			)
			return status.Errorf(codes.Unauthenticated, "authentication failed: %v", err)
		}

		// Authorize the request.
		if err := manager.Authorize(ctx, principal, info.FullMethod); err != nil {
			log.Warn("authorization failed",
				zap.String("method", info.FullMethod),
				zap.String("user", principal.User),
				zap.Error(err),
			)
			return status.Errorf(codes.PermissionDenied, "authorization failed: %v", err)
		}

		wrapped := &wrappedServerStream{
			ServerStream: ss,
			ctx:          core.WithPrincipal(ctx, principal),
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
