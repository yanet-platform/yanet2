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

func NewModuleConfig(agent *ffi.Agent, name string, state *ProxyState) (*ModuleConfig, error) {
	if agent == nil {
		return nil, fmt.Errorf("agent cannot be nil")
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	cState := (*C.struct_proxy_state)(state.cHandle.AsRawPtr())

	ptr, err := C.proxy_module_config_init((*C.struct_agent)(agent.AsRawPtr()), cName, cState)
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

func DeleteConfig(m *ProxyService, configName string) bool {
	cTypeName := C.CString("proxy")
	defer C.free(unsafe.Pointer(cTypeName))

	cConfigName := C.CString(configName)
	defer C.free(unsafe.Pointer(cConfigName))

	result := C.agent_delete_module((*C.struct_agent)(m.agent.AsRawPtr()), cTypeName, cConfigName)
	return result == 0
}

type ModuleStatePtr struct {
	inner *C.struct_proxy_state
}

func (moduleConfig ModuleStatePtr) AsRawPtr() unsafe.Pointer {
	return unsafe.Pointer(moduleConfig.inner)
}

func (state *ModuleStatePtr) Free() {
	C.proxy_state_destroy(state.inner)
}

func NewModuleState(
	agent *ffi.Agent,
	config *ProxyConfig,
) (ModuleStatePtr, error) {
	if config.ConnTableSize == 0 {
		return ModuleStatePtr{
			inner: nil,
		}, fmt.Errorf("connections table size must be greater than 0")
	}
	state, err := C.proxy_state_create(
		(*C.struct_agent)(agent.AsRawPtr()),
		C.uint32_t(config.ConnTableSize),
	)
	if err != nil {
		return ModuleStatePtr{
				inner: nil,
			}, fmt.Errorf(
				"failed to create state: %w",
				err,
			)
	}
	if state == nil {
		return ModuleStatePtr{
				inner: nil,
			}, fmt.Errorf(
				"failed to create state",
			)
	}
	return ModuleStatePtr{inner: state}, nil
}
