package balancer

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NewStatusError creates a sentinel error carrying a gRPC status code but with
// a plain Error() message (no "rpc error: code = ..." prefix). This keeps
// wrapped error chains readable while still letting NewError inherit the code.
func NewStatusError(code codes.Code, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	return &wrappedStatusError{
		st:    status.New(code, msg),
		cause: errors.New(msg),
	}
}

// WrapStatusError converts an error to a gRPC status error at the RPC boundary,
// preserving any code inherited through NewError/NewStatusError. Falls back to
// `fallback` when no status is attached. The formatted context is prepended.
func WrapStatusError(fallback codes.Code, err error, format string, args ...any) error {
	code := fallback
	if st := extractStatus(err); st != nil {
		code = st.Code()
	}
	ctx := fmt.Sprintf(format, args...)
	return status.Errorf(code, "%s: %s", ctx, err.Error())
}

type wrappedStatusError struct {
	st    *status.Status
	cause error
}

func (e *wrappedStatusError) Error() string {
	return e.cause.Error()
}

func (e *wrappedStatusError) GRPCStatus() *status.Status {
	return e.st
}

func (e *wrappedStatusError) Unwrap() error {
	return e.cause
}

// NewError creates a new formatted error. If any of the arguments contains
// a gRPC status (implements GRPCStatus()), the returned error inherits that status.
// The format string follows fmt.Errorf conventions (including %w for wrapping).
func NewError(format string, args ...any) error {
	// First, find a gRPC status among the arguments
	var grpcStatus *status.Status
	for _, arg := range args {
		if st := extractStatus(arg); st != nil {
			grpcStatus = st
			break
		}
	}

	// Build the underlying error with fmt.Errorf to support %w, %v, etc.
	underlying := fmt.Errorf(format, args...)

	// If no gRPC status was found, return a plain error
	if grpcStatus == nil {
		return underlying
	}

	// Return a statusError that preserves the gRPC status
	return &wrappedStatusError{
		st:    grpcStatus,
		cause: underlying,
	}
}

// extractStatus attempts to extract a *status.Status from a value.
// It checks:
//  1. Direct *statusError type
//  2. Any error implementing GRPCStatus() (including status.Status errors)
//  3. If it's an error, unwrap recursively via errors.As
func extractStatus(v any) *status.Status {
	// Check if the value itself implements GRPCStatus()
	type grpcStatusProvider interface {
		GRPCStatus() *status.Status
	}

	if sp, ok := v.(grpcStatusProvider); ok {
		return sp.GRPCStatus()
	}

	// If it's an error, try to find a GRPCStatus in the chain
	if err, ok := v.(error); ok {
		var sp grpcStatusProvider
		if errors.As(err, &sp) {
			return sp.GRPCStatus()
		}
	}

	return nil
}
