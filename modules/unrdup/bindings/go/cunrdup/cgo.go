package cunrdup

//#cgo CFLAGS: -I../../../../../ -I../../../../../lib
//#cgo LDFLAGS: -L../../../../../build/modules/unrdup/api -lunrdup_cp
//#cgo LDFLAGS: -L../../../../../build/lib/filter -lfilter_compiler
//
//#include <stdlib.h>
//#include "modules/unrdup/api/controlplane.h"
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

func NewModuleConfig(agent *ffi.Agent, name string) (*ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.yanet_error
	ptr := C.unrdup_module_config_new((*C.struct_agent)(agent.AsRawPtr()), cName, &cErr)
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

func (m *ModuleConfig) AsFFIModule() ffi.ModuleConfig {
	return m.ptr
}

func (m *ModuleConfig) Free() {
	if ptr := m.asRawPtr(); ptr != nil {
		C.unrdup_module_config_free(ptr)
		m.ptr = ffi.ModuleConfig{}
	}
}

func (m *ModuleConfig) setSource(family C.enum_ip_family, addr *C.uint8_t, mask *C.uint8_t) error {
	var cErr *C.yanet_error
	if rc := C.unrdup_module_config_set_source(
		m.asRawPtr(),
		family,
		addr,
		mask,
		&cErr,
	); rc != 0 {
		return fmt.Errorf("failed to set source: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}

	return nil
}

func (m *ModuleConfig) updateServices(
	services *C.struct_unrdup_service_config,
	count C.uint64_t,
) error {
	var cErr *C.yanet_error
	if rc := C.unrdup_module_config_update_services(
		m.asRawPtr(),
		services,
		count,
		&cErr,
	); rc != 0 {
		return fmt.Errorf("failed to update services: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}

	return nil
}
