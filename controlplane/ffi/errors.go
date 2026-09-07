package ffi

import (
	"errors"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
)

// ErrStillReferenced is reported by a handle Free when a live
// configuration generation still references the object: the object is
// intact, the free was refused, and the owner — the module, device or
// object control plane that knows the type — must remember the handle
// and free it again once the generations holding it drain.
var ErrStillReferenced = errors.New("object still referenced by a live configuration generation")

// Kinds a failed agent mutation reports, matched through errors.Is.
//
// The C layer tags the cause, the caller maps it to a status code: the same
// kind means different things to different operations.
const (
	// ErrNotFound: the entity the operation names does not exist.
	ErrNotFound = cerrors.NotFound
	// ErrFailedPrecondition: the system is not in the state the operation
	// needs, such as a live configuration naming an entity that does not
	// exist.
	ErrFailedPrecondition = cerrors.FailedPrecondition
	// ErrBusy: the entity is still referenced, so the operation was refused.
	ErrBusy = cerrors.Busy
	// ErrInvalidArgument: the request is wrong regardless of the system
	// state.
	ErrInvalidArgument = cerrors.InvalidArgument
	// ErrResourceExhausted: the pool the operation draws from has no room
	// left for the request.
	ErrResourceExhausted = cerrors.ResourceExhausted
)
