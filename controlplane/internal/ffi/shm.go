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

// DPConfig represents a handle to dataplane configuration.
type DPConfig struct {
	ptr *C.struct_dp_config
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
func (m *SharedMemory) NumaMap() uint32 {
	return uint32(C.yanet_shm_numa_map(m.ptr))
}
