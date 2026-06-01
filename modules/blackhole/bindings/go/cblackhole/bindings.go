// Package cblackhole is Go binding for the blackhole module
package cblackhole

//#cgo CFLAGS: -I../../../../../
//#cgo CFLAGS: -I../../../../../lib
//#cgo LDFLAGS: -L../../../../../build/modules/blackhole/api -lblackhole_cp
//
//#include "api/agent.h"
//#include "modules/blackhole/api/controlplane.h"
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// ModuleConfig is an opaque handle to the 'blackhole' module configuration in
// shared memory.
type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

// NewModuleConfig allocates a new Blackhole module configuration via the C API.
func NewModuleConfig(agent *ffi.Agent, name string) (*ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.yanet_error
	ptr := C.blackhole_module_config_init((*C.struct_agent)(agent.AsRawPtr()), cName, &cErr)
	if ptr == nil {
		return nil, fmt.Errorf(
			"failed to initialize module config: %w",
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
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
		C.blackhole_module_config_free(ptr)
		m.ptr = ffi.ModuleConfig{}
	}
}
