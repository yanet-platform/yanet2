package cdscp

//#cgo CFLAGS: -I../../../../../ -I../../../../../lib
//#cgo LDFLAGS: -L../../../../../build/modules/dscp/api -ldscp_cp
//
//#include <stdlib.h>
//#include "modules/dscp/api/controlplane.h"
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

func NewModuleConfig(agent *ffi.Agent, name string) (*ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	ptr, err := C.dscp_module_config_create((*C.struct_agent)(agent.AsRawPtr()), cName)
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

func (m *ModuleConfig) AsFFIModule() ffi.ModuleConfig {
	return m.ptr
}

func (m *ModuleConfig) Free() {
	if ptr := m.asRawPtr(); ptr != nil {
		C.dscp_module_config_free(ptr)
		m.ptr = ffi.ModuleConfig{}
	}
}

func (m *ModuleConfig) prefixAdd4(addrStart [4]byte, addrEnd [4]byte) error {
	if rc := C.dscp_module_config_add_prefix_v4(
		m.asRawPtr(),
		(*C.uint8_t)(&addrStart[0]),
		(*C.uint8_t)(&addrEnd[0]),
	); rc != 0 {
		return fmt.Errorf("failed to add v4 prefix: unknown error code=%d", rc)
	}

	return nil
}

func (m *ModuleConfig) prefixAdd6(addrStart [16]byte, addrEnd [16]byte) error {
	if rc := C.dscp_module_config_add_prefix_v6(
		m.asRawPtr(),
		(*C.uint8_t)(&addrStart[0]),
		(*C.uint8_t)(&addrEnd[0]),
	); rc != 0 {
		return fmt.Errorf("failed to add v6 prefix: unknown error code=%d", rc)
	}

	return nil
}

func (m *ModuleConfig) SetDscpMarking(flag uint8, mark uint8) error {
	if rc := C.dscp_module_config_set_dscp_marking(
		m.asRawPtr(),
		C.uint8_t(flag),
		C.uint8_t(mark),
	); rc != 0 {
		return fmt.Errorf("failed to set DSCP marking: unknown error code=%d", rc)
	}

	return nil
}
