package cfwstate

//#cgo CFLAGS: -I../../../../../
//#cgo CFLAGS: -I../../../../../lib
//#cgo LDFLAGS: -L../../../../../build/modules/fwstate/api -lfwstate_cp
//
//#include "api/agent.h"
//#include "common/numutils.h"
//#include "modules/fwstate/api/fwstate_cp.h"
//#include "lib/fwstate/config.h"
//#include "lib/fwstate/fwmap.h"
//#include "lib/fwstate/fwstate_cursor.h"
//#include "lib/errors/errors.h"
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// ModuleConfig is an opaque handle to the fwstate module configuration in
// shared memory.
type ModuleConfig struct {
	name       string
	ptr        ffi.ModuleConfig
	generation uint64
}

// NewModuleConfig creates a new FWState module configuration.
func NewModuleConfig(agent *ffi.Agent, name string) (*ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.yanet_error
	ptr := C.fwstate_module_config_init((*C.struct_agent)(agent.AsRawPtr()), cName, &cErr)
	if ptr == nil {
		return nil, fmt.Errorf("failed to initialize FWState module config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}

	return &ModuleConfig{
		name: name,
		ptr:  ffi.NewModuleConfig(unsafe.Pointer(ptr)),
	}, nil
}

func (m *ModuleConfig) Name() string {
	return m.name
}

func (m *ModuleConfig) asRawPtr() *C.struct_cp_module {
	return (*C.struct_cp_module)(m.ptr.AsRawPtr())
}

func (m *ModuleConfig) AsFFIModule() ffi.ModuleConfig {
	return m.ptr
}

func (m *ModuleConfig) Generation() uint64 {
	return m.generation
}

func (m *ModuleConfig) PropagateConfig(old *ModuleConfig) {
	C.fwstate_module_config_propogate(m.asRawPtr(), old.asRawPtr())
}

func (m *ModuleConfig) DetachMaps() {
	C.fwstate_module_config_detach_maps(m.asRawPtr())
}

// Free releases the underlying C memory.
//
// Safe to call multiple times: subsequent calls are no-ops.
func (m *ModuleConfig) Free() {
	if ptr := m.asRawPtr(); ptr != nil {
		C.fwstate_module_config_free(ptr)
		m.ptr = ffi.ModuleConfig{}
	}
}
