package ffi

//#cgo CFLAGS: -I../../../../
//#cgo CFLAGS: -I../../../../lib
//#cgo LDFLAGS: -L../../../../build/modules/forward/api -lforward_cp
//#cgo LDFLAGS: -L../../../../build/filter -lfilter_compiler
//
//#include "api/agent.h"
//#include "modules/forward/api/controlplane.h"
import "C"

import (
	"fmt"
	"unsafe"

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

	ptr, err := C.forward_module_config_init((*C.struct_agent)(agent.AsRawPtr()), cName)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize module config: %w", err)
	}
	if ptr == nil {
		return nil, fmt.Errorf("failed to initialize module config: module %q not found", name)
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

// Free releases the underlying C memory.
//
// Safe to call multiple times: subsequent calls are no-ops.
func (m *ModuleConfig) Free() {
	if ptr := m.asRawPtr(); ptr != nil {
		C.forward_module_config_free(ptr)
		m.ptr = ffi.ModuleConfig{}
	}
}

// update maps 1:1 to forward_module_config_update.
func (m *ModuleConfig) update(rules *C.struct_forward_rule, count C.uint32_t) error {
	rc, err := C.forward_module_config_update(m.asRawPtr(), rules, count)
	if err != nil {
		return fmt.Errorf("failed to update config: %w", err)
	}
	if rc != 0 {
		return fmt.Errorf("failed to update config: %d", rc)
	}
	return nil
}
