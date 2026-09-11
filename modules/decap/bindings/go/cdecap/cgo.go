package cdecap

//#cgo CFLAGS: -I../../../../../
//#cgo LDFLAGS: -L../../../../../build/modules/decap/api -ldecap_cp
//
// #include "api/agent.h"
// #include "modules/decap/api/controlplane.h"
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
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

	var cErr *C.yanet_error
	ptr := C.decap_module_config_new((*C.struct_agent)(agent.AsRawPtr()), cName, &cErr)
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

// Free destroys the module config, or reports ffi.ErrStillReferenced while a
// live generation still holds it. Safe to call multiple times.
func (m *ModuleConfig) Free() error {
	return m.ptr.Free(func(ptr unsafe.Pointer) (int, unsafe.Pointer, error) {
		var cErr *C.yanet_error
		rc, errno := C.decap_module_config_free((*C.struct_cp_module)(ptr), &cErr)
		return int(rc), unsafe.Pointer(cErr), errno
	})
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
