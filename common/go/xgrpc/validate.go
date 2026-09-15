package xgrpc

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// validator is implemented by a request or streamed message that carries its
// own request-only validation rules.
type validator interface{ Validate() error }

// validate turns a failing Validate() into InvalidArgument and passes a
// message without one, such as a raw proxy frame, unchanged.
func validate(msg any) error {
	v, ok := msg.(validator)
	if !ok {
		return nil
	}
	if err := v.Validate(); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	return nil
}

// ValidateUnaryInterceptor rejects a request whose Validate() fails with
// InvalidArgument before the handler runs.
func ValidateUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		if err := validate(req); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// ValidateStreamInterceptor validates every message a handler receives and
// returns InvalidArgument from the receive call for an invalid one.
//
// The client sees that status once the handler returns the receive error.
func ValidateStreamInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv any,
		stream grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		return handler(srv, &validatingServerStream{ServerStream: stream})
	}
}

// validatingServerStream wraps a grpc.ServerStream so a handler's RecvMsg
// call validates the decoded message before the handler sees it.
type validatingServerStream struct {
	grpc.ServerStream
}

func (m *validatingServerStream) RecvMsg(msg any) error {
	if err := m.ServerStream.RecvMsg(msg); err != nil {
		return err
	}
	return validate(msg)
}
