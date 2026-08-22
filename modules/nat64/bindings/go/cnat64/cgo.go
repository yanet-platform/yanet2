package cnat64

//#cgo CFLAGS: -I../../../../../
//#cgo LDFLAGS: -L../../../../../build/modules/nat64/api -lnat64_cp
//#cgo LDFLAGS: -L../../../../../build/lib/logging/ -llogging
//
//#include "api/agent.h"
//#include "modules/nat64/api/nat64cp.h"
import "C"

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// ModuleConfig wraps C module configuration
type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

// NewModuleConfig creates a new NAT64 module configuration
func NewModuleConfig(agent *ffi.Agent, name string) (*ModuleConfig, error) {
	if agent == nil {
		return nil, fmt.Errorf("agent cannot be nil")
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.yanet_error
	ptr := C.nat64_module_config_create((*C.struct_agent)(agent.AsRawPtr()), cName, &cErr)
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

// AsFFIModule returns the module configuration as an FFI module
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
	rc, errno := C.nat64_module_config_free(ptr, &cErr)
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

// addMapping maps 1:1 to nat64_module_config_add_mapping.
func (m *ModuleConfig) addMapping(ipv4 [4]byte, ipv6 [16]byte, prefixIndex uint32) error {
	rc, err := C.nat64_module_config_add_mapping(
		m.asRawPtr(),
		*(*C.uint32_t)(unsafe.Pointer(&ipv4[0])),
		(*C.uint8_t)(unsafe.Pointer(&ipv6[0])),
		C.size_t(prefixIndex),
	)
	if err != nil {
		return fmt.Errorf("failed to add mapping: %w", err)
	}
	if rc < 0 {
		return fmt.Errorf("failed to add mapping: return code %d", rc)
	}

	return nil
}

// addPrefix maps 1:1 to nat64_module_config_add_prefix.
func (m *ModuleConfig) addPrefix(prefix [12]byte) error {
	rc, err := C.nat64_module_config_add_prefix(
		m.asRawPtr(),
		(*C.uint8_t)(unsafe.Pointer(&prefix[0])),
	)
	if err != nil {
		return fmt.Errorf("failed to add prefix: %w", err)
	}
	if rc < 0 {
		return fmt.Errorf("failed to add prefix: return code %d", rc)
	}

	return nil
}

// setDropUnknown maps 1:1 to nat64_module_config_set_drop_unknown.
func (m *ModuleConfig) setDropUnknown(dropUnknownPrefix bool, dropUnknownMapping bool) error {
	rc, err := C.nat64_module_config_set_drop_unknown(
		m.asRawPtr(),
		C.bool(dropUnknownPrefix),
		C.bool(dropUnknownMapping),
	)
	if err != nil {
		return fmt.Errorf("failed to set drop unknown flags: %w", err)
	}
	if rc < 0 {
		return fmt.Errorf("failed to set drop unknown flags: return code %d", rc)
	}

	return nil
}

// setMTU maps 1:1 to nat64_module_config_set_mtu.
func (m *ModuleConfig) setMTU(ipv4MTU uint16, ipv6MTU uint16) error {
	rc, err := C.nat64_module_config_set_mtu(
		m.asRawPtr(),
		C.uint16_t(ipv4MTU),
		C.uint16_t(ipv6MTU),
	)
	if err != nil {
		return fmt.Errorf("failed to set MTU: %w", err)
	}
	if rc < 0 {
		return fmt.Errorf("failed to set MTU: return code %d", rc)
	}

	return nil
}
