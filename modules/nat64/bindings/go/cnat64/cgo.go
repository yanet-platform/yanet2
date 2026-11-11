package cnat64

//#cgo CFLAGS: -I../../../../../
//
//#include "api/agent.h"
//#include "modules/nat64/api/nat64cp.h"
import "C"

import (
	"fmt"
	"runtime"
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

// Free destroys the module config, or reports ffi.ErrStillReferenced while a
// live generation still holds it. Safe to call multiple times.
func (m *ModuleConfig) Free() error {
	return m.ptr.Free(func(ptr unsafe.Pointer) (int, unsafe.Pointer, error) {
		var cErr *C.yanet_error
		rc, errno := C.nat64_module_config_free((*C.struct_cp_module)(ptr), &cErr)
		return int(rc), unsafe.Pointer(cErr), errno
	})
}

// MalformedPackets reads the currently published NAT64 configuration's count.
//
// The count resets when the module configuration is replaced. Configuration
// locking keeps the lookup and atomic read valid during updates.
func MalformedPackets(sharedMemory *ffi.SharedMemory, instanceIdx uint32, name string) (uint64, error) {
	if !sharedMemory.DataplaneReady(instanceIdx) {
		return 0, fmt.Errorf("dataplane instance %d is not ready or out of range", instanceIdx)
	}

	moduleName := C.CString(name)
	defer C.free(unsafe.Pointer(moduleName))

	var value C.uint64_t
	pinner := runtime.Pinner{}
	pinner.Pin(&value)
	defer pinner.Unpin()
	if result, err := C.nat64_module_config_malformed_packets(
		(*C.struct_yanet_shm)(sharedMemory.AsRawPtr()),
		C.uint32_t(instanceIdx),
		moduleName,
		&value,
	); result != 0 {
		return 0, fmt.Errorf("read NAT64 module %q diagnostic: %w", name, err)
	}
	return uint64(value), nil
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
