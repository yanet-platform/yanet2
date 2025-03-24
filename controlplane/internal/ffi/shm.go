package ffi

//#cgo CFLAGS: -I../../../
//#cgo LDFLAGS: -L../../../build/lib/controlplane/agent -lagent
//#include "api/agent.h"
import "C"
import (
	"fmt"
	"unsafe"
)

// SharedMemory represents a handle to YANET shared memory segment.
type SharedMemory struct {
	ptr *C.struct_yanet_shm
}

// AttachSharedMemory attaches to YANET shared memory segment.
func AttachSharedMemory(path string) (*SharedMemory, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	ptr, err := C.yanet_shm_attach(cPath)
	if err != nil {
		return nil, fmt.Errorf("failed to attach to shared memory %q: %w", path, err)
	}

	return &SharedMemory{ptr: ptr}, nil
}

// Detach detaches from YANET shared memory segment.
func (m *SharedMemory) Detach() error {
	if m.ptr != nil {
		if _, err := C.yanet_shm_detach(m.ptr); err != nil {
			return err
		}

		m.ptr = nil
	}

	return nil
}

// DPConfig gets dataplane configuration from shared memory.
func (m *SharedMemory) DPConfig(numaIdx uint32) *DPConfig {
	ptr := C.yanet_shm_dp_config(m.ptr, C.uint32_t(numaIdx))

	return &DPConfig{ptr: ptr}
}

// AgentAttach attaches to a module agent to shared memory.
func (m *SharedMemory) AgentAttach(name string, numaIdx uint32, size uint) (*Agent, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	ptr := C.agent_attach(m.ptr, C.uint32_t(numaIdx), cName, C.size_t(size))
	if ptr == nil {
		return nil, fmt.Errorf("failed to attach agent: %s", name)
	}

	return &Agent{ptr: ptr}, nil
}

// NumaMap returns the NUMA node mapping as a bitmap.
//
// The returned value is a bitmap where each bit represents a NUMA node that
// is available in the system.
//
// Bit 0 represents NUMA node 0, bit 1 represents NUMA node 1, and so on.
// A bit set to 1 indicates that the corresponding NUMA node is available for
// use by YANET.
func (m *SharedMemory) NumaMap() uint32 {
	return uint32(C.yanet_shm_numa_map(m.ptr))
}

// DPConfig represents a handle to dataplane configuration.
type DPConfig struct {
	ptr *C.struct_dp_config
}

// Modules returns a list of dataplane modules available.
func (m *DPConfig) Modules() []DPModule {
	ptr := C.yanet_get_dp_module_list_info(m.ptr)
	defer C.dp_module_list_info_free(ptr)

	out := make([]DPModule, ptr.module_count)
	for idx := C.uint64_t(0); idx < ptr.module_count; idx++ {
		mod := C.struct_dp_module_info{}

		rc := C.yanet_get_dp_module_info(ptr, idx, &mod)
		if rc != 0 {
			panic("FFI corruption: module index became invalid")
		}

		out[idx] = DPModule{
			name: C.GoString(&mod.name[0]),
		}
	}

	return out
}

// DPModule represents a dataplane module in the YANET configuration.
type DPModule struct {
	name string
}

// Name returns the name of the dataplane module.
//
// The module name uniquely identifies the module in the dataplane.
func (m *DPModule) Name() string {
	return m.name
}

// String implements the Stringer interface for DPModule.
//
// Used in "zap".
func (m DPModule) String() string {
	return m.Name()
}
