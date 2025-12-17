package fwstate_test

//#cgo CFLAGS: -I../../../.. -I../../../../lib -I../../../../common
//#cgo LDFLAGS: -L../../../../build/modules/fwstate/dataplane -lfwstate_dp
//#cgo LDFLAGS: -L../../../../build/lib/dataplane/packet -lpacket
//#cgo LDFLAGS: -L../../../../build/lib/fwstate -lfwstate
//#cgo LDFLAGS: -L../../../../build/lib/logging -llogging
/*
#include <stdlib.h>
#include "modules/fwstate/dataplane/config.h"
#include "lib/fwstate/config.h"
#include "lib/fwstate/fwmap.h"
#include "lib/fwstate/types.h"
#include "lib/dataplane/time/clock.h"
#include "common/memory.h"

void
fwstate_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
);

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

void
set_offset_of(void **field, void *ptr) {
	SET_OFFSET_OF(field, ptr);
}

void *
addr_of(void **field) {
	return ADDR_OF(field);
}

uint64_t
tsc_clock_get_time_ns(struct tsc_clock *clock) {
       struct timespec ts;
       clock_gettime(CLOCK_MONOTONIC, &ts);
       return ts.tv_sec * (uint64_t)1e9 + ts.tv_nsec;
}

*/
import "C"
import (
	"fmt"
	"net/netip"
	"runtime"
	"unsafe"

	"github.com/gopacket/gopacket"

	"github.com/yanet-platform/yanet2/common/go/dataplane"
	"github.com/yanet-platform/yanet2/common/go/testutils"
)

func fwstateModuleConfig(memCtx testutils.MemoryContext) *C.struct_fwstate_module_config {
	m := (*C.struct_fwstate_module_config)(C.memory_balloc(
		(*C.struct_memory_context)(memCtx.AsRawPtr()),
		C.sizeof_struct_fwstate_module_config),
	)

	// Create fw4state and fw6state maps using fwmap
	fw4config := C.struct_fwmap_config{
		key_size:           C.sizeof_struct_fw4_state_key,
		value_size:         C.sizeof_struct_fw_state_value,
		hash_seed:          0,
		worker_count:       1,
		hash_fn_id:         C.FWMAP_HASH_FNV1A,
		key_equal_fn_id:    C.FWMAP_KEY_EQUAL_FW4,
		rand_fn_id:         C.FWMAP_RAND_DEFAULT,
		copy_key_fn_id:     C.FWMAP_COPY_KEY_FW4,
		copy_value_fn_id:   C.FWMAP_COPY_VALUE_FWSTATE,
		merge_value_fn_id:  C.FWMAP_MERGE_VALUE_FWSTATE,
		index_size:         1024,
		extra_bucket_count: 64,
	}
	fw4state := C.fwmap_new(&fw4config, (*C.struct_memory_context)(memCtx.AsRawPtr()))
	C.set_offset_of((*unsafe.Pointer)(unsafe.Pointer(&m.cfg.fw4state)), unsafe.Pointer(fw4state))

	fw6config := C.struct_fwmap_config{
		key_size:           C.sizeof_struct_fw6_state_key,
		value_size:         C.sizeof_struct_fw_state_value,
		hash_seed:          0,
		worker_count:       1,
		hash_fn_id:         C.FWMAP_HASH_FNV1A,
		key_equal_fn_id:    C.FWMAP_KEY_EQUAL_FW6,
		rand_fn_id:         C.FWMAP_RAND_DEFAULT,
		copy_key_fn_id:     C.FWMAP_COPY_KEY_FW6,
		copy_value_fn_id:   C.FWMAP_COPY_VALUE_FWSTATE,
		merge_value_fn_id:  C.FWMAP_MERGE_VALUE_FWSTATE,
		index_size:         1024,
		extra_bucket_count: 64,
	}
	fw6state := C.fwmap_new(&fw6config, (*C.struct_memory_context)(memCtx.AsRawPtr()))
	C.set_offset_of((*unsafe.Pointer)(unsafe.Pointer(&m.cfg.fw6state)), unsafe.Pointer(fw6state))

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

	return m
}

func fwstateHandlePackets(mc *C.struct_fwstate_module_config, packets ...gopacket.Packet) (*dataplane.PacketFrontPayload, error) {
	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	pf, err := dataplane.NewPacketFrontFromPackets(&pinner, packets...)
	if err != nil {
		return nil, fmt.Errorf("failed to create packet front: %w", err)
	}

	// Create a dummy dp_worker
	dpWorker := &C.struct_dp_worker{
		idx: 0,
	}
	C.test_fwstate_handle_packets(dpWorker, &mc.cp_module, (*C.struct_packet_front)(unsafe.Pointer(pf)))
	result := pf.Payload()
	return &result, nil
}

// createSyncFrame creates a properly formatted fw_state_sync_frame
func createSyncFrame(proto uint8, addrType uint8, srcPort uint16, dstPort uint16, dstIP6, srcIP6 []byte) []byte {
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
	cfg *C.struct_fwstate_config,
	isIPv6 bool,
	proto uint8,
	srcPort uint16,
	dstPort uint16,
	srcAddr string,
	dstAddr string,
) bool {

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

	if isIPv6 {
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
		cfgReal := (*C.struct_fwstate_config)(C.addr_of((*unsafe.Pointer)(unsafe.Pointer(cfg))))
		fwmap = (*C.fwmap_t)(C.addr_of((*unsafe.Pointer)(unsafe.Pointer(&cfgReal.fw4state))))
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

	// Check if state exists
	var value unsafe.Pointer
	now := C.uint64_t(0)
	ret := C.fwmap_get(fwmap, now, keyPtr, &value, nil)
	return ret >= 0
}
