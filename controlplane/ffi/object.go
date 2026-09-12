package ffi

//#cgo CFLAGS: -I../../
//#cgo LDFLAGS: -L../../build/lib/controlplane/agent -lagent
//#include "api/agent.h"
//#include "lib/controlplane/agent/agent.h"
import "C"

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
)

// ObjectConfig is a Go wrapper around a C cp_object pointer, representing
// a shared object's configuration, such as a route module's FIB.
//
// Each object owner wraps it in its own typed handle, using
// unsafe.Pointer as a bridge between CGo contexts of different
// packages.
type ObjectConfig struct {
	ptr *C.struct_cp_object
}

// NewObjectConfig wraps a raw C pointer into an ObjectConfig.
//
// The pointer must originate from an object-specific C constructor that
// returns a valid cp_object pointer.
// The caller is responsible for ensuring the pointer's validity and lifetime.
func NewObjectConfig(ptr unsafe.Pointer) ObjectConfig {
	return ObjectConfig{
		ptr: (*C.struct_cp_object)(ptr),
	}
}

// AsRawPtr returns the underlying C pointer as unsafe.Pointer for passing
// across CGo package boundaries.
func (m ObjectConfig) AsRawPtr() unsafe.Pointer {
	return unsafe.Pointer(m.ptr)
}

// Free destroys the object config through the typed free the object
// supplies and forgets the pointer once it is gone.
//
// The refusal contract is the one of a module config's free.
func (m *ObjectConfig) Free(
	free func(ptr unsafe.Pointer) (rc int, cErr unsafe.Pointer, errno error),
) error {
	if m.ptr == nil {
		return nil
	}

	rc, cErr, errno := free(unsafe.Pointer(m.ptr))
	if rc == 0 {
		m.ptr = nil
		return nil
	}
	if errors.Is(errno, syscall.EAGAIN) {
		cerrors.Free(cErr)
		return ErrStillReferenced
	}

	return fmt.Errorf("failed to free object config: %w", freeFailure(cErr, errno))
}

// UpdateObjects upserts the given objects into the active configuration
// generation.
func (m *Agent) UpdateObjects(objects []ObjectConfig) error {
	if len(objects) == 0 {
		return fmt.Errorf("no objects provided")
	}

	configs := make([]*C.struct_cp_object, len(objects))
	for idx, object := range objects {
		if object.ptr == nil {
			return fmt.Errorf("object config at index %d is nil", idx)
		}
		configs[idx] = (*C.struct_cp_object)(object.AsRawPtr())
	}

	var cErr *C.yanet_error
	rc := C.agent_update_objects(
		(*C.struct_agent)(m.AsRawPtr()),
		C.uint64_t(len(objects)),
		&configs[0],
		&cErr,
	)
	if rc != 0 {
		return fmt.Errorf("failed to update objects: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}

	return nil
}

// DeleteObject removes the named shared object.
func (m *Agent) DeleteObject(objectType, objectName string) error {
	cType := C.CString(objectType)
	defer C.free(unsafe.Pointer(cType))

	cName := C.CString(objectName)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.yanet_error
	rc := C.agent_delete_object(
		(*C.struct_agent)(m.AsRawPtr()),
		cType,
		cName,
		&cErr,
	)
	if rc != 0 {
		return fmt.Errorf(
			"failed to delete object type %q name %q: %w",
			objectType,
			objectName,
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}

	return nil
}
