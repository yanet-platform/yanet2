package dscp_test

//#cgo CFLAGS: -I../../../.. -I../../../../lib -I../../../../common
//#cgo LDFLAGS: -L../../../../build/modules/dscp -ldscp_dp
//#cgo LDFLAGS: -L../../../../build/lib/dataplane/packet -lpacket
/*
#include "lib/dataplane/packet/dscp.h"
#include "modules/dscp/config.h"

uint8_t dscp_mark_never = DSCP_MARK_NEVER;
uint8_t dscp_mark_default = DSCP_MARK_DEFAULT;
uint8_t dscp_mark_always = DSCP_MARK_ALWAYS;

void
dscp_handle_packets(
	struct dp_config *dp_config,
	struct module_data *module_data,
	struct packet_front *packet_front
);
*/
import "C"
import (
	"net/netip"
	"unsafe"

	"common/xnetip"
	"tests/common"

	"github.com/gopacket/gopacket"
)

var (
	DSCPMarkNever   uint8 = uint8(C.dscp_mark_never)
	DSCPMarkAlways  uint8 = uint8(C.dscp_mark_always)
	DSCPMarkDefault uint8 = uint8(C.dscp_mark_default)
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
			from := (*C.uint8_t)(unsafe.Pointer(&ipv4[0]))
			to := (*C.uint8_t)(unsafe.Pointer(&mask[0]))
			C.lpm_insert(lpm4, 4, from, to, 1)
		} else {
			ipv6 := prefix.Addr().As16()
			mask := xnetip.LastAddr(prefix).As16()
			from := (*C.uint8_t)(unsafe.Pointer(&ipv6[0]))
			to := (*C.uint8_t)(unsafe.Pointer(&mask[0]))
			C.lpm_insert(lpm6, 16, from, to, 1)
		}
	}
}

func dscpModuleConfig(prefixes []netip.Prefix, flag, dscp uint8, memCtx *C.struct_memory_context) *C.struct_dscp_module_config {
	m := &C.struct_dscp_module_config{
		module_data: C.struct_module_data{},
	}
	buildLPMs(prefixes, memCtx, &m.lpm_v4, &m.lpm_v6)

	m.dscp = C.struct_dscp_config{
		flag: C.uint8_t(flag),
		mark: C.uint8_t(dscp),
	}

	return m
}

func dscpHandlePackets(mc *C.struct_dscp_module_config, packets ...gopacket.Packet) common.PacketFrontResult {
	payload := common.PacketsToPaylod(packets)
	pinner, pf := common.PacketFrontFromPayload(payload)
	common.ParsePackets(pf)
	C.dscp_handle_packets(nil, &mc.module_data, (*C.struct_packet_front)(unsafe.Pointer(pf)))
	result := common.PacketFrontToPayload(pf)
	pinner.Unpin()
	return result
}
