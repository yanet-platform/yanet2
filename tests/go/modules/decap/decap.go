package decap_test

//#cgo CFLAGS: -I../../../.. -I../../../../lib -I../../../../common
//#cgo LDFLAGS: -L../../../../build/modules/decap -ldecap_dp
//#cgo LDFLAGS: -L../../../../build/lib/dataplane/packet -lpacket
/*
#include "modules/decap/dataplane.h"

void
decap_handle_packets(
	struct module *module,
	struct module_config *config,
	struct packet_front *packet_front
);
*/
import "C"
import (
	"net/netip"
	"unsafe"

	"tests/common"

	"github.com/gopacket/gopacket"
)

func decapModuleConfig(prefixes []netip.Prefix) C.struct_decap_module_config {
	m := C.struct_decap_module_config{
		config: C.struct_module_config{},
	}
	lpm4, lpm6 := common.BuildLPMs(prefixes)
	m.prefixes4 = *(*C.struct_lpm)(unsafe.Pointer(&lpm4))
	m.prefixes6 = *(*C.struct_lpm)(unsafe.Pointer(&lpm6))

	return m
}

func decapHandlePackets(mc *C.struct_decap_module_config, packets ...gopacket.Packet) common.PacketFrontResult {
	payload := common.PacketsToPaylod(packets)
	pinner, pf := common.PacketFrontFromPayload(payload)
	common.ParsePackets(pf)
	C.decap_handle_packets(nil, &mc.config, (*C.struct_packet_front)(unsafe.Pointer(pf)))
	result := common.PacketFrontToPayload(pf)
	pinner.Unpin()
	return result
}
