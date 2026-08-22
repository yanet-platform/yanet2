package cforward

//#cgo CFLAGS: -I../../../../../
//#cgo LDFLAGS: -L../../../../../build/modules/forward/api -lforward_cp
//#cgo LDFLAGS: -L../../../../../build/lib/filter -lfilter_compiler
//
//#include "api/agent.h"
//#include "modules/forward/api/controlplane.h"
import "C"

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// ModuleConfig is an opaque handle to the forward module configuration in
// shared memory.
type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

// NewModuleConfig allocates a new forward module configuration via the C API.
func NewModuleConfig(agent *ffi.Agent, name string) (*ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.yanet_error
	ptr := C.forward_module_config_init((*C.struct_agent)(agent.AsRawPtr()), cName, &cErr)
	if ptr == nil {
		return nil, fmt.Errorf("failed to initialize module config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}

	return &ModuleConfig{
		ptr: ffi.NewModuleConfig(unsafe.Pointer(ptr)),
	}, nil
}

func (m *ModuleConfig) asRawPtr() *C.struct_cp_module {
	return (*C.struct_cp_module)(m.ptr.AsRawPtr())
}

// AsFFIModule returns the underlying common module config handle.
func (m *ModuleConfig) AsFFIModule() ffi.ModuleConfig {
	return m.ptr
}

// Free destroys the module config when it is dangling — referenced by no live
// configuration generation — and reports nil. While a live generation
// still references it the free is refused with ffi.ErrStillReferenced
// and the handle stays usable: the caller must remember it and free it
// again once the generations holding it drain. Safe to call multiple
// times: subsequent calls are no-ops reporting nil.
func (m *ModuleConfig) Free() error {
	ptr := m.asRawPtr()
	if ptr == nil {
		return nil
	}
	var cErr *C.yanet_error
	rc, errno := C.forward_module_config_free(ptr, &cErr)
	if rc == 0 {
		m.ptr = ffi.ModuleConfig{}
		return nil
	}
	if errors.Is(errno, syscall.EAGAIN) {
		// The refused attempt allocated an error chain; release it
		// rather than leaking one per attempt. The object is intact.
		C.yanet_error_free(cErr)
		return ffi.ErrStillReferenced
	}
	return fmt.Errorf("failed to free module config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
}

// update maps 1:1 to forward_module_config_update.
func (m *ModuleConfig) update(rules *C.struct_forward_rule, count C.uint32_t) error {
	var cErr *C.yanet_error
	rc := C.forward_module_config_update(m.asRawPtr(), rules, count, &cErr)
	if rc != 0 {
		return fmt.Errorf("failed to update forward config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}
	return nil
}
