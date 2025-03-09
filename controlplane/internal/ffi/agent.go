package ffi

//#cgo CFLAGS: -I../../../ -I../../../lib
//#cgo LDFLAGS: -L../../../build/lib/controlplane/agent -lagent
//
//#include "api/agent.h"
//#include "controlplane/agent/agent.h"
import "C"
import (
	"fmt"
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

// TODO: consider renaming to something more specific, like:
// - "MemoryContext"
// - "SharedContext"
// - etc.
type Agent struct {
	ptr *C.struct_agent
}

// TODO: docs for 'it's user responsibility to call "Close"'.
func NewAgent(path string, name string, size uint) (*Agent, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	ptr, err := C.agent_connect(cPath, cName, C.ulong(size))
	if ptr == nil {
		return nil, fmt.Errorf("failed to connect to shared memory %q: %w", path, err)
	}

	return &Agent{
		ptr: ptr,
	}, nil
}

func (m *Agent) Close() error {
	// "TODO: currently there is no way to free an agent"
	return fmt.Errorf("TODO: currently there is no way to free an agent")
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
