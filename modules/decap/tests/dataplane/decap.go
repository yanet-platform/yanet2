package decap_test

//#cgo CFLAGS: -I../../../../ -I../../../../lib
//#cgo LDFLAGS: -L../../../../build/modules/decap/dataplane -ldecap_dp
//#cgo LDFLAGS: -L../../../../build/lib/dataplane/packet -lpacket
//#cgo LDFLAGS: -L../../../../build/lib/logging -llogging
/*
#include "common/memory.h"
#include "common/lpm.h"
#include "modules/decap/dataplane/config.h"
#include "dataplane/module/module.h"

void
decap_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
);

void
test_decap_handle_packets(
	struct dp_worker *dp_worker,
	struct cp_module *cp_module,
	struct packet_front *packet_front
) {
	struct module_ectx module_ectx = {};
	SET_OFFSET_OF(&module_ectx.cp_module, cp_module);
	decap_handle_packets(dp_worker, &module_ectx, packet_front);
}

*/
import "C"
import (
	"cmp"
	"fmt"
	"net/netip"
	"runtime"
	"slices"
	"unsafe"

	"github.com/gopacket/gopacket"

	"github.com/yanet-platform/yanet2/common/go/dataplane"
	"github.com/yanet-platform/yanet2/common/go/testutils"
	"github.com/yanet-platform/yanet2/common/go/xnetip"
)

func buildLPMs(
	prefixes []netip.Prefix,
	memCtx *C.struct_memory_context,
	lpm4 *C.struct_lpm,
	lpm6 *C.struct_lpm,
) {
	C.lpm_init(lpm4, memCtx)
	C.lpm_init(lpm6, memCtx)

	slices.SortFunc(prefixes, func(a netip.Prefix, b netip.Prefix) int {
		return cmp.Compare(a.Bits(), b.Bits())
	})

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

func decapModuleConfig(prefixes []netip.Prefix, memCtx testutils.MemoryContext) *C.struct_decap_module_config {
	m := &C.struct_decap_module_config{
		cp_module: C.struct_cp_module{},
	}
	buildLPMs(prefixes, (*C.struct_memory_context)(memCtx.AsRawPtr()), &m.prefixes4, &m.prefixes6)

	return m
}

func decapHandlePackets(mc *C.struct_decap_module_config, packets ...gopacket.Packet) (*dataplane.PacketFrontPayload, error) {
	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	pf, err := dataplane.NewPacketFrontFromPackets(&pinner, packets...)
	if err != nil {
		return nil, fmt.Errorf("failed to create packet front: %w", err)
	}
	C.test_decap_handle_packets(nil, &mc.cp_module, (*C.struct_packet_front)(unsafe.Pointer(pf)))
	payload := pf.Payload()
	return &payload, nil
}
