// Package cp provides a thin Go wrapper around the lib/testutils/cp C harness.
//
// The harness creates an in-process shared memory arena that mirrors a real
// YANET shared memory segment. Tests use it to build cp-API module configs
// without standing up a full dataplane process.
package cp

//#cgo CFLAGS: -I../../../../ -I../../../../lib
//#cgo LDFLAGS: -L../../../../build/lib/testutils/cp -ltestutils_cp
//#cgo LDFLAGS: -L../../../../build/lib/controlplane/agent -lagent
//#cgo LDFLAGS: -L../../../../build/lib/controlplane/config -lconfig_cp
//#cgo LDFLAGS: -L../../../../build/lib/counters -lcounters
//#cgo LDFLAGS: -L../../../../build/lib/dataplane/pipeline -lpipeline
//#cgo LDFLAGS: -L../../../../build/lib/dataplane/config -lconfig_dp
//#cgo LDFLAGS: -L../../../../build/lib/dataplane/module -lmodule
//#cgo LDFLAGS: -L../../../../build/lib/logging -llogging
//#cgo LDFLAGS: -L../../../../build/lib/errors -lerrors
//#cgo LDFLAGS: -ldl
/*
#include <stdlib.h>
#include "lib/testutils/cp/cp.h"
#include "lib/errors/errors.h"
*/
import "C"
import (
	"fmt"
	"runtime"
	"unsafe"
)

// SHM is an in-process test shared memory arena.
//
// Create one with NewSHM, then close it when the test is done.
type SHM struct {
	ptr *C.struct_test_shm
}

// NewSHM creates an in-process test arena and registers the named modules.
//
// Each name in modules must correspond to a new_module_<name> symbol
// exported by a statically linked module library. Sizes cpSize and dpSize
// are the byte capacities of the cp and dp memory regions respectively.
func NewSHM(cpSize, dpSize uint64, modules []string) (*SHM, error) {
	var cerr *C.yanet_error
	defer C.yanet_error_reset(&cerr)

	if len(modules) == 0 {
		shm := C.test_shm_create(
			C.size_t(cpSize),
			C.size_t(dpSize),
			nil,
			0,
			&cerr,
		)
		if shm == nil {
			msg := C.GoString(C.yanet_error_message(cerr))
			return nil, fmt.Errorf("failed to create test shm: %s", msg)
		}
		return &SHM{ptr: shm}, nil
	}

	// Build a null-terminated C string array for the module names.
	cnames := make([]*C.char, len(modules))
	for idx, name := range modules {
		cnames[idx] = C.CString(name)
	}
	defer func() {
		for _, cname := range cnames {
			C.free(unsafe.Pointer(cname))
		}
	}()

	pinner := runtime.Pinner{}
	defer pinner.Unpin()
	pinner.Pin(&cnames[0])

	shm := C.test_shm_create(
		C.size_t(cpSize),
		C.size_t(dpSize),
		(**C.char)(unsafe.Pointer(&cnames[0])),
		C.size_t(len(modules)),
		&cerr,
	)
	if shm == nil {
		msg := C.GoString(C.yanet_error_message(cerr))
		return nil, fmt.Errorf("failed to create test shm: %s", msg)
	}

	return &SHM{ptr: shm}, nil
}

// Close releases all resources owned by the SHM arena.
func (m *SHM) Close() error {
	if m.ptr == nil {
		return nil
	}
	C.test_shm_destroy(m.ptr)
	m.ptr = nil
	return nil
}

// RawPtr returns the in-process arena pointer as the shm handle understood
// by ffi.NewSharedMemoryFromRaw.
//
// The returned pointer is the arena base address, which the ffi layer treats
// as a *C.struct_yanet_shm. Valid only for the lifetime of the SHM.
func (m *SHM) RawPtr() unsafe.Pointer {
	if m.ptr == nil {
		return nil
	}
	return C.test_shm_dp_config(m.ptr)
}
