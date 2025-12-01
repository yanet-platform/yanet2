package nat64_test

//#cgo CFLAGS: -I../../../../../build
//#cgo CFLAGS: -I../../../../.. -I../../../../../lib -I../../../../../common
//#cgo CFLAGS: -I../../../dataplane -I../../../api
//#cgo LDFLAGS: -L../../../../../build/modules/nat64/dataplane -lnat64_dp
//#cgo LDFLAGS: -L../../../../../build/modules/nat64/api -lnat64_cp
//#cgo LDFLAGS: -L../../../../../build/lib/dataplane/packet -lpacket
//#cgo LDFLAGS: -L../../../../../build/lib/controlplane/config -lconfig_cp
//#cgo LDFLAGS: -L../../../../../build/lib/dataplane/config -lconfig_dp
//#cgo LDFLAGS: -L../../../../../build/lib/counters -lcounters
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
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
);

void
test_nat64_handle_packets(
	struct dp_worker *dp_worker,
	struct cp_module *cp_module,
	struct packet_front *packet_front
) {
	struct module_ectx module_ectx = {};
	SET_OFFSET_OF(&module_ectx.cp_module, cp_module);
	nat64_handle_packets(dp_worker, &module_ectx, packet_front);
}

*/
import "C"
import (
	"fmt"
	"net/netip"
	"runtime"
	"unsafe"

	"github.com/yanet-platform/yanet2/common/go/dataplane"

	"log"

	"github.com/gopacket/gopacket"
)

type mapping struct {
	ip4 netip.Addr
	ip6 netip.Addr
}

// nat64ModuleConfig creates and configures NAT64 module configuration
func nat64ModuleConfig(mappings []mapping) *C.struct_nat64_module_config {
	cDebug := C.CString("debug")
	defer C.free(unsafe.Pointer(cDebug))
	_, err := C.log_enable_name(cDebug)
	if err != nil {
		log.Printf("log enable fail: %v", err.Error())
		return nil
	}

	config := new(C.struct_nat64_module_config)

	blockAlloc := C.struct_block_allocator{}
	arena := C.malloc(1 << 20)
	C.block_allocator_put_arena(&blockAlloc, arena, 1<<20)
	C.memory_context_init(&config.cp_module.memory_context, C.CString("test"), &blockAlloc)

	if C.nat64_module_config_data_init(config, &config.cp_module.memory_context) != 0 {
		log.Printf("nat64 module config init fail")
		return nil
	}

	// Add NAT64 prefix
	pfx := [12]byte{0x20, 0x01, 0x0d, 0xb8, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00}

	if rc, err := C.nat64_module_config_add_prefix(&config.cp_module, (*C.uint8_t)(&pfx[0])); err != nil || rc < 0 {
		log.Printf("prefix add fail: %v, %v", rc, err.Error())
		return nil
	}

	// Add mappings
	for _, m := range mappings {
		ip4 := m.ip4.As4()
		ip6 := m.ip6.As16()
		if C.nat64_module_config_add_mapping(
			&config.cp_module,
			*(*C.uint32_t)(unsafe.Pointer(&ip4[0])),
			(*C.uint8_t)(&ip6[0]),
			0,
		) < 0 {
			return nil
		}
	}

	return config
}

// nat64HandlePackets processes packets through NAT64 module
func nat64HandlePackets(mc *C.struct_nat64_module_config, packets ...gopacket.Packet) dataplane.PacketFrontPayload {
	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	pf, err := dataplane.NewPacketFrontFromPackets(&pinner, packets...)
	if err != nil {
		msg := fmt.Sprintf("failed to create packet front: %v", err)
		panic(msg)
	}
	C.test_nat64_handle_packets(nil, &mc.cp_module, (*C.struct_packet_front)(unsafe.Pointer(pf)))
	return pf.Payload()
}
