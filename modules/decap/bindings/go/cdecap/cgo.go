package cdecap

//#cgo CFLAGS: -I../../../../../ -I../../../../../lib
//#cgo LDFLAGS: -L../../../../../build/modules/decap/api -ldecap_cp
//
// #include "api/agent.h"
// #include "modules/decap/api/controlplane.h"
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// ModuleConfig is an opaque handle to the decap module configuration in shared
// memory.
type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

// NewModuleConfig allocates a new decap module configuration via the C API.
func NewModuleConfig(agent *ffi.Agent, name string) (*ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	ptr, err := C.decap_module_config_create((*C.struct_agent)(agent.AsRawPtr()), cName)
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
		C.decap_module_config_free(ptr)
		m.ptr = ffi.ModuleConfig{}
	}
}

// addPrefixV4 maps 1:1 to decap_module_config_add_prefix_v4.
func (m *ModuleConfig) addPrefixV4(from [4]byte, to [4]byte) error {
	if rc := C.decap_module_config_add_prefix_v4(
		m.asRawPtr(),
		(*C.uint8_t)(&from[0]),
		(*C.uint8_t)(&to[0]),
	); rc != 0 {
		return fmt.Errorf("failed to add v4 prefix: error code=%d", rc)
	}
	return nil
}

// addPrefixV6 maps 1:1 to decap_module_config_add_prefix_v6.
func (m *ModuleConfig) addPrefixV6(from [16]byte, to [16]byte) error {
	if rc := C.decap_module_config_add_prefix_v6(
		m.asRawPtr(),
		(*C.uint8_t)(&from[0]),
		(*C.uint8_t)(&to[0]),
	); rc != 0 {
		return fmt.Errorf("failed to add v6 prefix: error code=%d", rc)
	}
	return nil
}
