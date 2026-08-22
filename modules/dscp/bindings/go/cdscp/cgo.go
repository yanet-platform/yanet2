package cdscp

//#cgo CFLAGS: -I../../../../../
//#cgo LDFLAGS: -L../../../../../build/modules/dscp/api -ldscp_cp
//
//#include <stdlib.h>
//#include "modules/dscp/api/controlplane.h"
import "C"

import (
	"errors"
	"fmt"
	"syscall"
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
	ptr := C.dscp_module_config_new((*C.struct_agent)(agent.AsRawPtr()), cName, &cErr)
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
	rc, errno := C.dscp_module_config_free(ptr, &cErr)
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
