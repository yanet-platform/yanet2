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

// Error is a Go wrapper around a formatted *C.yanet_error chain.
//
// The underlying C error chain is formatted and freed inside FromC().
type Error struct {
	msg string
}

func (m Error) Error() string {
	return m.msg
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

	return &Error{msg: C.GoString(cMsg)}
}
