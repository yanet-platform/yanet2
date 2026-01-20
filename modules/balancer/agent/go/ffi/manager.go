package ffi

/*
#cgo CFLAGS: -I../../ -I../../../../../
#cgo LDFLAGS: -L../../../../../build/modules/balancer/agent -lbalancer_agent -L../../../../../build/modules/balancer/controlplane/api -lbalancer_cp -L../../../../../build/modules/balancer/controlplane/handler -lbalancer_packet_handler -L../../../../../build/modules/balancer/controlplane/state -lbalancer_state -lbalancer_packet_handler -L../../../../../build/filter -lfilter_compiler
#include "manager.h"
#include <stdlib.h>
*/
import "C"
import (
	"fmt"
	"time"
	"unsafe"
)

var DontUpdateRealWeight uint16 = uint16(C.DONT_UPDATE_REAL_WEIGHT)
var DontUpdateRealEnabled uint8 = uint8(C.DONT_UPDATE_REAL_ENABLED)
var MaxRealWeight uint16 = uint16(C.MAX_REAL_WEIGHT)

// BalancerManager wraps a C balancer_manager handle
type BalancerManager struct {
	handle *C.struct_balancer_manager
}

// Name returns the name of the balancer manager
func (m *BalancerManager) Name() string {
	cName := C.balancer_manager_name(m.handle)
	return C.GoString(cName)
}

// Config retrieves the current configuration of the manager
func (m *BalancerManager) Config() *BalancerManagerConfig {
	var cConfig C.struct_balancer_manager_config
	C.balancer_manager_config(m.handle, &cConfig)
	return cToGo_BalancerManagerConfig(&cConfig)
}

// Update updates the manager's configuration
func (m *BalancerManager) Update(
	config *BalancerManagerConfig,
	now time.Time,
) error {
	if config == nil {
		return fmt.Errorf("config is nil")
	}

	cConfig, err := goToC_BalancerManagerConfig(config)
	if err != nil {
		return fmt.Errorf("failed to convert config: %w", err)
	}
	defer freeC_BalancerManagerConfig(cConfig)

	cNow := C.uint32_t(now.Unix())

	if C.balancer_manager_update(m.handle, cConfig, cNow) != 0 {
		cErr := C.balancer_manager_take_error(m.handle)
		errMsg := C.GoString(cErr)
		C.free(unsafe.Pointer(cErr))
		return fmt.Errorf("failed to perform update: %s", errMsg)
	}

	return nil
}

// UpdateReals applies a batch of real server updates
func (m *BalancerManager) UpdateReals(updates []RealUpdate) error {
	if len(updates) == 0 {
		return nil
	}

	// Convert Go updates to C updates
	cUpdates := make([]C.struct_real_update, len(updates))
	for i, update := range updates {
		cUpdates[i] = goToC_RealUpdate(update)
	}

	if C.balancer_manager_update_reals(
		m.handle,
		C.size_t(len(updates)),
		&cUpdates[0],
	) != 0 {
		cErr := C.balancer_manager_take_error(m.handle)
		errMsg := C.GoString(cErr)
		C.free(unsafe.Pointer(cErr))
		return fmt.Errorf("%s", errMsg)
	}

	return nil
}

// UpdateRealsWlc applies a batch of real server weight updates for WLC algorithm
// This method only updates state weights, not config weights, preserving the
// original static weights for WLC calculations
func (m *BalancerManager) UpdateRealsWlc(updates []RealUpdate) error {
	if len(updates) == 0 {
		return nil
	}

	// Convert Go updates to C updates
	cUpdates := make([]C.struct_real_update, len(updates))
	for i, update := range updates {
		cUpdates[i] = goToC_RealUpdate(update)
	}

	if C.balancer_manager_update_reals_wlc(
		m.handle,
		C.size_t(len(updates)),
		&cUpdates[0],
	) != 0 {
		cErr := C.balancer_manager_take_error(m.handle)
		errMsg := C.GoString(cErr)
		C.free(unsafe.Pointer(cErr))
		return fmt.Errorf("%s", errMsg)
	}

	return nil
}

// ResizeSessionTable resizes the session table used by the manager's balancer
func (m *BalancerManager) ResizeSessionTable(
	newSize uint,
	now time.Time,
) error {
	cNow := C.uint32_t(now.Unix())

	if C.balancer_manager_resize_session_table(
		m.handle,
		C.size_t(newSize),
		cNow,
	) != 0 {
		cErr := C.balancer_manager_take_error(m.handle)
		errMsg := C.GoString(cErr)
		C.free(unsafe.Pointer(cErr))
		return fmt.Errorf("%s", errMsg)
	}

	return nil
}

// Info queries aggregated balancer information from the manager
func (m *BalancerManager) Info(now time.Time) (*BalancerInfo, error) {
	var cInfo C.struct_balancer_info
	cNow := C.uint32_t(now.Unix())

	if C.balancer_manager_info(m.handle, &cInfo, cNow) != 0 {
		cErr := C.balancer_manager_take_error(m.handle)
		errMsg := C.GoString(cErr)
		C.free(unsafe.Pointer(cErr))
		return nil, fmt.Errorf("%s", errMsg)
	}

	// Convert C info to Go, copying all data
	info := cToGo_BalancerInfo(&cInfo)

	// Free C allocations
	C.balancer_manager_info_free(&cInfo)

	return info, nil
}

// Sessions enumerates active sessions tracked by the manager's balancer
func (m *BalancerManager) Sessions(now time.Time) *Sessions {
	var cSessions C.struct_sessions
	cNow := C.uint32_t(now.Unix())

	C.balancer_manager_sessions(m.handle, &cSessions, cNow)

	// Convert C sessions to Go, copying all data
	sessions := cToGo_Sessions(&cSessions)

	// Free C allocations
	C.balancer_manager_sessions_free(&cSessions)

	return sessions
}

// Stats reads balancer statistics from the manager
func (m *BalancerManager) Stats(ref *PacketHandlerRef) (*BalancerStats, error) {
	if ref == nil {
		return nil, fmt.Errorf("ref is nil")
	}

	var cStats C.struct_balancer_stats
	var cRef *C.struct_packet_handler_ref

	cRef = goToC_PacketHandlerRef(ref)
	defer freeC_PacketHandlerRef(cRef)

	if C.balancer_manager_stats(m.handle, &cStats, cRef) != 0 {
		cErr := C.balancer_manager_take_error(m.handle)
		errMsg := C.GoString(cErr)
		C.free(unsafe.Pointer(cErr))
		return nil, fmt.Errorf("%s", errMsg)
	}

	// Convert C stats to Go, copying all data
	stats := cToGo_BalancerStats(&cStats)

	// Free C allocations
	C.balancer_manager_stats_free(&cStats)

	return stats, nil
}

// Graph retrieves graph representation of the manager's balancer topology
func (m *BalancerManager) Graph() *BalancerGraph {
	var cGraph C.struct_balancer_graph

	C.balancer_manager_graph(m.handle, &cGraph)

	// Convert C graph to Go, copying all data
	graph := cToGo_BalancerGraph(&cGraph)

	// Free C allocations
	C.balancer_manager_graph_free(&cGraph)

	return graph
}
