package proxy

//#cgo CFLAGS: -I../../../
//#cgo LDFLAGS: -L../../../build/modules/proxy/api -lproxy_cp
//
//#include "api/agent.h"
//#include "modules/proxy/api/controlplane.h"
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
	if agent == nil {
		return nil, fmt.Errorf("agent cannot be nil")
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	ptr, err := C.proxy_module_config_init((*C.struct_agent)(agent.AsRawPtr()), cName)
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

func (m *ModuleConfig) SetAddr(addr uint32) error {
	rc, err := C.proxy_module_config_set_addr(
		m.asRawPtr(),
		C.uint32_t(addr),
	)
	if err != nil {
		return fmt.Errorf("failed to set address: %w", err)
	}
	if rc < 0 {
		return fmt.Errorf("failed to set address: code=%d", rc)
	}

	return nil
}

func DeleteConfig(m *ProxyService, configName string, instance uint32) bool {
	cTypeName := C.CString(agentName)
	defer C.free(unsafe.Pointer(cTypeName))

	cConfigName := C.CString(configName)
	defer C.free(unsafe.Pointer(cConfigName))

	if instance >= uint32(len(m.agents)) {
		return true
	}
	agent := m.agents[instance]
	result := C.agent_delete_module((*C.struct_agent)(agent.AsRawPtr()), cTypeName, cConfigName)
	return result == 0
}
