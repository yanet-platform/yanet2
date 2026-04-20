package balancer

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// codedError attaches a gRPC code to an error while remaining transparent
// to errors.Is/As and %w chains.
type codedError struct {
	code codes.Code
	err  error
}

func (e *codedError) Error() string { return e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }
func (e *codedError) GRPCStatus() *status.Status {
	return status.New(e.code, e.err.Error())
}

// CodedErrorf builds a new error tagged with a gRPC code.
// The format string follows fmt.Errorf conventions (including %w).
func CodedErrorf(code codes.Code, format string, args ...any) error {
	return &codedError{code: code, err: fmt.Errorf(format, args...)}
}

// AsStatus converts err to a gRPC status error at the RPC boundary.
// It walks the error chain for an attached code; if none is found,
// it falls back to the supplied code.
func AsStatus(err error, fallback codes.Code) error {
	if err == nil {
		return nil
	}
	code := fallback
	var ce *codedError
	if errors.As(err, &ce) {
		code = ce.code
	}
	return status.Error(code, err.Error())
}
