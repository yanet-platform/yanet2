package dscp_test

//#cgo CFLAGS: -I../../../.. -I../../../../lib -I../../../../common
//#cgo LDFLAGS: -L../../../../build/modules/dscp/dataplane -ldscp_dp
//#cgo LDFLAGS: -L../../../../build/lib/dataplane/packet -lpacket
//#cgo LDFLAGS: -L../../../../build/lib/logging -llogging
/*
#include "lib/dataplane/packet/dscp.h"
#include "modules/dscp/dataplane/config.h"

uint8_t dscp_mark_never = DSCP_MARK_NEVER;
uint8_t dscp_mark_default = DSCP_MARK_DEFAULT;
uint8_t dscp_mark_always = DSCP_MARK_ALWAYS;

void
dscp_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
);

void
test_dscp_handle_packets(
	struct dp_worker *dp_worker,
	struct cp_module *cp_module,
	struct packet_front *packet_front
) {
	struct module_ectx module_ectx = {};
	SET_OFFSET_OF(&module_ectx.cp_module, cp_module);
	dscp_handle_packets(dp_worker, &module_ectx, packet_front);
}

*/
import "C"
import (
	"net/netip"
	"runtime"
	"unsafe"

	"github.com/gopacket/gopacket"

	"github.com/yanet-platform/yanet2/common/go/dataplane"
	"github.com/yanet-platform/yanet2/common/go/xnetip"
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

func dscpModuleConfig(prefixes []netip.Prefix, flag, dscp uint8, memCtx *C.struct_memory_context) *C.struct_dscp_module_config {
	m := &C.struct_dscp_module_config{
		cp_module: C.struct_cp_module{},
	}
	buildLPMs(prefixes, memCtx, &m.lpm_v4, &m.lpm_v6)

	m.dscp = C.struct_dscp_config{
		flag: C.uint8_t(flag),
		mark: C.uint8_t(dscp),
	}

	return m
}

func dscpHandlePackets(mc *C.struct_dscp_module_config, packets ...gopacket.Packet) dataplane.PacketFrontPayload {
	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	pf, err := dataplane.NewPacketFrontFromPackets(&pinner, packets...)
	if err != nil {
		panic(err)
	}
	C.test_dscp_handle_packets(nil, &mc.cp_module, (*C.struct_packet_front)(unsafe.Pointer(pf)))
	return pf.Payload()
}
