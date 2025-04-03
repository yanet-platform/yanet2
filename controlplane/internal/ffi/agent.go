package ffi

//#cgo CFLAGS: -I../../../ -I../../../lib
//#cgo LDFLAGS: -L../../../build/lib/controlplane/agent -lagent
//#cgo LDFLAGS: -L../../../build/lib/controlplane/config -lconfig_cp
//#cgo LDFLAGS: -L../../../build/lib/dataplane/config -lconfig_dp
//
//#define _GNU_SOURCE
//#include "api/agent.h"
//#include "controlplane/agent/agent.h"
import "C"
import (
	"unsafe"
)

type ModuleConfig struct {
	ptr *C.struct_module_data
}

func NewModuleConfig(ptr unsafe.Pointer) ModuleConfig {
	return ModuleConfig{
		ptr: (*C.struct_module_data)(ptr),
	}
}

func (m *ModuleConfig) AsRawPtr() unsafe.Pointer {
	return unsafe.Pointer(m.ptr)
}

type Agent struct {
	ptr *C.struct_agent
}

func (m *Agent) Close() error {
	_, err := C.agent_detach(m.ptr)
	return err
}

func (m *Agent) AsRawPtr() unsafe.Pointer {
	return unsafe.Pointer(m.ptr)
}

func (m *Agent) UpdateModules(modules []ModuleConfig) error {
	configs := make([]*C.struct_module_data, len(modules))
	for i, module := range modules {
		configs[i] = (*C.struct_module_data)(module.AsRawPtr())
	}

	len := C.size_t(len(modules))

	C.agent_update_modules(
		(*C.struct_agent)(m.AsRawPtr()),
		len,
		&configs[0],
	)

	return nil
}

func (m *Agent) DPConfig() *DPConfig {
	return &DPConfig{
		ptr: m.ptr.dp_config,
	}
}
