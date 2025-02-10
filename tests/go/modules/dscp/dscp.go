package dscp_test

//#cgo CFLAGS: -I../../../.. -I../../../../lib -I../../../../common
//#cgo LDFLAGS: -L../../../../build/modules/dscp -ldscp_dp
//#cgo LDFLAGS: -L../../../../build/lib/dataplane/packet -lpacket
/*
#include "lib/dataplane/packet/dscp.h"
#include "modules/dscp/dataplane.h"

uint8_t dscp_mark_never = DSCP_MARK_NEVER;
uint8_t dscp_mark_default = DSCP_MARK_DEFAULT;
uint8_t dscp_mark_always = DSCP_MARK_ALWAYS;

void
dscp_handle_packets(
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

var (
	DSCPMarkNever   uint8 = uint8(C.dscp_mark_never)
	DSCPMarkAlways  uint8 = uint8(C.dscp_mark_always)
	DSCPMarkDefault uint8 = uint8(C.dscp_mark_default)
)

func dscpModuleConfig(prefixes []netip.Prefix, flag, dscp uint8) C.struct_dscp_module_config {
	m := C.struct_dscp_module_config{
		config: C.struct_module_config{},
	}
	lpm4, lpm6 := common.BuildLPMs(prefixes)
	m.lpm_v4 = *(*C.struct_lpm)(unsafe.Pointer(&lpm4))
	m.lpm_v6 = *(*C.struct_lpm)(unsafe.Pointer(&lpm6))
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
	C.dscp_handle_packets(nil, &mc.config, (*C.struct_packet_front)(unsafe.Pointer(pf)))
	result := common.PacketFrontToPayload(pf)
	pinner.Unpin()
	return result
}
