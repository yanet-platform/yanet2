package balancer

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/status"
)

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
