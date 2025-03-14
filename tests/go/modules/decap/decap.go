package decap_test

//#cgo CFLAGS: -I../../../.. -I../../../../lib -I../../../../common
//#cgo LDFLAGS: -L../../../../build/modules/decap -ldecap_dp
//#cgo LDFLAGS: -L../../../../build/lib/dataplane/packet -lpacket
/*
#include "common/memory.h"
#include "lpm.h"
#include "modules/decap/config.h"
#include "dataplane/module/module.h"

void
decap_handle_packets(
	struct dp_config *dp_config,
	struct module_data *module_data,
	struct packet_front *packet_front
);
*/
import "C"
import (
	"net/netip"
	"unsafe"

	"tests/common"

	"common/xnetip"

	"github.com/gopacket/gopacket"
)

func memCtxCreate() *C.struct_memory_context {
	blockAlloc := C.struct_block_allocator{}
	arena := C.malloc(1 << 20)
	C.block_allocator_put_arena(&blockAlloc, arena, 1<<20)
	memCtx := C.struct_memory_context{}
	C.memory_context_init(&memCtx, C.CString("test"), &blockAlloc)
	return &memCtx
}

func buildLPMs(
	prefixes []netip.Prefix,
	memCtx *C.struct_memory_context,
	lpm4 *C.struct_lpm,
	lpm6 *C.struct_lpm,
) {
	C.lpm_init(lpm4, memCtx)
	C.lpm_init(lpm6, memCtx)

	for _, prefix := range prefixes {
		if prefix.Addr().Is4() {
			ipv4 := prefix.Addr().As4()
			mask := xnetip.LastAddr(prefix).As4()
			from := (*C.uint8_t)(&ipv4[0])
			to := (*C.uint8_t)(&mask[0])
			C.lpm_insert(lpm4, 4, from, to, 1)
		} else {
			ipv6 := prefix.Addr().As16()
			mask := xnetip.LastAddr(prefix).As16()
			from := (*C.uint8_t)(&ipv6[0])
			to := (*C.uint8_t)(&mask[0])
			C.lpm_insert(lpm6, 16, from, to, 1)
		}
	}
}

func decapModuleConfig(prefixes []netip.Prefix, memCtx *C.struct_memory_context) *C.struct_decap_module_config {
	m := &C.struct_decap_module_config{
		module_data: C.struct_module_data{},
	}
	buildLPMs(prefixes, memCtx, &m.prefixes4, &m.prefixes6)

	return m
}

func decapHandlePackets(mc *C.struct_decap_module_config, packets ...gopacket.Packet) common.PacketFrontResult {
	payload := common.PacketsToPaylod(packets)
	pinner, pf := common.PacketFrontFromPayload(payload)
	common.ParsePackets(pf)
	C.decap_handle_packets(nil, &mc.module_data, (*C.struct_packet_front)(unsafe.Pointer(pf)))
	result := common.PacketFrontToPayload(pf)
	pinner.Unpin()
	return result
}
