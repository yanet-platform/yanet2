package balancer_test

//#cgo CFLAGS: -I../../../../../build
//#cgo CFLAGS: -I../../../../.. -I../../../../../lib -I../../../../../common
//#cgo CFLAGS: -I../../../
//#cgo LDFLAGS: -L../../../../../build/modules/balancer -lbalancer_dp
//#cgo LDFLAGS: -L../../../../../build/modules/balancer -lbalancer_cp
//#cgo LDFLAGS: -L../../../../../build/lib/dataplane/packet -lpacket
//#cgo LDFLAGS: -L../../../../../build/lib/controlplane/config -lconfig_cp
//#cgo LDFLAGS: -L../../../../../build/lib/dataplane/config -lconfig_dp
//#cgo LDFLAGS: -L../../../../../build/lib/logging -llogging
//#cgo LDFLAGS: -L../../../../../build/lib/counters -lcounters
/*
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

#include "common/memory.h"
#include "common/lpm.h"
#include "controlplane.h"
#include "config.h"
#include "defines.h"

#include "dataplane/module/module.h"
#include "dataplane/packet/packet.h"
#include "logging/log.h"

void
balancer_handle_packets(
	struct dp_config *dp_config,
	uint64_t worker_idx,
	struct cp_module *cp_module,
	struct counter_storage *counter_storage,
	struct packet_front *packet_front
);
*/
import "C"
import (
	"net/netip"
	"unsafe"

	"github.com/yanet-platform/yanet2/common/go/xnetip"
	"github.com/yanet-platform/yanet2/tests/go/common"

	"github.com/gopacket/gopacket"
)

// balancerModuleConfig creates and configures balancer module configuration
func balancerModuleConfig() *C.struct_balancer_module_config {
	config := new(C.struct_balancer_module_config)

	blockAlloc := C.struct_block_allocator{}
	arena := C.malloc(1 << 20)
	C.block_allocator_put_arena(&blockAlloc, arena, 1<<20)
	C.memory_context_init(&config.cp_module.memory_context, C.CString("test"), &blockAlloc)

	C.balancer_module_config_data_init(config, &config.cp_module.memory_context)

	return config
}

// balancerHandlePackets processes packets through balancer module
func balancerHandlePackets(mc *C.struct_balancer_module_config, packets ...gopacket.Packet) common.PacketFrontResult {
	payload := common.PacketsToPaylod(packets)
	pf := common.PacketFrontFromPayload(payload)
	common.ParsePackets(pf)
	C.balancer_handle_packets(nil, 0, &mc.cp_module, nil, (*C.struct_packet_front)(unsafe.Pointer(pf)))
	result := common.PacketFrontToPayload(pf)
	return result
}

type balancerServiceConfig struct {
	addr     netip.Addr
	reals    []balancerRealConfig
	prefixes []netip.Prefix
}

type balancerRealConfig struct {
	dst, src, srcMask netip.Addr
	weight            uint16
}

func toCPtr(netAddr netip.Addr) *C.uint8_t {
	var buf [16]byte
	if netAddr.Is4() {
		ipv4 := netAddr.As4()
		copy(buf[:], ipv4[:])
	} else {
		buf = netAddr.As16()
	}
	return (*C.uint8_t)(&buf[0])
}

func balancerModuleConfigUpdateRealWeight(
	mc *C.struct_balancer_module_config,
	serviceIdx uint64,
	realIdx uint64,
	weight uint16,
) {
	C.balancer_module_config_update_real_weight(
		&mc.cp_module,
		C.uint64_t(serviceIdx),
		C.uint64_t(realIdx),
		C.uint16_t(weight),
	)
}

func balancerModuleConfigAddService(mc *C.struct_balancer_module_config, sc balancerServiceConfig) {
	typ := C.uint64_t(C.VS_OPT_ENCAP)
	if sc.addr.Is4() {
		typ = typ | C.VS_TYPE_V4
	} else {
		typ = typ | C.VS_TYPE_V6
	}
	csc := C.balancer_service_config_create(typ, toCPtr(sc.addr), C.uint64_t(len(sc.reals)), C.uint64_t(len(sc.prefixes)))
	defer C.balancer_service_config_free(csc)

	for i, r := range sc.reals {
		typ := C.uint64_t(C.RS_TYPE_V6)
		if r.dst.Is4() {
			typ = C.RS_TYPE_V4
		}
		C.balancer_service_config_set_real(
			csc,
			C.uint64_t(i),
			typ,
			C.uint16_t(r.weight),
			toCPtr(r.dst),
			toCPtr(r.src),
			toCPtr(r.srcMask),
		)
	}

	for i, p := range sc.prefixes {
		C.balancer_service_config_set_src_prefix(csc, C.uint64_t(i), toCPtr(p.Addr()), toCPtr(xnetip.LastAddr(p)))
	}

	C.balancer_module_config_add_service(&mc.cp_module, csc)
}
