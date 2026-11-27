// Package cerrors provides Go bindings for the C yanet_error type defined in
// lib/errors.
//
// It is meant to be used by FFI wrappers to convert C error chains into
// idiomatic Go errors. The failure kinds and the refused free build without
// cgo, so a native control plane reports them too.
package cerrors

import "errors"

// ErrStillReferenced is reported by a handle Free when a live
// configuration generation still references the object: the object is
// intact, the free was refused, and the owner — the module, device or
// object control plane that knows the type — must remember the handle
// and free it again once the generations holding it drain.
var ErrStillReferenced = errors.New("object still referenced by a live configuration generation")

// Kind is the failure category a C error chain reports, the target of
// errors.Is the way a syscall.Errno is.
//
// The C layer tags the cause, the caller maps it to a status code: the same
// kind means different things to different operations, so no mapping lives
// here. The values are those of the C enum.
type Kind int

const (
	// NotFound: the entity the operation names does not exist.
	NotFound Kind = 1
	// FailedPrecondition: the system is not in the state the operation
	// needs, such as a live configuration naming an entity that does not
	// exist.
	FailedPrecondition Kind = 2
	// Busy: the entity is still referenced, so the operation was refused.
	Busy Kind = 3
	// InvalidArgument: the request is wrong regardless of the system state.
	InvalidArgument Kind = 4
	// ResourceExhausted: the pool the operation draws from has no room
	// left for the request.
	ResourceExhausted Kind = 5
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
