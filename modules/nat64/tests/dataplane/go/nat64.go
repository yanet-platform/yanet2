package nat64_test

//#cgo CFLAGS: -I../../../../../build
//#cgo CFLAGS: -I../../../../.. -I../../../../../lib -I../../../../../common
//#cgo CFLAGS: -I../../../dataplane -I../../../api
//#cgo LDFLAGS: -L../../../../../build/modules/nat64/dataplane -lnat64_dp
//#cgo LDFLAGS: -L../../../../../build/modules/nat64/api -lnat64_cp
//#cgo LDFLAGS: -L../../../../../build/lib/dataplane/packet -lpacket
//#cgo LDFLAGS: -L../../../../../build/lib/dataplane/config -lconfig_dp
//#cgo LDFLAGS: -L../../../../../build/lib/logging -llogging
//#cgo LDFLAGS: -L../../../../../build/subprojects/dpdk/lib -l:librte_log.a
/*
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

#include "common/memory.h"
#include "common/lpm.h"
#include "nat64cp.h"
#include "config.h"
#include "dataplane/module/module.h"
#include "dataplane/packet/packet.h"
#include "logging/log.h"

void
nat64_handle_packets(
    struct dp_config *dp_config,
    struct module_data *module_data,
    struct packet_front *packet_front
);
*/
import "C"
import (
	"net/netip"
	"unsafe"

	"github.com/yanet-platform/yanet2/tests/go/common"

	"log"

	"github.com/gopacket/gopacket"
)

type mapping struct {
	ip4 netip.Addr
	ip6 netip.Addr
}

// memCtxCreate creates and initializes memory context for tests
func memCtxCreate() *C.struct_memory_context {
	blockAlloc := C.struct_block_allocator{}
	arena := C.malloc(1 << 20)
	C.block_allocator_put_arena(&blockAlloc, arena, 1<<20)
	memCtx := C.struct_memory_context{}
	C.memory_context_init(&memCtx, C.CString("test"), &blockAlloc)
	return &memCtx
}

// nat64ModuleConfig creates and configures NAT64 module configuration
func nat64ModuleConfig(mappings []mapping, memCtx *C.struct_memory_context) *C.struct_nat64_module_config {
	cDebug := C.CString("debug")
	defer C.free(unsafe.Pointer(cDebug))
	_, err := C.log_enable_name(cDebug)
	if err != nil {
		log.Printf("log enable fail: %v", err.Error())
		return nil
	}

	config, err := C.nat64_module_config_init_config(memCtx, C.CString("nat64"), 0)
	if err != nil {
		log.Printf("nat64 module config init fail: %v", err.Error())
		return nil
	}

	// Add NAT64 prefix
	pfx := [12]byte{0x20, 0x01, 0x0d, 0xb8, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00}

	if rc, err := C.nat64_module_config_add_prefix((*C.struct_module_data)(unsafe.Pointer(config)), (*C.uint8_t)(&pfx[0])); err != nil || rc < 0 {
		log.Printf("prefix add fail: %v, %v", rc, err.Error())
		return nil
	}

	// Add mappings
	for _, m := range mappings {
		ip4 := m.ip4.As4()
		ip6 := m.ip6.As16()
		if C.nat64_module_config_add_mapping(
			(*C.struct_module_data)(unsafe.Pointer(config)),
			*(*C.uint32_t)(unsafe.Pointer(&ip4[0])),
			(*C.uint8_t)(&ip6[0]),
			0,
		) < 0 {
			return nil
		}
	}

	return (*C.struct_nat64_module_config)(unsafe.Pointer(config))
}

// nat64HandlePackets processes packets through NAT64 module
func nat64HandlePackets(mc *C.struct_nat64_module_config, packets ...gopacket.Packet) common.PacketFrontResult {
	payload := common.PacketsToPaylod(packets)
	pf := common.PacketFrontFromPayload(payload)
	common.ParsePackets(pf)
	C.nat64_handle_packets(nil, &mc.module_data, (*C.struct_packet_front)(unsafe.Pointer(pf)))
	result := common.PacketFrontToPayload(pf)
	return result
}
