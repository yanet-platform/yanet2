//go:build cgo

package ffi

/*
#cgo CFLAGS: -I../../../../../../ -I../../../../../../lib -I../../../../../../common
#cgo LDFLAGS: -L../../../../../../build/lib/controlplane/agent -lagent
#cgo LDFLAGS: -L../../../../../../build/lib/controlplane/config -lconfig_cp
#cgo LDFLAGS: -L../../../../../../build/lib/dataplane/config -lconfig_dp
#cgo LDFLAGS: -L../../../../../../build/lib/controlplane/diag -ldiag
#cgo LDFLAGS: -L../../../../../../build/common/tls_stack -ltls_stack
#cgo LDFLAGS: -L../../../../../../build/lib/counters -lcounters
#cgo LDFLAGS: -L../../../../../../build/lib/logging -llogging
#cgo LDFLAGS: -L../../../../../../build/modules/balancer/controlplane/state -lbalancer_state
#cgo LDFLAGS: -L../../../../../../build/modules/balancer/controlplane/handler -lbalancer_packet_handler
#cgo LDFLAGS: -L../../../../../../build/modules/balancer/controlplane/api -lbalancer_cp
#cgo LDFLAGS: -L../../../../../../build/modules/balancer/controlplane/agent -lbalancer_agent

#include <stdlib.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>

#include "modules/balancer/controlplane/agent/agent.h"
#include "api/agent.h"
*/
import "C"

import (
	"fmt"
	"unsafe"

	yanet "github.com/yanet-platform/yanet2/controlplane/ffi"
)

// BalancerAgent is a Go wrapper for the balancer agent C structure.
type BalancerAgent struct {
	agent *C.struct_balancer_agent
}

// NewBalancerAgent creates a new balancer agent instance.
// It wraps the balancer_agent() C function.
// Note: shm must be a pointer to yanet_shm C struct
func NewBalancerAgent(shm *yanet.SharedMemory, memory uint) (*BalancerAgent, error) {
	cAgent := C.balancer_agent(
		(*C.struct_yanet_shm)(unsafe.Pointer(shm)),
		C.size_t(memory),
	)
	if cAgent == nil {
		return nil, fmt.Errorf("failed to create balancer agent")
	}

	return &BalancerAgent{agent: cAgent}, nil
}

// ListBalancers retrieves all balancers from the agent.
// It wraps the balancer_agent_balancers() C function.
func (ba *BalancerAgent) ListBalancers() (*BalancerAgentBalancersList, error) {
	var cBalancers C.struct_balancer_agent_balancers
	C.memset(unsafe.Pointer(&cBalancers), 0, C.sizeof_struct_balancer_agent_balancers)

	C.balancer_agent_balancers(ba.agent, &cBalancers)
	defer C.balancer_agent_balancers_free(&cBalancers)

	if cBalancers.count == 0 {
		return &BalancerAgentBalancersList{Balancers: []BalancerAgentBalancerItem{}}, nil
	}

	// Convert C balancers to Go
	cBalancersSlice := unsafe.Slice(cBalancers.balancers, int(cBalancers.count))
	balancers := make([]BalancerAgentBalancerItem, 0, int(cBalancers.count))

	for i := range cBalancersSlice {
		cBal := &cBalancersSlice[i]

		// Convert config
		config, err := goFromCBalancerAgentConfig(&cBal.config)
		if err != nil {
			return nil, fmt.Errorf("failed to convert balancer config at index %d: %w", i, err)
		}

		balancers = append(balancers, BalancerAgentBalancerItem{
			Handle: &Balancer{h: cBal.handle},
			Config: *config,
		})
	}

	return &BalancerAgentBalancersList{Balancers: balancers}, nil
}

// UpdateBalancer updates or creates a balancer with the given configuration.
// It wraps the balancer_agent_update_balancer() C function.
func (ba *BalancerAgent) UpdateBalancer(config BalancerAgentConfig) (*BalancerAgentBalancerItem, error) {
	// Build C config
	cConfig, cleanup, err := buildCBalancerAgentConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to build C config: %w", err)
	}
	defer cleanup()

	// Allocate result balancer
	var cBalancer C.struct_balancer_agent_balancer
	C.memset(unsafe.Pointer(&cBalancer), 0, C.sizeof_struct_balancer_agent_balancer)

	// Call C function
	ret := C.balancer_agent_update_balancer(ba.agent, cConfig, &cBalancer)
	if ret != 0 {
		err := ba.TakeError()
		return nil, fmt.Errorf("balancer_agent_update_balancer failed: %s", err)
	}

	// Convert result to Go
	resultConfig, err := goFromCBalancerAgentConfig(&cBalancer.config)
	if err != nil {
		return nil, fmt.Errorf("failed to convert result config: %w", err)
	}

	return &BalancerAgentBalancerItem{
		Handle: &Balancer{h: cBalancer.handle},
		Config: *resultConfig,
	}, nil
}

// TakeError retrieves and clears the last error message from the agent.
// It wraps the balancer_agent_take_error_msg() C function.
func (ba *BalancerAgent) TakeError() error {
	cMsg := C.balancer_agent_take_error_msg(ba.agent)
	if cMsg == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(cMsg))
	return fmt.Errorf("%s", C.GoString(cMsg))
}

// Helper functions for converting between C and Go structures

// goFromCBalancerAgentConfig converts a C balancer_agent_balancer_config to Go.
func goFromCBalancerAgentConfig(cConfig *C.struct_balancer_agent_balancer_config) (*BalancerAgentConfig, error) {
	if cConfig == nil {
		return nil, fmt.Errorf("nil config")
	}

	// Extract balancer name
	name := C.GoString(&cConfig.balancer_name[0])

	// Convert balancer config
	balancerConfig := goFromCBalancerConfig(&cConfig.balancer_config)
	if balancerConfig == nil {
		return nil, fmt.Errorf("failed to convert balancer config")
	}

	// Convert adjust weights config
	adjustWeightsConfig := AdjustWeightsConfig{
		AdjustPower:   uint(cConfig.adjust_weights_config.adjust_power),
		MaxRealWeight: uint(cConfig.adjust_weights_config.max_real_weight),
	}

	// Convert adjust_weights_vs array (flexible array member at end of struct)
	var adjustWeightsVs []uint32
	if cConfig.adjust_weights_vs_count > 0 {
		// Calculate offset to flexible array member
		basePtr := unsafe.Pointer(cConfig)
		offset := unsafe.Sizeof(*cConfig)
		vsPtr := (*C.uint32_t)(unsafe.Add(basePtr, offset))
		vsSlice := unsafe.Slice(vsPtr, int(cConfig.adjust_weights_vs_count))
		adjustWeightsVs = make([]uint32, len(vsSlice))
		for i, vs := range vsSlice {
			adjustWeightsVs[i] = uint32(vs)
		}
	}

	return &BalancerAgentConfig{
		BalancerName:        name,
		BalancerConfig:      *balancerConfig,
		AdjustWeightsConfig: adjustWeightsConfig,
		RefreshPeriod:       uint32(cConfig.refresh_period),
		AdjustWeightsVs:     adjustWeightsVs,
	}, nil
}

// buildCBalancerAgentConfig converts a Go BalancerAgentConfig to C.
// Returns the C config, a cleanup function, and an error.
func buildCBalancerAgentConfig(config BalancerAgentConfig) (*C.struct_balancer_agent_balancer_config, func(), error) {
	// Calculate size including flexible array member
	baseSize := C.sizeof_struct_balancer_agent_balancer_config
	vsArraySize := C.size_t(len(config.AdjustWeightsVs)) * C.sizeof_uint32_t
	totalSize := C.size_t(baseSize) + vsArraySize

	// Allocate memory
	cConfig := (*C.struct_balancer_agent_balancer_config)(C.malloc(totalSize))
	if cConfig == nil {
		return nil, nil, fmt.Errorf("malloc failed for balancer_agent_balancer_config")
	}
	C.memset(unsafe.Pointer(cConfig), 0, totalSize)

	// Copy balancer name
	cName := C.CString(config.BalancerName)
	defer C.free(unsafe.Pointer(cName))
	C.strncpy(&cConfig.balancer_name[0], cName, 80)

	// Build balancer config
	cBalancerConfig, cleanupBalancer, err := buildCBalancerConfig(config.BalancerConfig)
	if err != nil {
		C.free(unsafe.Pointer(cConfig))
		return nil, nil, fmt.Errorf("failed to build balancer config: %w", err)
	}

	// Copy balancer config (shallow copy of the struct, pointers are already set)
	C.memcpy(
		unsafe.Pointer(&cConfig.balancer_config),
		unsafe.Pointer(cBalancerConfig),
		C.sizeof_struct_balancer_config,
	)

	// Set adjust weights config
	cConfig.adjust_weights_config.adjust_power = C.size_t(config.AdjustWeightsConfig.AdjustPower)
	cConfig.adjust_weights_config.max_real_weight = C.size_t(config.AdjustWeightsConfig.MaxRealWeight)

	// Set refresh period
	cConfig.refresh_period = C.uint32_t(config.RefreshPeriod)

	// Set adjust_weights_vs array (flexible array member at end of struct)
	cConfig.adjust_weights_vs_count = C.size_t(len(config.AdjustWeightsVs))
	if len(config.AdjustWeightsVs) > 0 {
		// Calculate offset to flexible array member
		basePtr := unsafe.Pointer(cConfig)
		offset := unsafe.Sizeof(*cConfig)
		vsPtr := (*C.uint32_t)(unsafe.Add(basePtr, offset))
		vsSlice := unsafe.Slice(vsPtr, len(config.AdjustWeightsVs))
		for i, vs := range config.AdjustWeightsVs {
			vsSlice[i] = C.uint32_t(vs)
		}
	}

	cleanup := func() {
		cleanupBalancer()
		C.free(unsafe.Pointer(cBalancerConfig))
		C.free(unsafe.Pointer(cConfig))
	}

	return cConfig, cleanup, nil
}
