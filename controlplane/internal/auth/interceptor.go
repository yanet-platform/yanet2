package auth

import (
	"context"
	"errors"

	"google.golang.org/grpc"
)

// UnaryServerInterceptor returns a gRPC unary server interceptor that performs
// authentication and authorization.
//
// A call that carries an admission runs as the admission's principal only
// for its original method and policy. Other calls present credentials.
func (m *Manager) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		policy := m.active.Load()
		ctx, _, err := m.admitIncoming(ctx, info.FullMethod, policy)
		if err != nil {
			return nil, err
		}

		if policy != nil {
			ctx = context.WithValue(ctx, policyContextKey{}, policy)
		}
		return handler(ctx, req)
	}
}

// StreamServerInterceptor returns a gRPC stream server interceptor that performs authentication and authorization.
//
// It admits a call as UnaryServerInterceptor does.
func (m *Manager) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		policy := m.active.Load()
		ctx, identity, err := m.admitIncoming(ss.Context(), info.FullMethod, policy)
		if err != nil {
			return err
		}
		release, err := m.trackStream(ctx, info.FullMethod, identity, policy)
		if err != nil {
			return err
		}
		defer release()

		// The tracker needs only the verified identity, not a prior
		// snapshot retained for the lifetime of this stream.
		wrapped := &wrappedServerStream{
			ServerStream: ss,
			ctx:          ctx,
			revocable:    m.managedPolicy != nil,
		}

		err = handler(srv, wrapped)
		if wrapped.Revoked() {
			return errPolicyRevoked
		}
		return err
	}
}

// wrappedServerStream wraps grpc.ServerStream to override the Context()
// method.
//
// This allows us to inject the Principal into the stream context.
type wrappedServerStream struct {
	grpc.ServerStream
	ctx       context.Context
	revocable bool
}

// Context returns the wrapped context with Principal.
func (m *wrappedServerStream) Context() context.Context {
	return m.ctx
}

func (m *wrappedServerStream) Revoked() bool {
	return m.revocable && errors.Is(context.Cause(m.ctx), errPolicyRevoked)
}

// SendMsg refuses new writes after cancellation even if the transport has
// queue space. A write that already passed this check may still enqueue.
func (m *wrappedServerStream) SendMsg(message any) error {
	if m.Revoked() {
		return errPolicyRevoked
	}
	return m.ServerStream.SendMsg(message)
}

// RecvMsg does not hand queued input to the handler after revocation.
func (m *wrappedServerStream) RecvMsg(message any) error {
	if m.Revoked() {
		return errPolicyRevoked
	}
	err := m.ServerStream.RecvMsg(message)
	if m.Revoked() {
		return errPolicyRevoked
	}
	return err
}
