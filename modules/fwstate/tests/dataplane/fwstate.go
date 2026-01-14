package fwstate_test

//#cgo CFLAGS: -I../../../.. -I../../../../lib -I../../../../common
//#cgo LDFLAGS: -L../../../../build/modules/fwstate/dataplane -lfwstate_dp
//#cgo LDFLAGS: -L../../../../build/modules/fwstate/api -lfwstate_cp
//#cgo LDFLAGS: -L../../../../build/lib/dataplane/packet -lpacket
//#cgo LDFLAGS: -L../../../../build/lib/fwstate -lfwstate
//#cgo LDFLAGS: -L../../../../build/lib/logging -llogging
/*
#include <stdlib.h>
#include "modules/fwstate/dataplane/config.h"
#include "modules/fwstate/api/fwstate_cp.h"
#include "lib/fwstate/config.h"
#include "lib/fwstate/fwmap.h"
#include "lib/fwstate/layermap.h"
#include "lib/fwstate/types.h"
#include "lib/dataplane/time/clock.h"
#include "lib/controlplane/agent/agent.h"
#include "common/memory.h"

// Forward declaration of fwstate_handle_packets from dataplane module
void
fwstate_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
);

// Test wrapper for fwstate_handle_packets that constructs module_ectx from cp_module
void
test_fwstate_handle_packets(
	struct dp_worker *dp_worker,
	struct cp_module *cp_module,
	struct packet_front *packet_front
) {
	struct module_ectx module_ectx = {};
	SET_OFFSET_OF(&module_ectx.cp_module, cp_module);
	fwstate_handle_packets(dp_worker, &module_ectx, packet_front);
}

// Helper to get actual pointer from offset pointer
void *
addr_of(void **field) {
	return ADDR_OF(field);
}

// Mock implementation of clock_get_time_ns for tests
// Returns current monotonic time in nanoseconds
uint64_t
clock_get_time_ns(struct tsc_clock *clock) {
       struct timespec ts;
       clock_gettime(CLOCK_MONOTONIC, &ts);
       return ts.tv_sec * (uint64_t)1e9 + ts.tv_nsec;
}

// Mock implementation of cp_module_init for tests
// Provides minimal initialization without requiring full dp_config
int
cp_module_init(
	struct cp_module *cp_module,
	struct agent *agent,
	const char *module_type,
	const char *module_name
) {
	// Minimal initialization for tests (based on lib/controlplane/config/cp_module.c:13-74)
	memset(cp_module, 0, sizeof(struct cp_module));

	// We don't have dp_config in tests, so skip dp_module_idx lookup
	cp_module->dp_module_idx = 0;

	// Copy module type and name
	strncpy(cp_module->type, module_type, sizeof(cp_module->type) - 1);
	strncpy(cp_module->name, module_name, sizeof(cp_module->name) - 1);

	// Initialize memory context from agent
	memory_context_init_from(
		&cp_module->memory_context, &agent->memory_context, module_name
	);

	// Set agent offset
	SET_OFFSET_OF(&cp_module->agent, agent);

	return 0;
}

*/
import "C"
import (
	"fmt"
	"net/netip"
	"runtime"
	"unsafe"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"github.com/yanet-platform/yanet2/common/go/dataplane"
	"github.com/yanet-platform/yanet2/common/go/testutils"
)

func fwstateModuleConfig(memCtx testutils.MemoryContext) *C.struct_cp_module {
	// Allocate agent in the memory context (it needs to be in the same memory space)
	agent := (*C.struct_agent)(C.memory_balloc(
		(*C.struct_memory_context)(memCtx.AsRawPtr()),
		C.sizeof_struct_agent,
	))
	if agent == nil {
		panic("failed to allocate agent")
	}

	// Initialize agent's memory_context (similar to dataplane.c:441-448 and mock.c:206-211)
	cStubAgent := C.CString("stub agent")
	defer C.free(unsafe.Pointer(cStubAgent))

	C.memory_context_init_from(
		&agent.memory_context,
		(*C.struct_memory_context)(memCtx.AsRawPtr()),
		cStubAgent,
	)

	// Use the proper API to create the module config
	cName := C.CString("test")
	defer C.free(unsafe.Pointer(cName))

	cpModule, err := C.fwstate_module_config_init(agent, cName)
	if cpModule == nil {
		if err != nil {
			panic(fmt.Sprintf("failed to initialize fwstate module config: %v", err))
		}
		panic("failed to initialize fwstate module config")
	}

	// Create maps using the proper API
	rc, cErr := C.fwstate_config_create_maps(
		cpModule,
		C.uint32_t(1024),
		C.uint32_t(64),
		C.uint16_t(1),
	)
	if rc != 0 {
		panic(fmt.Sprintf("failed to create maps: rc=%d, err=%v", rc, cErr))
	}

	// Get the fwstate_module_config to set sync settings
	m := (*C.struct_fwstate_module_config)(unsafe.Pointer(cpModule))

	// Configure sync settings
	// Multicast IPv6 address: ff02::1
	multicastAddr := [16]C.uint8_t{0xff, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01}
	for i := range 16 {
		m.cfg.sync_config.dst_addr_multicast[i] = multicastAddr[i]
	}
	m.cfg.sync_config.port_multicast = C.uint16_t(0x0f27) // 9999 in network byte order

	// Set timeouts (in nanoseconds)
	m.cfg.sync_config.timeouts.tcp_syn_ack = C.uint64_t(120e9)
	m.cfg.sync_config.timeouts.tcp_syn = C.uint64_t(120e9)
	m.cfg.sync_config.timeouts.tcp_fin = C.uint64_t(120e9)
	m.cfg.sync_config.timeouts.tcp = C.uint64_t(120e9)
	m.cfg.sync_config.timeouts.udp = C.uint64_t(30e9)
	m.cfg.sync_config.timeouts.default_ = C.uint64_t(16e9)

	return cpModule
}

func fwstateHandlePackets(cpModule *C.struct_cp_module, packets ...gopacket.Packet) (*dataplane.PacketFrontPayload, error) {
	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	pf, err := dataplane.NewPacketFrontFromPackets(&pinner, packets...)
	if err != nil {
		return nil, fmt.Errorf("failed to create packet front: %w", err)
	}

	// Create a dummy dp_worker
	dpWorker := &C.struct_dp_worker{
		idx:          0,
		current_time: C.clock_get_time_ns(nil),
	}
	C.test_fwstate_handle_packets(dpWorker, cpModule, (*C.struct_packet_front)(unsafe.Pointer(pf)))
	result := pf.Payload()
	return &result, nil
}

// createSyncFrame creates a properly formatted fw_state_sync_frame
func createSyncFrame(proto layers.IPProtocol, addrType uint8, srcPort uint16, dstPort uint16, dstIP6, srcIP6 []byte) []byte {
	syncFrame := make([]byte, C.sizeof_struct_fw_state_sync_frame)

	// Use unsafe pointer to treat the byte slice as a C struct
	framePtr := (*C.struct_fw_state_sync_frame)(unsafe.Pointer(&syncFrame[0]))

	framePtr.proto = C.uint8_t(proto)
	framePtr.addr_type = C.uint8_t(addrType)
	framePtr.src_port = C.uint16_t(srcPort)
	framePtr.dst_port = C.uint16_t(dstPort)

	// Copy IPv6 addresses if provided
	if len(dstIP6) == 16 {
		for i := range 16 {
			framePtr.dst_ip6[i] = C.uint8_t(dstIP6[i])
		}
	}
	if len(srcIP6) == 16 {
		for i := range 16 {
			framePtr.src_ip6[i] = C.uint8_t(srcIP6[i])
		}
	}

	return syncFrame
}

// CheckStateExists checks if a state exists in the fwmap
func CheckStateExists(
	cpModule *C.struct_cp_module,
	proto layers.IPProtocol,
	srcPort uint16,
	dstPort uint16,
	srcAddr string,
	dstAddr string,
) bool {
	// Get the fwstate_config from cp_module
	m := (*C.struct_fwstate_module_config)(unsafe.Pointer(cpModule))
	cfg := &m.cfg

	// Parse addresses using netip
	srcIP, err1 := netip.ParseAddr(srcAddr)
	dstIP, err2 := netip.ParseAddr(dstAddr)
	if err1 != nil || err2 != nil {
		return false
	}

	// Select the appropriate map and create C struct directly
	var fwmap *C.fwmap_t
	var keyPtr unsafe.Pointer

	var pinner runtime.Pinner
	defer pinner.Unpin()

	if srcIP.Is6() && dstIP.Is6() {
		fwmap = (*C.fwmap_t)(C.addr_of((*unsafe.Pointer)(unsafe.Pointer(&cfg.fw6state))))
		key6 := C.struct_fw6_state_key{}

		key6.proto = C.uint16_t(proto)
		key6.src_port = C.uint16_t(srcPort)
		key6.dst_port = C.uint16_t(dstPort)

		// Copy addresses using unsafe.Slice (addresses stay in network order)
		srcBytes := srcIP.As16()
		dstBytes := dstIP.As16()
		srcAddrSlice := unsafe.Slice((*byte)(&key6.src_addr[0]), 16)
		dstAddrSlice := unsafe.Slice((*byte)(&key6.dst_addr[0]), 16)
		copy(srcAddrSlice, srcBytes[:])
		copy(dstAddrSlice, dstBytes[:])

		pinner.Pin(&key6)
		keyPtr = unsafe.Pointer(&key6)

	} else {
		fwmap = (*C.fwmap_t)(C.addr_of((*unsafe.Pointer)(unsafe.Pointer(&cfg.fw4state))))
		key4 := C.struct_fw4_state_key{}

		key4.proto = C.uint16_t(proto)
		key4.src_port = C.uint16_t(srcPort)
		key4.dst_port = C.uint16_t(dstPort)

		// Copy addresses using unsafe.Slice (addresses stay in network order)
		srcBytes := srcIP.As4()
		dstBytes := dstIP.As4()
		srcAddrSlice := unsafe.Slice((*byte)(unsafe.Pointer(&key4.src_addr)), 4)
		dstAddrSlice := unsafe.Slice((*byte)(unsafe.Pointer(&key4.dst_addr)), 4)
		copy(srcAddrSlice, srcBytes[:])
		copy(dstAddrSlice, dstBytes[:])

		pinner.Pin(&key4)
		keyPtr = unsafe.Pointer(&key4)
	}

	// Check if state exists using layermap_get
	var value unsafe.Pointer
	now := C.uint64_t(0)
	var valueFromStale C.bool
	ret := C.layermap_get(fwmap, now, keyPtr, &value, nil, &valueFromStale)
	return ret >= 0
}

// InsertNewLayer inserts a new layer using the C API
func InsertNewLayer(cpModule *C.struct_cp_module) {
	rc, cErr := C.fwstate_config_insert_new_layer(
		cpModule,
		C.uint32_t(1024),
		C.uint32_t(64),
		C.uint16_t(1),
	)
	if rc != 0 {
		panic(fmt.Sprintf("failed to insert new layer: rc=%d, err=%v", rc, cErr))
	}
}

// GetStateDeadline returns the deadline of a state entry
func GetStateDeadline(
	cpModule *C.struct_cp_module,
	proto layers.IPProtocol,
	srcPort uint16,
	dstPort uint16,
	srcAddr string,
	dstAddr string,
) uint64 {
	// Get the fwstate_config from cp_module
	m := (*C.struct_fwstate_module_config)(unsafe.Pointer(cpModule))
	cfg := &m.cfg

	srcIP, err1 := netip.ParseAddr(srcAddr)
	dstIP, err2 := netip.ParseAddr(dstAddr)
	if err1 != nil || err2 != nil {
		return 0
	}

	var fwmap *C.fwmap_t
	var keyPtr unsafe.Pointer

	var pinner runtime.Pinner
	defer pinner.Unpin()

	if srcIP.Is6() && dstIP.Is6() {
		fwmap = (*C.fwmap_t)(C.addr_of((*unsafe.Pointer)(unsafe.Pointer(&cfg.fw6state))))
		key6 := C.struct_fw6_state_key{}
		key6.proto = C.uint16_t(proto)
		key6.src_port = C.uint16_t(srcPort)
		key6.dst_port = C.uint16_t(dstPort)

		srcBytes := srcIP.As16()
		dstBytes := dstIP.As16()
		srcAddrSlice := unsafe.Slice((*byte)(&key6.src_addr[0]), 16)
		dstAddrSlice := unsafe.Slice((*byte)(&key6.dst_addr[0]), 16)
		copy(srcAddrSlice, srcBytes[:])
		copy(dstAddrSlice, dstBytes[:])

		pinner.Pin(&key6)
		keyPtr = unsafe.Pointer(&key6)
	} else {
		cfgReal := (*C.struct_fwstate_config)(C.addr_of((*unsafe.Pointer)(unsafe.Pointer(cfg))))
		fwmap = (*C.fwmap_t)(C.addr_of((*unsafe.Pointer)(unsafe.Pointer(&cfgReal.fw4state))))
		key4 := C.struct_fw4_state_key{}
		key4.proto = C.uint16_t(proto)
		key4.src_port = C.uint16_t(srcPort)
		key4.dst_port = C.uint16_t(dstPort)

		srcBytes := srcIP.As4()
		dstBytes := dstIP.As4()
		srcAddrSlice := unsafe.Slice((*byte)(unsafe.Pointer(&key4.src_addr)), 4)
		dstAddrSlice := unsafe.Slice((*byte)(unsafe.Pointer(&key4.dst_addr)), 4)
		copy(srcAddrSlice, srcBytes[:])
		copy(dstAddrSlice, dstBytes[:])

		pinner.Pin(&key4)
		keyPtr = unsafe.Pointer(&key4)
	}

	var value unsafe.Pointer
	var deadline C.uint64_t
	var valueFromStale C.bool
	now := C.uint64_t(C.clock_get_time_ns(nil))
	ret := C.layermap_get_value_and_deadline(fwmap, now*0, keyPtr, &value, nil, &deadline, &valueFromStale)
	if ret < 0 {
		return 0
	}
	return uint64(deadline)
}

// GetLayerCount returns the number of layers in both IPv4 and IPv6 maps
func GetLayerCount(cpModule *C.struct_cp_module) (uint32, uint32) {
	// Get the fwstate_config from cp_module
	m := (*C.struct_fwstate_module_config)(unsafe.Pointer(cpModule))
	cfg := &m.cfg

	// Get layer count for IPv4
	fwmap4 := (*C.fwmap_t)(C.addr_of((*unsafe.Pointer)(unsafe.Pointer(&cfg.fw4state))))
	layerCount4 := uint32(C.fwmap_layer_count(fwmap4))

	// Get layer count for IPv6
	fwmap6 := (*C.fwmap_t)(C.addr_of((*unsafe.Pointer)(unsafe.Pointer(&cfg.fw6state))))
	layerCount6 := uint32(C.fwmap_layer_count(fwmap6))

	return layerCount4, layerCount6
}

// GetCurrentTime returns current time in nanoseconds
func GetCurrentTime() uint64 {
	return uint64(C.clock_get_time_ns(nil))
}

// TrimStaleLayers trims stale layers using the C API
func TrimStaleLayers(cpModule *C.struct_cp_module, now uint64) {
	outdated := C.fwstate_config_trim_stale_layers(cpModule, C.uint64_t(now))
	if outdated != nil {
		C.fwstate_outdated_layers_free(outdated, cpModule)
	}
}
