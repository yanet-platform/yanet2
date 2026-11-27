package ffi

import (
	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
)

// ErrStillReferenced is reported by a handle Free when a live
// configuration generation still references the object; see
// cerrors.ErrStillReferenced.
var ErrStillReferenced = cerrors.ErrStillReferenced

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
