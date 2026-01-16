package ffi

/*
#cgo CFLAGS: -I../../ -I../../../../../
#cgo LDFLAGS: -L../../../../../build/modules/balancer/agent -lbalancer_agent -L../../../../../build/modules/balancer/controlplane/api -lbalancer_cp -L../../../../../build/modules/balancer/controlplane/handler -lbalancer_packet_handler -L../../../../../build/modules/balancer/controlplane/state -lbalancer_state -lbalancer_packet_handler -lbalancer_state
#include "agent.h"
#include <stdlib.h>
*/
import "C"
import (
	"fmt"
	"unsafe"

	yanet "github.com/yanet-platform/yanet2/controlplane/ffi"
)

// BalancerAgent wraps a C balancer_agent handle
type BalancerAgent struct {
	handle *C.struct_balancer_agent
}

// NewBalancerAgent creates a new balancer agent instance
func NewBalancerAgent(
	shm *yanet.SharedMemory,
	memory uint,
) (*BalancerAgent, error) {
	if shm == nil {
		return nil, fmt.Errorf("shared memory is nil")
	}

	cShm := (*C.struct_yanet_shm)(shm.AsRawPtr())
	cMemory := C.size_t(memory)

	handle := C.balancer_agent(cShm, cMemory)
	if handle == nil {
		return nil, fmt.Errorf("failed to attach balancer agent")
	}

	return &BalancerAgent{handle: handle}, nil
}

// Managers retrieves all balancer managers registered with the agent
func (a *BalancerAgent) Managers() []BalancerManager {
	var cManagers C.struct_balancer_managers
	C.balancer_agent_managers(a.handle, &cManagers)

	if cManagers.count == 0 || cManagers.managers == nil {
		return nil
	}

	// Convert C array to Go slice
	managers := make([]BalancerManager, cManagers.count)
	cManagersSlice := unsafe.Slice(cManagers.managers, cManagers.count)

	for i := range managers {
		managers[i] = BalancerManager{handle: cManagersSlice[i]}
	}

	return managers
}

// NewManager creates and registers a new balancer manager with the agent
func (a *BalancerAgent) NewManager(
	name string,
	config *BalancerManagerConfig,
) (*BalancerManager, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	// Convert Go config to C config
	cConfig, err := goToC_BalancerManagerConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to convert config: %w", err)
	}
	defer freeC_BalancerManagerConfig(cConfig)

	handle := C.balancer_agent_new_manager(a.handle, cName, cConfig)
	if handle == nil {
		// Get error message from agent
		cErr := C.balancer_agent_take_error(a.handle)
		if cErr != nil {
			errMsg := C.GoString(cErr)
			C.free(unsafe.Pointer(cErr))
			return nil, fmt.Errorf("failed to create manager: %s", errMsg)
		}
		return nil, fmt.Errorf("failed to create manager")
	}

	return &BalancerManager{handle: handle}, nil
}
