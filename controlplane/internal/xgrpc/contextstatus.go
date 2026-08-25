package xgrpc

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// callerGone reports whether code says the call ended because whoever
// asked for it stopped waiting, rather than because the service failed.
func callerGone(code codes.Code) bool {
	return code == codes.Canceled || code == codes.DeadlineExceeded
}

// ContextStatusInterceptor reports a call whose caller walked away with
// the code describing that, rather than a server fault.
//
// A handler that gives up on a dead context wraps the reason into its own
// internal failure, which metrics and access logs would otherwise count
// as the service breaking. A failure that merely raced the hangup is
// relabelled too, because handlers flatten the reason into a message
// rather than a wrapped error and the caller is gone either way; the
// original text survives so the log still says what happened. Work that
// could not be abandoned still finishes after its caller has gone, and
// counts the same way, so one hangup reads alike however late it lands.
func ContextStatusInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		response, err := handler(ctx, req)

		var code codes.Code
		switch {
		case errors.Is(ctx.Err(), context.Canceled):
			code = codes.Canceled
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			code = codes.DeadlineExceeded
		case err != nil:
			return nil, err
		default:
			return response, nil
		}

		if err == nil {
			return nil, status.Error(code, "completed after its caller left")
		}

		// A refusal the handler decided on its own says something the
		// caller leaving does not explain, so only the two codes it
		// reaches for when it flattens a reason into a message are
		// eligible.
		switch status.Code(err) {
		case codes.Unknown, codes.Internal:
		default:
			return nil, err
		}

		// Re-wrapping the error itself would nest a rendered status
		// inside the message of another one.
		return nil, status.Error(code, status.Convert(err).Message())
	}
}
