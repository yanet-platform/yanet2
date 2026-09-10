// Package cerrors provides Go bindings for the C yanet_error type defined in
// lib/errors.
//
// It is meant to be used by FFI wrappers to convert C error chains into
// idiomatic Go errors.
package cerrors

//#cgo CFLAGS: -I../../../
//#cgo LDFLAGS: -L../../../build/lib/errors -lerrors
//
//#include <stdlib.h>
//#include "lib/errors/errors.h"
import "C"

import (
	"errors"
	"unsafe"
)

// Kind is the failure category a C error chain reports, the target of
// errors.Is the way a syscall.Errno is.
//
// The C layer tags the cause, the caller maps it to a status code: the same
// kind means different things to different operations, so no mapping lives
// here.
type Kind int

const (
	// NotFound: the entity the operation names does not exist.
	NotFound Kind = C.YANET_ERROR_NOT_FOUND
	// FailedPrecondition: the system is not in the state the operation
	// needs, such as a live configuration naming an entity that does not
	// exist.
	FailedPrecondition Kind = C.YANET_ERROR_FAILED_PRECONDITION
	// Busy: the entity is still referenced, so the operation was refused.
	Busy Kind = C.YANET_ERROR_BUSY
	// InvalidArgument: the request is wrong regardless of the system state.
	InvalidArgument Kind = C.YANET_ERROR_INVALID_ARGUMENT
	// ResourceExhausted: the pool the operation draws from has no room
	// left for the request.
	ResourceExhausted Kind = C.YANET_ERROR_RESOURCE_EXHAUSTED
)

func (m Kind) Error() string {
	switch m {
	case NotFound:
		return "not found"
	case FailedPrecondition:
		return "failed precondition"
	case Busy:
		return "busy"
	case InvalidArgument:
		return "invalid argument"
	case ResourceExhausted:
		return "resource exhausted"
	}
	return "unknown error kind"
}

// Error is a Go wrapper around a formatted *C.yanet_error chain.
//
// The underlying C error chain is formatted and freed inside FromC().
type Error struct {
	msg  string
	kind Kind
}

func (m Error) Error() string {
	return m.msg
}

// Is reports whether the C chain carried the target kind. An untagged
// chain matches nothing.
func (m Error) Is(target error) bool {
	kind, ok := target.(Kind)
	return ok && kind != 0 && kind == m.kind
}

// FromC converts a *C.yanet_error returned from C code into a Go error and
// releases the underlying C chain.
//
// Returns nil when cErr is NULL.
func FromC(cErr unsafe.Pointer) error {
	if cErr == nil {
		return nil
	}
	err := (*C.yanet_error)(cErr)
	defer C.yanet_error_free(err)

	cMsg := C.yanet_error_format(err)
	if cMsg == nil {
		return errors.New("yanet: out of memory formatting error")
	}
	defer C.free(unsafe.Pointer(cMsg))

	return &Error{
		msg:  C.GoString(cMsg),
		kind: Kind(C.yanet_error_kind(err)),
	}
}
