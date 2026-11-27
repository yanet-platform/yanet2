package cerrors

//#cgo CFLAGS: -I../../../
//
//#include <stdlib.h>
//#include "lib/errors/errors.h"
import "C"

import (
	"errors"
	"unsafe"
)

// The kinds carry the values of the C enum: a kind that differs indexes
// past the array and fails the build.
func _() {
	var kinds [1]struct{}
	_ = kinds[NotFound-C.YANET_ERROR_NOT_FOUND]
	_ = kinds[FailedPrecondition-C.YANET_ERROR_FAILED_PRECONDITION]
	_ = kinds[Busy-C.YANET_ERROR_BUSY]
	_ = kinds[InvalidArgument-C.YANET_ERROR_INVALID_ARGUMENT]
	_ = kinds[ResourceExhausted-C.YANET_ERROR_RESOURCE_EXHAUSTED]
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

// Free releases a C error without reading it, for an outcome the caller
// classifies from the return code alone.
func Free(cErr unsafe.Pointer) {
	if cErr == nil {
		return
	}
	C.yanet_error_free((*C.yanet_error)(cErr))
}
