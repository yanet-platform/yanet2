package nat64

//#cgo CFLAGS: -I../../../ -I../../../lib
//#cgo LDFLAGS: -L../../../build/modules/nat64/api -lnat64_cp -llogging
//#cgo LDFLAGS: -L../../../build/lib/logging/ -llogging
//
//#include "api/agent.h"
//#include "modules/nat64/api/nat64cp.h"
import "C"

import (
	"fmt"
	"unsafe"

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

	ptr, err := C.nat64_module_config_init((*C.struct_agent)(agent.AsRawPtr()), cName)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize NAT64 module config: %w", err)
	}
	if ptr == nil {
		return nil, fmt.Errorf("failed to initialize NAT64 module config: module %q not found", name)
	}

	return &ModuleConfig{
		ptr: ffi.NewModuleConfig(unsafe.Pointer(ptr)),
	}, nil
}

func (m *ModuleConfig) asRawPtr() *C.struct_module_data {
	return (*C.struct_module_data)(m.ptr.AsRawPtr())
}

// AsFFIModule returns the module configuration as an FFI module
func (m *ModuleConfig) AsFFIModule() ffi.ModuleConfig {
	return m.ptr
}

// AddMapping adds a new IPv4-IPv6 address mapping
func (m *ModuleConfig) AddMapping(ipv4 []byte, ipv6 []byte, prefixIndex uint32) error {
	if len(ipv4) != 4 {
		return fmt.Errorf("invalid IPv4 address length: got %d, want 4", len(ipv4))
	}
	if len(ipv6) != 16 {
		return fmt.Errorf("invalid IPv6 address length: got %d, want 16", len(ipv6))
	}

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

// AddPrefix adds a new NAT64 prefix
func (m *ModuleConfig) AddPrefix(prefix []byte) error {
	if len(prefix) != 12 {
		return fmt.Errorf("invalid prefix length: got %d, want 12", len(prefix))
	}

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

// SetDropUnknown sets drop_unknown_prefix and drop_unknown_mapping flags
func (m *ModuleConfig) SetDropUnknown(dropUnknownPrefix bool, dropUnknownMapping bool) error {
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
